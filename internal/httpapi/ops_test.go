package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"go.uber.org/zap"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/service"
)

// ---- TR-10.3：health 探针 no-store ----

func TestHealthNoStore(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender, "10.0.0.0/8")

	// 即使配置了管理端 CIDR，/health 对任意直连来源开放。
	w := do(srv, http.MethodGet, "/health", nil, "192.168.9.9:4000", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/health 应 200，实际 %d", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("/health 必须带 no-store，实际 %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["status"] != "ok" {
		t.Fatalf("/health 响应体异常: %s", w.Body.String())
	}
}

// ---- TR-10.4：管理端 CIDR 只信直连 RemoteAddr ----

func TestAdminCIDRGuard(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender, "10.0.0.0/8")
	srv.AdminMux().HandleFunc("GET /__probe", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// 白名单直连：放行。
	if w := do(srv, http.MethodGet, "/api/__probe", nil, "10.2.3.4:5000", nil); w.Code != http.StatusOK {
		t.Fatalf("白名单直连应放行，实际 %d", w.Code)
	}
	// 非白名单直连：即使伪造 X-Forwarded-For 为白名单地址也拒绝（XFF 不可信）。
	w := do(srv, http.MethodGet, "/api/__probe", nil, "192.168.1.1:5000",
		map[string]string{"X-Forwarded-For": "10.2.3.4"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("非白名单直连应 403 且 XFF 不得生效，实际 %d", w.Code)
	}
	// 白名单直连：携带外网 XFF 不影响放行。
	if w := do(srv, http.MethodGet, "/api/__probe", nil, "10.2.3.4:5000",
		map[string]string{"X-Forwarded-For": "192.168.1.1"}); w.Code != http.StatusOK {
		t.Fatalf("白名单直连应无视 XFF 放行，实际 %d", w.Code)
	}
}

func TestAdminCIDREmptyAllowsAll(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)
	srv.AdminMux().HandleFunc("GET /__probe", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	if w := do(srv, http.MethodGet, "/api/__probe", nil, "8.8.8.8:5000", nil); w.Code != http.StatusOK {
		t.Fatalf("未配置 CIDR 时不应做网络层限制，实际 %d", w.Code)
	}
}

func TestNewServerRejectsBadCIDR(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	_, err := NewServer(Deps{
		Settings:   service.NewSettingsRuntime(cache),
		Forward:    service.NewForwardService(st, cache, sender),
		Receipts:   service.NewReceiptService(st, cache),
		AdminCIDRs: []string{"not-a-cidr"},
		Logger:     zap.NewNop(),
	})
	if err == nil {
		t.Fatal("非法 CIDR 必须在装配期报错")
	}
}

// ---- 厂商回执路由 ----

func TestReceiptRoutes(t *testing.T) {
	st, cache, sender := newTestEnv(t)

	cfg := channel.BuiltinPresetConfig()
	raw, _ := json.Marshal(cfg)
	ch, err := st.CreateChannel(context.Background(), "demo", "预置", string(raw),
		map[string]string{"appSecret": "demo-app-secret"})
	if err != nil {
		t.Fatal(err)
	}
	srv := buildServer(t, st, cache, sender)

	// 示例厂商 DELIVRD 样例：200 + 精确 ACK + JSON/no-store。
	receipt := []byte(`{"smsId":"vendor-1","appSmsId":"evt-ok-1","status":"DELIVRD","statusMessage":"成功","seqNo":1}`)
	w := do(srv, http.MethodPost, "/receipts/"+ch.ID, receipt, "203.0.113.7:3000", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("正常回执应 200，实际 %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != `{"status":0,"message":"success"}` {
		t.Fatalf("回执 ACK 必须为示例厂商精确字节，实际 %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("回执 Content-Type 异常: %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("回执响应必须 no-store，实际 %q", cc)
	}

	// 通道不存在 → 404。
	if w := do(srv, http.MethodPost, "/receipts/missing-id", receipt, "203.0.113.7:3000", nil); w.Code != http.StatusNotFound {
		t.Fatalf("不存在通道的回执应 404，实际 %d", w.Code)
	}
	// 坏 JSON → 400。
	if w := do(srv, http.MethodPost, "/receipts/"+ch.ID, []byte("{bad"), "203.0.113.7:3000", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("坏回执 JSON 应 400，实际 %d", w.Code)
	}
	// 缺 appSmsId → 400。
	missing := []byte(`{"smsId":"vendor-2","status":"DELIVRD"}`)
	if w := do(srv, http.MethodPost, "/receipts/"+ch.ID, missing, "203.0.113.7:3000", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("缺 appSmsId 应 400，实际 %d", w.Code)
	}
}
