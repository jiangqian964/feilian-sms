package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// putSecretsTx 在给定事务内 UPSERT 通道密钥（明文仅在调用栈内短暂存在）。
func putSecretsTx(ctx context.Context, tx *sql.Tx, dataKey []byte, channelID string, secrets map[string]string) error {
	now := time.Now().UnixMilli()
	for name, plain := range secrets {
		if name == "" {
			return fmt.Errorf("通道 %s 的密钥名不能为空", channelID)
		}
		ct, err := Encrypt(dataKey, []byte(plain))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO channel_secrets (channel_id, name, ciphertext, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(channel_id, name) DO UPDATE SET
				ciphertext = excluded.ciphertext,
				updated_at = excluded.updated_at`,
			channelID, name, ct, now); err != nil {
			return fmt.Errorf("写入通道密钥 %s 失败: %w", name, err)
		}
	}
	return nil
}

// jsonIntSlice 把 []int 序列化为 JSON 文本（nil 输出 []）。
func jsonIntSlice(v []int) (string, error) {
	if v == nil {
		v = []int{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseIntSlice 解析 param_index JSON（空值返回空切片）。
func parseIntSlice(raw string) []int {
	out := []int{}
	if raw == "" || raw == "[]" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}
