package service

import (
	"context"
	"testing"

	"feilian-sms/internal/store"
)

func newTestRuntime(t *testing.T) (*store.Store, *store.Cache) {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	st, err := store.Open(t.TempDir()+"/sms.db", key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cache, err := store.NewCache(st)
	if err != nil {
		t.Fatal(err)
	}
	return st, cache
}

func TestSettingsRuntimeHotReload(t *testing.T) {
	st, cache := newTestRuntime(t)
	rt := NewSettingsRuntime(cache)
	ctx := context.Background()

	if rt.WebhookPath() != "/feilian/sms/events" || rt.DownstreamTimeoutMS() != 2000 ||
		rt.StalePendingMS() != 120000 || rt.VerificationToken() != "" {
		t.Fatalf("默认设置异常: %#v", rt.Current())
	}

	s := rt.Current()
	s.VerificationToken = "vt-new"
	s.WebhookPath = "/custom/hook"
	s.DownstreamTimeoutMS = 1500
	s.StalePendingMS = 60000
	s.PublicBaseURL = "http://10.0.0.1:8080/"
	if err := st.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}

	if rt.VerificationToken() != "vt-new" || rt.WebhookPath() != "/custom/hook" ||
		rt.DownstreamTimeoutMS() != 1500 || rt.StalePendingMS() != 60000 {
		t.Fatal("PUT 后设置应在同进程立即生效")
	}
	if got := rt.ReceiptURL("ch1"); got != "http://10.0.0.1:8080/receipts/ch1" {
		t.Fatalf("ReceiptURL 尾斜杠处理错误: %s", got)
	}
}

func TestReceiptURLNoBase(t *testing.T) {
	_, cache := newTestRuntime(t)
	rt := NewSettingsRuntime(cache)
	if got := rt.ReceiptURL("ch9"); got != "/receipts/ch9" {
		t.Fatalf("无基址应返回相对路径: %s", got)
	}
}
