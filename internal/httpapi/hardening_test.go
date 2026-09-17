// 质量审计修复回归：回执全局 token 鉴权（#3）、非短信事件先鉴权（#5 另见
// server_test.go）、panic 恢复统一 500（#6）、设置项回执 token 与保留前缀校验
// （#13/#17）、通道更新孤儿密钥清理（#14）。
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/store"
)

// enableReceiptToken 直接落库启用全局回执 token，并等待缓存视图可见。
func enableReceiptToken(t *testing.T, st *store.Store, cache *store.Cache, token string) {
	t.Helper()
	s := cache.Current().Settings
	s.ReceiptAuthToken = token
	saveSettings(t, st, s)
}

func TestReceiptAuthToken(t *testing.T) {
	st, cache, sender := newTestEnv(t)

	cfg := channel.BuiltinPresetConfig()
	raw, _ := json.Marshal(cfg)
	ch, err := st.CreateChannel(context.Background(), "demo", "预置", string(raw),
		map[string]string{"appSecret": "demo-app-secret"})
	if err != nil {
		t.Fatal(err)
	}
	srv := buildServer(t, st, cache, sender)

	receipt := []byte(`{"smsId":"vendor-1","appSmsId":"evt-ok-1","status":"DELIVRD","statusMessage":"成功","seqNo":1}`)

	// 未配置 token：保持放行（内网隔离部署的向后兼容语义）。
	if w := do(srv, http.MethodPost, "/receipts/"+ch.ID, receipt, "203.0.113.7:3000", nil); w.Code != http.StatusOK {
		t.Fatalf("未配置回执 token 时应放行，实际 %d: %s", w.Code, w.Body.String())
	}

	const secret = "rcpt-secret-token-0123456789"
	enableReceiptToken(t, st, cache, secret)

	// 缺失 token：401，且鉴权先于通道查找（不存在的通道同样是 401 而非 404）。
	if w := do(srv, http.MethodPost, "/receipts/missing-id", receipt, "203.0.113.7:3000", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("缺失回执 token 应 401（且先于 404），实际 %d", w.Code)
	}
	if w := do(srv, http.MethodPost, "/receipts/"+ch.ID, receipt, "203.0.113.7:3000", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("缺失回执 token 应 401，实际 %d", w.Code)
	}
	// 错误 token（头与查询参数两种携带方式）。
	if w := do(srv, http.MethodPost, "/receipts/"+ch.ID, receipt, "203.0.113.7:3000",
		map[string]string{"X-Receipt-Token": "wrong"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("错误回执 token 头应 401，实际 %d", w.Code)
	}
	if w := do(srv, http.MethodPost, "/receipts/"+ch.ID+"?token=wrong", receipt, "203.0.113.7:3000", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("错误回执查询 token 应 401，实际 %d", w.Code)
	}

	// 正确 token（头）：正常处理并返回厂商 ACK。
	if w := do(srv, http.MethodPost, "/receipts/"+ch.ID, receipt, "203.0.113.7:3000",
		map[string]string{"X-Receipt-Token": secret}); w.Code != http.StatusOK ||
		w.Body.String() != `{"status":0,"message":"success"}` {
		t.Fatalf("正确回执 token 头应 200 并 ACK，实际 %d: %s", w.Code, w.Body.String())
	}
	// 正确 token（查询参数）。
	if w := do(srv, http.MethodPost, "/receipts/"+ch.ID+"?token="+secret, receipt,
		"203.0.113.7:3000", nil); w.Code != http.StatusOK {
		t.Fatalf("正确回执查询 token 应 200，实际 %d: %s", w.Code, w.Body.String())
	}
}

func TestRecoverMiddlewarePanic500(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)
	// 仅测试期注册一个必然 panic 的管理路由。
	srv.AdminMux().HandleFunc("GET /__panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	w := adminJSON(t, srv, http.MethodGet, "/api/__panic", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应被恢复并回 500，实际 %d", w.Code)
	}
	if e := decodeError(t, w); e.Code != "internal_error" {
		t.Fatalf("500 必须为统一错误体，实际 %+v（%s）", e, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "boom") || strings.Contains(strings.ToLower(w.Body.String()), "goroutine") {
		t.Fatalf("500 响应不得泄露 panic 内容或堆栈: %s", w.Body.String())
	}

	// panic 只影响当次请求：中间件为每请求独立 defer，后续请求仍正常。
	if w2 := adminJSON(t, srv, http.MethodGet, "/api/health", nil); w2.Code != http.StatusOK {
		t.Fatalf("panic 恢复后后续请求应正常，实际 %d", w2.Code)
	}
}

