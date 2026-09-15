package store

// MaskSecret 返回密钥的脱敏形态：长度 >4 时保留前 2 后 2，中间统一 ****；
// 长度 ≤4 时整体 ****（连长度也不暴露）。
func MaskSecret(v string) string {
	if len(v) <= 4 {
		return "****"
	}
	return v[:2] + "****" + v[len(v)-2:]
}
