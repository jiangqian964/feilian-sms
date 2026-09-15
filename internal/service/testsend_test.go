package service

import (
	"context"
	"strings"
	"testing"

	"feilian-sms/internal/channel"
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
