// WebUI 装配测试：内嵌静态资源挂在根路径、未装配时 JSON 404。
package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"go.uber.org/zap"

	"feilian-sms/internal/service"
	"feilian-sms/internal/webui"
)

func buildServerWithUI(t *testing.T) *Server {
	t.Helper()
	st, cache, sender := newTestEnv(t)
	ui, err := webui.Handler()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Deps{
		Settings: service.NewSettingsRuntime(cache),
		Forward:  service.NewForwardService(st, cache, sender),
		Receipts: service.NewReceiptService(st, cache),
		Store:    st,
		Logger:   zap.NewNop(),
		UI:       ui,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestWebUIIndexAndAssetsAtRoot(t *testing.T) {
	srv := buildServerWithUI(t)

	w := do(srv, http.MethodGet, "/", nil, "10.0.0.1:5000", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / 应返回 WebUI 首页 200，实际 %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("首页 Content-Type=%q，期望 text/html", ct)
	}

	for _, p := range []string{"/css/app.css", "/js/app.js", "/js/views/channels.js"} {
		w := do(srv, http.MethodGet, p, nil, "10.0.0.1:5000", nil)
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("静态资源 %s 应 200 且非空，实际 %d", p, w.Code)
		}
	}
}

func TestWebUINilReturnsJSON404(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender) // 未注入 UI

	w := do(srv, http.MethodGet, "/", nil, "10.0.0.1:5000", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("未挂载 UI 时应 404，实际 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"code"`) {
		t.Fatalf("404 必须是统一 JSON 错误体，实际 %s", w.Body.String())
	}
}
