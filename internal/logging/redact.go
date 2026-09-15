// Package logging 提供统一的 Zap 日志构造与出口脱敏。
// 脱敏在 zapcore.Core 层完成：任何调用点（含未来新增）写出的消息与字段，
// 都会先经过手机号正则兜底与“按字段名”的密钥/签名/令牌掩码，
// 不依赖各业务路径的自觉，满足 FR-13/NFR-3 的“日志绝不出现
// 密钥/sign/完整手机号/验证码”。
package logging

import (
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// redactedPlaceholder 是敏感字段被整体掩码后的统一占位值。
const redactedPlaceholder = "[REDACTED]"

// mobilePattern 匹配大陆手机号（可选 86 前缀）；调用方需再做数字边界校验，
// 避免把 12/13 位时间戳或更长数字串中的 11 位片段误判为手机号。
var mobilePattern = regexp.MustCompile(`(86)?(1[3-9]\d{9})`)

// sensitiveWords 是按“词”命中的敏感字段名词表（比较前对 key 小写化，
// 并按非字母数字字符切分），避免 design 含 sign 子串这类误伤。
var sensitiveWords = map[string]bool{
	"secret": true, "appsecret": true, "secretkey": true,
	"password": true, "passwd": true,
	"token":         true,
	"sign":          true,
	"signature":     true,
	"authorization": true,
	"auth":          true,
	"key":           true,
	"credential":    true,
}

// fieldTokenSplit 把字段名切成词（app_secret -> app/secret）。
var fieldTokenSplit = regexp.MustCompile(`[^a-z0-9]+`)

// paramKeyPattern 精确识别验证码参数类字段：params / param / param0、param1…
// 刻意不匹配 param_index（那只是绑定配置里的下标数组，并非验证码本身）。
var paramKeyPattern = regexp.MustCompile(`^params?$|^param[0-9]+$`)

// RedactString 对任意待输出文本做兜底脱敏：手机号保留前 3 后 2，其余星号；
// 86 前缀原样保留。非手机号内容（含毫秒时间戳）不受影响。
func RedactString(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range mobilePattern.FindAllStringSubmatchIndex(s, -1) {
		start, end := m[0], m[1]
		// 数字边界：紧邻字符仍是数字时视为更长数字串的片段，不脱敏。
		if start > 0 && isDigit(s[start-1]) {
			continue
		}
		if end < len(s) && isDigit(s[end]) {
			continue
		}
		bodyStart := m[4] // 第二捕获组（1[3-9]\d{9}）起点；86 前缀在其之前
		b.WriteString(s[last:bodyStart])
		b.WriteString(maskMobileNumber(s[bodyStart:end]))
		last = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// maskMobileNumber 对 11 位号身保留前 3 后 2，中间以等长星号替换。
func maskMobileNumber(body string) string {
	if len(body) <= 5 {
		return redactedPlaceholder
	}
	return body[:3] + strings.Repeat("*", len(body)-5) + body[len(body)-2:]
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// isSensitiveKey 判断字段名是否属于密钥/签名/令牌/鉴权头类。
func isSensitiveKey(lowerKey string) bool {
	if sensitiveWords[lowerKey] {
		return true
	}
	for _, token := range fieldTokenSplit.Split(lowerKey, -1) {
		if sensitiveWords[token] {
			return true
		}
	}
	return false
}

// RedactField 对单个日志字段做脱敏：
//   - 敏感名/参数类字段：无论何种类型，整体替换为 [REDACTED]；
//   - 字符串/字节串/error/Stringer：值文本过手机号正则兜底；
//   - 其余标量（数字/布尔/耗时）原样保留。
func RedactField(f zap.Field) zap.Field {
	lower := strings.ToLower(f.Key)
	if isSensitiveKey(lower) || paramKeyPattern.MatchString(lower) {
		return zap.String(f.Key, redactedPlaceholder)
	}
	switch f.Type {
	case zapcore.StringType:
		f.String = RedactString(f.String)
	case zapcore.ByteStringType:
		if bs, ok := f.Interface.([]byte); ok {
			return zap.String(f.Key, RedactString(string(bs)))
		}
	case zapcore.ErrorType, zapcore.StringerType:
		switch v := f.Interface.(type) {
		case error:
			return zap.String(f.Key, RedactString(v.Error()))
		case fmt.Stringer:
			return zap.String(f.Key, RedactString(v.String()))
		}
	}
	return f
}

// redactFields 批量脱敏字段。
func redactFields(fs []zap.Field) []zap.Field {
	if len(fs) == 0 {
		return fs
	}
	out := make([]zap.Field, len(fs))
	for i, f := range fs {
		out[i] = RedactField(f)
	}
	return out
}
