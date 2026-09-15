// Package webui 的 embed 资源测试：覆盖 TR-12.1——
// 断网/无 CDN 环境下单二进制必须能独立提供首页、样式、全部 ESM 模块，
// 且静态资源中不得出现任何指向外网的资源引用。
package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"

	"feilian-sms/web"
)

// 前端运行所必需的全部资源（新增模块时必须在此登记，防止漏 embed）。
// url 为浏览器实际请求路径（index.html 经 / 提供，直接请求会 301）。
var requiredAssets = []struct{ url, file string }{
	{"/", "index.html"},
	{"/css/app.css", "css/app.css"},
	{"/js/app.js", "js/app.js"},
	{"/js/api.js", "js/api.js"},
	{"/js/dom.js", "js/dom.js"},
	{"/js/ui.js", "js/ui.js"},
	{"/js/views/settings.js", "js/views/settings.js"},
	{"/js/views/channels.js", "js/views/channels.js"},
	{"/js/views/channel-form.js", "js/views/channel-form.js"},
	{"/js/views/roweditor.js", "js/views/roweditor.js"},
	{"/js/views/bindings.js", "js/views/bindings.js"},
	{"/js/views/records.js", "js/views/records.js"},
}

func TestHandlerServesAllEmbeddedAssets(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}

	for _, a := range requiredAssets {
		req := httptest.NewRequest(http.MethodGet, a.url, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("资源 %s 应返回 200，实际 %d", a.url, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("资源 %s 响应体为空", a.url)
		}
		ct := rec.Header().Get("Content-Type")
		wantPrefix := contentTypePrefix(a.file)
		if !strings.HasPrefix(ct, wantPrefix) {
			t.Fatalf("资源 %s Content-Type=%q，期望以 %q 开头", a.url, ct, wantPrefix)
		}
	}
}

func TestRootServesIndexHTML(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / 应返回首页 200，实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<script type="module" src="/js/app.js">`) {
		t.Fatal("首页必须以内联 ESM 方式引导 /js/app.js")
	}
	if !strings.Contains(body, `href="/css/app.css"`) {
		t.Fatal("首页必须引用 /css/app.css")
	}
}

func TestEmbeddedAssetsContainNoExternalReferences(t *testing.T) {
	sub, err := fs.Sub(web.StaticFS, "static")
	if err != nil {
		t.Fatal(err)
	}

	// 仅匹配“资源引用位”的外链：标签属性、ESM 导入、CSS url()、fetch/Worker。
	// 业务默认值（如示例厂商接口域名、占位基址）是普通字符串，不会命中这些规则。
	patterns := map[string]*regexp.Regexp{
		"html-src/href": regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']https?://`),
		"esm-import":    regexp.MustCompile(`import\s+(?:[^'"]*?\s+from\s+)?["']https?://`),
		"css-url":       regexp.MustCompile(`url\(\s*["']?https?://`),
		"js-fetch":      regexp.MustCompile(`(?:fetch|new\s+Worker)\(\s*["']https?://`),
	}

	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isTextAsset(p) {
			return nil
		}
		raw, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		for name, re := range patterns {
			if loc := re.FindIndex(raw); loc != nil {
				line := lineAt(raw, loc[0])
				t.Fatalf("资源 %s 出现外网引用（%s）：%s", p, name, line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// 非法 embed 子路径必须启动即报错，而不是静默服务空目录；
// 注：embed.FS.Sub 对“不存在但合法”的路径是惰性的（请求时才报错），
// 仅对含 .. 段/绝对路径等非法参数立即报错，这里锁定该行为。
func TestHandlerFromInvalidSubtreeReturnsError(t *testing.T) {
	if _, err := handlerFrom(web.StaticFS, "../static"); err == nil {
		t.Fatal("非法 embed 子路径应返回错误")
	}
	h, err := handlerFrom(web.StaticFS, "static")
	if err != nil || h == nil {
		t.Fatalf("合法子树应返回处理器，h=%v err=%v", h, err)
	}
}

func contentTypePrefix(p string) string {
	switch path.Ext(p) {
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		// Go 不同版本对 .js 的 MIME 可能是 text/javascript 或 application/javascript。
		return "text/javascript"
	}
	return "application/octet-stream"
}

func isTextAsset(p string) bool {
	switch path.Ext(p) {
	case ".html", ".css", ".js", ".json", ".svg", ".txt":
		return true
	}
	return false
}

func lineAt(raw []byte, offset int) string {
	start := strings.LastIndexByte(string(raw[:offset]), '\n') + 1
	end := strings.IndexByte(string(raw[start:]), '\n')
	if end < 0 {
		return strings.TrimSpace(string(raw[start:]))
	}
	return strings.TrimSpace(string(raw[start : start+end]))
}
