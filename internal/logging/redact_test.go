package logging

import (
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// NFR-3：日志中手机号保留前 3 后 2，其余以等长星号掩码；
// 任何夹带在中文句子/错误文本里的号码都必须被兜住。
func TestRedactStringMobile(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"纯手机号", "13812345678", "138******78"},
		{"带 86 前缀", "8613812345678", "86138******78"},
		{"带 +86 前缀", "+8613812345678", "+86138******78"},
		{"中文句子夹带", "向 13812345678 发送验证码", "向 138******78 发送验证码"},
		{"多个号码", "13812345678 和 13987654321", "138******78 和 139******21"},
		{"后边界为数字不算手机号", "138123456789", "138123456789"},
		{"前边界为数字不算手机号", "913812345678", "913812345678"},
		{"毫秒时间戳不被误伤", "ts=1700000000000", "ts=1700000000000"},
		{"空串", "", ""},
		{"无敏感内容", "飞连事件处理完成", "飞连事件处理完成"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactString(tc.in); got != tc.want {
				t.Fatalf("RedactString(%q) = %q，期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

// FR-13：按字段名识别密钥/签名/令牌/鉴权头等，整值替换为固定占位符；
// 识别必须按“词”匹配，避免 design 被 sign 误伤、id 类字段被牵连。
func TestRedactFieldByKey(t *testing.T) {
	secret := "demo-app-secret-value"
	sensitive := []string{
		"app_secret", "appSecret", "appsecret", "secret",
		"password", "passwd",
		"sign", "signature", "to_sign",
		"token", "verification_token",
		"authorization", "auth",
		"encrypt_key", "data_key", "secret_key",
		"credential",
	}
	for _, key := range sensitive {
		f := RedactField(zap.String(key, secret))
		if f.String != redactedPlaceholder {
			t.Fatalf("字段 %q 应整体脱敏，实际 %q", key, f.String)
		}
	}
}

// params（验证码按序数组）必须掩码；但 param_index（绑定里的下标数组）不是验证码，
// 不应被误伤，以免损失排障信息。
func TestRedactFieldParams(t *testing.T) {
	if got := RedactField(zap.Strings("params", []string{"482916", "5"})).String; got != redactedPlaceholder {
		t.Fatalf("params 应整体脱敏，实际 %q", got)
	}
	if got := RedactField(zap.String("param0", "482916")).String; got != redactedPlaceholder {
		t.Fatalf("param0 应整体脱敏，实际 %q", got)
	}
	if got := RedactField(zap.String("param_index", "[0 1]")).String; got != "[0 1]" {
		t.Fatalf("param_index 是下标配置不应掩码，实际 %q", got)
	}
}

// 非敏感字段：值原样保留；标识类字段不被误判；但值里夹带的手机号仍要被正则兜住。
func TestRedactFieldBenign(t *testing.T) {
	keep := map[string]string{
		"channel_id": "bdc0743b-420e-481f-9f76-efb2b4a9c6b8",
		"app_sms_id": "smoke-code-0001",
		"event_id":   "evt-0001",
		"design":     "原始 design 字段含 sign 子串也不该被当签名",
		"sms_type":   "code",
		"path":       "/feilian/sms/events",
	}
	for key, val := range keep {
		if got := RedactField(zap.String(key, val)).String; got != val {
			t.Fatalf("字段 %q 不应被改写，实际 %q（原值 %q）", key, got, val)
		}
	}
	if got := RedactField(zap.String("reason", "向 13812345678 下发失败")).String; got != "向 138******78 下发失败" {
		t.Fatalf("非敏感字段值中的手机号应被兜底脱敏，实际 %q", got)
	}
}

// zap.Error 与 Stringer 字段的值同样要过脱敏（错误文本可能夹带号码）。
func TestRedactFieldError(t *testing.T) {
	f := RedactField(zap.Error(errors.New("请求厂商失败: 号码 13812345678 超时")))
	if strings.Contains(f.String, "13812345678") {
		t.Fatalf("error 字段不应含完整手机号，实际 %q", f.String)
	}
	if !strings.Contains(f.String, "138******78") {
		t.Fatalf("error 字段应含掩码后手机号，实际 %q", f.String)
	}
}
