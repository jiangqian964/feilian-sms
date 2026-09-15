package channel

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// recomputeSign 模拟厂商端：按收到的 timestamp/nonce/appSmsId 重算示例厂商签名。
func recomputeSign(secret, ts, nonce, appSmsID string) string {
	raw := "timestamp=" + ts + "&nonce=" + nonce + "&signData=" + appSmsID
	sum := sha1.Sum(append([]byte(secret), []byte(raw)...))
	return hex.EncodeToString(sum[:])
}

func TestClientSendSuccess(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type 错误: %q", r.Header.Get("Content-Type"))
		}
		ts := received["timestamp"]
		nonce := received["nonce"].(string)
		appSmsID := received["appSmsId"].(string)
		wantSign := recomputeSign("demo-app-secret", numToString(ts), nonce, appSmsID)
		if received["sign"] != wantSign {
			t.Errorf("签名不一致: got %v want %s", received["sign"], wantSign)
		}
		if received["mobile"] != "8613800001111" || received["appCode"] != "DEMOAPP" {
			t.Errorf("映射字段错误: %#v", received)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":0,"message":"success","data":{"id":"vendor-987","appSmsId":"evt-0001"}}`)
	}))
	defer srv.Close()

	cfg := validConfig(t)
	cfg.Request.URL = srv.URL
	cfg.Request.BaseURL = ""

	client := NewClient(2000)
	res, err := client.Send(context.Background(), cfg,
		map[string]string{"appSecret": "demo-app-secret"}, sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("应判定为成功: %#v", res)
	}
	if res.VendorID != "vendor-987" {
		t.Fatalf("VendorID = %q, want vendor-987", res.VendorID)
	}
	if res.HTTPCode != 200 {
		t.Fatalf("HTTPCode = %d", res.HTTPCode)
	}
}

func TestClientSendBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":50001,"message":"appCode invalid","data":null}`)
	}))
	defer srv.Close()

	cfg := validConfig(t)
	cfg.Request.URL = srv.URL
	cfg.Request.BaseURL = ""
	client := NewClient(2000)
	res, err := client.Send(context.Background(), cfg,
		map[string]string{"appSecret": "s"}, sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Fatal("status=50001 不应判定成功")
	}
	if res.Message != "appCode invalid" {
		t.Fatalf("Message = %q", res.Message)
	}
}

func TestClientSendHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	cfg := validConfig(t)
	cfg.Request.URL = srv.URL
	cfg.Request.BaseURL = ""
	client := NewClient(2000)
	res, err := client.Send(context.Background(), cfg,
		map[string]string{"appSecret": "s"}, sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.HTTPCode != 502 {
		t.Fatalf("应判定失败: %#v", res)
	}
}

func TestClientAuthHeaderInjection(t *testing.T) {
	gotAuth := ""
	gotAPIKey := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("X-Api-Key")
		_, _ = io.WriteString(w, `{"status":0,"data":{"id":"z"}}`)
	}))
	defer srv.Close()

	cfg := validConfig(t)
	cfg.Request.URL = srv.URL
	cfg.Request.BaseURL = ""
	cfg.Constants["token"] = ConstantSpec{Secret: true}
	cfg.Constants["cred"] = ConstantSpec{Secret: true}
	cfg.Constants["apikey"] = ConstantSpec{Secret: true}
	cfg.Request.Headers = map[string]string{
		"Authorization": "Bearer ${const:token}",
		"X-Api-Key":     "${const:apikey}",
	}
	secrets := map[string]string{
		"appSecret": "s", "token": "abc.def", "cred": "u:p", "apikey": "key-1",
	}

	client := NewClient(2000)
	if _, err := client.Send(context.Background(), cfg, secrets, sampleInput()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer abc.def" {
		t.Fatalf("Bearer 注入错误: %q", gotAuth)
	}
	if gotAPIKey != "key-1" {
		t.Fatalf("静态密钥头注入错误: %q", gotAPIKey)
	}

	// Basic 场景
	cfg.Request.Headers = map[string]string{"Authorization": "Basic ${constb64:cred}"}
	if _, err := client.Send(context.Background(), cfg, secrets, sampleInput()); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("u:p"))
	if gotAuth != want {
		t.Fatalf("Basic 注入错误: got %q want %q", gotAuth, want)
	}
}

func TestParseSendResultStringStatus(t *testing.T) {
	res, err := ParseSendResult(ResponseConfig{SuccessPath: "status", SuccessValue: "0", MsgIDPath: "data.id"},
		200, []byte(`{"status":"0","data":{"id":"x1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.VendorID != "x1" {
		t.Fatalf("字符串状态码应兼容: %#v", res)
	}
}

func numToString(v any) string {
	switch x := v.(type) {
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return x
	}
	return ""
}
