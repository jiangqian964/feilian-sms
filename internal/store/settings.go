package store

import (
	"context"
	"fmt"
	"time"
)

// GetSettings 读取系统设置单行；EncryptKey 在内存中解密。
func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	var (
		st Settings
		ct string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT feilian_verification_token, feilian_encrypt_key, webhook_path,
		       public_base_url, downstream_timeout_ms, stale_pending_ms, updated_at
		FROM system_settings WHERE id = 1`,
	).Scan(&st.VerificationToken, &ct, &st.WebhookPath, &st.PublicBaseURL,
		&st.DownstreamTimeoutMS, &st.StalePendingMS, &st.UpdatedAt)
	if err != nil {
		return Settings{}, fmt.Errorf("读取系统设置失败: %w", err)
	}
	if ct != "" {
		plain, err := Decrypt(s.dataKey, ct)
		if err != nil {
			return Settings{}, fmt.Errorf("解密 Encrypt Key 失败: %w", err)
		}
		st.EncryptKey = string(plain)
	}
	return st, nil
}

// UpdateSettings 全量更新系统设置（单行）；EncryptKey 空串表示清空（
// 「空提交=不修改」语义由 API 层读改写合并后调用）。
func (s *Store) UpdateSettings(ctx context.Context, st Settings) error {
	encryptCT := ""
	if st.EncryptKey != "" {
		ct, err := Encrypt(s.dataKey, []byte(st.EncryptKey))
		if err != nil {
			return err
		}
		encryptCT = ct
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE system_settings SET
			feilian_verification_token = ?,
			feilian_encrypt_key = ?,
			webhook_path = ?,
			public_base_url = ?,
			downstream_timeout_ms = ?,
			stale_pending_ms = ?,
			updated_at = ?
		WHERE id = 1`,
		st.VerificationToken, encryptCT, st.WebhookPath, st.PublicBaseURL,
		st.DownstreamTimeoutMS, st.StalePendingMS, time.Now().UnixMilli())
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("更新系统设置失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyChanged()
	return nil
}
