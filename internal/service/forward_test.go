package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/feilian"
	"feilian-sms/internal/store"
)

// ---- 假下发器：实现 service.Sender，记录调用次数与请求体 ----

type fakeSender struct {
	mu     sync.Mutex
	calls  int
	bodies []map[string]any
	result *channel.SendResult
	err    error
}

func (f *fakeSender) Do(_ context.Context, _ *channel.Config, r *channel.RenderResult) (*channel.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.bodies = append(f.bodies, r.Body)
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &channel.SendResult{Success: true, HTTPCode: 200, VendorID: "vendor-1", Message: "success"}, nil
}

func (f *fakeSender) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// ---- 构造辅助 ----

func obj(smsType, mobile string, params ...string) feilian.SMSObject {
	return feilian.SMSObject{
		CountryCode:  "+86",
		MobileNumber: mobile,
		Mobile:       "+86" + mobile,
		SMSType:      smsType,
		Params:       params,
	}
}

func sampleConfigJSON(t *testing.T) string {
	t.Helper()
	cfg := channel.BuiltinPresetConfig()
	cfg.Request.URL = "http://vendor.test/sms/send"
	cfg.Request.BaseURL = ""
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func createChannel(t *testing.T, st *store.Store, name string, enabled bool) string {
	t.Helper()
	ch, err := st.CreateChannel(context.Background(), name, "", sampleConfigJSON(t),
		map[string]string{"appSecret": "demo-app-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		if err := st.SetChannelEnabled(context.Background(), ch.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	return ch.ID
}

func bind(t *testing.T, st *store.Store, smsType, channelID, tmpl string, idx []int, enabled bool) {
	t.Helper()
	if err := st.ReplaceBindings(context.Background(), []store.Binding{{
		SMSType: smsType, ChannelID: channelID, TemplateCode: tmpl,
		ParamIndex: idx, Enabled: enabled,
	}}); err != nil {
		t.Fatal(err)
	}
}

func newSvc(t *testing.T, now int64) (*ForwardService, *store.Store, *fakeSender) {
	t.Helper()
	st, cache := newTestRuntime(t)
	fk := &fakeSender{}
	s := NewForwardService(st, cache, fk)
	s.now = func() int64 { return now }
	return s, st, fk
}

// ---- 用例 ----

func TestForwardSuccess(t *testing.T) {
	s, st, fk := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "SMS_CODE", nil, true)

	res := s.HandleEvent(context.Background(), "evt-1",
		[]feilian.SMSObject{obj("code", "13800001111", "654321")})

	if res.Total != 1 || res.Succeeded != 1 || res.Failed != 0 || res.Skipped != 0 {
		t.Fatalf("结果汇总异常: %+v", res)
	}
	if fk.callCount() != 1 {
		t.Fatalf("应下发 1 次，实际 %d", fk.callCount())
	}
	rec, err := st.GetSend(context.Background(), "evt-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != store.StatusSuccess || rec.ProviderMsgID != "vendor-1" ||
		rec.ChannelID != chID || rec.TemplateCode != "SMS_CODE" || rec.Source != store.SourceFeilian {
		t.Fatalf("成功记录异常: %#v", rec)
	}
	if rec.MobileMasked == "" || rec.MobileMasked == "13800001111" {
		t.Fatalf("手机号必须脱敏落库: %q", rec.MobileMasked)
	}
	if rec.LatencyMS < 0 {
		t.Fatal("latency 不应为负")
	}
}

func TestForwardIdempotentReplay(t *testing.T) {
	s, st, fk := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)
	objects := []feilian.SMSObject{obj("code", "13800001111", "1")}

	first := s.HandleEvent(context.Background(), "evt-replay", objects)
	second := s.HandleEvent(context.Background(), "evt-replay", objects)
	if first.Succeeded != 1 || second.Skipped != 1 || second.Succeeded != 0 {
		t.Fatalf("重放应跳过: first=%+v second=%+v", first, second)
	}
	if fk.callCount() != 1 {
		t.Fatalf("重放不得重复下发，实际调用 %d", fk.callCount())
	}

	// 失败重放同样跳过
	bind(t, st, "alert", chID, "T", nil, true)
	fk.result = &channel.SendResult{Success: false, HTTPCode: 200, Message: "50001"}
	failEvt := []feilian.SMSObject{func() feilian.SMSObject { o := obj("alert", "13800001111", "1"); return o }()}
	r1 := s.HandleEvent(context.Background(), "evt-fail", failEvt)
	r2 := s.HandleEvent(context.Background(), "evt-fail", failEvt)
	if r1.Failed != 1 || r2.Skipped != 1 {
		t.Fatalf("失败重放应跳过: r1=%+v r2=%+v", r1, r2)
	}
	if fk.callCount() != 2 {
		t.Fatalf("失败重放不得再下发，总调用应为 2，实际 %d", fk.callCount())
	}
}

func TestForwardStaleRetry(t *testing.T) {
	s, st, fk := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)

	// 预置一条 stale pending（created 早于 now-stale），应补发一次
	_, _, _ = st.InsertPendingIfAbsent(context.Background(), store.SendRecord{
		AppSmsID: "evt-stale", ChannelID: chID, SMSType: "code",
		Status: store.StatusPending, CreatedAt: 1_000_000 - 120_000 - 1, UpdatedAt: 1_000_000 - 120_000 - 1,
	})
	r := s.HandleEvent(context.Background(), "evt-stale",
		[]feilian.SMSObject{obj("code", "13800001111", "1")})
	if r.Succeeded != 1 || fk.callCount() != 1 {
		t.Fatalf("stale pending 应补发: %+v calls=%d", r, fk.callCount())
	}

	// 一条 fresh pending（在阈值内），应视为在途跳过
	_, _, _ = st.InsertPendingIfAbsent(context.Background(), store.SendRecord{
		AppSmsID: "evt-fresh", ChannelID: chID, SMSType: "code",
		Status: store.StatusPending, CreatedAt: 1_000_000 - 1000, UpdatedAt: 1_000_000 - 1000,
	})
	r2 := s.HandleEvent(context.Background(), "evt-fresh",
		[]feilian.SMSObject{obj("code", "13800001111", "1")})
	if r2.Skipped != 1 || fk.callCount() != 1 {
		t.Fatalf("fresh pending 不应重发: %+v calls=%d", r2, fk.callCount())
	}
}

func TestForwardDegradationsNoCall(t *testing.T) {
	const now int64 = 1_000_000
	objects := []feilian.SMSObject{obj("code", "13800001111", "1")}

	// 1) 无绑定 → unbound
	s, st, fk := newSvc(t, now)
	r := s.HandleEvent(context.Background(), "e-unbound", objects)
	assertFailedKind(t, st, r, fk, "e-unbound", store.ErrorKindUnbound)

	// 2) 绑定停用 → binding_disabled
	s, st, fk = newSvc(t, now)
	chID := createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, false)
	r = s.HandleEvent(context.Background(), "e-bdoff", objects)
	assertFailedKind(t, st, r, fk, "e-bdoff", store.ErrorKindBindingDisabled)

	// 3) 通道停用 → channel_disabled
	s, st, fk = newSvc(t, now)
	chID = createChannel(t, st, "demo", false)
	bind(t, st, "code", chID, "T", nil, true)
	r = s.HandleEvent(context.Background(), "e-choff", objects)
	assertFailedKind(t, st, r, fk, "e-choff", store.ErrorKindChannelDisabled)

	// 4) 手机号非法（cc_prefix 但国家码无 +）→ invalid_mobile
	s, st, fk = newSvc(t, now)
	chID = createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)
	bad := []feilian.SMSObject{{CountryCode: "86", MobileNumber: "13800001111", SMSType: "code", Params: []string{"1"}}}
	r = s.HandleEvent(context.Background(), "e-mobile", bad)
	assertFailedKind(t, st, r, fk, "e-mobile", store.ErrorKindInvalidMobile)
}

