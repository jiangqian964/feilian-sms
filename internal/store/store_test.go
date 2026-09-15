package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	path := filepath.Join(t.TempDir(), "sms.db")
	s, err := Open(path, key)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenMigrateIdempotent(t *testing.T) {
	key := make([]byte, 32)
	path := filepath.Join(t.TempDir(), "sms.db")
	s1, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, key)
	if err != nil {
		t.Fatalf("二次打开迁移应幂等: %v", err)
	}
	_ = s2.Close()
}

func TestDefaultSettings(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	st, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.WebhookPath != "/feilian/sms/events" {
		t.Errorf("默认 webhook_path = %q", st.WebhookPath)
	}
	if st.DownstreamTimeoutMS != 2000 {
		t.Errorf("默认 downstream_timeout_ms = %d", st.DownstreamTimeoutMS)
	}
	if st.StalePendingMS != 120000 {
		t.Errorf("默认 stale_pending_ms = %d", st.StalePendingMS)
	}
	if st.VerificationToken != "" || st.EncryptKey != "" || st.PublicBaseURL != "" {
		t.Errorf("首启字符串字段应为空: %#v", st)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	want := Settings{
		VerificationToken:   "vt-xyz",
		EncryptKey:          "ek-secret",
		WebhookPath:         "/custom/hook",
		PublicBaseURL:       "http://10.0.0.1:8080",
		DownstreamTimeoutMS: 1500,
		StalePendingMS:      60000,
	}
	if err := s.UpdateSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt <= 0 {
		t.Fatalf("updated_at 应由存储层写入，实际 %d", got.UpdatedAt)
	}
	got.UpdatedAt = 0
	if got != want {
		t.Fatalf("设置往返不一致\n got: %#v\nwant: %#v", got, want)
	}
}

func TestSettingsPersistAcrossReopen(t *testing.T) {
	key := make([]byte, 32)
	path := filepath.Join(t.TempDir(), "sms.db")
	s, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	err = s.UpdateSettings(context.Background(), Settings{
		VerificationToken:   "vt",
		EncryptKey:          "ek",
		WebhookPath:         "/h",
		DownstreamTimeoutMS: 1,
		StalePendingMS:      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	st, err := s2.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.VerificationToken != "vt" || st.EncryptKey != "ek" || st.WebhookPath != "/h" {
		t.Fatalf("持久化设置异常: %#v", st)
	}
}

func TestNewStoreRejectsBadKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sms.db")
	if _, err := Open(path, []byte("short")); err == nil {
		os.Remove(path)
		t.Fatal("非 32 字节数据密钥必须拒绝打开")
	}
}
