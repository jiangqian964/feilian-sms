// Package mapping 实现「源字段 → 目标 JSON 点分路径」的表单式字段映射，
// 是通用 HTTP 短信通道的配置核心：不允许手写 JSON 模板，所有出站报文
// 均由映射表确定性组装，保存前可试渲染做字段级校验。
package mapping

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SourceType 源值类型。
type SourceType string

const (
	SourceVariable SourceType = "variable" // 内置运行时变量
	SourceConst    SourceType = "const"    // 通道常量（含密钥）
	SourceLiteral  SourceType = "literal"  // 字面值
)

// ValueType 目标 JSON 值类型。
type ValueType string

const (
	ValueString  ValueType = "string"
	ValueNumber  ValueType = "number"
	ValueBoolean ValueType = "boolean"
	ValueRaw     ValueType = "raw" // 合法 JSON 片段（数组/对象原样嵌入）
)

// Mapping 一条字段映射：源值 → 目标路径。
type Mapping struct {
	Target     string     `json:"target"`      // 目标点分路径，如 data.id、params、list.0.name
	SourceType SourceType `json:"source_type"` // variable / const / literal
	Source     string     `json:"source"`      // 变量名/常量名/字面值
	ValueType  ValueType  `json:"value_type"`  // string / number / boolean / raw
}

// BuildBody 按映射表把变量/常量组装为目标 JSON 对象。
// vars 为运行时变量（mobile/nonce/sign/params 等），consts 为通道常量明文。
func BuildBody(mappings []Mapping, vars, consts map[string]string) (map[string]any, error) {
	root := map[string]any{}
	seen := map[string]bool{}

	for _, m := range mappings {
		segs, err := splitPath(m.Target)
		if err != nil {
			return nil, err
		}
		if seen[m.Target] {
			return nil, fmt.Errorf("目标路径 %s 重复映射", m.Target)
		}
		raw, err := resolveSource(m, vars, consts)
		if err != nil {
			return nil, fmt.Errorf("映射 %s: %w", m.Target, err)
		}
		val, err := convertValue(raw, m.ValueType)
		if err != nil {
			return nil, fmt.Errorf("映射 %s: %w", m.Target, err)
		}
		if err := assignPath(root, segs, val); err != nil {
			return nil, fmt.Errorf("映射 %s: %w", m.Target, err)
		}
		seen[m.Target] = true
	}
	return root, nil
}

func resolveSource(m Mapping, vars, consts map[string]string) (string, error) {
	switch m.SourceType {
	case SourceVariable:
		v, ok := vars[m.Source]
		if !ok {
			return "", fmt.Errorf("未知变量 %q", m.Source)
		}
		return v, nil
	case SourceConst:
		v, ok := consts[m.Source]
		if !ok {
			return "", fmt.Errorf("未知常量 %q", m.Source)
		}
		return v, nil
	case SourceLiteral:
		return m.Source, nil
	default:
		return "", fmt.Errorf("非法 source_type %q（仅 variable/const/literal）", m.SourceType)
	}
}

func convertValue(raw string, t ValueType) (any, error) {
	switch t {
	case ValueString, "":
		return raw, nil
	case ValueNumber:
		if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return i, nil
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("值 %q 无法转换为 number", raw)
		}
		return f, nil
	case ValueBoolean:
		switch raw {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, fmt.Errorf("值 %q 无法转换为 boolean（仅 true/false）", raw)
		}
	case ValueRaw:
		if !json.Valid([]byte(raw)) {
			return nil, fmt.Errorf("raw 值不是合法 JSON 片段: %q", raw)
		}
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("raw 值无法解析为 JSON: %w", err)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("非法 value_type %q", t)
	}
}

// splitPath 校验并切分点分路径；数字段代表数组下标。
func splitPath(p string) ([]string, error) {
	if p == "" || strings.HasPrefix(p, ".") || strings.HasSuffix(p, ".") ||
		strings.Contains(p, "..") {
		return nil, fmt.Errorf("非法 target 路径 %q", p)
	}
	return strings.Split(p, "."), nil
}

// assignPath 按段写入；对象用 map，全数字段用 slice 自动扩容。
func assignPath(root map[string]any, segs []string, val any) error {
	var (
		cur any = root
	)
	for i, seg := range segs {
		last := i == len(segs)-1
		switch container := cur.(type) {
		case map[string]any:
			if last {
				if existing, ok := container[seg]; ok && isContainer(existing) {
					return fmt.Errorf("路径段 %s 已是对象/数组，不能再赋标量", seg)
				}
				container[seg] = val
				continue
			}
			next, ok := container[seg]
			if !ok {
				next = newContainer(segs[i+1])
				container[seg] = next
			} else if !isContainer(next) {
				return fmt.Errorf("路径段 %s 已是标量，不能继续下钻", seg)
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 {
				return fmt.Errorf("数组下标必须是非负整数，实际 %q", seg)
			}
			if last {
				if idx >= len(container) {
					grown := make([]any, idx+1)
					copy(grown, container)
					container = grown
					setBack(root, segs[:i], container)
				}
				if isContainer(container[idx]) {
					return fmt.Errorf("下标 %d 已是对象/数组，不能再赋标量", idx)
				}
				container[idx] = val
				continue
			}
			if idx >= len(container) {
				grown := make([]any, idx+1)
				copy(grown, container)
				container = grown
				setBack(root, segs[:i], container)
			}
			if container[idx] == nil {
				container[idx] = newContainer(segs[i+1])
			} else if !isContainer(container[idx]) {
				return fmt.Errorf("下标 %d 已是标量，不能继续下钻", idx)
			}
			cur = container[idx]
		default:
			return fmt.Errorf("路径段 %s 无法下钻", seg)
		}
	}
	return nil
}

// setBack 在切片扩容后把新切片挂回父节点（根场景除外）。
func setBack(root map[string]any, parentSegs []string, grown []any) {
	if len(parentSegs) == 0 {
		return
	}
	cur := any(root)
	for i, seg := range parentSegs {
		m := cur.(map[string]any)
		if i == len(parentSegs)-1 {
			m[seg] = grown
			return
		}
		if child, ok := m[seg].([]any); ok {
			cur = child
			continue
		}
		cur = m[seg]
	}
}

func newContainer(nextSeg string) any {
	if _, err := strconv.Atoi(nextSeg); err == nil {
		return []any{}
	}
	return map[string]any{}
}

func isContainer(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}
