package bootstrap

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDataKeyInlineBase64(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	c := &Config{Secrets: SecretsConfig{DataKey: base64.StdEncoding.EncodeToString(raw)}}
	key, err := ResolveDataKey(c)
	if err != nil {
		t.Fatalf("内联 base64 密钥解析失败: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("密钥长度 = %d, 期望 32", len(key))
	}
	for i := range key {
		if key[i] != raw[i] {
			t.Fatalf("密钥内容不一致，位置 %d", i)
		}
	}
}

func TestResolveDataKeyInlineBadLength(t *testing.T) {
	c := &Config{Secrets: SecretsConfig{DataKey: base64.StdEncoding.EncodeToString([]byte("short"))}}
	if _, err := ResolveDataKey(c); err == nil {
		t.Fatal("非 32 字节内联密钥应报错")
	}
}

func TestResolveDataKeyGenerateFile(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "nested", "data.key")
	c := &Config{Secrets: SecretsConfig{KeyPath: keyPath}}

	key1, err := ResolveDataKey(c)
	if err != nil {
		t.Fatalf("首次生成密钥失败: %v", err)
	}
	if len(key1) != 32 {
		t.Fatalf("生成密钥长度 = %d", len(key1))
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("密钥文件未创建: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("密钥文件权限 = %o, 期望 0600", perm)
	}

	content, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("读取密钥文件失败: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(content))
	if err != nil {
		t.Fatalf("密钥文件内容不是合法 base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("落盘密钥长度 = %d", len(decoded))
	}

	key2, err := ResolveDataKey(c)
	if err != nil {
		t.Fatalf("二次读取密钥失败: %v", err)
	}
	if string(key2) != string(key1) {
		t.Fatal("二次读取应复用同一密钥文件，密钥不一致")
	}
}

func TestResolveDataKeyDefaultPath(t *testing.T) {
	dir := t.TempDir()
	c := &Config{SQLite: SQLiteConfig{Path: filepath.Join(dir, "data", "sms.db")}}
	key, err := ResolveDataKey(c)
	if err != nil {
		t.Fatalf("默认路径生成密钥失败: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("密钥长度 = %d", len(key))
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "data.key")); err != nil {
		t.Fatalf("密钥应生成在 DB 同目录: %v", err)
	}
}

func TestResolveDataKeyCorruptFile(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "data.key")
	if err := os.WriteFile(keyPath, []byte("!!!not-base64!!!"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Config{Secrets: SecretsConfig{KeyPath: keyPath}}
	if _, err := ResolveDataKey(c); err == nil {
		t.Fatal("损坏的密钥文件应报错")
	}
}

func TestResolveDataKeyInlinePrecedence(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "data.key")
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i * 2)
	}
	c := &Config{Secrets: SecretsConfig{
		DataKey: base64.StdEncoding.EncodeToString(raw),
		KeyPath: keyPath,
	}}
	key, err := ResolveDataKey(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(key) != string(raw) {
		t.Fatal("内联密钥应优先于文件")
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("内联密钥存在时不应创建密钥文件")
	}
}
