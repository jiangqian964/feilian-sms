package channel

import (
	"crypto/sha1"
	"encoding/hex"
	"testing"
)

func TestBuiltinPresetRenderAndSign(t *testing.T) {
	cfg := BuiltinPresetConfig()
	if cfg.ResolveURL() != "https://sms.example.com:1443/sms/send" {
		t.Fatalf("预置 URL 错误: %s", cfg.ResolveURL())
	}
	if cfg.MobilePolicy != MobilePolicyCCPrefix {
		t.Fatalf("预置手机号策略错误: %s", cfg.MobilePolicy)
	}
	if cfg.Receipt.DeliveredValue != "DELIVRD" {
		t.Fatalf("回执成功值应为 DELIVRD: %s", cfg.Receipt.DeliveredValue)
	}

	secrets := map[string]string{"appSecret": "demo-app-secret"}
	res, err := cfg.Render(secrets, sampleInput())
	if err != nil {
		t.Fatalf("预置模板必须可直接试渲染: %v", err)
	}

	// 用与 T4 JDK 向量相同的算法独立重算签名，验证引擎端到端正确
	raw := "timestamp=" + res.TimestampString() + "&nonce=" + res.Nonce + "&signData=evt-0001"
	sum := sha1.Sum(append([]byte("demo-app-secret"), []byte(raw)...))
	want := hex.EncodeToString(sum[:])

	signVal, _ := res.Body["sign"].(string)
	if signVal != want {
		t.Fatalf("预置模板签名不匹配: got %s want %s", signVal, want)
	}
	if len(signVal) != 40 {
		t.Fatalf("SHA1 hex 应为 40 位: %d", len(signVal))
	}

	// orgCode 与 appCode 均未填也不影响渲染（值为空串）
	if res.Body["orgCode"] != "" {
		t.Fatalf("orgCode 预置应为空串待填: %v", res.Body["orgCode"])
	}
}

func TestBuiltinPresetMissingSecretFails(t *testing.T) {
	cfg := BuiltinPresetConfig()
	if _, err := cfg.Render(map[string]string{}, sampleInput()); err == nil {
		t.Fatal("缺少 appSecret 应渲染失败，便于保存/下发前暴露配置缺口")
	}
}
