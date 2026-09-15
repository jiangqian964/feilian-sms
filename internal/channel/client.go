package channel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"feilian-sms/internal/channel/mapping"
)

// SendResult 是一次同步下发的判定结果。
type SendResult struct {
	Success  bool   // 厂商业务成功（SuccessPath 值 == SuccessValue）
	HTTPCode int    // HTTP 状态码
	VendorID string // 厂商消息 ID（MsgIDPath）
	Message  string // 厂商描述/错误信息
	RawBody  string // 原始响应（留痕，调用方负责脱敏后落库/日志）
}

// Client 是通用 HTTP 短信通道客户端（线程安全，随 http.Client）。
type Client struct {
	http *http.Client
}

// NewClient 构造客户端；timeoutMS 为单次下发总超时。
func NewClient(timeoutMS int) *Client {
	if timeoutMS <= 0 {
		timeoutMS = 2000
	}
	return &Client{http: &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond}}
}

// Send 渲染并下发一次短信，再按 ResponseConfig 判定结果（一步到位的便捷方法）。
func (c *Client) Send(ctx context.Context, cfg *Config, secrets map[string]string, in SendInput) (*SendResult, error) {
	rendered, err := cfg.Render(secrets, in)
	if err != nil {
		return nil, err
	}
	return c.Do(ctx, cfg, rendered)
}

// Do 用已渲染结果执行纯 HTTP 下发并判定响应；编排层可先调 cfg.Render
// 以区分 invalid_mobile/render 类错误，再调 Do 区分 timeout/network。
func (c *Client) Do(ctx context.Context, cfg *Config, rendered *RenderResult) (*SendResult, error) {
	payload, err := json.Marshal(rendered.Body)
	if err != nil {
		return nil, &RenderError{Stage: "mapping", Err: fmt.Errorf("%w: %v", ErrRender, err)}
	}

	url := cfg.ResolveURL()
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(cfg.Request.Method), url, bytes.NewReader(payload))
	if err != nil {
		return nil, &TransportError{Err: fmt.Errorf("构造请求失败: %w", err)}
	}
	if cfg.Request.ContentType != "" {
		req.Header.Set("Content-Type", cfg.Request.ContentType)
	}
	for k, v := range rendered.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &TransportError{Timeout: isTimeout(err), Err: fmt.Errorf("请求厂商失败: %w", err)}
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &TransportError{Timeout: isTimeout(err), Err: fmt.Errorf("读取厂商响应失败: %w", err)}
	}
	return ParseSendResult(cfg.Response, resp.StatusCode, respBytes)
}

// isTimeout 判断错误是否为上下文/HTTP 超时。
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne netError
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}

// netError 仅用于断言 net.Error 的 Timeout()。
type netError interface {
	Timeout() bool
}

// ParseSendResult 按 ResponseConfig 解析厂商响应（数字保持 json.Number 以便宽松比较）。
func ParseSendResult(rc ResponseConfig, httpCode int, body []byte) (*SendResult, error) {
	res := &SendResult{HTTPCode: httpCode, RawBody: string(body)}

	var decoded any
	if len(bytes.TrimSpace(body)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&decoded); err != nil {
			// 非 JSON 响应：HTTP 2xx 视为成功，否则失败但不抛错（保留留痕）
			res.Success = httpCode >= 200 && httpCode < 300
			return res, nil
		}
	}

	if rc.SuccessPath != "" {
		got, ok := mapping.GetPath(decoded, rc.SuccessPath)
		if ok {
			res.Success = mapping.LooseEqual(got, rc.SuccessValue)
		}
	} else {
		res.Success = httpCode >= 200 && httpCode < 300
	}
	if v, ok := mapping.GetString(decoded, rc.MsgIDPath); ok {
		res.VendorID = v
	}
	if v, ok := mapping.GetString(decoded, rc.MessagePath); ok {
		res.Message = v
	}
	return res, nil
}

// renderHeader 处理请求头占位（鉴权注入，密钥不落 config_json）：
//   - ${const:name}  → 常量/密钥明文，用于 "Bearer ${const:token}" 或静态头；
//   - ${constb64:name} → base64(常量)，用于 "Basic ${constb64:credential}"
//     （credential 以 user:password 形式存在 channel_secrets）。
func renderHeader(v string, secrets map[string]string) string {
	out := v
	for name, val := range secrets {
		out = strings.ReplaceAll(out, "${const:"+name+"}", val)
		out = strings.ReplaceAll(out, "${constb64:"+name+"}", base64.StdEncoding.EncodeToString([]byte(val)))
	}
	return out
}
