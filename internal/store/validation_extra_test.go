package store

import (
	"context"
	"errors"
	"testing"
)

// 通道与绑定的入参校验：非法输入必须在写库前被拒绝。
func TestChannelAndBindingValidation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateChannel(ctx, "", "描述", "{}", nil); err == nil {
		t.Fatal("空通道名必须报错")
	}
	if err := s.UpdateChannel(ctx, Channel{Name: "无 ID"}); err == nil {
		t.Fatal("空通道 ID 必须报错")
	}
	if err := s.UpdateChannel(ctx, Channel{ID: "x", Name: ""}); err == nil {
		t.Fatal("空通道名必须报错")
	}
	if err := s.UpdateChannel(ctx, Channel{ID: "missing", Name: "幽灵"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在通道应 ErrNotFound，实际 %v", err)
	}
	if err := s.SetChannelEnabled(ctx, "missing", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("启停不存在通道应 ErrNotFound，实际 %v", err)
	}
	if err := s.DeleteChannel(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除不存在通道应 ErrNotFound，实际 %v", err)
	}

	// ReplaceBindings 遇空 sms_type 必须回滚：报错且不留任何绑定。
	if err := s.ReplaceBindings(ctx, []Binding{
		{SMSType: "code", ChannelID: "ch-1", Enabled: true},
		{SMSType: "", ChannelID: "ch-2"},
	}); err == nil {
		t.Fatal("空 sms_type 必须报错并回滚整个事务")
	}
	got, err := s.ListBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("回滚后不应残留绑定，实际 %d 条", len(got))
	}
}

// 幂等闸门的入参校验：缺主键直接拒绝，不产生行。
func TestInsertPendingRequiresAppSmsID(t *testing.T) {
	s := openTestStore(t)
	inserted, _, err := s.InsertPendingIfAbsent(context.Background(), SendRecord{})
	if err == nil {
		t.Fatal("空 app_sms_id 必须报错")
	}
	if inserted {
		t.Fatal("校验失败不应插入")
	}
}

// PutChannelSecrets 对不存在通道仍可写入密钥行（运行时绑定校验在快照层），
// 但空提交不应产生行；这里锁定“空 map 不报错”的幂等行为。
func TestPutChannelSecretsEmptyMapNoop(t *testing.T) {
	s := openTestStore(t)
	if err := s.PutChannelSecrets(context.Background(), "ch-empty", map[string]string{}); err != nil {
		t.Fatalf("空密钥集应静默成功，实际 %v", err)
	}
}
