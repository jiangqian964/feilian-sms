package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"feilian-sms/internal/channel"
)

// ---- TR-10.3：health 探针 no-store ----

func TestHealthNoStore(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)

	// /health 对任意直连来源开放。
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
