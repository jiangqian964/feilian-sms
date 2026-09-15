package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestChannelCRUD(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	ch, err := s.CreateChannel(ctx, "示例厂商", "示例渠道", `{"request":{"url":"https://x"}}`, map[string]string{
		"appSecret": "super-secret-9988",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID == "" || !ch.Enabled || ch.ConfigJSON != `{"request":{"url":"https://x"}}` {
		t.Fatalf("新建通道断言失败: %#v", ch)
	}

	got, err := s.GetChannel(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "示例厂商" {
		t.Errorf("name = %q", got.Name)
	}

	list, err := s.ListChannels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != ch.ID {
		t.Fatalf("列表异常: %#v", list)
	}

	got.Name = "示例厂商2"
	got.Description = "改"
	got.ConfigJSON = `{"request":{"url":"https://y"}}`
	if err := s.UpdateChannel(ctx, *got); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.GetChannel(ctx, ch.ID)
	if got2.Name != "示例厂商2" || got2.ConfigJSON != `{"request":{"url":"https://y"}}` {
		t.Fatalf("更新失败: %#v", got2)
	}

	if err := s.SetChannelEnabled(ctx, ch.ID, false); err != nil {
		t.Fatal(err)
	}
	got3, _ := s.GetChannel(ctx, ch.ID)
	if got3.Enabled {
		t.Fatal("停用未生效")
	}
}

func TestGetChannelNotFound(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.GetChannel(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

func TestChannelSecretsMaskAndPlaintext(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ch, err := s.CreateChannel(ctx, "c", "", `{}`, map[string]string{
		"appSecret": "abcdef123456",
		"token":     "t1",
	})
	if err != nil {
		t.Fatal(err)
	}

	plain, err := s.GetChannelSecrets(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plain["appSecret"] != "abcdef123456" || plain["token"] != "t1" {
		t.Fatalf("内部解密视图错误: %#v", plain)
	}

	masked, err := s.GetChannelSecretsMasked(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !masked["appSecret"].Set || masked["appSecret"].Value == "abcdef123456" {
		t.Fatalf("应回掩码且标记已设置: %#v", masked)
	}
	if !strings.HasPrefix(masked["appSecret"].Value, "ab") ||
		!strings.HasSuffix(masked["appSecret"].Value, "56") {
		t.Fatalf("掩码应保留前2后2: %q", masked["appSecret"].Value)
	}
	if masked["appSecret"].Value == masked["token"].Value {
		// 短值与长值掩码形态应不同
		t.Fatalf("掩码疑似返回明文: %#v", masked)
	}
	if masked["token"].Value != "****" {
		t.Fatalf("过短密钥应统一 ****: %q", masked["token"].Value)
	}

	if err := s.DeleteChannelSecret(ctx, ch.ID, "token"); err != nil {
		t.Fatal(err)
	}
	masked2, _ := s.GetChannelSecretsMasked(ctx, ch.ID)
	if masked2["token"].Set {
		t.Fatal("删除密钥后 set 应为 false")
	}
}

func TestChannelSecretsEmptySubmitKeepsOld(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ch, _ := s.CreateChannel(ctx, "c", "", `{}`, map[string]string{"a": "1", "b": "2"})

	// 仅更新 a：未提交的 b 必须保持旧值（空提交=不修改语义由上层合并后表现为只 Put a）。
	if err := s.PutChannelSecrets(ctx, ch.ID, map[string]string{"a": "3"}); err != nil {
		t.Fatal(err)
	}
	plain, _ := s.GetChannelSecrets(ctx, ch.ID)
	if plain["a"] != "3" || plain["b"] != "2" {
		t.Fatalf("未提交密钥不应被覆盖: %#v", plain)
	}
}

func TestChannelSecretsCiphertextRotates(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ch, _ := s.CreateChannel(ctx, "c", "", `{}`, map[string]string{"a": "old-value"})
	oldCT := s.mustSecretCiphertext(t, ch.ID, "a")

	if err := s.PutChannelSecrets(ctx, ch.ID, map[string]string{"a": "new-value"}); err != nil {
		t.Fatal(err)
	}
	newCT := s.mustSecretCiphertext(t, ch.ID, "a")
	if oldCT == newCT {
		t.Fatal("覆盖后密文必须更换（随机 nonce + 新明文）")
	}
	plain, _ := s.GetChannelSecrets(ctx, ch.ID)
	if plain["a"] != "new-value" {
		t.Fatalf("解密应为新值: %q", plain["a"])
	}
}

func TestNoPlaintextInDBFile(t *testing.T) {
	path := t.TempDir() + "/sms.db"
	key := make([]byte, 32)
	s, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	secret := "PLAINTEXT-SECRET-XYZ-123"
	_, err = s.CreateChannel(context.Background(), "c", "", `{}`, map[string]string{"appSecret": secret})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	dump, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dump), secret) {
		t.Fatal("DB 主文件中不得出现明文密钥")
	}
}

func TestDeleteChannelCascades(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ch, _ := s.CreateChannel(ctx, "c", "", `{}`, map[string]string{"a": "1"})
	if err := s.ReplaceBindings(ctx, []Binding{{
		SMSType: "code", ChannelID: ch.ID, TemplateCode: "T1", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetChannel(ctx, ch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("删除后应 NotFound")
	}
	binds, _ := s.ListBindings(ctx)
	for _, b := range binds {
		if b.ChannelID == ch.ID {
			t.Fatal("删除通道应级联删除绑定")
		}
	}
	if _, err := s.GetChannelSecrets(ctx, ch.ID); err != nil {
		t.Fatal("无密钥时应返回空 map 而非错误")
	}
}

// mustSecretCiphertext 直接读取指定密钥的密文（测试用，验证密文轮换）。
func (s *Store) mustSecretCiphertext(t *testing.T, channelID, name string) string {
	t.Helper()
	var ct string
	err := s.db.QueryRow(
		`SELECT ciphertext FROM channel_secrets WHERE channel_id = ? AND name = ?`,
		channelID, name).Scan(&ct)
	if err != nil {
		t.Fatalf("读取密钥密文失败: %v", err)
	}
	return ct
}

func TestBindingsUpsert(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ch1, _ := s.CreateChannel(ctx, "c1", "", `{}`, nil)
	ch2, _ := s.CreateChannel(ctx, "c2", "", `{}`, nil)

	first := []Binding{
		{SMSType: "code", ChannelID: ch1.ID, TemplateCode: "T1", ParamIndex: []int{0, 2, 1}, Enabled: true},
		{SMSType: "alert", ChannelID: ch1.ID, TemplateCode: "T2", Enabled: false},
	}
	if err := s.ReplaceBindings(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := []Binding{
		{SMSType: "code", ChannelID: ch2.ID, TemplateCode: "T3", ParamIndex: []int{1, 0}, Enabled: true},
		{SMSType: "guest_wifi", ChannelID: ch2.ID, TemplateCode: "T4", Enabled: true},
	}
	if err := s.ReplaceBindings(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("UPSERT 不应产生重复行，数量 = %d", len(got))
	}
	byType := map[string]Binding{}
	for _, b := range got {
		byType[b.SMSType] = b
	}
	code := byType["code"]
	if code.ChannelID != ch2.ID || code.TemplateCode != "T3" ||
		len(code.ParamIndex) != 2 || code.ParamIndex[0] != 1 || code.ParamIndex[1] != 0 {
		t.Fatalf("code 绑定未更新: %#v", code)
	}
	if !byType["guest_wifi"].Enabled || byType["alert"].Enabled {
		t.Fatalf("其他绑定状态异常: %#v", byType)
	}
}
