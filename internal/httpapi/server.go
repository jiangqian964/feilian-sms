// Package httpapi 是网关入站 HTTP 装配层：
//   - 飞连事件 webhook：路径在运行时按系统设置比对（故可热改），
//     限 1MiB、支持可选加密信封、challenge 秒级 JSON 回显、token 恒定时间校验，
//     事件交编排层后恒回 200（失败只留痕，避免飞连反复重推）；
//   - 厂商异步回执：POST /receipts/{channel_id}；
//   - GET /health：存活探针；
//   - 管理面（/api 与 WebUI）直接暴露，不做来源 IP 限制。
package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/http"
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

// receiptTokenHeader 是厂商回执鉴权 token 的请求头名；同时兼容 ?token= 查询参数。
const receiptTokenHeader = "X-Receipt-Token"

// acceptedBody 是事件被接收后的恒 200 响应体（飞连仅以 HTTP 状态判定重推）。
const acceptedBody = `{"code":0,"message":"success"}`

// Deps 是入站层装配依赖（业务逻辑全部在 service 层，本层只做协议适配）。
type Deps struct {
	Settings *service.SettingsRuntime
	Forward  *service.ForwardService
	Receipts *service.ReceiptService
	// Store 为管理 API 提供配置/记录读写（T11）；事件转发不经此依赖。
	Store *store.Store
	// Logger 必传；nil 时回落 nop 日志。
	Logger *zap.Logger
	// UI 为 WebUI 静态资源处理器（T12 接入完整四视图）；nil 时不挂载界面。
	UI http.Handler
}

// Server 持有已完成路由装配的处理器；管理子路由经 AdminMux 暴露给 T11 扩展。
type Server struct {
	deps  Deps
	root  *http.ServeMux
	admin *http.ServeMux
}

// NewServer 完成全部路由注册。
func NewServer(d Deps) (*Server, error) {
	if d.Logger == nil {
		d.Logger = zap.NewNop()
	}

	s := &Server{deps: d, root: http.NewServeMux(), admin: http.NewServeMux()}
	s.registerAdminRoutes()

	// 公网固定路由。
	s.root.HandleFunc("POST /receipts/{channel_id}", s.handleReceipt)
	s.root.HandleFunc("GET /health", s.handleHealth)
	// 管理面：/api 全方法直接进入管理子 mux；StripPrefix 后子 mux 以 /settings
	// 等相对自身根的模式注册，PathValue 仍可正常取到 {id}。
	s.root.Handle("/api/", http.StripPrefix("/api", s.admin))
	// 其余路径经根分发：POST 按运行时设置匹配飞连 webhook（路径可热改），
	// 非 POST 直接返回 WebUI 静态资源。
	// （不能用 "POST /" 与 "/api/" 并列注册：方法与路径两个维度各有胜负，
	// 会被 ServeMux 判定为冲突。）
	s.root.HandleFunc("/", s.dispatchRoot)
	return s, nil
}

// Handler 返回根处理器（供 http.Server 使用）；统一包裹 panic 恢复中间件，
// 任何处理器内的意外 panic 都落结构化日志并回统一 500，而不是断连/裸堆栈。
func (s *Server) Handler() http.Handler { return s.recoverMiddleware(s.root) }

// statusTrackingWriter 记录响应是否已提交，panic 恢复时据此决定能否补 500。
type statusTrackingWriter struct {
	http.ResponseWriter
	committed bool
}

func (w *statusTrackingWriter) WriteHeader(code int) {
	w.committed = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusTrackingWriter) Write(b []byte) (int, error) {
	w.committed = true
	return w.ResponseWriter.Write(b)
}

// recoverMiddleware 兜底所有处理器 panic：未提交响应时回统一 500；
// 已提交（如边写边 panic）则仅记录日志，避免二次写头部。
func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &statusTrackingWriter{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				s.deps.Logger.Error("HTTP 处理器发生 panic，已恢复",
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr),
					zap.Any("panic", rec),
					zap.Stack("stack"))
				if !tw.committed {
					writeErrorJSON(tw, http.StatusInternalServerError, codeInternal, "服务器内部错误")
				}
			}
		}()
		next.ServeHTTP(tw, r)
	})
}

// AdminMux 返回管理面子路由（/api/... 在此注册）。
func (s *Server) AdminMux() *http.ServeMux { return s.admin }

// handleHealth 存活探针；no-store 由 writeJSON 统一保证。
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

