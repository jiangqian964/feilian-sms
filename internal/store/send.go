package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidTransition 表示发送状态机不允许的迁移（终态不可逆）。
var ErrInvalidTransition = errors.New("非法的发送状态迁移")

// InsertPendingIfAbsent 是幂等闸门：app_sms_id 不存在才插入 pending 行。
// 已存在时 inserted=false 并返回既有行（调用方据其状态决定跳过或 stale 续发），
// 绝不覆盖既有行。sms_send 属业务数据，不触发配置快照回调。
func (s *Store) InsertPendingIfAbsent(ctx context.Context, rec SendRecord) (bool, *SendRecord, error) {
	if rec.AppSmsID == "" {
		return false, nil, fmt.Errorf("app_sms_id 不能为空")
	}
	if rec.Status == "" {
		rec.Status = StatusPending
	}
	if rec.Source == "" {
		rec.Source = SourceFeilian
	}
	if rec.ParamsMasked == "" {
		rec.ParamsMasked = "[]"
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, err
	}
	res, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO sms_send (
			app_sms_id, event_id, source, channel_id, sms_type,
			mobile_masked, params_masked, template_code, status,
			created_at, updated_at, payload_enc
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`,
		rec.AppSmsID, rec.EventID, rec.Source, rec.ChannelID, rec.SMSType,
		rec.MobileMasked, rec.ParamsMasked, rec.TemplateCode,
		rec.CreatedAt, rec.UpdatedAt, rec.EncryptedPayload)
	if err != nil {
		_ = tx.Rollback()
		return false, nil, fmt.Errorf("写入发送记录失败: %w", err)
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return false, nil, err
	}
	if n == 0 {
		existing, err := s.getSendLocked(ctx, rec.AppSmsID)
		if err != nil {
			return false, nil, err
		}
		return false, existing, nil
	}
	return true, nil, nil
}

// GetSend 按 app_sms_id 查询；不存在返回 ErrNotFound。
func (s *Store) GetSend(ctx context.Context, appSmsID string) (*SendRecord, error) {
	return s.getSendLocked(ctx, appSmsID)
}

func (s *Store) getSendLocked(ctx context.Context, appSmsID string) (*SendRecord, error) {
	row := s.db.QueryRowContext(ctx, sendColumns+` FROM sms_send WHERE app_sms_id = ?`, appSmsID)
	return scanSend(row)
}

// MarkSuccess 将 pending 行置为 success；重复 success 幂等，failed→success 拒绝。
func (s *Store) MarkSuccess(ctx context.Context, appSmsID string, m MarkSuccess) error {
	return s.transition(ctx, appSmsID, StatusSuccess, m.NowMS, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE sms_send SET status='success', provider_msg_id=?,
				provider_status=?, provider_message=?, latency_ms=?, updated_at=?
			WHERE app_sms_id=? AND status='pending'`,
			m.ProviderMsgID, m.ProviderStatus, m.ProviderMessage, m.LatencyMS, m.NowMS, appSmsID)
		return err
	})
}

