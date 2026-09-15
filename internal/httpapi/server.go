// Package httpapi 是网关入站 HTTP 装配层：
//   - 飞连事件 webhook：路径在运行时按系统设置比对（故可热改），
//     限 1MiB、支持可选加密信封、challenge 秒级回显、token 恒定时间校验，
//     事件交编排层后恒回 200（失败只留痕，避免飞连反复重推）；
//   - 厂商异步回执：POST /receipts/{channel_id}；
//   - GET /health：存活探针；
//   - 管理面（/api 与 WebUI）由可选 CIDR 白名单守卫，只信直连 RemoteAddr。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"go.uber.org/zap"

	"feilian-sms/internal/feilian"
	"feilian-sms/internal/service"
	"feilian-sms/internal/store"
)

// maxBodyBytes 限制飞连事件与厂商回执请求体上限（飞连事件体很小，1MiB 足够）。
const maxBodyBytes = 1 << 20

// smsEventType 是本服务唯一处理的飞连事件类型；其余类型记日志后回 200。
const smsEventType = "notify.v1.sms"

// acceptedBody 是事件被接收后的恒 200 响应体（飞连仅以 HTTP 状态判定重推）。
const acceptedBody = `{"code":0,"message":"success"}`

// Deps 是入站层装配依赖（业务逻辑全部在 service 层，本层只做协议适配）。
type Deps struct {
	Settings *service.SettingsRuntime
	Forward  *service.ForwardService
	Receipts *service.ReceiptService
	// Store 为管理 API 提供配置/记录读写（T11）；事件转发不经此依赖。
	Store *store.Store
	// AdminCIDRs 管理端白名单（CIDR）；为空表示不做网络层限制。
	AdminCIDRs []string
	// Logger 必传；nil 时回落 nop 日志。
	Logger *zap.Logger
	// UI 为 WebUI 静态资源处理器（T12 接入完整四视图）；nil 时不挂载界面。
	UI http.Handler
}

// Server 持有已完成路由装配的处理器；管理子路由经 AdminMux 暴露给 T11 扩展。
type Server struct {
	deps      Deps
	root      *http.ServeMux
	admin     *http.ServeMux
	adminNets []netip.Prefix
}

// NewServer 解析 CIDR 并完成全部路由注册；非法 CIDR 在装配期直接报错。
func NewServer(d Deps) (*Server, error) {
	if d.Logger == nil {
		d.Logger = zap.NewNop()
	}
	nets := make([]netip.Prefix, 0, len(d.AdminCIDRs))
	for _, raw := range d.AdminCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("管理端 CIDR 非法 %q: %w", raw, err)
		}
		nets = append(nets, prefix.Masked())
	}

	s := &Server{deps: d, root: http.NewServeMux(), admin: http.NewServeMux(), adminNets: nets}
	s.registerAdminRoutes()

	// 公网固定路由（不经管理端 CIDR）。
	s.root.HandleFunc("POST /receipts/{channel_id}", s.handleReceipt)
	s.root.HandleFunc("GET /health", s.handleHealth)
	// 管理面：/api 全方法统一过 CIDR 守卫；StripPrefix 后子 mux 以 /settings 等
	// 相对自身根的模式注册，PathValue 仍可正常取到 {id}。
	s.root.Handle("/api/", s.adminGuard(http.StripPrefix("/api", s.admin)))
	// 其余路径经根分发：POST 按运行时设置匹配飞连 webhook（路径可热改），
	// 非 POST 走 CIDR 守卫后的 WebUI 静态资源。
	// （不能用 "POST /" 与 "/api/" 并列注册：方法与路径两个维度各有胜负，
	// 会被 ServeMux 判定为冲突。）
	s.root.HandleFunc("/", s.dispatchRoot)
	return s, nil
}

// Handler 返回根处理器（供 http.Server 使用）。
func (s *Server) Handler() http.Handler { return s.root }

// AdminMux 返回管理面子路由（/api/... 在此注册）；整体已由 CIDR 守卫包裹。
func (s *Server) AdminMux() *http.ServeMux { return s.admin }

// handleHealth 存活探针；no-store 由 writeJSON 统一保证。
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

// dispatchRoot 处理未被固定模式命中的路径：
// POST 全部交给飞连 webhook（内部按运行时路径比对，故支持热改路径）；
// 其余方法进入受 CIDR 守卫的 WebUI；未挂载 UI 时回 404。
func (s *Server) dispatchRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleWebhook(w, r)
		return
	}
	if s.deps.UI == nil {
		writeErrorJSON(w, http.StatusNotFound, codeNotFound, "资源不存在")
		return
	}
	s.adminGuard(s.deps.UI).ServeHTTP(w, r)
}

