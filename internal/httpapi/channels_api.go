package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/feilian"
	"feilian-sms/internal/service"
	"feilian-sms/internal/store"
)

// presetInfo 是内置通道预置的列表项。
type presetInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// 内置预置目录（当前提供一个通用 HTTP JSON 示例，对接更多厂商时在此扩展）。
var presetCatalog = []presetInfo{
	{
		ID:          channel.PresetBuiltin,
		Name:        "通用 HTTP JSON 短信通道（示例）",
		Description: "JSON 报文 + SHA-1 加盐签名；基址、凭证与模板码需补填",
	},
}

// channelRequest 是创建/更新通道的表单载体：
// Config 为结构化配置对象（不允许手写 JSON 模板，但配置本身以 JSON 传输）；
// Secrets 仅给出需要（覆盖）写入的密钥，空值=不修改。
type channelRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Config      json.RawMessage   `json:"config"`
	Secrets     map[string]string `json:"secrets,omitempty"`
}

// channelResponse 是通道详情/列表项；密钥只回掩码与是否已设置。
type channelResponse struct {
	ID            string                `json:"id"`
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	Enabled       bool                  `json:"enabled"`
	Config        json.RawMessage       `json:"config"`
	SecretsMasked map[string]secretView `json:"secrets_masked"`
	ReceiptURL    string                `json:"receipt_url,omitempty"`
	CreatedAt     int64                 `json:"created_at"`
	UpdatedAt     int64                 `json:"updated_at"`
}

// testSendRequest 是「测试发送」表单。
type testSendRequest struct {
	TemplateCode string   `json:"template_code"`
	SMSType      string   `json:"sms_type"`
	CountryCode  string   `json:"country_code"`
	MobileNumber string   `json:"mobile_number"`
	Mobile       string   `json:"mobile"`
	Params       []string `json:"params"`
}

