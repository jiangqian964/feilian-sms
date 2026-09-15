package store

import (
	"context"
	"fmt"
	"time"
)

// ListBindings 返回全部场景绑定（按 sms_type 排序）。
func (s *Store) ListBindings(ctx context.Context) ([]Binding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sms_type, channel_id, template_code, param_index, enabled, updated_at
		FROM bindings ORDER BY sms_type ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Binding
	for rows.Next() {
		var b Binding
		var paramIndex string
		var enabled int
		if err := rows.Scan(&b.SMSType, &b.ChannelID, &b.TemplateCode,
			&paramIndex, &enabled, &b.UpdatedAt); err != nil {
			return nil, err
		}
		b.ParamIndex = parseIntSlice(paramIndex)
		b.Enabled = enabled == 1
		out = append(out, b)
	}
	return out, rows.Err()
}

// ReplaceBindings 在单个事务内批量 UPSERT 绑定（不删除未出现的其他行）。
func (s *Store) ReplaceBindings(ctx context.Context, bindings []Binding) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for _, b := range bindings {
		if b.SMSType == "" {
			_ = tx.Rollback()
			return fmt.Errorf("绑定的 sms_type 不能为空")
		}
		pi, err := jsonIntSlice(b.ParamIndex)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bindings (sms_type, channel_id, template_code, param_index, enabled, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(sms_type) DO UPDATE SET
				channel_id = excluded.channel_id,
				template_code = excluded.template_code,
				param_index = excluded.param_index,
				enabled = excluded.enabled,
				updated_at = excluded.updated_at`,
			b.SMSType, b.ChannelID, b.TemplateCode, pi, boolToInt(b.Enabled), now); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("UPSERT 绑定 %s 失败: %w", b.SMSType, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyChanged()
	return nil
}
