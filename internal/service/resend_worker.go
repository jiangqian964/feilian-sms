package service

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"feilian-sms/internal/feilian"
	"feilian-sms/internal/store"
)

const (
	// DefaultResendInterval 是补发扫描间隔默认值；主进程装配时显式传入。
	DefaultResendInterval = 30 * time.Second
	// resendBatchSize 单轮补发最多认领的记录数，避免单轮长时间占用。
	resendBatchSize = 100
)

// StartResendWorker 启动后台补发循环：周期性扫描 stale pending，
// 解密其加密载荷后 CAS 认领并重放。随 ForwardService.Shutdown 停止。
func (f *ForwardService) StartResendWorker(interval time.Duration) {
	if interval <= 0 {
		interval = DefaultResendInterval
	}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-f.bgCtx.Done():
				return
			case <-ticker.C:
				f.RunResendOnce(f.bgCtx)
			}
		}
	}()
}

// RunResendOnce 执行一轮补发扫描与重放，返回本轮处理的 stale 记录数。
// 生产由 ticker 调用；测试直接调用以获得确定性。
func (f *ForwardService) RunResendOnce(ctx context.Context) int {
	snap := f.cache.Current()
	staleMS := snap.Settings.StalePendingMS
	if staleMS <= 0 {
		staleMS = 120_000
	}
	now := f.now()
	ids, err := f.store.StalePendingIDs(ctx, now, int64(staleMS), resendBatchSize)
	if err != nil {
		f.logger.Warn("补发扫描失败", zap.Error(err))
		return 0
	}
	processed := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		item := f.resendOne(ctx, snap, id, now)
		processed++
		f.logItem(item)
	}
	if processed > 0 {
		f.logger.Info("补发轮次完成", zap.Int("processed", processed))
	}
	return processed
}

// resendOne 解密单条 stale 记录的原始对象并走与实时事件一致的认领/下发通道。
func (f *ForwardService) resendOne(ctx context.Context, snap *store.Snapshot, appSmsID string, now int64) ItemResult {
	rec, err := f.store.GetSend(ctx, appSmsID)
	if err != nil {
		f.logger.Warn("补发读取记录失败，跳过",
			zap.String("app_sms_id", appSmsID), zap.Error(err))
		return ItemResult{AppSmsID: appSmsID, Outcome: outcomeSkipped, Reason: "记录读取失败"}
	}
	if rec.Status != store.StatusPending {
		return ItemResult{AppSmsID: appSmsID, SMSType: rec.SMSType, Outcome: outcomeSkipped,
			Reason: "记录已离开 pending"}
	}

	// 取加密载荷：缺失/损坏无法重放，置终态避免该记录每轮被重复扫描（v2 历史行）。
	ct, err := f.store.GetSendPayload(ctx, appSmsID)
	if err != nil {
		f.logger.Error("补发读取载荷失败", zap.String("app_sms_id", appSmsID), zap.Error(err))
		return ItemResult{AppSmsID: appSmsID, SMSType: rec.SMSType, Outcome: outcomeSkipped,
			Reason: "载荷读取失败"}
	}
	if ct == "" {
		f.markFailed(appSmsID, store.ErrorKindInternal, "", "历史记录缺少补发载荷，无法自动重放", 0, now)
		return ItemResult{AppSmsID: appSmsID, SMSType: rec.SMSType, Outcome: outcomeFailed,
			ErrorKind: store.ErrorKindInternal, Reason: "缺少补发载荷"}
	}
	plain, err := f.store.OpenPayload(ct)
	if err != nil {
		f.logger.Error("补发载荷解密失败（数据密钥可能已变更）",
			zap.String("app_sms_id", appSmsID), zap.Error(err))
		f.markFailed(appSmsID, store.ErrorKindInternal, "", "补发载荷解密失败", 0, now)
		return ItemResult{AppSmsID: appSmsID, SMSType: rec.SMSType, Outcome: outcomeFailed,
			ErrorKind: store.ErrorKindInternal, Reason: "补发载荷解密失败"}
	}
	var o feilian.SMSObject
	if err := json.Unmarshal(plain, &o); err != nil {
		f.logger.Error("补发载荷不是合法短信对象",
			zap.String("app_sms_id", appSmsID), zap.Error(err))
		f.markFailed(appSmsID, store.ErrorKindInternal, "", "补发载荷损坏", 0, now)
		return ItemResult{AppSmsID: appSmsID, SMSType: rec.SMSType, Outcome: outcomeFailed,
			ErrorKind: store.ErrorKindInternal, Reason: "补发载荷损坏"}
	}

	// CAS 单飞：与飞连 stale 重推、其他 worker 实例竞争，仅赢者重放。
	plan := f.buildPlan(snap, appSmsID, o)
	_, skip, terminal := f.claimForResend(snap, rec, plan, o, now)
	if skip != nil {
		return *skip
	}
	if terminal != nil {
		return *terminal
	}
	return f.dispatch(snap, appSmsID, o, plan)
}