type fromPresetRequest struct {
	Preset      string `json:"preset"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) listPresets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"presets": presetCatalog})
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.deps.Store.ListChannels(r.Context())
	if err != nil {
		s.failInternal(w, r, "列出通道失败", err)
		return
	}
	resp := struct {
		Channels []channelResponse `json:"channels"`
	}{Channels: make([]channelResponse, 0, len(channels))}
	for i := range channels {
		view, err := s.buildChannelResponse(r, channels[i])
		if err != nil {
			s.failInternal(w, r, "组装通道视图失败", err)
			return
		}
		resp.Channels = append(resp.Channels, view)
	}
	writeJSON(w, resp)
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	var req channelRequest
	if !decodeAdminBody(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "name", "通道名称不能为空")
		return
	}
	cfg, field, msg := validateChannelConfig(req.Config, nil)
	if field != "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, field, msg)
		return
	}
	cleanSecrets, field := sanitizeSecrets(cfg, req.Secrets)
	if field != "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, field,
			"提交的密钥名未在配置常量中声明为 secret")
		return
	}

	configJSON := canonicalConfig(cfg)
	ch, err := s.deps.Store.CreateChannel(r.Context(), req.Name, req.Description, configJSON, cleanSecrets)
	if err != nil {
		s.failInternal(w, r, "创建通道失败", err)
		return
	}
	if req.Enabled != nil && !*req.Enabled {
		if err := s.deps.Store.SetChannelEnabled(r.Context(), ch.ID, false); err != nil {
			s.failInternal(w, r, "设置通道启停失败", err)
			return
		}
		ch.Enabled = false
	}
	s.deps.Logger.Info("通道已创建", zap.String("channel_id", ch.ID), zap.String("name", ch.Name))
	s.respondChannel(w, r, http.StatusCreated, *ch)
}

func (s *Server) createChannelFromPreset(w http.ResponseWriter, r *http.Request) {
	var req fromPresetRequest
	if !decodeAdminBody(w, r, &req) {
		return
	}
	if req.Preset != channel.PresetBuiltin {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "preset",
			"未知的通道预置标识（当前支持: http_json_v1）")
		return
	}
	if req.Name == "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "name", "通道名称不能为空")
		return
	}
	// 预置脚手架允许密钥待填：直接落库，不经试渲染拦截。
	cfg := channel.BuiltinPresetConfig()
	ch, err := s.deps.Store.CreateChannel(r.Context(), req.Name, req.Description, canonicalConfig(cfg), nil)
	if err != nil {
		s.failInternal(w, r, "从预置创建通道失败", err)
		return
	}
	s.deps.Logger.Info("通道已从预置创建", zap.String("channel_id", ch.ID), zap.String("preset", req.Preset))
	s.respondChannel(w, r, http.StatusCreated, *ch)
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.loadChannel(w, r)
	if !ok {
		return
	}
	s.respondChannel(w, r, http.StatusOK, *ch)
}

func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.loadChannel(w, r)
	if !ok {
		return
	}
	var req channelRequest
	if !decodeAdminBody(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "name", "通道名称不能为空")
		return
	}

	// 合并既有密钥与本次提交（空值=不修改），供试渲染使用。
	merged, err := s.deps.Store.GetChannelSecrets(r.Context(), existing.ID)
	if err != nil {
		s.failInternal(w, r, "读取通道密钥失败", err)
		return
	}
	cleanSecrets, field := sanitizeSecretsFromRaw(req.Config, req.Secrets)
	if field != "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, field,
			"提交的密钥名未在配置常量中声明为 secret")
		return
	}
	if merged == nil {
		merged = map[string]string{}
	}
	for k, v := range cleanSecrets {
		merged[k] = v
	}

	cfg, field, msg := validateChannelConfig(req.Config, merged)
	if field != "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, field, msg)
		return
	}

	updated := store.Channel{
		ID:          existing.ID,
		Name:        req.Name,
		Description: req.Description,
		Enabled:     existing.Enabled,
		ConfigJSON:  canonicalConfig(cfg),
	}
	if req.Enabled != nil {
		updated.Enabled = *req.Enabled
	}
	if err := s.deps.Store.UpdateChannel(r.Context(), updated); err != nil {
		s.failInternal(w, r, "更新通道失败", err)
		return
	}
	if len(cleanSecrets) > 0 {
		if err := s.deps.Store.PutChannelSecrets(r.Context(), existing.ID, cleanSecrets); err != nil {
			s.failInternal(w, r, "写入通道密钥失败", err)
			return
		}
	}
	s.respondChannel(w, r, http.StatusOK, updated)
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.deps.Store.DeleteChannel(r.Context(), id); err != nil {
		if isStoreNotFound(err) {
			writeErrorJSON(w, http.StatusNotFound, codeNotFound, "通道不存在")
			return
		}
		s.failInternal(w, r, "删除通道失败", err)
		return
	}
	s.deps.Logger.Info("通道已删除", zap.String("channel_id", id))
	writeNoContent(w)
}

func (s *Server) enableChannel(w http.ResponseWriter, r *http.Request) {
	s.setChannelEnabled(w, r, true)
}

func (s *Server) disableChannel(w http.ResponseWriter, r *http.Request) {
	s.setChannelEnabled(w, r, false)
}

func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req testSendRequest
	if !decodeAdminBody(w, r, &req) {
		return
	}
	if req.TemplateCode == "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "template_code", "模板码不能为空")
		return
	}
	if req.MobileNumber == "" {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "mobile_number", "手机号不能为空")
		return
	}
	if req.SMSType == "" {
		req.SMSType = "code"
	} else if !feilian.IsKnownSMSType(req.SMSType) {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "sms_type",
			"未知的短信场景类型，须为飞连支持的 9 种 sms_type 之一")
		return
	}

	result, err := s.deps.Forward.TestSend(r.Context(), id, req.TemplateCode, feilian.SMSObject{
		CountryCode:  req.CountryCode,
		MobileNumber: req.MobileNumber,
		Mobile:       req.Mobile,
		SMSType:      req.SMSType,
		Params:       req.Params,
	})
	switch {
	case err == nil:
		fields := []zap.Field{
			zap.String("channel_id", id),
			zap.String("app_sms_id", result.AppSmsID),
			zap.Int("http_code", result.HTTPCode),
		}
		if result.ErrorKind != "" {
			fields = append(fields, zap.String("error_kind", result.ErrorKind))
		}
		if result.Success {
			s.deps.Logger.Info("测试发送成功", fields...)
		} else {
			s.deps.Logger.Warn("测试发送失败", fields...)
		}
		writeJSON(w, result)
	case errors.Is(err, store.ErrNotFound):
		writeErrorJSON(w, http.StatusNotFound, codeNotFound, "通道不存在")
	case errors.Is(err, service.ErrChannelDisabled):
		writeErrorJSON(w, http.StatusConflict, codeConflict, "通道已停用，请先启用后再测试")
	default:
		s.failInternal(w, r, "测试发送失败", err)
	}
}

// setChannelEnabled 处理启停子路由。
func (s *Server) setChannelEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id := r.PathValue("id")
	if err := s.deps.Store.SetChannelEnabled(r.Context(), id, enabled); err != nil {
		if isStoreNotFound(err) {
			writeErrorJSON(w, http.StatusNotFound, codeNotFound, "通道不存在")
			return
		}
		s.failInternal(w, r, "切换通道启停失败", err)
		return
	}
	ch, err := s.deps.Store.GetChannel(r.Context(), id)
	if err != nil {
		s.failInternal(w, r, "回读通道失败", err)
		return
	}
	s.respondChannel(w, r, http.StatusOK, *ch)
}

// loadChannel 按路径 id 取通道；不存在时已写 404。
func (s *Server) loadChannel(w http.ResponseWriter, r *http.Request) (*store.Channel, bool) {
	ch, err := s.deps.Store.GetChannel(r.Context(), r.PathValue("id"))
	if err != nil {
		if isStoreNotFound(err) {
			writeErrorJSON(w, http.StatusNotFound, codeNotFound, "通道不存在")
			return nil, false
		}
		s.failInternal(w, r, "读取通道失败", err)
		return nil, false
	}
	return ch, true
}

// respondChannel 组装视图并按给定状态写出。
func (s *Server) respondChannel(w http.ResponseWriter, r *http.Request, status int, ch store.Channel) {
	view, err := s.buildChannelResponse(r, ch)
	if err != nil {
		s.failInternal(w, r, "组装通道视图失败", err)
		return
	}
	writeJSONStatus(w, status, view)
}

// buildChannelResponse 以配置中声明的 secret 常量为目录合并已存掩码，
// 保证未填写的密钥也以 set=false 回显（供表单渲染）。
func (s *Server) buildChannelResponse(r *http.Request, ch store.Channel) (channelResponse, error) {
	cfg, err := channel.ParseConfig(ch.ConfigJSON)
	if err != nil {
		return channelResponse{}, err
	}
	views := map[string]secretView{}
	for name, spec := range cfg.Constants {
		if spec.Secret {
			views[name] = secretView{Value: "****", Set: false}
		}
	}
	stored, err := s.deps.Store.GetChannelSecretsMasked(r.Context(), ch.ID)
	if err != nil {
		return channelResponse{}, err
	}
	for name, m := range stored {
		views[name] = secretView{Value: m.Value, Set: m.Set}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return channelResponse{}, err
	}
	return channelResponse{
		ID:            ch.ID,
		Name:          ch.Name,
		Description:   ch.Description,
		Enabled:       ch.Enabled,
		Config:        raw,
		SecretsMasked: views,
		ReceiptURL:    s.deps.Settings.ReceiptURL(ch.ID),
		CreatedAt:     ch.CreatedAt,
		UpdatedAt:     ch.UpdatedAt,
	}, nil
}

// renderSampleInput 是通道保存前试渲染的样例输入（不发起真实请求）：
// 大陆手机号、一个验证码参数，足以暴露变量/映射/签名/号码策略全部结构错误。
func renderSampleInput() channel.SendInput {
	return channel.SendInput{
		AppSmsID:     "validate-sample-0001",
		CountryCode:  "+86",
		MobileNumber: "13800000000",
		Mobile:       "8613800000000",
		SMSType:      "code",
		TemplateCode: "VALIDATE",
		Params:       []string{"123456"},
	}
}

// validateChannelConfig 执行保存前试渲染：
// 结构性错误（坏 JSON/坏 URL/未知变量/重复 target/值类型/签名/号码策略）→ 返回 field+msg；
// 仅「密钥未填写」（ErrSecretNotSet）放行（预置脚手架态，真实下发时再报错）。
func validateChannelConfig(raw json.RawMessage, secrets map[string]string) (*channel.Config, string, string) {
	var probe map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &probe) != nil {
		return nil, "config", "通道配置必须是合法 JSON 对象"
	}
	cfg, err := channel.ParseConfig(string(raw))
	if err != nil {
		return nil, "config", err.Error()
	}
	if cfg.ResolveURL() == "" {
		return nil, "config.request.url", "出站请求 URL 不能为空（可用 ${base}/path 形式）"
	}
	if _, _, err := cfg.TryRender(secrets, renderSampleInput()); err != nil {
		if errors.Is(err, channel.ErrSecretNotSet) {
			return cfg, "", ""
		}
		if errors.Is(err, channel.ErrInvalidMobile) {
			return nil, "config.mobile_policy", err.Error()
		}
		var re *channel.RenderError
		if errors.As(err, &re) {
			switch re.Stage {
			case "mapping":
				return nil, "config.body_mappings", err.Error()
			case "sign":
				return nil, "config.sign", err.Error()
			}
		}
		return nil, "config", err.Error()
	}
	return cfg, "", ""
}

// sanitizeSecrets 校验提交的密钥名均在配置中声明为 secret，返回需要 UPSERT 的非空值。
func sanitizeSecrets(cfg *channel.Config, submitted map[string]string) (map[string]string, string) {
	clean := map[string]string{}
	for name, val := range submitted {
		spec, ok := cfg.Constants[name]
		if !ok || !spec.Secret {
			return nil, "secrets"
		}
		if val != "" {
			clean[name] = val
		}
	}
	return clean, ""
}

// sanitizeSecretsFromRaw 在更新场景先用原始配置解析后再校验密钥名。
func sanitizeSecretsFromRaw(raw json.RawMessage, submitted map[string]string) (map[string]string, string) {
	cfg, err := channel.ParseConfig(string(raw))
	if err != nil {
		return nil, "config"
	}
	return sanitizeSecrets(cfg, submitted)
}

// canonicalConfig 以解析后（含默认值）的配置重新序列化落库，统一形态。
func canonicalConfig(cfg *channel.Config) string {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
