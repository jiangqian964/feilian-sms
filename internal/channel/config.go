// Package channel 是通用 HTTP 短信通道的运行时：
// 配置结构 → 字段映射试渲染 → 签名 → HTTP 下发 → 响应判定。
// 配置以 JSON 存于 channels.config_json，明文常量内联、密钥常量
// 单独经 channel_secrets（AES-GCM）保存，运行时在 Secrets 中合并。
package channel

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"feilian-sms/internal/channel/mapping"
	"feilian-sms/internal/channel/sign"
)

// Config 是单个短信通道的完整可持久化配置。
type Config struct {
	Request      RequestConfig           `json:"request"`       // 出站 HTTP 请求
	Constants    map[string]ConstantSpec `json:"constants"`     // 通道常量（非密钥内联，密钥仅声明）
	BodyMappings []mapping.Mapping       `json:"body_mappings"` // 字段映射表（禁止手写模板）
	Sign         SignConfig              `json:"sign"`          // 签名策略
	Response     ResponseConfig          `json:"response"`      // 响应判定
	MobilePolicy string                  `json:"mobile_policy"` // 手机号归一化策略
	Receipt      ReceiptConfig           `json:"receipt"`       // 回执映射
}

// allowedRequestMethods 是通用 HTTP 通道允许的请求方法白名单。
var allowedRequestMethods = map[string]bool{"POST": true, "GET": true, "PUT": true}

// RequestConfig 出站请求配置；URL 可用 ${base} 引用 BaseURL。
type RequestConfig struct {
	BaseURL     string            `json:"base_url"`     // 如 https://sms.example.com:1443
	URL         string            `json:"url"`          // 如 ${base}/sms/send
	Method      string            `json:"method"`       // 默认 POST
	ContentType string            `json:"content_type"` // 默认 application/json
	Headers     map[string]string `json:"headers"`      // 额外请求头（值可用 ${const:name} 占位）
}

// ConstantSpec 声明一个通道常量；Secret=true 时 Value 不落配置，
// 真实值由 channel_secrets 提供。
type ConstantSpec struct {
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret"`
}

// SignConfig 签名配置；Segments 为有序待签名片段。
type SignConfig struct {
	Strategy    string         `json:"strategy"`     // none/sha1_salt/hmac_sha256
	Encoding    string         `json:"encoding"`     // hmac: hex/base64
	SecretConst string         `json:"secret_const"` // 作为签名密钥的常量名（Constants 中 Secret 项）
	Segments    []sign.Segment `json:"segments"`
}

// ResponseConfig 厂商同步响应判定。
type ResponseConfig struct {
	SuccessPath  string `json:"success_path"`  // 成功标志取值路径，如 status
	SuccessValue string `json:"success_value"` // 期望值（宽松比较），如 0
	MsgIDPath    string `json:"msg_id_path"`   // 厂商消息 ID 路径，如 data.id
	MessagePath  string `json:"message_path"`  // 厂商错误描述路径，如 message
}

// ReceiptConfig 厂商异步回执映射（POST JSON）。
type ReceiptConfig struct {
	MsgIDPath      string `json:"msg_id_path"`     // 厂商短信 ID 字段，如 smsId
	AppMsgIDPath   string `json:"app_msg_id_path"` // 我方 appSmsId 回传字段，如 appSmsId
	StatusPath     string `json:"status_path"`     // 回执状态字段，如 status
	DeliveredValue string `json:"delivered_value"` // 送达成功值，如 DELIVRD
	MessagePath    string `json:"message_path"`    // 回执描述字段
	SeqNoPath      string `json:"seq_no_path"`     // 回执序号字段，如 seqNo
	SuccessBody    string `json:"success_body"`    // 回执成功响应原文；空=内置默认 {"status":0,"message":"success"}
}

// ParseConfig 解析并补默认值（方法/Content-Type/手机号策略）。
func ParseConfig(raw string) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("通道配置不是合法 JSON: %w", err)
	}
	method := strings.ToUpper(strings.TrimSpace(cfg.Request.Method))
	if method == "" {
		method = "POST"
	}
	if !allowedRequestMethods[method] {
		return nil, fmt.Errorf("不支持的请求方法 %q（仅支持 POST/GET/PUT）", cfg.Request.Method)
	}
	cfg.Request.Method = method
	if cfg.Request.ContentType == "" {
		cfg.Request.ContentType = "application/json"
	}
	if cfg.MobilePolicy == "" {
		cfg.MobilePolicy = MobilePolicyCCPrefix
	}
	return &cfg, nil
}

