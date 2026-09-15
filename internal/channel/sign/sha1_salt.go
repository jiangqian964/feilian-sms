package sign

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

// saltedSHA1Signer 是常见的「先加盐后摘要」SHA-1 签名：
//
//	md = SHA-1
//	md.update(salt=签名密钥 UTF-8)
//	digest(raw=待签名串 UTF-8)   // 单次摘要，等价 SHA-1(secret ‖ raw)
//	每个字节两位小写十六进制（<0x10 前补 0）
type saltedSHA1Signer struct{}

func (saltedSHA1Signer) Name() string { return StrategySHA1Salt }

func (saltedSHA1Signer) Sign(secret, raw string) (string, error) {
	h := sha1.New()
	if _, err := h.Write([]byte(secret)); err != nil {
		return "", fmt.Errorf("SHA-1 写入 salt 失败: %w", err)
	}
	if _, err := h.Write([]byte(raw)); err != nil {
		return "", fmt.Errorf("SHA-1 写入待签名串失败: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
