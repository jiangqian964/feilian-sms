package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStub(t *testing.T, mode string) *vendorStub {
	t.Helper()
	return &vendorStub{
		mode:        mode,
		failCode:    50001,
		failMessage: "模拟业务失败",
		dumpDir:     t.TempDir(),
		now:         func() int64 { return 1700000000000 },
	}
}

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, body=%s", err, rec.Body.String())
	}
	return out
}

func TestSendSuccess(t *testing.T) {
	stub := newTestStub(t, modeSuccess)
	rec := postJSON(t, stub, "/sms/send",
		`{"appSmsId":"evt-1","mobile":"8613800000056","templateCode":"SMS_TEST"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("成功模式应回 200，实际 %d", rec.Code)
	}
	body := decodeBody(t, rec)
	if status, _ := body["status"].(float64); status != 0 {
		t.Fatalf("status 应为 0，实际 %v", body["status"])
	}
	data, ok := body["data"].(map[string]any)
	if !ok || data["appSmsId"] != "evt-1" {
		t.Fatalf("data.appSmsId 应回显请求值: %#v", body["data"])
	}
	if data["id"] == "" || data["id"] == nil {
		t.Fatal("成功响应必须带厂商消息 ID")
	}

	last, err := os.ReadFile(filepath.Join(stub.dumpDir, "last.json"))
	if err != nil {
		t.Fatalf("请求体应落盘 last.json: %v", err)
	}
	if !strings.Contains(string(last), `"appSmsId":"evt-1"`) {
		t.Fatalf("落盘报文与请求不一致: %s", last)
	}
}

func TestSendBusinessError(t *testing.T) {
	stub := newTestStub(t, modeBusinessError)
	rec := postJSON(t, stub, "/sms/send", `{"appSmsId":"evt-2"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("示例厂商业务失败也是 HTTP 200，实际 %d", rec.Code)
	}
	body := decodeBody(t, rec)
	if status, _ := body["status"].(float64); status != 50001 {
		t.Fatalf("status 应为 50001，实际 %v", body["status"])
	}
	if body["message"] != "模拟业务失败" {
		t.Fatalf("message 不匹配: %v", body["message"])
	}
}

func TestSendHTTPError(t *testing.T) {
	stub := newTestStub(t, modeHTTPError)
	rec := postJSON(t, stub, "/sms/send", `{"appSmsId":"evt-3"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("http_error 模式应回 500，实际 %d", rec.Code)
	}
}

func TestControlSwitchesMode(t *testing.T) {
	stub := newTestStub(t, modeSuccess)

	rec := postJSON(t, stub, "/__control",
		`{"mode":"business_error","fail_code":50008,"fail_message":"流控"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("控制端点应 200，实际 %d", rec.Code)
	}
	state := decodeBody(t, rec)
	if state["mode"] != modeBusinessError {
		t.Fatalf("当前模式应为 business_error: %v", state["mode"])
	}

	rec = postJSON(t, stub, "/sms/send", `{"appSmsId":"evt-4"}`)
	body := decodeBody(t, rec)
	if status, _ := body["status"].(float64); status != 50008 || body["message"] != "流控" {
		t.Fatalf("切换后的失败参数未生效: %#v", body)
	}
}

func TestControlRejectsUnknownMode(t *testing.T) {
	stub := newTestStub(t, modeSuccess)
	rec := postJSON(t, stub, "/__control", `{"mode":"nonsense"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知模式应 400，实际 %d", rec.Code)
	}
}

func TestHealthzAndUnknownRoute(t *testing.T) {
	stub := newTestStub(t, modeSuccess)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	stub.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("ok")) {
		t.Fatalf("healthz 异常: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec = httptest.NewRecorder()
	stub.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知路由应 404，实际 %d", rec.Code)
	}
}

func TestDumpSequenceFiles(t *testing.T) {
	stub := newTestStub(t, modeSuccess)
	postJSON(t, stub, "/sms/send", `{"appSmsId":"a"}`)
	postJSON(t, stub, "/sms/send", `{"appSmsId":"b"}`)

	for i, want := range []string{"a", "b"} {
		name := filepath.Join(stub.dumpDir, "req-001.json")
		if i == 1 {
			name = filepath.Join(stub.dumpDir, "req-002.json")
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("序号落盘文件缺失: %v", err)
		}
		if !strings.Contains(string(raw), `"appSmsId":"`+want+`"`) {
			t.Fatalf("序号文件内容不匹配: %s", raw)
		}
	}
}
