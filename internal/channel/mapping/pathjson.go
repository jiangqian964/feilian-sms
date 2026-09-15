package mapping

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// GetPath 从 JSON 解码后的对象中按点分路径取值（数字段取数组下标）。
// 路径不存在或类型不匹配时 ok=false。
func GetPath(v any, path string) (any, bool) {
	cur := v
	for _, seg := range splitSegments(path) {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// GetString 取路径值并转为字符串（json.Number/数字/布尔按字面量）。
func GetString(v any, path string) (string, bool) {
	got, ok := GetPath(v, path)
	if !ok || got == nil {
		return "", false
	}
	switch x := got.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case bool:
		return strconv.FormatBool(x), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case int64:
		return strconv.FormatInt(x, 10), true
	default:
		return fmt.Sprintf("%v", x), true
	}
}

// LooseEqual 厂商响应判定专用宽松比较：
// 数字与数字字符串等价（"0" 与 0），布尔与 "true"/"false" 等价，
// 其余按字符串相等。
func LooseEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == b
	}
	if na, ok := toNumber(a); ok {
		if nb, ok := toNumber(b); ok {
			return na == nb
		}
		return false
	}
	if ba, ok := a.(bool); ok {
		switch bb := b.(type) {
		case bool:
			return ba == bb
		case string:
			return bb == "true" && ba || bb == "false" && !ba
		}
		return false
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func splitSegments(path string) []string {
	out := []string{}
	cur := ""
	for _, r := range path {
		if r == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
