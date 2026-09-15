package channel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// 错误类型的 Error/Unwrap 是编排层 errors.Is/As 分类的基础，需显式锁定。
func TestRenderAndTransportErrorWrapping(t *testing.T) {
	render := &RenderError{Stage: "sign", Err: ErrRender}
	if render.Error() != ErrRender.Error() {
		t.Fatalf("RenderError.Error 应透传内部错误，实际 %q", render.Error())
	}
	if !errors.Is(render, ErrRender) {
		t.Fatal("RenderError.Unwrap 应支持 errors.Is(ErrRender)")
	}
	if render.Stage != "sign" {
		t.Fatalf("Stage 应保留，实际 %q", render.Stage)
	}

	te := &TransportError{Timeout: true, Err: context.DeadlineExceeded}
	if !errors.Is(te, context.DeadlineExceeded) {
		t.Fatal("TransportError.Unwrap 应支持 errors.Is(DeadlineExceeded)")
	}
	var asTE *TransportError
	if !errors.As(fmt.Errorf("包裹: %w", te), &asTE) || !asTE.Timeout {
		t.Fatal("errors.As 应还原 TransportError 且保留 Timeout 标记")
	}
}

// timeoutNetErr 实现 net.Error 且 Timeout()=true，模拟 http.Client 的超时错误。
type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o timeout" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return true }

func TestIsTimeoutClassification(t *testing.T) {
	if !isTimeout(context.DeadlineExceeded) {
		t.Fatal("DeadlineExceeded 必须判定为超时")
	}
	wrapped := fmt.Errorf("请求厂商失败: %w", timeoutNetErr{})
	if !isTimeout(wrapped) {
		t.Fatal("被包裹的 net.Error(timeout) 必须判定为超时")
	}
	if isTimeout(nil) {
		t.Fatal("nil 错误不是超时")
	}
	if isTimeout(errors.New("普通错误")) {
		t.Fatal("普通错误不应判为超时")
	}
	var _ net.Error = timeoutNetErr{}
}

func TestNewClientNonPositiveTimeoutDefaults(t *testing.T) {
	for _, ms := range []int{0, -100} {
		c := NewClient(ms)
		if c.http.Timeout != 2*time.Second {
			t.Fatalf("timeoutMS=%d 应回落到 2s，实际 %v", ms, c.http.Timeout)
		}
	}
	if got := NewClient(5000).http.Timeout; got != 5*time.Second {
		t.Fatalf("正值应原样生效，实际 %v", got)
	}
}

// normalizeMobile 各策略的错误分支（白盒直接调用）。
func TestNormalizeMobileErrors(t *testing.T) {
	cases := []struct {
		name   string
		in     SendInput
		policy string
	}{
		{"cc_prefix 国家码缺加号", SendInput{CountryCode: "86", MobileNumber: "13800000000"}, MobilePolicyCCPrefix},
		{"cc_prefix 国家码为空", SendInput{CountryCode: "", MobileNumber: "13800000000"}, MobilePolicyCCPrefix},
		{"strip_plus 手机号为空", SendInput{}, MobilePolicyStripPlus},
		{"raw 手机号为空", SendInput{}, MobilePolicyRaw},
		{"未知策略", SendInput{Mobile: "13800000000"}, "weird"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeMobile(tc.in, tc.policy); !errors.Is(err, ErrInvalidMobile) {
				t.Fatalf("应返回 ErrInvalidMobile，实际 %v", err)
			}
		})
	}
}

// 非 JSON 响应：按 HTTP 状态码判定成败且不报错（留痕由 RawBody 承担）。
func TestParseSendResultNonJSONBody(t *testing.T) {
	res, err := ParseSendResult(ResponseConfig{}, http.StatusBadGateway, []byte("Bad Gateway"))
	if err != nil {
		t.Fatalf("非 JSON 不应返回错误: %v", err)
	}
	if res.Success || res.HTTPCode != http.StatusBadGateway {
		t.Fatalf("非 2xx 非 JSON 应为失败，实际 %+v", res)
	}
	res, err = ParseSendResult(ResponseConfig{}, http.StatusOK, []byte("ok"))
	if err != nil || !res.Success {
		t.Fatalf("2xx 非 JSON 应视为成功，实际 %+v err=%v", res, err)
	}
}
