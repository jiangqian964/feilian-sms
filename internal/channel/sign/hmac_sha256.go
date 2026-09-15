package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// hmacSigner 计算 HMAC-SHA256(secret, raw)，输出 hex（默认）或 base64。
type hmacSigner struct {
	encoding string
}

func (s hmacSigner) Name() string { return StrategyHMACSHA256 }

func (s hmacSigner) Sign(secret, raw string) (string, error) {
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(raw)); err != nil {
		return "", fmt.Errorf("HMAC 写入失败: %w", err)
	}
	sum := mac.Sum(nil)
	switch s.encoding {
	case EncodingBase64:
		return base64.StdEncoding.EncodeToString(sum), nil
	default:
		return hex.EncodeToString(sum), nil
	}
}
