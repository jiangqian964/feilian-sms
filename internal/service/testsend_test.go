package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/feilian"
	"feilian-sms/internal/store"
)

func TestTestSendSuccess(t *testing.T) {
	s, st, fk := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)

	out := obj("code", "13800001111", "654321")
	res, err := s.TestSend(context.Background(), chID, "SMS_CODE", out)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.ProviderMsgID != "vendor-1" {
		t.Fatalf("测试发送应成功: %+v", res)
	}
	if !strings.HasPrefix(res.AppSmsID, "test-") {
		t.Fatalf("测试发送 appSmsId 应以 test- 开头: %s", res.AppSmsID)
	}
	if fk.callCount() != 1 {
		t.Fatalf("应下发 1 次: %d", fk.callCount())
	}
	rec, err := st.GetSend(context.Background(), res.AppSmsID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Source != store.SourceTest || rec.Status != store.StatusSuccess ||
		rec.TemplateCode != "SMS_CODE" || rec.ChannelID != chID {
		t.Fatalf("测试发送留痕异常: %#v", rec)
	}
}

func TestTestSendChannelValidation(t *testing.T) {
	s, st, _ := newSvc(t, 1_000_000)
	out := obj("code", "13800001111", "1")

	if _, err := s.TestSend(context.Background(), "missing", "T", out); err == nil {
		t.Fatal("通道不存在应报错")
	}
	offID := createChannel(t, st, "off", false)
	if _, err := s.TestSend(context.Background(), offID, "T", out); err == nil {
		t.Fatal("通道停用应报错")
	}
}

func TestTestSendVendorFailure(t *testing.T) {
	s, st, _ := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)
	s.sender = &fakeSender{result: &channel.SendResult{Success: false, HTTPCode: 200, Message: "50001"}}

	res, err := s.TestSend(context.Background(), chID, "T", obj("code", "13800001111", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.ErrorKind != store.ErrorKindVendor {
		t.Fatalf("应返回失败分类: %+v", res)
	}
}

// TestTestSendIndeterminateKeepsPending 超时等不确定结果必须保 pending，
// 由补发 worker 兜底，不得向 WebUI 谎报确定性失败。
func TestTestSendIndeterminateKeepsPending(t *testing.T) {
	s, st, _ := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)
	s.sender = &fakeSender{err: &channel.TransportError{Timeout: true, Err: errors.New("context deadline exceeded")}}

	res, err := s.TestSend(context.Background(), chID, "T", obj("code", "13800001111", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || !res.Pending || res.ErrorKind != store.ErrorKindTimeout {
		t.Fatalf("超时测试发送应保 pending: %+v", res)
	}
	rec, _ := st.GetSend(context.Background(), res.AppSmsID)
	if rec.Status != store.StatusPending {
		t.Fatalf("记录应停留 pending: %#v", rec)
	}
}

// TestTestSendPayloadStored 测试发送同样留存加密载荷，供 worker 补发。
func TestTestSendPayloadStored(t *testing.T) {
	s, st, _ := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)
	in := obj("code", "13800001111", "654321")

	res, err := s.TestSend(context.Background(), chID, "SMS_CODE", in)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := st.GetSendPayload(context.Background(), res.AppSmsID)
	if err != nil || ct == "" {
		t.Fatalf("测试发送应留存加密载荷: ct=%q err=%v", ct, err)
	}
	plain, err := st.OpenPayload(ct)
	if err != nil {
		t.Fatal(err)
	}
	var got feilian.SMSObject
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatal(err)
	}
	if got.MobileNumber != in.MobileNumber || got.SMSType != in.SMSType ||
		len(got.Params) != 1 || got.Params[0] != "654321" {
		t.Fatalf("载荷与原始对象不一致: %#v", got)
	}
}

func TestTestSendUniqueAppSmsID(t *testing.T) {
	s, st, fk := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)
	out := obj("code", "13800001111", "1")
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		res, err := s.TestSend(context.Background(), chID, "T", out)
		if err != nil {
			t.Fatal(err)
		}
		if seen[res.AppSmsID] {
			t.Fatalf("测试发送 appSmsId 必须唯一: %s", res.AppSmsID)
		}
		seen[res.AppSmsID] = true
	}
	if fk.callCount() != 5 {
		t.Fatalf("每次测试发送都应下发: %d", fk.callCount())
	}
}
