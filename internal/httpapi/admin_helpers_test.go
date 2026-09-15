// 管理 API 测试共享辅助（仅 _test.go 使用）：JSON 请求/解码、DB 明文扫描。
package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jbody 把任意值编码为 JSON（测试夹具）。
func jbody(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// adminJSON 以直连内网地址发起管理 API 请求；v 为 nil 时无请求体。
func adminJSON(t *testing.T, srv *Server, method, path string, v any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if v != nil {
		reader = bytes.NewReader(jbody(t, v))
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.10.0.1:51234"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// adminRaw 以原始字节发请求（坏 JSON/空体场景）。
func adminRaw(t *testing.T, srv *Server, method, path string, raw []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.10.0.1:51234"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// jdecode 解码响应体到 dst。
func jdecode(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, body=%s", err, w.Body.String())
	}
}

// apiError 解析统一错误体。
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) apiError {
	t.Helper()
	var e apiError
	jdecode(t, w, &e)
	return e
}

// assertNoStore 断言管理响应禁止缓存/嗅探。
func assertNoStore(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, 期望 no-store", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, 期望 nosniff", got)
	}
}

// assertDBHasNoPlaintext 校验 SQLite 主库与 WAL 文件中均不存在明文片段
// （调用前先 Checkpoint；AC-9② 密钥安全证据）。
func assertDBHasNoPlaintext(t *testing.T, dbPath, secret string) {
	t.Helper()
	matches, err := filepath.Glob(dbPath + "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("未找到数据库文件: %s", dbPath)
	}
	for _, p := range matches {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) {
			t.Fatalf("明文片段 %q 出现在数据库文件 %s 中", secret, p)
		}
	}
}