// MarkFailed 将 pending 行置为 failed；重复 failed 幂等，success→failed 拒绝。
func (s *Store) MarkFailed(ctx context.Context, appSmsID string, m MarkFailed) error {
	return s.transition(ctx, appSmsID, StatusFailed, m.NowMS, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE sms_send SET status='failed', error_kind=?,
				provider_status=?, provider_message=?, latency_ms=?, updated_at=?
			WHERE app_sms_id=? AND status='pending'`,
			m.ErrorKind, m.ProviderStatus, m.ProviderMessage, m.LatencyMS, m.NowMS, appSmsID)
		return err
	})
}

// transition 封装终态状态机：校验存在性与不可逆约束后执行条件 UPDATE。
func (s *Store) transition(ctx context.Context, appSmsID string, target SendStatus, nowMS int64,
	update func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	cur, err := s.getSendLocked(ctx, appSmsID)
	if err != nil {
		return err
	}
	if cur.Status == target {
		return nil // 幂等
	}
	if cur.Status != StatusPending {
		return fmt.Errorf("%w: %s→%s（app_sms_id=%s）", ErrInvalidTransition, cur.Status, target, appSmsID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := update(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ClaimStalePending 是 stale pending 续发的单飞闸门：仅当行仍为 pending 且
// updated_at 与调用方先前读到的版本一致时，原子地把 updated_at 前推到 nowMS
// 并令 attempts+1。返回 claimed=true 表示赢得本次续发权；并发竞争者（飞连重推与
// 补发 worker 同时命中、或已被其他回执/终态改变）只会有一方拿到 true。
func (s *Store) ClaimStalePending(ctx context.Context, appSmsID string, oldUpdatedAt, nowMS int64) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.db.ExecContext(ctx, `
		UPDATE sms_send SET updated_at=?, attempts=attempts+1
		WHERE app_sms_id=? AND status='pending' AND updated_at=?`,
		nowMS, appSmsID, oldUpdatedAt)
	if err != nil {
		return false, fmt.Errorf("认领 stale pending 失败: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// GetSendPayload 读取补发所需的加密原始对象密文；记录不存在返回 ErrNotFound。
func (s *Store) GetSendPayload(ctx context.Context, appSmsID string) (string, error) {
	var ct string
	err := s.db.QueryRowContext(ctx,
		`SELECT payload_enc FROM sms_send WHERE app_sms_id = ?`, appSmsID).Scan(&ct)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return ct, err
}

// ApplyReceipt 回写厂商异步回执（delivery_* 字段），不改变发送主状态。
// 三重保护：
//   - 未知 app_sms_id：Found=false，不报错（调用方记日志）；
//   - ChannelID 与记录归属不一致：Found=true、Applied=false（防跨通道伪造）；
//   - 乱序/回退保护：携带更新 seq_no 的旧回执跳过；无 seq_no 时不得把
//     delivered 回退为 delivery_failed（厂商重投重复回执的常见情形）。
func (s *Store) ApplyReceipt(ctx context.Context, r ReceiptUpdate) (ReceiptApplyResult, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	cur, err := s.getSendLocked(ctx, r.AppSmsID)
	if errors.Is(err, ErrNotFound) {
		return ReceiptApplyResult{Found: false}, nil
	}
	if err != nil {
		return ReceiptApplyResult{}, fmt.Errorf("查询回执目标记录失败: %w", err)
	}
	out := ReceiptApplyResult{Found: true}
	if r.ChannelID != "" && cur.ChannelID != r.ChannelID {
		return out, nil
	}
	if r.SeqNo > 0 && cur.SeqNo > 0 && r.SeqNo < cur.SeqNo {
		return out, nil
	}
	if cur.DeliveryStatus == StatusDeliveryDelivered && r.DeliveryStatus == StatusDeliveryFailed {
		if r.SeqNo <= cur.SeqNo {
			return out, nil
		}
	}

	// seq_no 单调地板：缺失序号（0）的回执不得把既有序号清零。
	newSeq := r.SeqNo
	if newSeq < cur.SeqNo {
		newSeq = cur.SeqNo
	}

	var query string
	var args []any
	if r.DeliveryStatus == "" {
		// 中间态/未知状态回执：仅推进 seq_no 与回执时间，delivery_status/message
		// 保持原值（厂商状态机中的「发送中/排队中」不得覆盖成送达失败）。
		query = `
			UPDATE sms_send SET seq_no=?, receipt_at=?, updated_at=?
			WHERE app_sms_id=?`
		args = []any{newSeq, r.ReceiptAtMS, r.ReceiptAtMS, r.AppSmsID}
	} else {
		query = `
			UPDATE sms_send SET delivery_status=?, delivery_message=?,
				seq_no=?, receipt_at=?, updated_at=?
			WHERE app_sms_id=?`
		args = []any{r.DeliveryStatus, r.DeliveryMessage, newSeq, r.ReceiptAtMS, r.ReceiptAtMS, r.AppSmsID}
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return ReceiptApplyResult{}, fmt.Errorf("回写回执失败: %w", err)
	}
	n, _ := res.RowsAffected()
	out.Applied = n > 0
	return out, nil
}

// RefreshPendingRoute 在 stale 续发认领成功后，把记录的路由/脱敏视图刷新为
// 当前快照的解析结果（绑定可能已改投其他通道；不刷新会导致后续回执被通道
// 归属守卫误判为跨通道伪造）。仅对 pending 行生效，终态行不动。
func (s *Store) RefreshPendingRoute(ctx context.Context, appSmsID, channelID, templateCode,
	mobileMasked, paramsMasked string, nowMS int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		UPDATE sms_send SET channel_id=?, template_code=?, mobile_masked=?,
			params_masked=?, updated_at=?
		WHERE app_sms_id=? AND status='pending'`,
		channelID, templateCode, mobileMasked, paramsMasked, nowMS, appSmsID)
	if err != nil {
		return fmt.Errorf("刷新续发路由失败: %w", err)
	}
	return nil
}

