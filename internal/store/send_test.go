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

	res, err := s.ApplyReceipt(ctx, ReceiptUpdate{
		AppSmsID: "r1", ChannelID: "ch1", DeliveryStatus: StatusDeliveryDelivered,
		DeliveryMessage: "delivered", SeqNo: 1, ReceiptAtMS: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found || !res.Applied {
		t.Fatalf("已存在记录且归属一致应 found/applied=true，实际 %+v", res)
	}
	got, _ := s.GetSend(ctx, "r1")
	if got.DeliveryStatus != StatusDeliveryDelivered || got.DeliveryMessage != "delivered" ||
		got.SeqNo != 1 || got.ReceiptAt != 300 {
		t.Fatalf("回执回写异常: %#v", got)
	}
	if got.Status != StatusSuccess {
		t.Fatal("回执不应改变发送主状态")
	}

	// 无 seq_no 的失败回执不得把 delivered 回退（厂商重投旧回执的常见情形）
	res, err = s.ApplyReceipt(ctx, ReceiptUpdate{
		AppSmsID: "r1", ChannelID: "ch1", DeliveryStatus: StatusDeliveryFailed, ReceiptAtMS: 400,
	})
	if err != nil || !res.Found || res.Applied {
		t.Fatalf("delivered 不得被无 seq 失败回执回退: %+v err=%v", res, err)
	}
	got, _ = s.GetSend(ctx, "r1")
	if got.DeliveryStatus != StatusDeliveryDelivered || got.Status != StatusSuccess {
		t.Fatalf("回退保护失效: %#v", got)
	}

	// 更大 seq_no 的失败回执允许覆盖（厂商真实改判）
	res, _ = s.ApplyReceipt(ctx, ReceiptUpdate{
		AppSmsID: "r1", ChannelID: "ch1", DeliveryStatus: StatusDeliveryFailed,
		SeqNo: 2, ReceiptAtMS: 500,
	})
	if !res.Applied {
		t.Fatalf("更大 seq 的改判应允许写入: %+v", res)
	}

	// 乱序回执（seq_no 倒退）必须跳过
	res, _ = s.ApplyReceipt(ctx, ReceiptUpdate{
		AppSmsID: "r1", ChannelID: "ch1", DeliveryStatus: StatusDeliveryDelivered,
		SeqNo: 1, ReceiptAtMS: 600,
	})
	if !res.Found || res.Applied {
		t.Fatalf("乱序回执应 found=true/applied=false: %+v", res)
	}
	got, _ = s.GetSend(ctx, "r1")
	if got.SeqNo != 2 || got.DeliveryStatus != StatusDeliveryFailed {
		t.Fatalf("乱序回执不得落库: %#v", got)
	}

	// 跨通道回执：归属不一致只裁决不落库
	res, err = s.ApplyReceipt(ctx, ReceiptUpdate{
		AppSmsID: "r1", ChannelID: "ch-other", DeliveryStatus: StatusDeliveryDelivered,
		SeqNo: 9, ReceiptAtMS: 700,
	})
	if err != nil || !res.Found || res.Applied {
		t.Fatalf("跨通道回执应 found=true/applied=false: %+v err=%v", res, err)
	}

	// 未知 appSmsId：found=false 但不报错（调用方记日志）
	res, err = s.ApplyReceipt(ctx, ReceiptUpdate{
		AppSmsID: "unknown", DeliveryStatus: StatusDeliveryDelivered, ReceiptAtMS: 1,
	})
	if err != nil || res.Found {
		t.Fatalf("未知 ID 应 found=false/nil，实际 %+v %v", res, err)
	}
}

func TestClaimStalePendingSingleFlight(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const base int64 = 1_000
	_, _, _ = s.InsertPendingIfAbsent(ctx, samplePending("stale-1", "ch1", base))

	// 首次 CAS（updated_at 与现值一致）应成功并前推租约、attempts+1
	ok, err := s.ClaimStalePending(ctx, "stale-1", base, base+100)
	if err != nil || !ok {
		t.Fatalf("首次认领应成功: ok=%v err=%v", ok, err)
	}
	got, _ := s.GetSend(ctx, "stale-1")
	if got.UpdatedAt != base+100 || got.Attempts != 1 {
		t.Fatalf("认领后租约/attempts 异常: %#v", got)
	}

	// 竞争者仍持旧 updated_at，CAS 必须失败（飞连重推 × worker 单飞）
	ok, err = s.ClaimStalePending(ctx, "stale-1", base, base+200)
	if err != nil || ok {
		t.Fatalf("过期版本认领必须失败: ok=%v err=%v", ok, err)
	}
	got, _ = s.GetSend(ctx, "stale-1")
	if got.UpdatedAt != base+100 || got.Attempts != 1 {
		t.Fatalf("失败认领不得改写记录: %#v", got)
	}

	// 持新租约者可继续认领（下一补发窗口）
	ok, _ = s.ClaimStalePending(ctx, "stale-1", base+100, base+200)
	if !ok {
		t.Fatal("持新租约者应认领成功")
	}
	got, _ = s.GetSend(ctx, "stale-1")
	if got.Attempts != 2 {
		t.Fatalf("attempts 应为 2，实际 %d", got.Attempts)
	}

	// 终态记录不得再被认领
	_ = s.MarkFailed(ctx, "stale-1", MarkFailed{ErrorKind: ErrorKindVendor, NowMS: base + 300})
	ok, _ = s.ClaimStalePending(ctx, "stale-1", base+200, base+400)
	if ok {
		t.Fatal("failed 记录不得被认领")
	}

	// 不存在的记录认领失败但不报错
	ok, err = s.ClaimStalePending(ctx, "missing", base, base+100)
	if err != nil || ok {
		t.Fatalf("不存在记录认领应 ok=false/nil: %v %v", ok, err)
	}
}

func TestInsertPendingWithPayloadRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	plain := []byte(`{"appSmsId":"p1","mobile":"13800001111"}`)
	sealed, err := s.SealPayload(plain)
	if err != nil || sealed == "" {
		t.Fatalf("SealPayload 失败: %v", err)
	}
	rec := samplePending("p1", "ch1", 100)
	rec.EncryptedPayload = sealed
	if _, _, err := s.InsertPendingIfAbsent(ctx, rec); err != nil {
		t.Fatal(err)
	}

	ct, err := s.GetSendPayload(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if ct == "" || ct == string(plain) {
		t.Fatal("应读到非空密文且不得等于明文")
	}
	opened, err := s.OpenPayload(ct)
	if err != nil {
		t.Fatalf("OpenPayload 失败: %v", err)
	}
	if string(opened) != string(plain) {
		t.Fatalf("载荷往返不一致: %s", opened)
	}
	// 密文具有随机性 nonce：同一明文两次密封结果必须不同
	sealed2, _ := s.SealPayload(plain)
	if sealed2 == sealed {
		t.Fatal("nonce 随机性缺失：两次密文相同")
	}

	// 空密文短路：历史行无载荷时返回 nil/nil
	if out, err := s.OpenPayload(""); err != nil || out != nil {
		t.Fatalf("空密文应返回 nil/nil: %v %v", out, err)
	}
	// 不存在记录取载荷返回 ErrNotFound
	if _, err := s.GetSendPayload(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应 ErrNotFound，实际 %v", err)
	}
}

func TestRefreshPendingRoute(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// pending 行：路由/脱敏视图应被整体刷新到最新快照解析结果
	_, _, _ = s.InsertPendingIfAbsent(ctx, samplePending("rp1", "ch1", 1000))
	err := s.RefreshPendingRoute(ctx, "rp1", "ch2", "SMS_ALERT", "15****22", `["77****33"]`, 2000)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSend(ctx, "rp1")
	if got.ChannelID != "ch2" || got.TemplateCode != "SMS_ALERT" ||
		got.MobileMasked != "15****22" || got.ParamsMasked != `["77****33"]` ||
		got.UpdatedAt != 2000 || got.Status != StatusPending {
		t.Fatalf("pending 行路由刷新异常: %#v", got)
	}

	// 终态行：刷新必须零改动（WHERE status='pending' 兜底）
	_, _, _ = s.InsertPendingIfAbsent(ctx, samplePending("rp2", "ch1", 1000))
	if err := s.MarkSuccess(ctx, "rp2", MarkSuccess{ProviderMsgID: "p", NowMS: 1100}); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshPendingRoute(ctx, "rp2", "ch2", "SMS_ALERT", "15****22", `[]`, 2000); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSend(ctx, "rp2")
	if got.ChannelID != "ch1" || got.TemplateCode != "SMS_CODE" ||
		got.MobileMasked != "13****11" || got.UpdatedAt != 1100 || got.Status != StatusSuccess {
		t.Fatalf("终态行不得被路由刷新改写: %#v", got)
	}

	// 不存在的记录：零行影响但不报错
	if err := s.RefreshPendingRoute(ctx, "missing", "ch2", "t", "m", "[]", 1); err != nil {
		t.Fatalf("不存在记录刷新应静默成功: %v", err)
	}
}

func TestSealPayloadEmptyShortCircuit(t *testing.T) {
	s := openTestStore(t)
	for _, in := range [][]byte{nil, {}} {
		out, err := s.SealPayload(in)
		if err != nil || out != "" {
			t.Fatalf("空明文应短路返回空串/nil，实际 %q err=%v", out, err)
		}
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
