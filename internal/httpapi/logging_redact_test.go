package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/logging"
	"feilian-sms/internal/service"
	"feilian-sms/internal/store"
)

// TR-14.2：在真实业务路径（握手/事件成功/幂等跳过/未绑定/回执/测试发送/
// 业务失败）上用 observer 捕获“实际落盘的日志内容”，断言任何级别都不含
// 完整手机号、验证码、通道密钥，也不出现签名字段或 40 位签名摘要。
func TestBusinessLogsNeverLeakSensitiveData(t *testing.T) {
	st, cache, sender := newTestEnv(t)

	// 自建示例通道（与 channelWithBinding 同构）以便拿到 channel ID 发回执。
	cfg := channel.BuiltinPresetConfig()
	cfg.Request.URL = "http://vendor.test/sms/send"
	rawCfg, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := st.CreateChannel(context.Background(), "demo-redact", "预置", string(rawCfg),
		map[string]string{"appSecret": "demo-app-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceBindings(context.Background(), []store.Binding{{
		SMSType: "code", ChannelID: ch.ID, TemplateCode: "TPL_CODE", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}

	core, logs := observer.New(zapcore.DebugLevel)
	logger := logging.NewWithCore(core)
	srv, err := NewServer(Deps{
		Settings: service.NewSettingsRuntime(cache),
		Forward:  service.NewForwardService(st, cache, sender).WithLogger(logger),
		Receipts: service.NewReceiptService(st, cache),
		Store:    st,
		Logger:   logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1) 握手。
	if w := do(srv, http.MethodPost, testPath, challengeBody(testToken, "ch-redact"),
		"10.0.0.1:5000", nil); w.Code != http.StatusOK {
		t.Fatalf("握手应 200，实际 %d", w.Code)
	}
	// 2) 事件成功：手机号 13812345678、验证码 482916。
	if w := do(srv, http.MethodPost, testPath,
		smsEvent("evt-redact-1", testToken, "code", "13812345678", []string{"482916", "5"}),
		"10.0.0.1:5000", nil); w.Code != http.StatusOK {
		t.Fatalf("事件应恒 200，实际 %d", w.Code)
	}
	// 3) 同 event_id 重放：Debug 级“跳过”日志。
	if w := do(srv, http.MethodPost, testPath,
		smsEvent("evt-redact-1", testToken, "code", "13812345678", []string{"482916", "5"}),
		"10.0.0.1:5000", nil); w.Code != http.StatusOK {
		t.Fatalf("重放应 200，实际 %d", w.Code)
	}
	// 4) 未绑定场景：手机号 13987654321，落 failed/unbound。
	if w := do(srv, http.MethodPost, testPath,
		smsEvent("evt-redact-2", testToken, "guest_wifi", "13987654321", []string{"wifi-pwd"}),
		"10.0.0.1:5000", nil); w.Code != http.StatusOK {
		t.Fatalf("未绑定事件应 200，实际 %d", w.Code)
	}
	// 5) 厂商回执（DELIVRD）。
	receipt := []byte(`{"smsId":"vendor-1","appSmsId":"evt-redact-1","status":"DELIVRD","statusMessage":"成功","seqNo":1}`)
	if w := do(srv, http.MethodPost, "/receipts/"+ch.ID, receipt, "203.0.113.7:3000", nil); w.Code != http.StatusOK {
		t.Fatalf("回执应 200，实际 %d: %s", w.Code, w.Body.String())
	}
	// 6) 管理端测试发送：手机号 13711112222、验证码 998877。
	payload := testSendRequest{
		TemplateCode: "TPL-CODE", SMSType: "code",
		CountryCode: "+86", MobileNumber: "13711112222", Mobile: "8613711112222",
		Params: []string{"998877"},
	}
	if w := adminJSON(t, srv, http.MethodPost, "/api/channels/"+ch.ID+"/test", payload); w.Code != http.StatusOK {
		t.Fatalf("测试发送应 200，实际 %d: %s", w.Code, w.Body.String())
	}
	// 7) 厂商业务失败分支：Warn 失败日志。
	sender.result = &channel.SendResult{Success: false, HTTPCode: 200, Message: "模板不存在"}
	if w := do(srv, http.MethodPost, testPath,
		smsEvent("evt-redact-3", testToken, "code", "13812345678", []string{"482916"}),
		"10.0.0.1:5000", nil); w.Code != http.StatusOK {
		t.Fatalf("业务失败事件应 200，实际 %d", w.Code)
	}

	// ---- 全量日志无泄漏断言（任何级别）----
	forbidden := []string{
		"13812345678", "13987654321", "13711112222", // 完整手机号
		"482916", "998877", "wifi-pwd", // 验证码/参数原值
		"demo-app-secret", // 通道密钥
	}
	sign40 := regexp.MustCompile(`[0-9a-f]{40}`)
	entries := logs.All()
	if len(entries) < 6 {
		t.Fatalf("覆盖的日志路径过少（%d 条），疑似主路径未打日志", len(entries))
	}
	for _, e := range entries {
		flat := e.Message + " " + fmt.Sprint(e.ContextMap())
		for _, secret := range forbidden {
			if strings.Contains(flat, secret) {
				t.Fatalf("级别 %s 日志泄漏敏感值 %q：%s", e.Level, secret, flat)
			}
		}
		if sign40.MatchString(flat) {
			t.Fatalf("日志出现疑似 40 位签名摘要：%s", flat)
		}
		for k := range e.ContextMap() {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "sign") || strings.Contains(lk, "secret") ||
				strings.Contains(lk, "token") || k == "params" {
				t.Fatalf("日志出现禁止字段 %q：%s", k, flat)
			}
		}
	}

	// ---- 正向：主路径结构化日志确实产生（不是静默通过）----
	var sawSuccess, sawSkipped, sawUnbound, sawTestSend bool
	for _, e := range entries {
		m := e.ContextMap()
		if e.Message == "短信已下发" && m["outcome"] == "success" {
			sawSuccess = true
		}
		if e.Message == "短信跳过未重复下发" && m["outcome"] == "skipped" {
			sawSkipped = true
		}
		if m["error_kind"] == "unbound" {
			sawUnbound = true
		}
		if e.Message == "测试发送成功" {
			sawTestSend = true
		}
	}
	for name, ok := range map[string]bool{
		"成功下发": sawSuccess, "幂等跳过": sawSkipped,
		"未绑定留痕": sawUnbound, "测试发送": sawTestSend,
	} {
		if !ok {
			t.Fatalf("缺少 %s 路径的结构化日志（共 %d 条）", name, len(entries))
		}
	}
}
