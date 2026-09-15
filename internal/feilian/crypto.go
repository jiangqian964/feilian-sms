package feilian

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// DecryptEvent 解密飞连加密事件：
// key = SHA256(Encrypt Key)；密文 = base64(16 字节 IV || AES-256-CBC 密文)，
// 采用严格 PKCS7 去填充（填充字节非法即判错，拒绝预言机类畸形输入）。
func DecryptEvent(encryptKey, encoded string) ([]byte, error) {
	if encryptKey == "" {
		return nil, errors.New("Encrypt Key 为空，无法解密加密事件")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("密文 base64 解码失败: %w", err)
	}
	if len(raw) < 2*aes.BlockSize {
		return nil, errors.New("密文长度不足：至少需要 IV(16B)+一个密文块(16B)")
	}

	keySum := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(keySum[:])
	if err != nil {
		return nil, fmt.Errorf("构造 AES cipher 失败: %w", err)
	}
	iv, ciphertext := raw[:aes.BlockSize], raw[aes.BlockSize:]
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("密文长度不是 AES 块大小的整数倍")
	}

	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)

	return pkcs7Unpad(plain, aes.BlockSize)
}

// pkcs7Unpad 严格校验并移除 PKCS7 填充。
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	n := int(data[len(data)-1])
	if n < 1 || n > blockSize || n > len(data) {
		return nil, errors.New("PKCS7 填充非法：尾字节越界")
	}
	for i := len(data) - n; i < len(data); i++ {
		if int(data[i]) != n {
			return nil, errors.New("PKCS7 填充非法：填充字节不一致")
		}
	}
	return data[:len(data)-n], nil
}
