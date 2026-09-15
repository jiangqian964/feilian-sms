package feilian

import (
	"os"
	"path/filepath"
	"testing"
)

// examplesDir 是仓库根 testdata/feilian 目录（测试运行目录为 internal/feilian）。
const examplesDir = "../../testdata/feilian"

func readExample(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(examplesDir, name))
	if err != nil {
		t.Fatalf("读取样例 %s 失败: %v", name, err)
	}
	return raw
}

func TestExampleURLVerification(t *testing.T) {
	ch, ok, err := ParseChallenge(readExample(t, "url_verification.json"))
	if err != nil || !ok {
		t.Fatalf("url_verification 样例解析失败: ok=%v err=%v", ok, err)
	}
	if ch.Challenge != "smoke-challenge-0001" || ch.Token != "smoke-token-001" {
		t.Fatalf("握手字段不匹配: %+v", ch)
	}
}

func TestExampleSMSEnvelopes(t *testing.T) {
	cases := []struct {
		file      string
		eventID   string
		smsType   string
		mobile    string
		paramsLen int
	}{
		{"event_sms_code.json", "smoke-code-0001", "code", "+8613800000056", 2},
		{"event_sms_guest_wifi.json", "smoke-guest-0001", "guest_wifi", "+8613900000057", 3},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			env, err := ParseEnvelope(readExample(t, tc.file))
			if err != nil {
				t.Fatalf("事件样例解析失败: %v", err)
			}
			if env.Header.EventID != tc.eventID || env.Header.EventType != "notify.v1.sms" {
				t.Fatalf("事件头不匹配: %+v", env.Header)
			}
			if len(env.Data.Events) != 1 {
				t.Fatalf("应含 1 个事件，实际 %d", len(env.Data.Events))
			}
			o := env.Data.Events[0].Object
			if o.SMSType != tc.smsType || o.Mobile != tc.mobile || len(o.Params) != tc.paramsLen {
				t.Fatalf("短信对象不匹配: %+v", o)
			}
		})
	}
}