// StalePendingIDs 返回最近一次状态变更（updated_at）早于等于 now-staleMS 的
// 在途 pending 记录 ID（恰等边界视为 stale），按更新时间升序。续发认领会前推
// updated_at，故已被认领的记录不会在同一补发窗口内被重复扫描到。
func (s *Store) StalePendingIDs(ctx context.Context, nowMS, staleMS int64, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	cutoff := nowMS - staleMS
	rows, err := s.db.QueryContext(ctx, `
		SELECT app_sms_id FROM sms_send
		WHERE status='pending' AND updated_at <= ?
		ORDER BY updated_at ASC, app_sms_id ASC LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListSends 按筛选条件分页返回发送记录（created_at DESC）与满足条件的总数。
func (s *Store) ListSends(ctx context.Context, f SendFilter) ([]SendRecord, int64, error) {
	where, args := buildSendFilter(f)

	var total int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sms_send`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	queryArgs := append(append([]any{}, args...), limit, f.Offset)
	rows, err := s.db.QueryContext(ctx,
		sendColumns+` FROM sms_send`+where+
			` ORDER BY created_at DESC, app_sms_id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []SendRecord
	for rows.Next() {
		rec, err := scanSend(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *rec)
	}
	return out, total, rows.Err()
}

func buildSendFilter(f SendFilter) (string, []any) {
	var conds []string
	var args []any
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	if f.SMSType != "" {
		conds = append(conds, "sms_type = ?")
		args = append(args, f.SMSType)
	}
	if f.ChannelID != "" {
		conds = append(conds, "channel_id = ?")
		args = append(args, f.ChannelID)
	}
	if f.Source != "" {
		conds = append(conds, "source = ?")
		args = append(args, f.Source)
	}
	if f.FromMS > 0 {
		conds = append(conds, "created_at >= ?")
		args = append(args, f.FromMS)
	}
	if f.ToMS > 0 {
		conds = append(conds, "created_at <= ?")
		args = append(args, f.ToMS)
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

const sendColumns = `
	SELECT app_sms_id, event_id, source, channel_id, sms_type,
		mobile_masked, params_masked, template_code, status,
		provider_msg_id, provider_status, provider_message,
		delivery_status, delivery_message, seq_no, receipt_at,
		error_kind, latency_ms, created_at, updated_at, attempts`

// rowScanner 同时兼容 *sql.Row 与 *sql.Rows。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSend(row rowScanner) (*SendRecord, error) {
	var r SendRecord
	err := row.Scan(
		&r.AppSmsID, &r.EventID, &r.Source, &r.ChannelID, &r.SMSType,
		&r.MobileMasked, &r.ParamsMasked, &r.TemplateCode, &r.Status,
		&r.ProviderMsgID, &r.ProviderStatus, &r.ProviderMessage,
		&r.DeliveryStatus, &r.DeliveryMessage, &r.SeqNo, &r.ReceiptAt,
		&r.ErrorKind, &r.LatencyMS, &r.CreatedAt, &r.UpdatedAt, &r.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
