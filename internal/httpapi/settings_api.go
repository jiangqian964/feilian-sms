package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"go.uber.org/zap"

	"feilian-sms/internal/store"
)

// 系统设置阈值边界（与 spec 的 3 秒预算、补发窗口一致）。
const (
	minDownstreamTimeoutMS = 100
	maxDownstreamTimeoutMS = 60000
	minStalePendingMS      = 1000
	maxStalePendingMS      = 24 * 60 * 60 * 1000
	// minReceiptAuthTokenLen 是回执鉴权 token 的最小长度：过短的 token 极易被
	// 爆破；空值语义保留给「不启用鉴权」，只能经 clear_receipt_auth_token 落空。
	minReceiptAuthTokenLen = 16
)

// settingsResponse 是 GET /api/settings 的回显：
// Verification Token 为普通接入参数故明文回显；Encrypt Key / 回执 token 仅回掩码与是否已设置。
type settingsResponse struct {
	VerificationToken      string `json:"verification_token"`
	EncryptKeySet          bool   `json:"encrypt_key_set"`
	EncryptKeyMasked       string `json:"encrypt_key_masked"`
	ReceiptAuthTokenSet    bool   `json:"receipt_auth_token_set"`
	ReceiptAuthTokenMasked string `json:"receipt_auth_token_masked"`
	WebhookPath            string `json:"webhook_path"`
	WebhookURL             string `json:"webhook_url"`
	PublicBaseURL          string `json:"public_base_url"`
	DownstreamTimeoutMS    int    `json:"downstream_timeout_ms"`
	StalePendingMS         int    `json:"stale_pending_ms"`
	UpdatedAt              int64  `json:"updated_at"`
}

