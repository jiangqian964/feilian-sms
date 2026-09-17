package feilian

import (
	"encoding/json"
	"strings"
	"testing"
)

// guest_wifi 文档样例（飞连「短信通知 notify.v1.sms」数据示例逐字固化）。
const guestWifiEnvelope = `{
  "schema": "1.0",
  "header": {
    "event_id": "e09288e2-a1b3-4b38-84a8-3c673725xxxx",
    "token": "token-test",
    "create_time": "1740385174957",
    "event_type": "notify.v1.sms",
    "app_id": "897957767eda448e9e3c53c6a51dxxxx"
  },
  "data": {
    "events": [
      {
        "object": {
          "country_code": "+86",
          "mobile_number": "133xxxx3333",
          "mobile": "+86133xxxx3333",
          "sms_type": "guest_wifi",
          "template": "Guest Wi-Fi Networks: %s, Username: %s, Password: %s",
          "params": ["Test Wi-Fi", "+86133xxxx3333", "123456"],
          "expired_time": 1744615987
        }
      }
    ]
  }
}`

const codeEnvelope = `{
  "schema": "1.0",
  "header": {
    "event_id": "evt-code-0001",
    "token": "vt-abc",
    "create_time": "1740385174957",
    "event_type": "notify.v1.sms",
    "app_id": "app-1"
  },
  "data": {
    "events": [
      {
        "object": {
          "country_code": "+86",
          "mobile_number": "13800001111",
          "mobile": "+8613800001111",
          "sms_type": "code",
          "template": "验证码 %s，5 分钟有效",
          "params": ["654321"],
          "expired_time": 1744615999
        }
      }
    ]
  }
}`

func TestParseEnvelopeGuestWifi(t *testing.T) {
	env, err := ParseEnvelope([]byte(guestWifiEnvelope))
	if err != nil {
		t.Fatalf("解析 guest_wifi 事件失败: %v", err)
	}
	if env.Schema != "1.0" {
		t.Errorf("schema = %q", env.Schema)
	}
	h := env.Header
	if h.EventID != "e09288e2-a1b3-4b38-84a8-3c673725xxxx" ||
		h.Token != "token-test" ||
		h.CreateTime != "1740385174957" ||
		h.EventType != "notify.v1.sms" ||
		h.AppID != "897957767eda448e9e3c53c6a51dxxxx" {
		t.Errorf("header 断言失败: %#v", h)
	}
	if len(env.Data.Events) != 1 {
		t.Fatalf("events 数量 = %d", len(env.Data.Events))
	}
	o := env.Data.Events[0].Object
	if o.CountryCode != "+86" ||
		o.MobileNumber != "133xxxx3333" ||
		o.Mobile != "+86133xxxx3333" ||
		o.SMSType != "guest_wifi" {
		t.Errorf("号码/类型断言失败: %#v", o)
	}
	if o.Template != "Guest Wi-Fi Networks: %s, Username: %s, Password: %s" {
		t.Errorf("template = %q", o.Template)
	}
	if len(o.Params) != 3 || o.Params[0] != "Test Wi-Fi" || o.Params[2] != "123456" {
		t.Errorf("params 断言失败: %#v", o.Params)
	}
	if o.ExpiredTime != 1744615987 {
		t.Errorf("expired_time = %d", o.ExpiredTime)
	}
}

func TestParseEnvelopeCode(t *testing.T) {
	env, err := ParseEnvelope([]byte(codeEnvelope))
	if err != nil {
		t.Fatalf("解析 code 事件失败: %v", err)
	}
	o := env.Data.Events[0].Object
	if o.SMSType != "code" || len(o.Params) != 1 || o.Params[0] != "654321" {
		t.Errorf("code 对象断言失败: %#v", o)
	}
}

func TestParseEnvelopeErrors(t *testing.T) {
	base := func(m map[string]any) []byte {
		b, _ := json.Marshal(m)
		return b
	}
	valid := map[string]any{
		"schema": "1.0",
		"header": map[string]any{
			"event_id":    "e1",
			"token":       "tok",
			"create_time": "1740385174957",
			"event_type":  "notify.v1.sms",
			"app_id":      "a1",
		},
		"data": map[string]any{
			"events": []any{map[string]any{
				"object": map[string]any{
					"country_code":  "+86",
					"mobile_number": "13800001111",
					"mobile":        "+8613800001111",
					"sms_type":      "code",
					"template":      "%s",
					"params":        []string{"1"},
				},
			}},
		},
	}

	tests := []struct {
		name    string
		mutate  func(m map[string]any)
		raw     string
		wantErr string
	}{
		{name: "坏 JSON", raw: "{not-json", wantErr: "JSON"},
		{name: "缺 schema", wantErr: "schema", mutate: func(m map[string]any) { delete(m, "schema") }},
		{name: "缺 event_id", wantErr: "event_id", mutate: func(m map[string]any) {
			h := m["header"].(map[string]any)
			delete(h, "event_id")
		}},
		{name: "缺 token", wantErr: "token", mutate: func(m map[string]any) {
			h := m["header"].(map[string]any)
			delete(h, "token")
		}},
		{name: "events 为空", wantErr: "events", mutate: func(m map[string]any) {
			m["data"] = map[string]any{"events": []any{}}
		}},
		{name: "缺 object", wantErr: "object", mutate: func(m map[string]any) {
			m["data"] = map[string]any{"events": []any{map[string]any{}}}
		}},
		{name: "缺 sms_type", wantErr: "sms_type", mutate: func(m map[string]any) {
			obj := m["data"].(map[string]any)["events"].([]any)[0].(map[string]any)["object"].(map[string]any)
			delete(obj, "sms_type")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw []byte
			if tt.raw != "" {
				raw = []byte(tt.raw)
			} else {
				m := cloneMap(valid)
				if tt.mutate != nil {
					tt.mutate(m)
				}
				raw = base(m)
			}
			_, err := ParseEnvelope(raw)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("期望错误含 %q，实际 %v", tt.wantErr, err)
			}
		})
	}
}

