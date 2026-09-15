// TR-11.1 场景绑定：GET 目录+已存绑定、PUT 批量 UPSERT、9 类型与字段级校验。
package httpapi

import (
	"net/http"
	"testing"

	"feilian-sms/internal/feilian"
)

func TestAdminListBindings_Empty(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	w := adminJSON(t, srv, http.MethodGet, "/api/bindings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/bindings = %d", w.Code)
	}
	var resp bindingsResponse
	jdecode(t, w, &resp)
	if len(resp.SMSTypes) != len(feilian.KnownSMSTypes) {
		t.Fatalf("sms_types 目录 = %v", resp.SMSTypes)
	}
	for i, k := range feilian.KnownSMSTypes {
		if resp.SMSTypes[i] != k {
			t.Fatalf("sms_types 顺序/内容不一致: %v", resp.SMSTypes)
		}
	}
	if len(resp.Bindings) != 0 {
		t.Fatalf("初始绑定应为空，实际 %d 条", len(resp.Bindings))
	}
}

func TestAdminReplaceBindings(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)
	ch := mustCreateChannel(t, srv, "绑定通道", signNoneConfig())
	ch2 := mustCreateChannel(t, srv, "第二通道", signNoneConfig())

	put := bindingsUpdate{Bindings: []bindingItem{
		{SMSType: "code", ChannelID: ch.ID, TemplateCode: "TPL-CODE", ParamIndex: []int{1, 0}, Enabled: true},
		{SMSType: "guest_wifi", ChannelID: ch.ID, TemplateCode: "TPL-WIFI", Enabled: false},
	}}
	w := adminJSON(t, srv, http.MethodPut, "/api/bindings", put)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body=%s", w.Code, w.Body.String())
	}
	var resp bindingsResponse
	jdecode(t, w, &resp)
	if len(resp.Bindings) != 2 {
		t.Fatalf("保存后应返回 2 条，实际 %d", len(resp.Bindings))
	}

	// GET 持久化校验：按 sms_type 升序、字段完整。
	w = adminJSON(t, srv, http.MethodGet, "/api/bindings", nil)
	jdecode(t, w, &resp)
	got := map[string]bindingItem{}
	for _, b := range resp.Bindings {
		got[b.SMSType] = bindingItem{
			SMSType: b.SMSType, ChannelID: b.ChannelID, TemplateCode: b.TemplateCode,
			ParamIndex: b.ParamIndex, Enabled: b.Enabled,
		}
	}
	code, ok := got["code"]
	if !ok || code.ChannelID != ch.ID || code.TemplateCode != "TPL-CODE" ||
		len(code.ParamIndex) != 2 || code.ParamIndex[0] != 1 || !code.Enabled {
		t.Fatalf("code 绑定异常: %+v", got)
	}
	wifi := got["guest_wifi"]
	if wifi.Enabled || wifi.TemplateCode != "TPL-WIFI" {
		t.Fatalf("guest_wifi 绑定异常: %+v", wifi)
	}

	// UPSERT 语义：只提交 code 改绑 ch2，guest_wifi 必须保留。
	w = adminJSON(t, srv, http.MethodPut, "/api/bindings", bindingsUpdate{Bindings: []bindingItem{
		{SMSType: "code", ChannelID: ch2.ID, TemplateCode: "TPL-2", Enabled: true},
	}})
	if w.Code != http.StatusOK {
		t.Fatalf("二次 PUT = %d", w.Code)
	}
	w = adminJSON(t, srv, http.MethodGet, "/api/bindings", nil)
	jdecode(t, w, &resp)
	if len(resp.Bindings) != 2 {
		t.Fatalf("UPSERT 不应删除未提及行，实际 %d 条", len(resp.Bindings))
	}
	for _, b := range resp.Bindings {
		if b.SMSType == "code" && (b.ChannelID != ch2.ID || b.TemplateCode != "TPL-2") {
			t.Fatalf("code 未更新: %+v", b)
		}
	}
}

func TestAdminReplaceBindings_Validation(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)
	ch := mustCreateChannel(t, srv, "通道", signNoneConfig())

	base := func() bindingItem {
		return bindingItem{SMSType: "code", ChannelID: ch.ID, TemplateCode: "TPL", Enabled: true}
	}
	cases := []struct {
		name string
		item bindingItem
	}{
		{"未知 sms_type", bindingItem{SMSType: "nope", ChannelID: ch.ID, TemplateCode: "T", Enabled: true}},
		{"通道不存在", bindingItem{SMSType: "code", ChannelID: "missing", TemplateCode: "T", Enabled: true}},
		{"模板码为空", bindingItem{SMSType: "code", ChannelID: ch.ID, TemplateCode: "", Enabled: true}},
		{"负参数下标", bindingItem{SMSType: "code", ChannelID: ch.ID, TemplateCode: "T", ParamIndex: []int{-1}, Enabled: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := adminJSON(t, srv, http.MethodPut, "/api/bindings",
				bindingsUpdate{Bindings: []bindingItem{tc.item}})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d, 期望 400, body=%s", w.Code, w.Body.String())
			}
			if e := decodeError(t, w); e.Code != "bad_request" || e.Field != "bindings" {
				t.Fatalf("错误体 = %+v", e)
			}
		})
	}

	// 同批重复 sms_type：400（必须在写库前拒绝）。
	dup := base()
	w := adminJSON(t, srv, http.MethodPut, "/api/bindings", bindingsUpdate{Bindings: []bindingItem{dup, dup}})
	if w.Code != http.StatusBadRequest || decodeError(t, w).Field != "bindings" {
		t.Fatalf("重复 sms_type = %d %+v", w.Code, decodeError(t, w))
	}
	// 任一非法整批不落库：bindings 仍为空。
	w = adminJSON(t, srv, http.MethodGet, "/api/bindings", nil)
	var resp bindingsResponse
	jdecode(t, w, &resp)
	if len(resp.Bindings) != 0 {
		t.Fatalf("校验失败的批次不应产生部分写入，实际 %d 条", len(resp.Bindings))
	}

	// 坏 JSON：400 无 field。
	w = adminRaw(t, srv, http.MethodPut, "/api/bindings", []byte("{bad"))
	if w.Code != http.StatusBadRequest || decodeError(t, w).Field != "" {
		t.Fatalf("坏 JSON = %d %+v", w.Code, decodeError(t, w))
	}
}
