package store

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func newTestKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("生成测试密钥失败: %v", err)
	}
	return key
}

func TestSecretBoxRoundTrip(t *testing.T) {
	key := newTestKey(t)
	plaintext := []byte("appSecret-示例厂商渠道-敏感")
	sealed, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Seal 失败: %v", err)
	}
	if sealed == "" {
		t.Fatal("密文不能为空")
	}
	if strings.Contains(sealed, string(plaintext)) {
		t.Fatal("密文不得包含明文")
	}
	if _, err := base64.StdEncoding.DecodeString(sealed); err != nil {
		t.Fatalf("密文应为 base64: %v", err)
	}
	opened, err := Decrypt(key, sealed)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if string(opened) != string(plaintext) {
		t.Fatalf("还原 = %q, 期望 %q", opened, plaintext)
	}
}

func TestSecretBoxRandomNonce(t *testing.T) {
	key := newTestKey(t)
	plaintext := []byte("same-value")
	c1, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if c1 == c2 {
		t.Fatal("随机 nonce 下同一明文两次密文必须不同")
	}
}

func TestSecretBoxWrongKey(t *testing.T) {
	k1 := newTestKey(t)
	k2 := newTestKey(t)
	sealed, err := Encrypt(k1, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(k2, sealed); err == nil {
		t.Fatal("错误密钥必须解密失败")
	}
}

func TestSecretBoxTampered(t *testing.T) {
	key := newTestKey(t)
	sealed, err := Encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(sealed)
	raw[len(raw)-1] ^= 0xFF
	if _, err := Decrypt(key, base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("篡改密文必须失败")
	}
}

func TestSecretBoxEmptyPlaintext(t *testing.T) {
	key := newTestKey(t)
	sealed, err := Encrypt(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Decrypt(key, sealed)
	if err != nil {
		t.Fatalf("空明文往返失败: %v", err)
	}
	if len(opened) != 0 {
		t.Fatalf("空明文还原后长度 = %d", len(opened))
	}
}

func TestSecretBoxInvalidInput(t *testing.T) {
	key := newTestKey(t)
	if _, err := Encrypt([]byte("short"), []byte("x")); err == nil {
		t.Fatal("非 32 字节密钥 Seal 应报错")
	}
	if _, err := Decrypt(key, "!!!not-base64!!!"); err == nil {
		t.Fatal("非 base64 密文应报错")
	}
	if _, err := Decrypt(key, base64.StdEncoding.EncodeToString([]byte("tooshort"))); err == nil {
		t.Fatal("短于 nonce 的密文应报错")
	}
}
