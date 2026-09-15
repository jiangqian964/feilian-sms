package feilian

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// 飞连官方文档「接收事件」页发布的测试向量（Java/Python/Go/Node 示例共用）：
// Encrypt Key=testkey，解密结果为 url_verification 明文。
// 文档站文本存在两处脱敏/污染，本测试以「官方密文解密结果」为权威：
//  1. 密文尾部被污染（多处出现 "123123"，Node 示例尾部还不同），无法直接
//     base64；但其前 6 个完整密文块（96B）可正常解密；
//  2. 文档注释里明文 UUID 写作 ...4cfa92401234（与全文 xxxx 一样是脱敏值），
//     而官方密文真实解密为 ...4cfa9240713e。
//
// 缺失的第 7 个密文块按 PKCS7 唯一确定：明文共 98B，补 14 字节 0x0e，
// 即末块明文为 `"}` + 14×0x0e。用同一把 key 重新加密补齐后，即得到与官方
// 算法完全等价的完整向量。原始发布串保留于此以便溯源。
const officialCiphertextAsPublished = "byxnrhs61Lk8dOCd7WEOoDA6ypOpFsM4zYahSGTKOYv2CFrRyAuHm2CB162eI2vFvn/RX3IJpwMgMTwADgjiLuEXzsvW70skf1RD5Ex2dyMOhbE70Np7m7u6ks/YxF1fSkHewOCW82IV5K+SBCqkMKntWOmSsU4123123="

const officialPlaintext = `{"challenge":"f8f76934-fcf2-49e4-8593-4cfa9240713e","token":"self-test","type":"url_verification"}`

// rebuildOfficialCiphertext 从文档污染串中恢复完整密文，
// 并以前 6 块解密结果作为文档锚点严格校验。
func rebuildOfficialCiphertext(t *testing.T) string {
	t.Helper()
	noisy := strings.ReplaceAll(officialCiphertextAsPublished, "123123", "")
	raw, err := base64.StdEncoding.DecodeString(noisy)
	if err != nil {
		t.Fatalf("文档密文去噪后仍不可解码: %v", err)
	}
	if len(raw) < 16+96 {
		t.Fatalf("文档密文长度不足以包含 6 个密文块: %d", len(raw))
	}
	keySum := sha256.Sum256([]byte("testkey"))
	block, err := aes.NewCipher(keySum[:])
	if err != nil {
		t.Fatal(err)
	}
	iv, prefix := raw[:16], raw[16:16+96]
	got := make([]byte, 96)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(got, prefix)
	if want := []byte(officialPlaintext)[:96]; !bytes.Equal(got, want) {
		t.Fatalf("文档密文前 6 块解密结果与预期明文前缀不一致\n got: %q\nwant: %q", got, want)
	}

	lastPlain := append([]byte(`"}`), bytes.Repeat([]byte{0x0e}, 14)...)
	padBlock := make([]byte, aes.BlockSize)
	cipher.NewCBCEncrypter(block, prefix[96-aes.BlockSize:]).
		CryptBlocks(padBlock, lastPlain)
	return base64.StdEncoding.EncodeToString(bytes.Join([][]byte{iv, prefix, padBlock}, nil))
}

func TestOfficialVector(t *testing.T) {
	officialCiphertext := rebuildOfficialCiphertext(t)
	plain, err := DecryptEvent("testkey", officialCiphertext)
	if err != nil {
		t.Fatalf("官方向量解密失败: %v", err)
	}
	if string(plain) != officialPlaintext {
		t.Fatalf("解密明文不匹配\n got: %s\nwant: %s", plain, officialPlaintext)
	}
	ch, ok, err := ParseChallenge(plain)
	if err != nil || !ok || ch.Challenge != "f8f76934-fcf2-49e4-8593-4cfa9240713e" {
		t.Fatalf("解密结果应能提取 challenge: ch=%#v ok=%v err=%v", ch, ok, err)
	}
}

func encryptCBC(t *testing.T, key string, plaintext []byte) string {
	t.Helper()
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+pad)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	return base64.StdEncoding.EncodeToString(bytes.Join([][]byte{iv, ct}, nil))
}

func TestDecryptRoundTrip(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"hello":"中文与特殊字符 !@#$%^&*()"}`),
		bytes.Repeat([]byte("a"), 16), // 恰好整块 → 补整块
		[]byte("x"),
	}
	for _, pt := range cases {
		enc := encryptCBC(t, "my-encrypt-key", pt)
		got, err := DecryptEvent("my-encrypt-key", enc)
		if err != nil {
			t.Fatalf("往返解密失败: %v", err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatalf("往返不一致\n got: %q\nwant: %q", got, pt)
		}
	}
}

func TestDecryptErrors(t *testing.T) {
	enc := encryptCBC(t, "k1", []byte("hello"))

	t.Run("错误密钥", func(t *testing.T) {
		if _, err := DecryptEvent("k2", enc); err == nil {
			t.Fatal("错误 Encrypt Key 必须失败")
		}
	})
	t.Run("非法 base64", func(t *testing.T) {
		if _, err := DecryptEvent("k1", "!!!not-b64!!!"); err == nil ||
			!strings.Contains(err.Error(), "base64") {
			t.Fatalf("应报 base64 错误，实际 %v", err)
		}
	})
	t.Run("短于 IV", func(t *testing.T) {
		short := base64.StdEncoding.EncodeToString([]byte("01234"))
		if _, err := DecryptEvent("k1", short); err == nil {
			t.Fatal("短于 16 字节应失败")
		}
	})
	t.Run("密文非整块", func(t *testing.T) {
		raw, _ := base64.StdEncoding.DecodeString(enc)
		bad := base64.StdEncoding.EncodeToString(append(raw, 0x01))
		if _, err := DecryptEvent("k1", bad); err == nil {
			t.Fatal("非整块密文应失败")
		}
	})
	t.Run("非法 PKCS7 填充", func(t *testing.T) {
		sum := sha256.Sum256([]byte("k1"))
		block, _ := aes.NewCipher(sum[:])
		iv := bytes.Repeat([]byte{0x00}, 16)
		// 单块全 0xFF：解密后尾字节几乎不可能是合法 PKCS7。
		ct := bytes.Repeat([]byte{0xFF}, 16)
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(ct, ct)
		bad := base64.StdEncoding.EncodeToString(append(iv, ct...))
		if _, err := DecryptEvent("k1", bad); err == nil {
			t.Fatal("非法 PKCS7 填充必须严格拒绝")
		}
	})
}