func assertFailedKind(t *testing.T, st *store.Store, r *ForwardResult, fk *fakeSender, appSmsID, kind string) {
	t.Helper()
	if fk.callCount() != 0 {
		t.Fatalf("[%s] 降级路径不得调用下发器，实际 %d", kind, fk.callCount())
	}
	if r.Failed != 1 {
		t.Fatalf("[%s] 应失败 1 条: %+v", kind, r)
	}
	rec, err := st.GetSend(context.Background(), appSmsID)
	if err != nil {
		t.Fatalf("[%s] 降级也必须留痕: %v", kind, err)
	}
	if rec.Status != store.StatusFailed || rec.ErrorKind != kind {
		t.Fatalf("[%s] 记录状态/分类错误: %#v", kind, rec)
	}
}

func TestForwardParamReorderAndBatch(t *testing.T) {
	s, st, fk := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)
	// 通道参数顺序与飞连相反：[2,1,0]
	bind(t, st, "code", chID, "T", []int{2, 1, 0}, true)

	objects := []feilian.SMSObject{
		obj("code", "13800001111", "a", "b", "c"),
		obj("code", "13800002222", "x", "y", "z"),
	}
	r := s.HandleEvent(context.Background(), "evt-batch", objects)
	if r.Total != 2 || r.Succeeded != 2 {
		t.Fatalf("批量结果异常: %+v", r)
	}
	if fk.callCount() != 2 {
		t.Fatalf("应下发 2 次: %d", fk.callCount())
	}
	for i, b := range fk.bodies {
		params, _ := b["params"].([]any)
		if len(params) != 3 {
			t.Fatalf("body %d params 异常: %#v", i, b["params"])
		}
	}
	want0 := []string{"c", "b", "a"}
	got0, _ := fk.bodies[0]["params"].([]any)
	for i := range want0 {
		if got0[i] != want0[i] {
			t.Fatalf("[2,1,0] 重排错误: %#v", got0)
		}
	}
	// 批量 appSmsId 唯一且 <64
	for _, suffix := range []string{"evt-batch-0", "evt-batch-1"} {
		rec, err := st.GetSend(context.Background(), suffix)
		if err != nil || len(suffix) >= 64 {
			t.Fatalf("批量 appSmsId 异常: %s err=%v", suffix, err)
		}
		if rec.AppSmsID == fk.bodies[0]["appSmsId"] && suffix == "evt-batch-1" {
			t.Fatal("两条批量 appSmsId 必须不同")
		}
	}
}