// handleWebhook 处理飞连事件订阅回调。
// 所有未命中固定路由的 POST 都会进入这里，再以系统设置中的 webhook 路径做
// 运行时比对，因此路径修改无需重新注册路由、下一条请求即按新路径生效。
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.deps.Settings.WebhookPath() {
		writeErrorJSON(w, http.StatusNotFound, codeNotFound, "事件订阅路径不存在")
		return
	}

	raw, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

	plain, err := s.resolvePlaintext(raw)
	if err != nil {
		s.deps.Logger.Warn("飞连事件信封解析失败", zap.Error(err))
		writeErrorJSON(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}

	// 1) url_verification 握手：token 校验通过后原样回显 challenge（须 <1 秒）。
	if challenge, isChallenge, perr := feilian.ParseChallenge(plain); perr != nil {
		writeErrorJSON(w, http.StatusBadRequest, codeBadRequest, perr.Error())
		return
	} else if isChallenge {
		if !feilian.TokenEqual(challenge.Token, s.deps.Settings.VerificationToken()) {
			s.deps.Logger.Warn("url_verification token 校验失败")
			writeErrorJSON(w, http.StatusUnauthorized, codeUnauthorized, "Verification Token 不匹配")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", headerNoStore)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.WriteString(w, challenge.Challenge)
		return
	}

	// 2) 非短信事件：仅留日志，回 200 且不调用编排层。
	if eventType := probeEventType(plain); eventType != "" && eventType != smsEventType {
		s.deps.Logger.Info("忽略非短信事件", zap.String("event_type", eventType))
		writeAccepted(w)
		return
	}

	// 3) 短信事件信封解析与 token 校验（不过不触发任何编排动作）。
	env, err := feilian.ParseEnvelope(plain)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}
	if !feilian.TokenEqual(env.Header.Token, s.deps.Settings.VerificationToken()) {
		s.deps.Logger.Warn("飞连事件 token 校验失败",
			zap.String("event_id", env.Header.EventID),
			zap.String("event_type", env.Header.EventType))
		writeErrorJSON(w, http.StatusUnauthorized, codeUnauthorized, "Verification Token 不匹配")
		return
	}

	objects := make([]feilian.SMSObject, 0, len(env.Data.Events))
	for _, ev := range env.Data.Events {
		objects = append(objects, ev.Object)
	}

	// 4) 交编排层；任何单条失败都在内部消化，入站恒 200。
	start := time.Now()
	result := s.deps.Forward.HandleEvent(r.Context(), env.Header.EventID, objects)
	s.deps.Logger.Info("飞连事件处理完成",
		zap.String("event_id", env.Header.EventID),
		zap.Int("total", result.Total),
		zap.Int("succeeded", result.Succeeded),
		zap.Int("failed", result.Failed),
		zap.Int("skipped", result.Skipped),
		zap.Int64("elapsed_ms", time.Since(start).Milliseconds()))

	writeAccepted(w)
}

// handleReceipt 接收厂商异步送达回执并原样返回通道配置的成功响应。
func (s *Server) handleReceipt(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channel_id")

	body, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

	remoteIP := directIP(r.RemoteAddr)
	result, err := s.deps.Receipts.Handle(r.Context(), channelID, body, remoteIP)
	if err != nil {
		if errors.Is(err, service.ErrReceiptChannelNotFound) {
			s.deps.Logger.Warn("回执指向不存在的通道",
				zap.String("channel_id", channelID), zap.String("remote_ip", remoteIP))
			writeErrorJSON(w, http.StatusNotFound, codeNotFound, "回执通道不存在")
			return
		}
		s.deps.Logger.Warn("回执报文处理失败",
			zap.String("channel_id", channelID), zap.Error(err))
		writeErrorJSON(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}

	s.deps.Logger.Info("厂商回执已处理",
		zap.String("channel_id", channelID),
		zap.String("app_sms_id", result.AppSmsID),
		zap.String("delivery_status", result.DeliveryStatus),
		zap.Bool("matched_record", result.Found),
		zap.String("remote_ip", remoteIP))

	w.Header().Set("Content-Type", jsonContentType)
	w.Header().Set("Cache-Control", headerNoStore)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.ResponseBody)
}

// resolvePlaintext 按 Encrypt Key 配置决定解密/拒绝：
//   - 收到加密信封：必须配置 key，解密失败回 400；
//   - 收到明文信封：一旦配置 key 即拒绝（强制密文，防止降级）。
func (s *Server) resolvePlaintext(raw []byte) ([]byte, error) {
	encrypted, sealed, err := feilian.ExtractEncrypted(raw)
	if err != nil {
		return nil, err
	}
	key := s.deps.Settings.EncryptKey()
	if sealed {
		if key == "" {
			return nil, errors.New("收到加密事件信封，但本服务未配置 Encrypt Key")
		}
		return feilian.DecryptEvent(key, encrypted)
	}
	if key != "" {
		return nil, errors.New("已配置 Encrypt Key，仅接受加密事件信封（明文请求被拒绝）")
	}
	return raw, nil
}

// probeEventType 仅做轻量探测，用于非短信事件提前放行；
// 结构损坏时返回空串，交由严格解析产出 400。
func probeEventType(raw []byte) string {
	var probe struct {
		Header struct {
			EventType string `json:"event_type"`
		} `json:"header"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return probe.Header.EventType
}

// writeAccepted 写出事件接收恒 200 响应。
func writeAccepted(w http.ResponseWriter) {
	w.Header().Set("Content-Type", jsonContentType)
	w.Header().Set("Cache-Control", headerNoStore)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, acceptedBody)
}

// readLimitedBody 以 1MiB 上限读取请求体；超限回 413，读取失败回 400。
func readLimitedBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge, "请求体超过 1MiB 上限")
			return nil, false
		}
		writeErrorJSON(w, http.StatusBadRequest, codeBadRequest, "读取请求体失败")
		return nil, false
	}
	return raw, true
}

// directIP 从直连 RemoteAddr 取 IP（去掉端口）；解析失败原样返回，
// 供回执留痕。该函数绝不读取 X-Forwarded-For。
func directIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}
