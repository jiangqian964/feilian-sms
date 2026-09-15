package sign

import (
	"fmt"
	"strings"
)

// SegmentKind 标识签名片段来源。
type SegmentKind string

const (
	// SegmentLiteral 字面量片段，Value 原样拼接（如 "timestamp="、"&nonce="）。
	SegmentLiteral SegmentKind = "literal"
	// SegmentVariable 变量片段，Value 为内置变量名，取 vars[Value]。
	SegmentVariable SegmentKind = "variable"
)

// Segment 是待签名原文的一个有序片段。
type Segment struct {
	Kind  SegmentKind `json:"kind"`
	Value string      `json:"value"`
}

// BuildRaw 按片段顺序拼接待签名原文；
// 变量缺失（未在 vars 中出现）或片段类型非法时返回字段级错误。
// 注意：变量值允许为空字符串（如空 nonce 边界），只要变量被显式提供。
func BuildRaw(segments []Segment, vars map[string]string) (string, error) {
	var b strings.Builder
	for i, seg := range segments {
		switch seg.Kind {
		case SegmentLiteral:
			b.WriteString(seg.Value)
		case SegmentVariable:
			v, ok := vars[seg.Value]
			if !ok {
				return "", fmt.Errorf("签名片段[%d]引用了未知变量 %q", i, seg.Value)
			}
			b.WriteString(v)
		default:
			return "", fmt.Errorf("签名片段[%d]类型非法 %q", i, seg.Kind)
		}
	}
	return b.String(), nil
}