func TestForwardParamIndexOutOfRange(t *testing.T) {
	s, st, fk := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", []int{5}, true)
	r := s.HandleEvent(context.Background(), "e-idx",
		[]feilian.SMSObject{obj("code", "13800001111", "a")})
	assertFailedKind(t, st, r, fk, "e-idx", store.ErrorKindRender)
}

func TestForwardVendorAndTransportFailures(t *testing.T) {
	const now int64 = 1_000_000
	objects := []feilian.SMSObject{obj("code", "13800001111", "1")}

	// 2xx 但业务失败 → vendor
	s, st, _ := newSvc(t, now)
	chID := createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)
	s.sender = &fakeSender{result: &channel.SendResult{Success: false, HTTPCode: 200, Message: "appCode invalid"}}
	r := s.HandleEvent(context.Background(), "e-vendor", objects)
	assertKind(t, st, r, "e-vendor", store.ErrorKindVendor)

	// 5xx → network
	s, st, _ = newSvc(t, now)
	chID = createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)
	s.sender = &fakeSender{result: &channel.SendResult{Success: false, HTTPCode: 502, Message: "bad gateway"}}
	r = s.HandleEvent(context.Background(), "e-5xx", objects)
	assertKind(t, st, r, "e-5xx", store.ErrorKindNetwork)

	// 超时 → timeout
	s, st, _ = newSvc(t, now)
	chID = createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)
	s.sender = &fakeSender{err: &channel.TransportError{Timeout: true, Err: errors.New("context deadline exceeded")}}
	r = s.HandleEvent(context.Background(), "e-timeout", objects)
	assertKind(t, st, r, "e-timeout", store.ErrorKindTimeout)

	// 普通传输错误 → network
	s, st, _ = newSvc(t, now)
	chID = createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)
	s.sender = &fakeSender{err: &channel.TransportError{Err: errors.New("connection refused")}}
	r = s.HandleEvent(context.Background(), "e-net", objects)
	assertKind(t, st, r, "e-net", store.ErrorKindNetwork)
}

func assertKind(t *testing.T, st *store.Store, r *ForwardResult, appSmsID, kind string) {
	t.Helper()
	if r.Failed != 1 {
		t.Fatalf("[%s] 应失败: %+v", kind, r)
	}
	rec, err := st.GetSend(context.Background(), appSmsID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ErrorKind != kind || rec.Status != store.StatusFailed {
		t.Fatalf("[%s] 分类错误: %#v", kind, rec)
	}
}