// dispatchRoot 处理未被固定模式命中的路径：
// POST 全部交给飞连 webhook（内部按运行时路径比对，故支持热改路径）；
// 其余方法直接返回 WebUI；未挂载 UI 时回 404。
func (s *Server) dispatchRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleWebhook(w, r)
		return
	}
	if s.deps.UI == nil {
		writeErrorJSON(w, http.StatusNotFound, codeNotFound, "资源不存在")
		return
	}
	s.deps.UI.ServeHTTP(w, r)
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

	// 1) url_verification 握手：token 校验通过后以 JSON 对象原样回显 challenge
	//    （飞连/飞书事件订阅网关要求 application/json 的 {"challenge":"..."}，须 <1 秒；
	//    早期版本回 text/plain 裸字符串会被网关以“解析不到 challenge”判定校验失败）。
	if challenge, isChallenge, perr := feilian.ParseChallenge(plain); perr != nil {
		writeErrorJSON(w, http.StatusBadRequest, codeBadRequest, perr.Error())
		return
	} else if isChallenge {
		if !feilian.TokenEqual(challenge.Token, s.deps.Settings.VerificationToken()) {
			s.deps.Logger.Warn("url_verification token 校验失败")
			writeErrorJSON(w, http.StatusUnauthorized, codeUnauthorized, "Verification Token 不匹配")
			return
		}
		writeJSON(w, map[string]string{"challenge": challenge.Challenge})
		return
	}

	// 2) 先只解析头部并校验 token：鉴权必须先于任何类型分流，未通过校验的请求
	// 不得借「非短信事件 200」分支探测 webhook 有效性或获得差异化响应。
	head, err := feilian.ParseEventHeader(plain)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}
	if !feilian.TokenEqual(head.Header.Token, s.deps.Settings.VerificationToken()) {
		s.deps.Logger.Warn("飞连事件 token 校验失败",
			zap.String("event_id", head.Header.EventID),
			zap.String("event_type", head.Header.EventType))
		writeErrorJSON(w, http.StatusUnauthorized, codeUnauthorized, "Verification Token 不匹配")
		return
	}

	// 3) 非短信事件：鉴权通过后仅留日志，回 200 且不调用编排层（其 object
	// 结构与短信无关，故不能走短信信封的严格校验）。
	if head.Header.EventType != smsEventType {
		s.deps.Logger.Info("忽略非短信事件",
			zap.String("event_id", head.Header.EventID),
			zap.String("event_type", head.Header.EventType))
		writeAccepted(w)
		return
	}

	// 4) 短信事件再做信封与 object 的严格校验。
	env, err := feilian.ParseEnvelope(plain)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}

	objects := make([]feilian.SMSObject, 0, len(env.Data.Events))
	for _, ev := range env.Data.Events {
		objects = append(objects, ev.Object)
	}

	// 5) 交编排层；任何单条失败都在内部消化，入站恒 200。
	// 不透传入站 ctx：编排层以服务自持上下文执行，客户端断连不影响下发/回写。
	start := time.Now()
	result := s.deps.Forward.HandleEvent(context.Background(), env.Header.EventID, objects)
	s.deps.Logger.Info("飞连事件处理完成",
		zap.String("event_id", env.Header.EventID),
		zap.Int("total", result.Total),
		zap.Int("succeeded", result.Succeeded),
		zap.Int("failed", result.Failed),
		zap.Int("pending", result.Pending),
		zap.Int("skipped", result.Skipped),
		zap.Int64("elapsed_ms", time.Since(start).Milliseconds()))

	writeAccepted(w)
}

// handleReceipt 接收厂商异步送达回执并原样返回通道配置的成功响应。
func (s *Server) handleReceipt(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channel_id")
	remoteIP := directIP(r.RemoteAddr)

	// 全局回执 token 先于报文解析：未配置 token 时不校验（内网/网络层已隔离场景）；
	// 一旦配置，缺失或不匹配一律 401，且不进入通道解析等任何后续逻辑。
	if !s.receiptTokenAuthorized(r) {
		s.deps.Logger.Warn("厂商回执鉴权失败",
			zap.String("channel_id", channelID),
			zap.String("remote_ip", remoteIP))
		writeErrorJSON(w, http.StatusUnauthorized, codeUnauthorized, "回执鉴权 token 缺失或不匹配")
		return
	}

	body, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

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
		zap.Bool("applied", result.Applied),
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

// receiptTokenAuthorized 校验厂商回执的全局鉴权 token：
// 系统未配置 token 时返回 true（兼容内网隔离部署）；配置后仅接受
// X-Receipt-Token 头或 ?token= 查询参数中的等值 token，恒定时间比较防计时侧信道。
func (s *Server) receiptTokenAuthorized(r *http.Request) bool {
	want := s.deps.Settings.ReceiptAuthToken()
	if want == "" {
		return true
	}
	got := r.Header.Get(receiptTokenHeader)
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
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
