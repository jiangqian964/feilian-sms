package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/feilian"
	"feilian-sms/internal/store"
)

// TestRunResendOnceReplayFromEncryptedPayload 回归补发主路径：
// 首次下发超时保 pending 并留存加密载荷 → 厂商恢复后 worker 解密重放成功。
func TestRunResendOnceReplayFromEncryptedPayload(t *testing.T) {
	var clock int64 = 1_000_000
	s, st, fk := newSvc(t, clock)
	s.now = func() int64 { return clock }
	chID := createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)

	// 首次下发超时：记录保 pending，加密载荷已落库。
	fk.err = &channel.TransportError{Timeout: true, Err: errors.New("context deadline exceeded")}
	want := obj("code", "13800001111", "654321")
	r := s.HandleEvent(context.Background(), "evt-replay", []feilian.SMSObject{want})
	assertPendingItem(t, st, r, "evt-replay", store.ErrorKindTimeout)
	ct, err := st.GetSendPayload(context.Background(), "evt-replay")
	if err != nil || ct == "" {
		t.Fatalf("超时 pending 必须留存加密载荷: ct=%q err=%v", ct, err)
	}

	// 厂商恢复：补发 worker 解密重放成功。
	fk.err = nil
	fk.result = &channel.SendResult{Success: true, HTTPCode: 200, VendorID: "v-9", Message: "ok"}
	clock += 120_001
	if n := s.RunResendOnce(context.Background()); n != 1 {
		t.Fatalf("应补发 1 条，实际 %d", n)
	}
	if fk.callCount() != 2 {
		t.Fatalf("首次+补发共应下发 2 次，实际 %d", fk.callCount())
	}
	rec, err := st.GetSend(context.Background(), "evt-replay")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != store.StatusSuccess || rec.ProviderMsgID != "v-9" || rec.Attempts != 1 {
		t.Fatalf("补发成功后记录异常: %#v", rec)
	}

	// 补发载荷解密后必须等于原始飞连对象。
	plain, err := st.OpenPayload(ct)
	if err != nil {
		t.Fatal(err)
	}
	var got feilian.SMSObject
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatal(err)
	}
	if got.MobileNumber != want.MobileNumber || got.SMSType != want.SMSType ||
		len(got.Params) != 1 || got.Params[0] != "654321" {
		t.Fatalf("补发载荷与原始对象不一致: %#v", got)
	}
}

// TestRunResendOnceLegacyRowWithoutPayloadTerminal 回归无载荷保护：
// v2 历史 pending 行没有 payload_enc，无法重放，必须置 internal 终态，
// 避免它在每一轮扫描中被反复捞起。
func TestRunResendOnceLegacyRowWithoutPayloadTerminal(t *testing.T) {
	var clock int64 = 1_000_000
	s, st, fk := newSvc(t, clock)
	s.now = func() int64 { return clock }
	chID := createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)

	if _, _, err := st.InsertPendingIfAbsent(context.Background(), store.SendRecord{
		AppSmsID: "evt-legacy", ChannelID: chID, SMSType: "code",
		Status: store.StatusPending, CreatedAt: clock - 120_001, UpdatedAt: clock - 120_001,
	}); err != nil {
		t.Fatal(err)
	}

	if n := s.RunResendOnce(context.Background()); n != 1 {
		t.Fatalf("首轮应扫描到 1 条，实际 %d", n)
	}
	if fk.callCount() != 0 {
		t.Fatalf("无载荷历史行不得下发，实际 %d 次", fk.callCount())
	}
	rec, _ := st.GetSend(context.Background(), "evt-legacy")
	if rec.Status != store.StatusFailed || rec.ErrorKind != store.ErrorKindInternal {
		t.Fatalf("无载荷历史行应置 internal 终态: %#v", rec)
	}

	// 终态行下一轮不再被扫描到。
	clock += 120_001
	if n := s.RunResendOnce(context.Background()); n != 0 {
		t.Fatalf("终态行不应再被扫描，实际 %d", n)
	}
}

// TestResendWorkerLifecycle 验证后台 worker 真实 ticker 驱动补发，并随 Shutdown 退出：
// 启动后无需外部触发即可自动重放 stale pending；Shutdown 等待 goroutine 退出且可重复调用。
func TestResendWorkerLifecycle(t *testing.T) {
	var clock int64 = 1_000_000
	s, st, fk := newSvc(t, clock)
	s.now = func() int64 { return clock }
	chID := createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)

	// 首次超时保 pending。
	fk.err = &channel.TransportError{Timeout: true, Err: errors.New("context deadline exceeded")}
	r := s.HandleEvent(context.Background(), "evt-worker", []feilian.SMSObject{obj("code", "13800001111", "654321")})
	assertPendingItem(t, st, r, "evt-worker", store.ErrorKindTimeout)

	// 时钟跨过 stale 阈值，厂商恢复。
	clock = 2_000_000
	fk.err = nil
	fk.result = &channel.SendResult{Success: true, HTTPCode: 200, VendorID: "v-w", Message: "ok"}

	s.WithLogger(nil) // nil 注入保持原值（防御分支）
	s.StartResendWorker(15 * time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fk.callCount() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fk.callCount() != 2 {
		t.Fatalf("worker 应自动补发使总下发达到 2 次，实际 %d", fk.callCount())
	}
	rec, err := st.GetSend(context.Background(), "evt-worker")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != store.StatusSuccess || rec.Attempts != 1 {
		t.Fatalf("worker 补发后记录异常: %#v", rec)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown 应等待 worker 退出: %v", err)
	}
	// 重复关停幂等：bgCtx 已取消、wg 已归零。
	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("重复 Shutdown 不应报错: %v", err)
	}
}

// TestSettingsRuntimeAccessors 覆盖设置访问器的默认值与热更新读取。
func TestSettingsRuntimeAccessors(t *testing.T) {
	st, cache := newTestRuntime(t)
	rt := NewSettingsRuntime(cache)
	if rt.EncryptKey() != "" || rt.ReceiptAuthToken() != "" {
		t.Fatalf("默认 EncryptKey/ReceiptAuthToken 应为空: %q/%q", rt.EncryptKey(), rt.ReceiptAuthToken())
	}
	s := cache.Current().Settings
	s.EncryptKey = "enc-key-01"
	s.ReceiptAuthToken = "rcpt-token-0123456789"
	if err := st.UpdateSettings(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if rt.EncryptKey() != "enc-key-01" || rt.ReceiptAuthToken() != "rcpt-token-0123456789" {
		t.Fatalf("热更新后访问器值异常: %q/%q", rt.EncryptKey(), rt.ReceiptAuthToken())
	}
	if nowMS() <= 0 {
		t.Fatal("nowMS 应返回正数毫秒")
	}
}

// TestRunResendOnceSkipsFresh 验证未超 stale 阈值的 pending 不会被补发。
func TestRunResendOnceSkipsFresh(t *testing.T) {
	s, st, fk := newSvc(t, 1_000_000)
	chID := createChannel(t, st, "demo", true)
	bind(t, st, "code", chID, "T", nil, true)

	if _, _, err := st.InsertPendingIfAbsent(context.Background(), store.SendRecord{
		AppSmsID: "evt-fresh2", ChannelID: chID, SMSType: "code",
		Status: store.StatusPending, CreatedAt: 1_000_000 - 1_000, UpdatedAt: 1_000_000 - 1_000,
	}); err != nil {
		t.Fatal(err)
	}
	if n := s.RunResendOnce(context.Background()); n != 0 || fk.callCount() != 0 {
		t.Fatalf("未超阈值的 pending 不应补发: n=%d calls=%d", n, fk.callCount())
	}
}
