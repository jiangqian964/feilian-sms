// TR-11.1 系统设置管理：GET 明文/掩码回显、PUT 合并语义、清空与字段级校验。
package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"feilian-sms/internal/store"
)

// settingsView 是测试侧对 GET /api/settings 响应结构的镜像。
type settingsView struct {
	VerificationToken   string `json:"verification_token"`
	EncryptKeySet       bool   `json:"encrypt_key_set"`
	EncryptKeyMasked    string `json:"encrypt_key_masked"`
	WebhookPath         string `json:"webhook_path"`
	WebhookURL          string `json:"webhook_url"`
	PublicBaseURL       string `json:"public_base_url"`
	DownstreamTimeoutMS int    `json:"downstream_timeout_ms"`
	StalePendingMS      int    `json:"stale_pending_ms"`
	UpdatedAt           int64  `json:"updated_at"`
}

func getSettingsView(t *testing.T, srv *Server) settingsView {
	t.Helper()
	w := adminJSON(t, srv, http.MethodGet, "/api/settings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/settings = %d, body=%s", w.Code, w.Body.String())
	}
	assertNoStore(t, w)
	var v settingsView
	jdecode(t, w, &v)
	return v
}

func TestAdminGetSettings_Defaults(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	v := getSettingsView(t, srv)
	if v.VerificationToken != testToken {
		t.Fatalf("verification_token = %q, 期望明文 %q", v.VerificationToken, testToken)
	}
	if v.EncryptKeySet {
		t.Fatalf("encrypt_key_set = true, 期望 false")
	}
	if v.EncryptKeyMasked != "****" {
		t.Fatalf("encrypt_key_masked = %q, 期望 ****", v.EncryptKeyMasked)
	}
	if v.WebhookPath != testPath {
		t.Fatalf("webhook_path = %q", v.WebhookPath)
	}
	if v.WebhookURL != testPath {
		t.Fatalf("未配置基址时 webhook_url 应为相对路径，实际 %q", v.WebhookURL)
	}
	if v.DownstreamTimeoutMS != 2000 || v.StalePendingMS != 120000 {
		t.Fatalf("超时/补发阈值 = %d/%d", v.DownstreamTimeoutMS, v.StalePendingMS)
	}
}

func TestAdminGetSettings_WithKeyAndBase(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)
	saveSettings(t, st, store.Settings{
		VerificationToken:   testToken,
		EncryptKey:          "testkey123",
		WebhookPath:         testPath,
		PublicBaseURL:       "https://gw.corp.example:8080",
		DownstreamTimeoutMS: 2000,
		StalePendingMS:      120000,
	})

	w := adminJSON(t, srv, http.MethodGet, "/api/settings", nil)
	var v settingsView
	jdecode(t, w, &v)
	if !v.EncryptKeySet {
		t.Fatalf("encrypt_key_set = false, 期望 true")
	}
	if want := store.MaskSecret("testkey123"); v.EncryptKeyMasked != want {
		t.Fatalf("encrypt_key_masked = %q, 期望 %q", v.EncryptKeyMasked, want)
	}
	if want := "https://gw.corp.example:8080" + testPath; v.WebhookURL != want {
		t.Fatalf("webhook_url = %q, 期望 %q", v.WebhookURL, want)
	}
	if body := w.Body.String(); strings.Contains(body, "testkey123") {
		t.Fatalf("GET 响应泄露 Encrypt Key 明文: %s", body)
	}
}

func TestAdminPutSettings_MergeSemantics(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	// 仅改 webhook 路径：其余字段保持不变。
	w := adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{
		"webhook_path": "/wh2",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body=%s", w.Code, w.Body.String())
	}
	v := getSettingsView(t, srv)
	if v.WebhookPath != "/wh2" || v.WebhookURL != "/wh2" {
		t.Fatalf("路径未更新: %+v", v)
	}
	if v.VerificationToken != testToken {
		t.Fatalf("token 被意外覆盖: %q", v.VerificationToken)
	}
	if v.DownstreamTimeoutMS != 2000 || v.StalePendingMS != 120000 {
		t.Fatalf("阈值被意外修改: %+v", v)
	}

	// 空对象：全部不变。
	w = adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("空 PUT = %d", w.Code)
	}
	v = getSettingsView(t, srv)
	if v.WebhookPath != "/wh2" || v.VerificationToken != testToken {
		t.Fatalf("空 PUT 不应改动任何字段: %+v", v)
	}

	// 阈值与 token 修改。
	w = adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{
		"verification_token":    "new-token",
		"downstream_timeout_ms": 1500,
		"stale_pending_ms":      60000,
		"public_base_url":       "https://x.example",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body=%s", w.Code, w.Body.String())
	}
	v = getSettingsView(t, srv)
	if v.VerificationToken != "new-token" || v.DownstreamTimeoutMS != 1500 ||
		v.StalePendingMS != 60000 || v.PublicBaseURL != "https://x.example" {
		t.Fatalf("字段更新不符合预期: %+v", v)
	}
}

