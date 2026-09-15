package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sync"

	// 纯 Go SQLite 驱动（CGO-free），交叉编译到 Ubuntu 无需 C 工具链。
	_ "modernc.org/sqlite"
)

// Store 封装 SQLite 连接、数据密钥与写串行锁。
type Store struct {
	db      *sql.DB
	dataKey []byte

	// writeMu 串行化所有写事务（短事务），配合 busy_timeout 消除 SQLITE_BUSY。
	writeMu sync.Mutex

	// onChanged 在每次写事务提交后回调（由配置快照注册，驱动热加载）。
	onChanged func()
}

// Open 打开/创建 SQLite 库并执行迁移。dataKey 必须为 32 字节（AES-256-GCM）。
func Open(path string, dataKey []byte) (*Store, error) {
	if len(dataKey) != 32 {
		return nil, fmt.Errorf("数据密钥必须为 32 字节，实际 %d", len(dataKey))
	}

	dsn := (&url.URL{
		Scheme: "file",
		Path:   path,
		RawQuery: url.Values{
			"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)", "foreign_keys(ON)", "synchronous(NORMAL)"},
		}.Encode(),
	}).String()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 失败: %w", err)
	}
	// 单连接：写天然串行，快照重读与写入不会跨连接看到中间态。
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接 SQLite 失败: %w", err)
	}

	s := &Store{db: db, dataKey: append([]byte(nil), dataKey...)}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭连接池。
func (s *Store) Close() error {
	return s.db.Close()
}

// SetOnChanged 注册写后回调（配置缓存热加载用）。
func (s *Store) SetOnChanged(fn func()) {
	s.onChanged = fn
}

// notifyChanged 在写事务提交后触发快照刷新。
func (s *Store) notifyChanged() {
	if s.onChanged != nil {
		s.onChanged()
	}
}

// Checkpoint 强制 WAL 回灌主库（测试/运维使用）。
func (s *Store) Checkpoint(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}
