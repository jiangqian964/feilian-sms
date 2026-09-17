package store

import (
	"context"
	"sync"
	"testing"
)

func TestCacheInitialSnapshot(t *testing.T) {
	s := openTestStore(t)
	c, err := NewCache(s)
	if err != nil {
		t.Fatal(err)
	}
	snap := c.Current()
	if snap.Version != 1 {
		t.Fatalf("首次快照版本 = %d, 期望 1", snap.Version)
	}
	if snap.Settings.WebhookPath != "/feilian/sms/events" {
		t.Fatalf("默认设置错误: %#v", snap.Settings)
	}
	if len(snap.Channels) != 0 || len(snap.Bindings) != 0 {
		t.Fatal("首启快照应为空集合")
	}
}

func TestCacheReloadSeesChanges(t *testing.T) {
	s := openTestStore(t)
	c, err := NewCache(s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ch, err := s.CreateChannel(ctx, "c", "", `{}`, map[string]string{"a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceBindings(ctx, []Binding{{
		SMSType: "code", ChannelID: ch.ID, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSettings(ctx, Settings{VerificationToken: "vt", WebhookPath: "/h"}); err != nil {
		t.Fatal(err)
	}

	snap := c.Current()
	if snap.Version < 3 {
		t.Fatalf("三次写后版本应 ≥3，实际 %d", snap.Version)
	}
	if snap.Settings.VerificationToken != "vt" || snap.Settings.WebhookPath != "/h" {
		t.Fatalf("设置未进入快照: %#v", snap.Settings)
	}
	if len(snap.Channels) != 1 {
		t.Fatalf("通道未进入快照: %d", len(snap.Channels))
	}
	if snap.Channels[0].Secrets["a"] != "1" {
		t.Fatalf("快照中密钥应已解密: %#v", snap.Channels[0].Secrets)
	}
	if len(snap.Bindings) != 1 || snap.Bindings[0].SMSType != "code" {
		t.Fatalf("绑定未进入快照: %#v", snap.Bindings)
	}
}

func TestCacheReloadErrorHook(t *testing.T) {
	s := openTestStore(t)
	c, err := NewCache(s)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	calls := 0
	var hookErr error
	c.SetReloadErrorHook(func(e error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		hookErr = e
	})

	// 正常写后重建成功：钩子不得触发
	if _, err := s.CreateChannel(context.Background(), "c", "", `{}`, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if calls != 0 {
		t.Fatalf("重建成功时钩子不应触发，实际 %d 次", calls)
	}
	mu.Unlock()

	// DB 关闭后重建必然失败：手动 Reload 返回错误，写后回调路径须把错误交给钩子
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Reload(); err == nil {
		t.Fatal("DB 关闭后 Reload 应返回错误")
	}
	s.onChanged() // 模拟写事务提交后的回调入口

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 || hookErr == nil {
		t.Fatalf("重建失败时钩子应收到 1 次非空错误，实际 calls=%d err=%v", calls, hookErr)
	}
}

func TestCacheConcurrentReadWrite(t *testing.T) {
	s := openTestStore(t)
	c, err := NewCache(s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	const writers = 20
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 读者持续取快照，-race 下检测数据竞争；读到的版本必须单调可见。
	wg.Add(1)
	go func() {
		defer wg.Done()
		var last int64
		for {
			select {
			case <-stop:
				return
			default:
				snap := c.Current()
				if snap.Version < last {
					t.Errorf("快照版本倒退: %d < %d", snap.Version, last)
					return
				}
				last = snap.Version
			}
		}
	}()

	// 写者：每个建一个通道并停用/改设置，触发整体替换。
	for i := 0; i < writers; i++ {
		_, err := s.CreateChannel(ctx, "c", "", `{}`, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdateSettings(ctx, Settings{VerificationToken: "final", WebhookPath: "/feilian/sms/events"}); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()

	snap := c.Current()
	if len(snap.Channels) != writers {
		t.Fatalf("最终快照通道数 = %d, 期望 %d", len(snap.Channels), writers)
	}
	if snap.Settings.VerificationToken != "final" {
		t.Fatal("最终值不可见")
	}
}