func TestAdminPutSettings_ReceiptAuthTokenLifecycle(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	// 默认未设置：set=false、掩码占位。
	if v := getSettingsView(t, srv); v.ReceiptAuthTokenSet {
		t.Fatalf("默认回执 token 不应为已设置: %+v", v)
	}

	token := "rcpt-token-abcdef1234567890"
	w := adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{
		"receipt_auth_token": token,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("设置回执 token = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ReceiptAuthTokenSet    bool   `json:"receipt_auth_token_set"`
		ReceiptAuthTokenMasked string `json:"receipt_auth_token_masked"`
	}
	jdecode(t, w, &resp)
	if !resp.ReceiptAuthTokenSet || resp.ReceiptAuthTokenMasked == "****" {
		t.Fatalf("设置后回显异常: %+v", resp)
	}
	if strings.Contains(w.Body.String(), token) {
		t.Fatalf("设置响应泄露回执 token 明文: %s", w.Body.String())
	}
	if got := srv.deps.Settings.ReceiptAuthToken(); got != token {
		t.Fatalf("运行时视图回执 token = %q", got)
	}

	// 省略字段：不修改；空串同样不修改。
	_ = adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{"stale_pending_ms": 60000})
	if got := srv.deps.Settings.ReceiptAuthToken(); got != token {
		t.Fatalf("省略回执 token 不应改变原值，实际 %q", got)
	}
	_ = adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{"receipt_auth_token": ""})
	if got := srv.deps.Settings.ReceiptAuthToken(); got != token {
		t.Fatalf("空串回执 token 不应清空原值，实际 %q", got)
	}

	// 显式清空。
	w = adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{
		"clear_receipt_auth_token": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("清空回执 token = %d, body=%s", w.Code, w.Body.String())
	}
	jdecode(t, w, &resp)
	if resp.ReceiptAuthTokenSet || resp.ReceiptAuthTokenMasked != "****" {
		t.Fatalf("清空后回显异常: %+v", resp)
	}
	if got := srv.deps.Settings.ReceiptAuthToken(); got != "" {
		t.Fatalf("清空后运行时 token 应为空，实际 %q", got)
	}
}

func TestAdminPutSettings_ReceiptTokenValidation(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"回执 token 过短", map[string]any{"receipt_auth_token": "short"}, "receipt_auth_token"},
		{"回执 token 含空白", map[string]any{"receipt_auth_token": "aaaaaaaaaaaaaaaa b"}, "receipt_auth_token"},
		{"设置与清空互斥", map[string]any{
			"receipt_auth_token": "aaaaaaaaaaaaaaaa", "clear_receipt_auth_token": true,
		}, "receipt_auth_token"},
		{"webhook 占用 /api 前缀", map[string]any{"webhook_path": "/api/hook"}, "webhook_path"},
		{"webhook 占用 /receipts 前缀", map[string]any{"webhook_path": "/receipts/x"}, "webhook_path"},
		{"webhook 占用 /health", map[string]any{"webhook_path": "/health"}, "webhook_path"},
		{"webhook 路径含查询符", map[string]any{"webhook_path": "/hook?x=1"}, "webhook_path"},
		{"webhook 路径含片段符", map[string]any{"webhook_path": "/hook#frag"}, "webhook_path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := adminJSON(t, srv, http.MethodPut, "/api/settings", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d, 期望 400, body=%s", w.Code, w.Body.String())
			}
			if e := decodeError(t, w); e.Code != "bad_request" || e.Field != tc.field {
				t.Fatalf("错误体 = %+v, 期望 field=%s", e, tc.field)
			}
		})
	}

	// 默认 webhook 路径与其他以 /receiptsxxx 开头但非保留前缀的路径不受影响。
	w := adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{
		"webhook_path": "/receipts-callback/hook",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("非保留前缀路径应允许，实际 %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminChannelUpdateOrphanSecretCleanup(t *testing.T) {
	dbPath, st, cache, sender := newTestEnvAt(t)
	srv := buildServer(t, st, cache, sender)
	ch := mustCreateChannel(t, srv, "带密钥通道", signNoneConfig())

	// 第一步：加入 appSecret secret 常量并写入密钥。
	cfgWithSecret := signNoneConfig()
	cfgWithSecret.Constants["appSecret"] = channel.ConstantSpec{Secret: true}
	w := adminJSON(t, srv, http.MethodPut, "/api/channels/"+ch.ID, channelRequest{
		Name:    "带密钥通道",
		Config:  rawConfig(t, cfgWithSecret),
		Secrets: map[string]string{"appSecret": "s3cr3et-appSecret-99"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("写入密钥更新 = %d, body=%s", w.Code, w.Body.String())
	}
	secrets, err := st.GetChannelSecrets(context.Background(), ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := secrets["appSecret"]; got != "s3cr3et-appSecret-99" {
		t.Fatalf("密钥未落库: %q", got)
	}

	// 第二步：配置中移除该 secret 常量（signNoneConfig 不含 appSecret）。
	w = adminJSON(t, srv, http.MethodPut, "/api/channels/"+ch.ID,
		createChannelReq("带密钥通道", signNoneConfig(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("移除密钥声明更新 = %d, body=%s", w.Code, w.Body.String())
	}
	var view channelResponse
	jdecode(t, w, &view)
	if _, present := view.SecretsMasked["appSecret"]; present {
		t.Fatalf("密钥常量移除后视图不应再回显: %+v", view.SecretsMasked)
	}
	secrets, err = st.GetChannelSecrets(context.Background(), ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := secrets["appSecret"]; present {
		t.Fatal("密钥常量移除后旧密文必须作为孤儿删除，防止被同名常量复活")
	}
	if err := st.Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertDBHasNoPlaintext(t, dbPath, "s3cr3et-appSecret-99")
}
