package store

import (
	"context"
	"errors"
	"testing"
)

func samplePending(appSmsID, channel string, createdAt int64) SendRecord {
	return SendRecord{
		AppSmsID:     appSmsID,
		EventID:      "evt-1",
		Source:       SourceFeilian,
		ChannelID:    channel,
		SMSType:      "code",
		MobileMasked: "13****11",
		ParamsMasked: `["65****21"]`,
		TemplateCode: "SMS_CODE",
		Status:       StatusPending,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
}

func TestSendStatusMachine(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const now int64 = 1_000_000

	rec := samplePending("a1", "ch1", now)
	inserted, existing, err := s.InsertPendingIfAbsent(ctx, rec)
	if err != nil || !inserted || existing != nil {
		t.Fatalf("首次插入应 inserted=true: %v %v %v", inserted, existing, err)
	}

	got, err := s.GetSend(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending || got.MobileMasked != "13****11" {
		t.Fatalf("pending 行异常: %#v", got)
	}

	// pending -> success
	if err := s.MarkSuccess(ctx, "a1", MarkSuccess{
		ProviderMsgID: "vendor-1", ProviderStatus: "DELIVRD",
		ProviderMessage: "ok", LatencyMS: 80, NowMS: now + 100,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSend(ctx, "a1")
	if got.Status != StatusSuccess || got.ProviderMsgID != "vendor-1" ||
		got.ProviderStatus != "DELIVRD" || got.LatencyMS != 80 || got.UpdatedAt != now+100 {
		t.Fatalf("success 回写异常: %#v", got)
	}

	// success 后再 mark failed 必须拒绝（状态不可倒退）
	err = s.MarkFailed(ctx, "a1", MarkFailed{
		ErrorKind: "vendor", ProviderMessage: "x", LatencyMS: 1, NowMS: now + 200,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("success→failed 应返回 ErrInvalidTransition，实际 %v", err)
	}

	// 重复 success 幂等不报错
	if err := s.MarkSuccess(ctx, "a1", MarkSuccess{
		ProviderMsgID: "vendor-1", NowMS: now + 300,
	}); err != nil {
		t.Fatalf("重复 success 应幂等: %v", err)
	}
}

func TestMarkFailed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	_, _, _ = s.InsertPendingIfAbsent(ctx, samplePending("f1", "ch1", 1000))
	if err := s.MarkFailed(ctx, "f1", MarkFailed{
		ErrorKind: ErrorKindVendor, ProviderStatus: "50001",
		ProviderMessage: "appCode invalid", LatencyMS: 12, NowMS: 1100,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSend(ctx, "f1")
	if got.Status != StatusFailed || got.ErrorKind != ErrorKindVendor ||
		got.ProviderStatus != "50001" || got.ProviderMessage != "appCode invalid" {
		t.Fatalf("failed 回写异常: %#v", got)
	}

	// 标记不存在的记录报错
	if err := s.MarkFailed(ctx, "nope", MarkFailed{NowMS: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在记录应 ErrNotFound，实际 %v", err)
	}
}

func TestInsertPendingIfAbsentIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	inserted, _, err := s.InsertPendingIfAbsent(ctx, samplePending("dup", "ch1", 100))
	if err != nil || !inserted {
		t.Fatal("首次应插入")
	}
	inserted, existing, err := s.InsertPendingIfAbsent(ctx, samplePending("dup", "ch1", 200))
	if err != nil || inserted {
		t.Fatal("重复 appSmsId 不应新增")
	}
	if existing == nil || existing.AppSmsID != "dup" || existing.CreatedAt != 100 {
		t.Fatalf("应返回既有行且不覆盖: %#v", existing)
	}
	if n := countSends(t, s); n != 1 {
		t.Fatalf("应只有 1 行，实际 %d", n)
	}
}

func TestStalePendingIDsBoundaries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const now, staleMS int64 = 1_000_000, 120_000
	cutoff := now - staleMS // 880_000

	mustInsert := func(id string, createdAt int64, status SendStatus) {
		_, _, _ = s.InsertPendingIfAbsent(ctx, samplePending(id, "ch1", createdAt))
		if status != StatusPending {
			r := MarkSuccess{ProviderMsgID: "p", NowMS: createdAt + 1}
			if status == StatusFailed {
				_ = s.MarkFailed(ctx, id, MarkFailed{ErrorKind: "x", NowMS: createdAt + 1})
				return
			}
			_ = s.MarkSuccess(ctx, id, r)
		}
	}
	mustInsert("equal", cutoff, StatusPending)   // 恰等边界 → stale
	mustInsert("older", cutoff-1, StatusPending) // 超过 → stale
	mustInsert("fresh", cutoff+1, StatusPending) // 未超过 → 不返回
	mustInsert("done", cutoff-1, StatusSuccess)  // success 不返回
	mustInsert("fail", cutoff-1, StatusFailed)   // failed 不返回

	ids, err := s.StalePendingIDs(ctx, now, staleMS, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("应恰有 2 个 stale pending（equal/older），实际 %v", ids)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen["equal"] || !seen["older"] {
		t.Fatalf("边界判定错误: %v", ids)
	}
}

func TestApplyReceipt(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	_, _, _ = s.InsertPendingIfAbsent(ctx, samplePending("r1", "ch1", 100))
	_ = s.MarkSuccess(ctx, "r1", MarkSuccess{ProviderMsgID: "vendor-1", NowMS: 200})

	found, err := s.ApplyReceipt(ctx, ReceiptUpdate{
		AppSmsID: "r1", DeliveryStatus: "DELIVRD", DeliveryMessage: "delivered",
		SeqNo: 1, ReceiptAtMS: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("已存在记录应 found=true")
	}
	got, _ := s.GetSend(ctx, "r1")
	if got.DeliveryStatus != "DELIVRD" || got.DeliveryMessage != "delivered" ||
		got.SeqNo != 1 || got.ReceiptAt != 300 {
		t.Fatalf("回执回写异常: %#v", got)
	}
	if got.Status != StatusSuccess {
		t.Fatal("回执不应改变发送主状态")
	}

	// 非 DELIVRD 也只是 delivery_status，不改主状态
	_, _ = s.ApplyReceipt(ctx, ReceiptUpdate{AppSmsID: "r1", DeliveryStatus: "delivery_failed", ReceiptAtMS: 400})
	got, _ = s.GetSend(ctx, "r1")
	if got.DeliveryStatus != "delivery_failed" || got.Status != StatusSuccess {
		t.Fatalf("失败回执应只落 delivery_status: %#v", got)
	}

	// 未知 appSmsId：found=false 但不报错（调用方记日志）
	found, err = s.ApplyReceipt(ctx, ReceiptUpdate{AppSmsID: "unknown", DeliveryStatus: "DELIVRD", ReceiptAtMS: 1})
	if err != nil || found {
		t.Fatalf("未知 ID 应 found=false/nil，实际 %v %v", found, err)
	}
}

func TestListSendsFilterAndPaging(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	_, _, _ = s.InsertPendingIfAbsent(ctx, SendRecord{AppSmsID: "a", EventID: "e", Source: SourceFeilian, ChannelID: "ch1", SMSType: "code", Status: StatusPending, CreatedAt: 100, UpdatedAt: 100})
	_, _, _ = s.InsertPendingIfAbsent(ctx, SendRecord{AppSmsID: "b", EventID: "e", Source: SourceTest, ChannelID: "ch2", SMSType: "alert", Status: StatusPending, CreatedAt: 200, UpdatedAt: 200})
	_ = s.MarkSuccess(ctx, "b", MarkSuccess{NowMS: 250})
	_, _, _ = s.InsertPendingIfAbsent(ctx, SendRecord{AppSmsID: "c", EventID: "e", Source: SourceFeilian, ChannelID: "ch1", SMSType: "code", Status: StatusPending, CreatedAt: 300, UpdatedAt: 300})

	// 无筛选，按 created_at DESC，共 3
	rows, total, err := s.ListSends(ctx, SendFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(rows) != 3 || rows[0].AppSmsID != "c" {
		t.Fatalf("默认列表异常: total=%d rows=%v", total, idsOf(rows))
	}

	// 状态筛选
	_, total, _ = s.ListSends(ctx, SendFilter{Status: string(StatusSuccess)})
	if total != 1 {
		t.Fatalf("success 应 1 行，实际 %d", total)
	}
	// 类型 + 通道
	rows, total, _ = s.ListSends(ctx, SendFilter{SMSType: "code", ChannelID: "ch1"})
	if total != 2 || len(rows) != 2 {
		t.Fatalf("code+ch1 应 2 行，实际 %d", total)
	}
	// 时间范围（含边界）
	_, total, _ = s.ListSends(ctx, SendFilter{FromMS: 200, ToMS: 300})
	if total != 2 {
		t.Fatalf("[200,300] 应 2 行，实际 %d", total)
	}
	// 分页
	page1, _, _ := s.ListSends(ctx, SendFilter{Limit: 2, Offset: 0})
	page2, _, _ := s.ListSends(ctx, SendFilter{Limit: 2, Offset: 2})
	if len(page1) != 2 || len(page2) != 1 || page1[0].AppSmsID != "c" || page2[0].AppSmsID != "a" {
		t.Fatalf("分页异常: %v %v", idsOf(page1), idsOf(page2))
	}
}

func TestGetSendNotFound(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.GetSend(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应 ErrNotFound，实际 %v", err)
	}
}

func countSends(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sms_send`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func idsOf(rows []SendRecord) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.AppSmsID
	}
	return out
}