func TestParseChallenge(t *testing.T) {
	raw := []byte(`{"challenge":"chal-123","token":"tok","type":"url_verification"}`)
	ch, ok, err := ParseChallenge(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("应识别为 url_verification")
	}
	if ch.Challenge != "chal-123" || ch.Token != "tok" || ch.Type != "url_verification" {
		t.Errorf("challenge 断言失败: %#v", ch)
	}

	event := []byte(guestWifiEnvelope)
	if _, ok, err := ParseChallenge(event); err != nil || ok {
		t.Fatalf("普通事件不应识别为 challenge: ok=%v err=%v", ok, err)
	}

	if _, ok, err := ParseChallenge([]byte(`{"type":"url_verification"}`)); err == nil {
		t.Fatal("缺 challenge 值应报错")
	} else if !ok {
		t.Fatal("类型是 url_verification 时 ok 应为 true")
	}
}

func TestExtractEncrypted(t *testing.T) {
	enc, ok, err := ExtractEncrypted([]byte(`{"encrypt":"AAAA=="}`))
	if err != nil || !ok || enc != "AAAA==" {
		t.Fatalf("加密信封识别失败: enc=%q ok=%v err=%v", enc, ok, err)
	}
	if _, ok, err := ExtractEncrypted([]byte(guestWifiEnvelope)); err != nil || ok {
		t.Fatalf("明文事件不应识别为加密信封: ok=%v err=%v", ok, err)
	}
	if _, ok, err := ExtractEncrypted([]byte(`{"encrypt":""}`)); err != nil || ok {
		t.Fatalf("空 encrypt 不应识别: ok=%v err=%v", ok, err)
	}
}

func TestTokenEqual(t *testing.T) {
	if !TokenEqual("abc", "abc") {
		t.Error("相同 token 应通过")
	}
	if TokenEqual("abc", "abd") {
		t.Error("不同 token 必须拒绝")
	}
	if TokenEqual("abc", "") {
		t.Error("期望 token 为空必须拒绝")
	}
}

func TestParseEventHeader(t *testing.T) {
	// 非短信事件：object 为任意结构，头部仍可被轻量解析（不触发短信校验）。
	other := `{"schema":"1.0","header":{"event_id":"evt-x","token":"tok",
"event_type":"device.v1.thing","app_id":"app"},"data":{"events":[{"object":{"foo":1}}]}}`
	h, err := ParseEventHeader([]byte(other))
	if err != nil {
		t.Fatalf("非短信事件头部解析失败: %v", err)
	}
	if h.Header.Token != "tok" || h.Header.EventType != "device.v1.thing" || h.Header.EventID != "evt-x" {
		t.Fatalf("头部字段异常: %+v", h.Header)
	}
	// 正常短信信封头部同样可解析。
	h, err = ParseEventHeader([]byte(codeEnvelope))
	if err != nil {
		t.Fatalf("短信事件头部解析失败: %v", err)
	}
	if h.Header.EventType != "notify.v1.sms" || h.Header.Token != "vt-abc" {
		t.Fatalf("头部字段异常: %+v", h.Header)
	}

	cases := map[string]string{
		"空体":            ``,
		"坏 JSON":        `{bad`,
		"缺少 schema":     `{"header":{"token":"t","event_type":"x"}}`,
		"不支持 schema":    `{"schema":"2.0","header":{"token":"t","event_type":"x"}}`,
		"缺少 token":      `{"schema":"1.0","header":{"event_type":"x"}}`,
		"缺少 event_type": `{"schema":"1.0","header":{"token":"t"}}`,
	}
	for name, body := range cases {
		if _, err := ParseEventHeader([]byte(body)); err == nil {
			t.Fatalf("%s：应返回错误", name)
		}
	}
}

func cloneMap(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}
