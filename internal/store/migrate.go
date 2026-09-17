package store

import (
	"context"
	"fmt"
)

type migration struct {
	version int
	stmts   []string
}

// migrations 按版本顺序执行；user_version 记录已应用版本。
// T7 将在此追加 sms_send 表（v2）。
var migrations = []migration{
	{
		version: 1,
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS system_settings (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				feilian_verification_token TEXT NOT NULL DEFAULT '',
				feilian_encrypt_key TEXT NOT NULL DEFAULT '',
				webhook_path TEXT NOT NULL DEFAULT '/feilian/sms/events',
				public_base_url TEXT NOT NULL DEFAULT '',
				downstream_timeout_ms INTEGER NOT NULL DEFAULT 2000,
				stale_pending_ms INTEGER NOT NULL DEFAULT 120000,
				updated_at INTEGER NOT NULL DEFAULT 0
			)`,
			`INSERT OR IGNORE INTO system_settings (id, updated_at) VALUES (1, 0)`,
			`CREATE TABLE IF NOT EXISTS channels (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				enabled INTEGER NOT NULL DEFAULT 1,
				config_json TEXT NOT NULL DEFAULT '{}',
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS channel_secrets (
				channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
				name TEXT NOT NULL,
				ciphertext TEXT NOT NULL,
				updated_at INTEGER NOT NULL,
				PRIMARY KEY (channel_id, name)
			)`,
			`CREATE TABLE IF NOT EXISTS bindings (
				sms_type TEXT PRIMARY KEY,
				channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
				template_code TEXT NOT NULL DEFAULT '',
				param_index TEXT NOT NULL DEFAULT '[]',
				enabled INTEGER NOT NULL DEFAULT 1,
				updated_at INTEGER NOT NULL
			)`,
		},
	},
	{
		version: 2,
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS sms_send (
				app_sms_id TEXT PRIMARY KEY,
				event_id TEXT NOT NULL DEFAULT '',
				source TEXT NOT NULL DEFAULT 'feilian',
				channel_id TEXT NOT NULL DEFAULT '',
				sms_type TEXT NOT NULL DEFAULT '',
				mobile_masked TEXT NOT NULL DEFAULT '',
				params_masked TEXT NOT NULL DEFAULT '[]',
				template_code TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'pending',
				provider_msg_id TEXT NOT NULL DEFAULT '',
				provider_status TEXT NOT NULL DEFAULT '',
				provider_message TEXT NOT NULL DEFAULT '',
				delivery_status TEXT NOT NULL DEFAULT '',
				delivery_message TEXT NOT NULL DEFAULT '',
				seq_no INTEGER NOT NULL DEFAULT 0,
				receipt_at INTEGER NOT NULL DEFAULT 0,
				error_kind TEXT NOT NULL DEFAULT '',
				latency_ms INTEGER NOT NULL DEFAULT 0,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_sms_send_status_created ON sms_send(status, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_sms_send_event ON sms_send(event_id)`,
			`CREATE INDEX IF NOT EXISTS idx_sms_send_channel ON sms_send(channel_id, created_at)`,
		},
	},
	{
		version: 3,
		stmts: []string{
			// attempts：补发认领次数（每次 ClaimStalePending 原子 +1），用于补发上限保护。
			`ALTER TABLE sms_send ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0`,
			// payload_enc：原始短信对象（feilian.SMSObject JSON）经数据密钥加密后的密文，
			// 仅供补发 worker 解密重放；不进入列表查询，也不经过任何管理 API 回显。
			`ALTER TABLE sms_send ADD COLUMN payload_enc TEXT NOT NULL DEFAULT ''`,
			// receipt_auth_token：厂商异步回执的可选共享密钥（空=不校验，保持默认开放）。
			`ALTER TABLE system_settings ADD COLUMN receipt_auth_token TEXT NOT NULL DEFAULT ''`,
		},
	},
}

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("读取 user_version 失败: %w", err)
	}
	for _, m := range migrations {
		if m.version <= version {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("迁移 v%d 开启事务失败: %w", m.version, err)
		}
		for _, stmt := range m.stmts {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("迁移 v%d 执行失败: %w", m.version, err)
			}
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("迁移 v%d 写入版本失败: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("迁移 v%d 提交失败: %w", m.version, err)
		}
	}
	return nil
}
