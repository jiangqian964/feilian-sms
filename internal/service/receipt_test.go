package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/store"
)

func newReceiptSvc(t *testing.T, now int64) (*ReceiptService, *store.Store) {
	t.Helper()
	st, cache := newTestRuntime(t)
	s := NewReceiptService(st, cache)
	s.now = func() int64 { return now }
	return s, st
}

func seedSuccess(t *testing.T, st *store.Store, appSmsID, chID string) {
	t.Helper()
	_, _, err := st.InsertPendingIfAbsent(context.Background(), store.SendRecord{
		AppSmsID: appSmsID, ChannelID: chID, SMSType: "code",
		Status: store.StatusPending, CreatedAt: 100, UpdatedAt: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSuccess(context.Background(), appSmsID, store.MarkSuccess{NowMS: 200}); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptAck(t *testing.T) {
	svc, st := newReceiptSvc(t, 300)
	chID := createChannel(t, st, "demo", true)
	seedSuccess(t, st, "evt-r", chID)

	body := []byte(`{"smsId":"sms-1","appSmsId":"evt-r","status":"DELIVRD","statusMessage":"delivered","seqNo":1}`)
	res, err := svc.Handle(context.Background(), chID, body, "10.1.1.1:5000")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found || !res.Delivered || res.AppSmsID != "evt-r" || res.VendorMsgID != "sms-1" {
		t.Fatalf("回执解析异常: %+v", res)
	}
	if string(res.ResponseBody) != `{"status":0,"message":"success"}` {
		t.Fatalf("示例厂商成功响应体必须精确: %s", res.ResponseBody)
	}
	if res.RemoteIP != "10.1.1.1:5000" {
		t.Fatalf("来源 IP 应透传: %s", res.RemoteIP)
	}
	rec, _ := st.GetSend(context.Background(), "evt-r")
	if rec.DeliveryStatus != "delivered" || rec.SeqNo != 1 || rec.ReceiptAt != 300 ||
		rec.DeliveryMessage != "delivered" || rec.Status != store.StatusSuccess {
		t.Fatalf("回执回写异常: %#v", rec)
	}
}

func TestReceiptUnknownIDStillACK(t *testing.T) {
	svc, st := newReceiptSvc(t, 300)
	chID := createChannel(t, st, "demo", true)

	body := []byte(`{"smsId":"sms-9","appSmsId":"who-dis","status":"DELIVRD","seqNo":1}`)
	res, err := svc.Handle(context.Background(), chID, body, "10.1.1.2:1")
	if err != nil {
		t.Fatalf("未知 appSmsId 不应报错: %v", err)
	}
	if res.Found {
		t.Fatal("未知 appSmsId 应 Found=false（仅记日志）")
	}
	if string(res.ResponseBody) != `{"status":0,"message":"success"}` {
		t.Fatalf("未知 ID 也必须回厂商成功响应: %s", res.ResponseBody)
	}
}

func TestReceiptChannelNotFound(t *testing.T) {
	svc, _ := newReceiptSvc(t, 300)
	_, err := svc.Handle(context.Background(), "missing-ch",
		[]byte(`{"appSmsId":"x","status":"DELIVRD"}`), "1.1.1.1:1")
	if !errors.Is(err, ErrReceiptChannelNotFound) {
		t.Fatalf("通道不存在应 ErrReceiptChannelNotFound（HTTP 404），实际 %v", err)
	}
}

func TestReceiptNonDelivered(t *testing.T) {
	svc, st := newReceiptSvc(t, 300)
	chID := createChannel(t, st, "demo", true)
	seedSuccess(t, st, "evt-nd", chID)

	body := []byte(`{"smsId":"sms-2","appSmsId":"evt-nd","status":"UNDELIV","statusMessage":"absent subscriber","seqNo":2}`)
	res, err := svc.Handle(context.Background(), chID, body, "10.1.1.3:1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered {
		t.Fatal("UNDELIV 不应判定送达")
	}
	rec, _ := st.GetSend(context.Background(), "evt-nd")
	if rec.DeliveryStatus != "delivery_failed" || rec.Status != store.StatusSuccess {
		t.Fatalf("非 DELIVRD 应落 delivery_failed 且不改主状态: %#v", rec)
	}
}

func TestReceiptCustomSuccessBody(t *testing.T) {
	st, cache := newTestRuntime(t)
	// 构造带自定义成功响应体的通道配置
	cfg := channel.BuiltinPresetConfig()
	cfg.Receipt.SuccessBody = `{"code":"0","msg":"ok"}`
	raw, _ := json.Marshal(cfg)
	ch, err := st.CreateChannel(context.Background(), "custom", "", string(raw),
		map[string]string{"appSecret": "s"})
	if err != nil {
		t.Fatal(err)
	}
	seedSuccess(t, st, "evt-c", ch.ID)

	svc := NewReceiptService(st, cache)
	svc.now = func() int64 { return 300 }
	res, err := svc.Handle(context.Background(), ch.ID,
		[]byte(`{"appSmsId":"evt-c","status":"DELIVRD"}`), "1.1.1.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.ResponseBody) != `{"code":"0","msg":"ok"}` {
		t.Fatalf("自定义出站 KV 应原样返回: %s", res.ResponseBody)
	}
}

// TestReceiptIntermediateStatus 回归 #12：排队中/发送中等中间态不得被二态
// 逻辑误判为 delivery_failed；只推进 seq_no 与回执时间，delivery_* 保持空。
func TestReceiptIntermediateStatus(t *testing.T) {
	svc, st := newReceiptSvc(t, 300)
	chID := createChannel(t, st, "demo", true)
	seedSuccess(t, st, "evt-mid", chID)

	body := []byte(`{"smsId":"sms-m","appSmsId":"evt-mid","status":"SENDING","statusMessage":"queued","seqNo":3}`)
	res, err := svc.Handle(context.Background(), chID, body, "10.1.1.4:1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered || res.DeliveryStatus != "" || !res.Applied {
		t.Fatalf("中间态不应判定送达/失败，且应推进 seq_no: %+v", res)
	}
	rec, _ := st.GetSend(context.Background(), "evt-mid")
	if rec.DeliveryStatus != "" || rec.DeliveryMessage != "" ||
		rec.SeqNo != 3 || rec.ReceiptAt != 300 {
		t.Fatalf("中间态只应推进 seq_no/receipt_at: %#v", rec)
	}
}

// TestReceiptIntermediateDoesNotRegressDelivered 已送达后收到更新序号的
// 中间态回执：送达结论不得被覆盖，仅序号推进。
func TestReceiptIntermediateDoesNotRegressDelivered(t *testing.T) {
	svc, st := newReceiptSvc(t, 300)
	chID := createChannel(t, st, "demo", true)
	seedSuccess(t, st, "evt-keep", chID)

	ok := []byte(`{"appSmsId":"evt-keep","status":"DELIVRD","statusMessage":"done","seqNo":5}`)
	if _, err := svc.Handle(context.Background(), chID, ok, "10.1.1.5:1"); err != nil {
		t.Fatal(err)
	}
	mid := []byte(`{"appSmsId":"evt-keep","status":"SENDING","statusMessage":"queued","seqNo":6}`)
	res, err := svc.Handle(context.Background(), chID, mid, "10.1.1.5:2")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied {
		t.Fatal("更新序号的中间态回执应落 seq_no")
	}
	rec, _ := st.GetSend(context.Background(), "evt-keep")
	if rec.DeliveryStatus != "delivered" || rec.DeliveryMessage != "done" || rec.SeqNo != 6 {
		t.Fatalf("中间态不得回退已送达状态: %#v", rec)
	}
}

// TestReceiptCrossChannelRejected 回归 #4：回执通道与记录归属通道不一致时
// Found=true 但 Applied=false，记录完全不被改动（防跨通道伪造送达）。
func TestReceiptCrossChannelRejected(t *testing.T) {
	svc, st := newReceiptSvc(t, 300)
	ch1 := createChannel(t, st, "demo-1", true)
	ch2 := createChannel(t, st, "demo-2", true)
	seedSuccess(t, st, "evt-x", ch1)

	body := []byte(`{"appSmsId":"evt-x","status":"DELIVRD","statusMessage":"ok","seqNo":1}`)
	res, err := svc.Handle(context.Background(), ch2, body, "10.9.9.9:1")
	if err != nil {
		t.Fatalf("跨通道回执不报错（仍回 ACK 防厂商探测）: %v", err)
	}
	if !res.Found || res.Applied {
		t.Fatalf("跨通道回执应 Found=true/Applied=false: %+v", res)
	}
	rec, _ := st.GetSend(context.Background(), "evt-x")
	if rec.DeliveryStatus != "" || rec.DeliveryMessage != "" || rec.SeqNo != 0 || rec.ReceiptAt != 0 {
		t.Fatalf("跨通道回执不得回写任何字段: %#v", rec)
	}
}

func TestReceiptBadPayload(t *testing.T) {
	svc, st := newReceiptSvc(t, 300)
	chID := createChannel(t, st, "demo", true)

	if _, err := svc.Handle(context.Background(), chID, []byte(`{not json`), "1.1.1.1:1"); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
	if _, err := svc.Handle(context.Background(), chID,
		[]byte(`{"smsId":"s","status":"DELIVRD"}`), "1.1.1.1:1"); err == nil {
		t.Fatal("缺 appSmsId 路径应报错")
	}
}