func TestAdminPutSettings_EncryptKeyLifecycle(t *testing.T) {
	dbPath := t.TempDir() + "/sms.db"
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	st, err := store.Open(dbPath, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cache, err := store.NewCache(st)
	if err != nil {
		t.Fatal(err)
	}
	saveSettings(t, st, store.Settings{VerificationToken: testToken, WebhookPath: testPath,
		DownstreamTimeoutMS: 2000, StalePendingMS: 120000})
	srv := buildServer(t, st, cache, nil)

	secret := "encrypt-secret-987654"
	w := adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{
		"encrypt_key": secret,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("设置 Encrypt Key = %d, body=%s", w.Code, w.Body.String())
	}
	if got := getSettingsView(t, srv); !got.EncryptKeySet || got.EncryptKeyMasked == "****" {
		t.Fatalf("设置后回显异常: %+v", got)
	}
	if err := st.Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertDBHasNoPlaintext(t, dbPath, secret)

	// 省略字段：保持不变。
	_ = adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{"webhook_path": testPath})
	if got := getSettingsView(t, srv); !got.EncryptKeySet {
		t.Fatalf("省略 encrypt_key 不应清空原值")
	}
	// 显式空串：同样视为不修改。
	_ = adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{"encrypt_key": ""})
	if got := getSettingsView(t, srv); !got.EncryptKeySet {
		t.Fatalf("空串 encrypt_key 不应清空原值")
	}

	// 显式清空。
	w = adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{
		"clear_encrypt_key": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("清空 = %d, body=%s", w.Code, w.Body.String())
	}
	if got := getSettingsView(t, srv); got.EncryptKeySet || got.EncryptKeyMasked != "****" {
		t.Fatalf("清空后回显异常: %+v", got)
	}
}

func TestAdminPutSettings_ConflictingClear(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	w := adminJSON(t, srv, http.MethodPut, "/api/settings", map[string]any{
		"encrypt_key": "something", "clear_encrypt_key": true,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("同时设置与清空 = %d, 期望 400", w.Code)
	}
	if e := decodeError(t, w); e.Code != "bad_request" || e.Field != "encrypt_key" {
		t.Fatalf("错误体 = %+v", e)
	}
}

func TestAdminPutSettings_FieldValidation(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"webhook 路径缺前导斜杠", map[string]any{"webhook_path": "feilian/x"}, "webhook_path"},
		{"webhook 路径为空格", map[string]any{"webhook_path": "/a b"}, "webhook_path"},
		{"基址协议非法", map[string]any{"public_base_url": "ftp://x"}, "public_base_url"},
		{"超时过小", map[string]any{"downstream_timeout_ms": 50}, "downstream_timeout_ms"},
		{"超时过大", map[string]any{"downstream_timeout_ms": 999999}, "downstream_timeout_ms"},
		{"补发阈值过小", map[string]any{"stale_pending_ms": 500}, "stale_pending_ms"},
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

	// 坏 JSON：400 且无 field。
	w := adminRaw(t, srv, http.MethodPut, "/api/settings", []byte("{not-json"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("坏 JSON = %d", w.Code)
	}
	if e := decodeError(t, w); e.Field != "" {
		t.Fatalf("坏 JSON 不应带 field, 实际 %q", e.Field)
	}
}

func TestAdminHealthAndNotFound(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	w := adminJSON(t, srv, http.MethodGet, "/api/health", nil)
	if w.Code != http.StatusOK || w.Body.String() != `{"status":"ok"}`+"\n" {
		t.Fatalf("/api/health = %d %q", w.Code, w.Body.String())
	}
	assertNoStore(t, w)

	w = adminJSON(t, srv, http.MethodGet, "/api/nope", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("未知路由 = %d, 期望 404", w.Code)
	}
	if e := decodeError(t, w); e.Code != "not_found" {
		t.Fatalf("未知路由错误体 = %+v", e)
	}
}
