package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateChannel 新建通道并在同一事务写入其密钥（密钥 AES-GCM 加密落库）。
func (s *Store) CreateChannel(ctx context.Context, name, description, configJSON string, secrets map[string]string) (*Channel, error) {
	if name == "" {
		return nil, errors.New("通道名称不能为空")
	}
	if configJSON == "" {
		configJSON = "{}"
	}
	ch := &Channel{
		ID:          uuid.NewString(),
		Name:        name,
		Description: description,
		Enabled:     true,
		ConfigJSON:  configJSON,
		CreatedAt:   time.Now().UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO channels (id, name, description, enabled, config_json, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?)`,
		ch.ID, ch.Name, ch.Description, ch.ConfigJSON, ch.CreatedAt, ch.UpdatedAt)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("创建通道失败: %w", err)
	}
	if err := putSecretsTx(ctx, tx, s.dataKey, ch.ID, secrets); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.notifyChanged()
	return ch, nil
}

// GetChannel 按 ID 查询通道；不存在返回 ErrNotFound。
func (s *Store) GetChannel(ctx context.Context, id string) (*Channel, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, enabled, config_json, created_at, updated_at
		FROM channels WHERE id = ?`, id)
	return scanChannel(row)
}

// ListChannels 返回全部通道（按创建时间、ID 排序）。
func (s *Store) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, enabled, config_json, created_at, updated_at
		FROM channels ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var ch Channel
		var enabled int
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &enabled,
			&ch.ConfigJSON, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, err
		}
		ch.Enabled = enabled == 1
		out = append(out, ch)
	}
	return out, rows.Err()
}

// UpdateChannel 更新通道可变字段（不含启停，不含密钥）。
func (s *Store) UpdateChannel(ctx context.Context, ch Channel) error {
	if ch.ID == "" {
		return errors.New("通道 ID 不能为空")
	}
	if ch.Name == "" {
		return errors.New("通道名称不能为空")
	}
	if ch.ConfigJSON == "" {
		ch.ConfigJSON = "{}"
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE channels SET name = ?, description = ?, enabled = ?,
		    config_json = ?, updated_at = ?
		WHERE id = ?`,
		ch.Name, ch.Description, boolToInt(ch.Enabled), ch.ConfigJSON,
		time.Now().UnixMilli(), ch.ID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_ = tx.Rollback()
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyChanged()
	return nil
}

// SetChannelEnabled 单独启停通道。
func (s *Store) SetChannelEnabled(ctx context.Context, id string, enabled bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE channels SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolToInt(enabled), time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.notifyChanged()
	return nil
}

// DeleteChannel 删除通道；密钥与绑定靠外键级联删除。
func (s *Store) DeleteChannel(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.notifyChanged()
	return nil
}

// PutChannelSecrets 覆盖写入若干密钥（UPSERT）；未给出的其他密钥保持不变。
func (s *Store) PutChannelSecrets(ctx context.Context, channelID string, secrets map[string]string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := putSecretsTx(ctx, tx, s.dataKey, channelID, secrets); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyChanged()
	return nil
}

// DeleteChannelSecret 删除单个密钥。
func (s *Store) DeleteChannelSecret(ctx context.Context, channelID, name string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM channel_secrets WHERE channel_id = ? AND name = ?`, channelID, name); err != nil {
		return err
	}
	s.notifyChanged()
	return nil
}

// GetChannelSecrets 返回通道全部密钥的明文（仅运行时/快照使用）。
func (s *Store) GetChannelSecrets(ctx context.Context, channelID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, ciphertext FROM channel_secrets WHERE channel_id = ?`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSecrets(rows, s.dataKey)
}

// GetChannelSecretsMasked 返回通道密钥的脱敏视图（API 层使用）。
func (s *Store) GetChannelSecretsMasked(ctx context.Context, channelID string) (map[string]MaskedSecret, error) {
	plain, err := s.GetChannelSecrets(ctx, channelID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]MaskedSecret, len(plain))
	for name, v := range plain {
		out[name] = MaskedSecret{Value: MaskSecret(v), Set: true}
	}
	return out, nil
}

func scanChannel(row *sql.Row) (*Channel, error) {
	var ch Channel
	var enabled int
	err := row.Scan(&ch.ID, &ch.Name, &ch.Description, &enabled,
		&ch.ConfigJSON, &ch.CreatedAt, &ch.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ch.Enabled = enabled == 1
	return &ch, nil
}

func scanSecrets(rows *sql.Rows, dataKey []byte) (map[string]string, error) {
	out := map[string]string{}
	for rows.Next() {
		var name, ct string
		if err := rows.Scan(&name, &ct); err != nil {
			return nil, err
		}
		plain, err := Decrypt(dataKey, ct)
		if err != nil {
			return nil, fmt.Errorf("解密通道密钥 %s 失败: %w", name, err)
		}
		out[name] = string(plain)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