// RenderResult 试渲染/真实渲染的结果（可用于日志与保存前校验）。
type RenderResult struct {
	Body      map[string]any
	Headers   map[string]string // 已完成 ${const:}/${constb64:} 插值的请求头
	RawToSign string
	Nonce     string
	Timestamp int64
}

// TimestampString 返回毫秒时间戳的十进制字符串（与签名变量一致）。
func (r *RenderResult) TimestampString() string {
	return strconv.FormatInt(r.Timestamp, 10)
}

// TryRender 用通道密钥与一次样例输入完整试渲染：变量→签名→请求体。
// 保存通道配置前调用，可在不发真实请求的前提下暴露全部字段级错误。
func (cfg *Config) TryRender(secrets map[string]string, in SendInput) (map[string]any, string, error) {
	res, err := cfg.Render(secrets, in)
	if err != nil {
		return nil, "", err
	}
	return res.Body, res.RawToSign, nil
}

// Render 执行一次完整渲染，返回请求体与签名明细。
func (cfg *Config) Render(secrets map[string]string, in SendInput) (*RenderResult, error) {
	consts := map[string]string{}
	for name, spec := range cfg.Constants {
		if spec.Secret {
			v, ok := secrets[name]
			if !ok {
				return nil, fmt.Errorf("%w: 常量 %q", ErrSecretNotSet, name)
			}
			consts[name] = v
		} else {
			consts[name] = spec.Value
		}
	}

	nonce := NewNonce()
	ts := timeNow().UnixMilli()
	vars, err := BuildVars(in, cfg.MobilePolicy, nonce, ts)
	if err != nil {
		// ErrInvalidMobile 已是哨兵错误，直接透传供编排层分类
		return nil, err
	}

	signer, err := sign.New(cfg.Sign.Strategy, cfg.Sign.Encoding)
	if err != nil {
		return nil, &RenderError{Stage: "sign", Err: fmt.Errorf("%w: %v", ErrRender, err)}
	}
	raw, err := sign.BuildRaw(cfg.Sign.Segments, vars)
	if err != nil {
		return nil, &RenderError{Stage: "sign", Err: fmt.Errorf("%w: %v", ErrRender, err)}
	}
	if cfg.Sign.Strategy != sign.StrategyNone {
		if cfg.Sign.SecretConst == "" {
			return nil, &RenderError{Stage: "sign", Err: fmt.Errorf("%w: 策略 %s 缺少 secret_const", ErrRender, cfg.Sign.Strategy)}
		}
		secretVal, ok := consts[cfg.Sign.SecretConst]
		if !ok {
			return nil, fmt.Errorf("%w: 签名密钥常量 %q", ErrSecretNotSet, cfg.Sign.SecretConst)
		}
		sig, err := signer.Sign(secretVal, raw)
		if err != nil {
			return nil, &RenderError{Stage: "sign", Err: fmt.Errorf("%w: %v", ErrRender, err)}
		}
		vars["sign"] = sig
	}

	body, err := mapping.BuildBody(cfg.BodyMappings, vars, consts)
	if err != nil {
		return nil, &RenderError{Stage: "mapping", Err: fmt.Errorf("%w: %v", ErrRender, err)}
	}
	headers := make(map[string]string, len(cfg.Request.Headers))
	for k, v := range cfg.Request.Headers {
		headers[k] = renderHeader(v, consts)
	}
	return &RenderResult{Body: body, Headers: headers, RawToSign: raw, Nonce: nonce, Timestamp: ts}, nil
}

// ResolveURL 把 ${base} 占位替换为 BaseURL；已是绝对地址时原样返回。
func (cfg *Config) ResolveURL() string {
	u := cfg.Request.URL
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return strings.ReplaceAll(u, "${base}", strings.TrimRight(cfg.Request.BaseURL, "/"))
}
