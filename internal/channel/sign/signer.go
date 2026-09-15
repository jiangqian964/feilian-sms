// Package sign 是可插拔的请求签名策略库，内置三种策略：
//   - none：不签名；
//   - sha1_salt：SHA-1 加盐（SHA1(secret‖raw) 单次摘要，两位小写补零 hex）；
//   - hmac_sha256：HMAC-SHA256，hex/base64 输出。
package sign

import "fmt"

// 策略标识（持久化到 channels.config_json，禁止改名）。
const (
	StrategyNone       = "none"
	StrategySHA1Salt   = "sha1_salt"
	StrategyHMACSHA256 = "hmac_sha256"
	EncodingHex        = "hex"
	EncodingBase64     = "base64"
)

// Signer 对「待签名原文 raw」用「密钥 secret」计算签名。
type Signer interface {
	// Name 返回策略标识。
	Name() string
	// Sign 返回签名字符串；none 策略返回空串与 nil。
	Sign(secret, raw string) (string, error)
}

// New 按策略名构造签名器；encoding 仅 hmac_sha256 使用（空=hex）。
func New(strategy, encoding string) (Signer, error) {
	switch strategy {
	case StrategyNone:
		return noneSigner{}, nil
	case StrategySHA1Salt:
		return saltedSHA1Signer{}, nil
	case StrategyHMACSHA256:
		enc := encoding
		if enc == "" {
			enc = EncodingHex
		}
		if enc != EncodingHex && enc != EncodingBase64 {
			return nil, fmt.Errorf("hmac_sha256 不支持的编码 %q（可选 hex/base64）", enc)
		}
		return hmacSigner{encoding: enc}, nil
	default:
		return nil, fmt.Errorf("未知签名策略 %q（支持 none/sha1_salt/hmac_sha256）", strategy)
	}
}
