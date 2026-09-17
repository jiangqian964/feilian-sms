package store

import (
	"context"
	"sync"
	"sync/atomic"
)

// Cache 是配置热加载快照：写操作后整体替换，读取无锁 O(1)。
type Cache struct {
	store *Store

	// reloadMu 串行化快照重建（读多写少）。
	reloadMu sync.Mutex
	cur      atomic.Pointer[Snapshot]

	// onReloadError 是写后回调中快照重建失败的可选通知钩子（引导期注入，
	// 例如接 Zap Error 日志）；为 nil 时失败只能被静默丢弃。
	// 仅在开始服务前设置，运行期不做并发保护。
	onReloadError func(error)
}

// NewCache 构建首帧快照并把自身注册为 Store 的写后回调，
// 此后任何配置写操作都会自动触发快照替换。
func NewCache(s *Store) (*Cache, error) {
	c := &Cache{store: s}
	s.SetOnChanged(func() {
		if err := c.Reload(); err != nil && c.onReloadError != nil {
			c.onReloadError(err)
		}
	})
	if err := c.Reload(); err != nil {
		return nil, err
	}
	return c, nil
}

// SetReloadErrorHook 注入「写后快照重建失败」的通知回调。
// 必须在开始对外服务前（引导装配阶段）调用。
func (c *Cache) SetReloadErrorHook(fn func(error)) {
	c.onReloadError = fn
}

// Current 返回当前不可变快照（永不为 nil；调用方不得修改返回内容）。
func (c *Cache) Current() *Snapshot {
	return c.cur.Load()
}

// Reload 从 DB 全量重建快照并版本号 +1。
func (c *Cache) Reload() error {
	c.reloadMu.Lock()
	defer c.reloadMu.Unlock()

	ctx := context.Background()
	settings, err := c.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	channels, err := c.store.ListChannels(ctx)
	if err != nil {
		return err
	}
	runtimes := make([]RuntimeChannel, 0, len(channels))
	for _, ch := range channels {
		secrets, err := c.store.GetChannelSecrets(ctx, ch.ID)
		if err != nil {
			return err
		}
		runtimes = append(runtimes, RuntimeChannel{Channel: ch, Secrets: secrets})
	}
	bindings, err := c.store.ListBindings(ctx)
	if err != nil {
		return err
	}

	var version int64 = 1
	if old := c.cur.Load(); old != nil {
		version = old.Version + 1
	}
	c.cur.Store(&Snapshot{
		Version:  version,
		Settings: settings,
		Channels: runtimes,
		Bindings: bindings,
	})
	return nil
}
