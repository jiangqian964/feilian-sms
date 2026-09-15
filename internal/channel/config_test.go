package channel

import (
	"encoding/json"
	"strings"
	"testing"

	"feilian-sms/internal/channel/mapping"
	"feilian-sms/internal/channel/sign"
)

func saltSegments() []sign.Segment {
	return []sign.Segment{
		{Kind: sign.SegmentLiteral, Value: "timestamp="},
		{Kind: sign.SegmentVariable, Value: "timestamp"},
		{Kind: sign.SegmentLiteral, Value: "&nonce="},
		{Kind: sign.SegmentVariable, Value: "nonce"},
		{Kind: sign.SegmentLiteral, Value: "&signData="},
		{Kind: sign.SegmentVariable, Value: "appSmsId"},
	}
}

func validConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{
		Request: RequestConfig{Method: "POST", URL: "${base}/sms/send", ContentType: "application/json"},
		Constants: map[string]ConstantSpec{
			"appCode":   {Value: "DEMOAPP", Secret: false},
			"orgCode":   {Value: "100001", Secret: false},
			"appSecret": {Secret: true},
		},
		BodyMappings: []mapping.Mapping{
			{Target: "appCode", SourceType: mapping.SourceConst, Source: "appCode", ValueType: mapping.ValueString},
			{Target: "appSmsId", SourceType: mapping.SourceVariable, Source: "appSmsId", ValueType: mapping.ValueString},
			{Target: "mobile", SourceType: mapping.SourceVariable, Source: "mobile", ValueType: mapping.ValueString},
			{Target: "nonce", SourceType: mapping.SourceVariable, Source: "nonce", ValueType: mapping.ValueString},
			{Target: "timestamp", SourceType: mapping.SourceVariable, Source: "timestamp", ValueType: mapping.ValueNumber},
			{Target: "orgCode", SourceType: mapping.SourceConst, Source: "orgCode", ValueType: mapping.ValueString},
			{Target: "params", SourceType: mapping.SourceVariable, Source: "params", ValueType: mapping.ValueRaw},
			{Target: "templateCode", SourceType: mapping.SourceVariable, Source: "templateCode", ValueType: mapping.ValueString},
			{Target: "sign", SourceType: mapping.SourceVariable, Source: "sign", ValueType: mapping.ValueString},
		},
		Sign: SignConfig{
			Strategy:    sign.StrategySHA1Salt,
			SecretConst: "appSecret",
			Segments:    saltSegments(),
		},
		Response: ResponseConfig{
			SuccessPath: "status", SuccessValue: "0",
			MsgIDPath: "data.id", MessagePath: "message",
		},
		MobilePolicy: MobilePolicyCCPrefix,
	}
}

func validConfigJSON(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(validConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseConfigDefaultsAndRoundTrip(t *testing.T) {
	cfg, err := ParseConfig(validConfigJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Request.Method != "POST" || cfg.Request.ContentType != "application/json" {
		t.Fatalf("请求配置异常: %#v", cfg.Request)
	}
	if len(cfg.BodyMappings) != 9 {
		t.Fatalf("应有 9 条映射，实际 %d", len(cfg.BodyMappings))
	}
}

func TestParseConfigBadJSON(t *testing.T) {
	if _, err := ParseConfig("{not json"); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestParseConfigMethodNormalized(t *testing.T) {
	raw := validConfig(t)
	raw.Request.Method = " put "
	cfg, err := ParseConfig(mustJSON(t, raw))
	if err != nil {
		t.Fatalf("小写带空格的方法应被归一化接受: %v", err)
	}
	if cfg.Request.Method != "PUT" {
		t.Fatalf("method 应归一化为 PUT，实际 %q", cfg.Request.Method)
	}
}

func TestParseConfigMethodRejected(t *testing.T) {
	for _, bad := range []string{"P", "DELETE", "请 求"} {
		raw := validConfig(t)
		raw.Request.Method = bad
		if _, err := ParseConfig(mustJSON(t, raw)); err == nil {
			t.Fatalf("非法 method %q 必须被拒绝", bad)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTryRenderOK(t *testing.T) {
	cfg := validConfig(t)
	body, rawToSign, err := cfg.TryRender(map[string]string{"appSecret": "demo-app-secret"}, sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if body["appCode"] != "DEMOAPP" || body["orgCode"] != "100001" {
		t.Fatalf("常量未注入: %#v", body)
	}
	if body["sign"] == "" {
		t.Fatal("sign 变量必须已生成")
	}
	if !strings.Contains(rawToSign, "signData=evt-0001") {
		t.Fatalf("待签名原文错误: %q", rawToSign)
	}
}

func TestTryRenderMissingSecret(t *testing.T) {
	cfg := validConfig(t)
	if _, _, err := cfg.TryRender(map[string]string{}, sampleInput()); err == nil {
		t.Fatal("签名密钥常量缺失应报错")
	}
}

func TestTryRenderBadMapping(t *testing.T) {
	cfg := validConfig(t)
	cfg.BodyMappings = append(cfg.BodyMappings, mapping.Mapping{
		Target: "extra", SourceType: mapping.SourceVariable, Source: "nope", ValueType: mapping.ValueString,
	})
	_, _, err := cfg.TryRender(map[string]string{"appSecret": "s"}, sampleInput())
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("非法映射应在保存前被发现，实际 %v", err)
	}
}

func TestTryRenderUnknownStrategy(t *testing.T) {
	cfg := validConfig(t)
	cfg.Sign.Strategy = "md5"
	_, _, err := cfg.TryRender(map[string]string{"appSecret": "s"}, sampleInput())
	if err == nil || !strings.Contains(err.Error(), "md5") {
		t.Fatalf("未知签名策略应报错，实际 %v", err)
	}
}