// settingsUpdateRequest 全部字段为指针：缺省=不修改（读改写合并语义）。
type settingsUpdateRequest struct {
	VerificationToken     *string `json:"verification_token"`
	EncryptKey            *string `json:"encrypt_key"`
	ClearEncryptKey       bool    `json:"clear_encrypt_key"`
	ReceiptAuthToken      *string `json:"receipt_auth_token"`
	ClearReceiptAuthToken bool    `json:"clear_receipt_auth_token"`
	WebhookPath           *string `json:"webhook_path"`
	PublicBaseURL         *string `json:"public_base_url"`
	DownstreamTimeoutMS   *int    `json:"downstream_timeout_ms"`
	StalePendingMS        *int    `json:"stale_pending_ms"`
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.deps.Store.GetSettings(r.Context())
	if err != nil {
		s.failInternal(w, r, "读取系统设置失败", err)
		return
	}
	writeJSON(w, makeSettingsResponse(st))
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsUpdateRequest
	if !decodeAdminBody(w, r, &req) {
		return
	}

	// 字段级校验（写入前一次性拒绝，不产生半成品状态）。
	if req.WebhookPath != nil {
		p := *req.WebhookPath
		if p == "" || !strings.HasPrefix(p, "/") || strings.ContainsAny(p, " \t\r\n") ||
			strings.ContainsAny(p, "?#") {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "webhook_path",
				"webhook 路径必须是以 / 开头、不含空白与 ?/# 的非空路径")
			return
		}
		// 不得占用固定公网/管理路由前缀，否则事件入口会被静态路由抢占或与 /api 混淆。
		if p == "/api" || strings.HasPrefix(p, "/api/") ||
			p == "/receipts" || strings.HasPrefix(p, "/receipts/") ||
			p == "/health" {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "webhook_path",
				"webhook 路径不得占用 /api、/receipts、/health 保留前缀")
			return
		}
	}
	if req.ClearReceiptAuthToken && req.ReceiptAuthToken != nil && *req.ReceiptAuthToken != "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "receipt_auth_token",
			"receipt_auth_token 与 clear_receipt_auth_token 不能同时提供")
		return
	}
	if req.ReceiptAuthToken != nil && *req.ReceiptAuthToken != "" {
		if v := *req.ReceiptAuthToken; len(v) < minReceiptAuthTokenLen {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "receipt_auth_token",
				"回执鉴权 token 长度至少 16 个字符；如需关闭鉴权请使用 clear_receipt_auth_token")
			return
		}
		if strings.ContainsAny(*req.ReceiptAuthToken, " \t\r\n") {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "receipt_auth_token",
				"回执鉴权 token 不能包含空白字符")
			return
		}
	}
	if req.PublicBaseURL != nil {
		if b := strings.TrimSpace(*req.PublicBaseURL); b != "" {
			u, err := url.Parse(b)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "public_base_url",
					"对外基址必须是合法的 http(s) URL")
				return
			}
		}
	}
	if req.DownstreamTimeoutMS != nil {
		if v := *req.DownstreamTimeoutMS; v < minDownstreamTimeoutMS || v > maxDownstreamTimeoutMS {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "downstream_timeout_ms",
				"下游超时须在 100~60000 毫秒之间")
			return
		}
	}
	if req.StalePendingMS != nil {
		if v := *req.StalePendingMS; v < minStalePendingMS || v > maxStalePendingMS {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "stale_pending_ms",
				"补发阈值须在 1000~86400000 毫秒之间")
			return
		}
	}
	if req.ClearEncryptKey && req.EncryptKey != nil && *req.EncryptKey != "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "encrypt_key",
			"encrypt_key 与 clear_encrypt_key 不能同时提供")
		return
	}

	// 读改写合并：store.UpdateSettings 为全量更新，未提供字段保持原值。
	st, err := s.deps.Store.GetSettings(r.Context())
	if err != nil {
		s.failInternal(w, r, "读取系统设置失败", err)
		return
	}
	if req.VerificationToken != nil {
		st.VerificationToken = *req.VerificationToken
	}
	if req.WebhookPath != nil {
		st.WebhookPath = *req.WebhookPath
	}
	if req.PublicBaseURL != nil {
		st.PublicBaseURL = strings.TrimSpace(*req.PublicBaseURL)
	}
	if req.DownstreamTimeoutMS != nil {
		st.DownstreamTimeoutMS = *req.DownstreamTimeoutMS
	}
	if req.StalePendingMS != nil {
		st.StalePendingMS = *req.StalePendingMS
	}
	switch {
	case req.ClearEncryptKey:
		st.EncryptKey = "" // store 层空串=清空密文
	case req.EncryptKey != nil && *req.EncryptKey != "":
		st.EncryptKey = *req.EncryptKey // 空串同样视为不修改
	}
	switch {
	case req.ClearReceiptAuthToken:
		st.ReceiptAuthToken = ""
	case req.ReceiptAuthToken != nil && *req.ReceiptAuthToken != "":
		st.ReceiptAuthToken = *req.ReceiptAuthToken // 空串视为不修改
	}

	if err := s.deps.Store.UpdateSettings(r.Context(), st); err != nil {
		s.failInternal(w, r, "更新系统设置失败", err)
		return
	}
	s.deps.Logger.Info("系统设置已更新",
		zap.Bool("encrypt_key_cleared", req.ClearEncryptKey),
		zap.Bool("encrypt_key_changed", req.EncryptKey != nil && *req.EncryptKey != ""),
		zap.Bool("receipt_auth_token_cleared", req.ClearReceiptAuthToken),
		zap.Bool("receipt_auth_token_changed", req.ReceiptAuthToken != nil && *req.ReceiptAuthToken != ""))

	updated, err := s.deps.Store.GetSettings(r.Context())
	if err != nil {
		s.failInternal(w, r, "回读系统设置失败", err)
		return
	}
	writeJSON(w, makeSettingsResponse(updated))
}

// makeSettingsResponse 组装设置回显；密钥仅给掩码。
func makeSettingsResponse(st store.Settings) settingsResponse {
	resp := settingsResponse{
		VerificationToken:      st.VerificationToken,
		WebhookPath:            st.WebhookPath,
		PublicBaseURL:          st.PublicBaseURL,
		DownstreamTimeoutMS:    st.DownstreamTimeoutMS,
		StalePendingMS:         st.StalePendingMS,
		UpdatedAt:              st.UpdatedAt,
		WebhookURL:             strings.TrimRight(st.PublicBaseURL, "/") + st.WebhookPath,
		EncryptKeyMasked:       "****",
		ReceiptAuthTokenMasked: "****",
	}
	if st.EncryptKey != "" {
		resp.EncryptKeySet = true
		resp.EncryptKeyMasked = store.MaskSecret(st.EncryptKey)
	}
	if st.ReceiptAuthToken != "" {
		resp.ReceiptAuthTokenSet = true
		resp.ReceiptAuthTokenMasked = store.MaskSecret(st.ReceiptAuthToken)
	}
	return resp
}

// failInternal 统一记录并返回 500（错误详情不外泄）。
func (s *Server) failInternal(w http.ResponseWriter, r *http.Request, msg string, err error) {
	s.deps.Logger.Error(msg, zap.String("method", r.Method), zap.String("path", r.URL.Path), zap.Error(err))
	writeErrorJSON(w, http.StatusInternalServerError, codeInternal, "服务器内部错误")
}
