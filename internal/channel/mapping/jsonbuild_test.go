package mapping

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildBody(t *testing.T) {
	vars := map[string]string{
		"appSmsId":     "evt-1",
		"mobile":       "8613800001111",
		"nonce":        "48291",
		"timestamp":    "1740385174957",
		"sign":         "abcd1234",
		"params":       `["654321"]`,
		"param0":       "654321",
		"templateCode": "SMS_CODE",
	}
	consts := map[string]string{
		"appCode": "DEMOAPP",
		"orgCode": "100001",
	}

	tests := []struct {
		name     string
		mappings []Mapping
		check    func(t *testing.T, body map[string]any)
		wantErr  string
	}{
		{
			name: "示例厂商 9 字段平铺",
			mappings: []Mapping{
				{Target: "appCode", SourceType: SourceConst, Source: "appCode", ValueType: ValueString},
				{Target: "appSmsId", SourceType: SourceVariable, Source: "appSmsId", ValueType: ValueString},
				{Target: "mobile", SourceType: SourceVariable, Source: "mobile", ValueType: ValueString},
				{Target: "nonce", SourceType: SourceVariable, Source: "nonce", ValueType: ValueString},
				{Target: "orgCode", SourceType: SourceConst, Source: "orgCode", ValueType: ValueString},
				{Target: "params", SourceType: SourceVariable, Source: "params", ValueType: ValueRaw},
				{Target: "sign", SourceType: SourceVariable, Source: "sign", ValueType: ValueString},
				{Target: "templateCode", SourceType: SourceVariable, Source: "templateCode", ValueType: ValueString},
				{Target: "timestamp", SourceType: SourceVariable, Source: "timestamp", ValueType: ValueNumber},
			},
			check: func(t *testing.T, body map[string]any) {
				if body["appCode"] != "DEMOAPP" || body["appSmsId"] != "evt-1" {
					t.Fatalf("常量/变量映射失败: %#v", body)
				}
				if _, ok := body["timestamp"].(json.Number); !ok {
					// 使用 Decoder UseNumber 时为 json.Number；BuildBody 返回 int64 亦可，接受两者
					if _, ok := body["timestamp"].(int64); !ok {
						t.Fatalf("timestamp 应为数字类型: %T", body["timestamp"])
					}
				}
				params, ok := body["params"].([]any)
				if !ok || len(params) != 1 || params[0] != "654321" {
					t.Fatalf("params 应为数组: %#v", body["params"])
				}
			},
		},
		{
			name: "嵌套对象",
			mappings: []Mapping{
				{Target: "auth.type", SourceType: SourceLiteral, Source: "bearer", ValueType: ValueString},
				{Target: "auth.token", SourceType: SourceLiteral, Source: "t", ValueType: ValueString},
			},
			check: func(t *testing.T, body map[string]any) {
				auth := body["auth"].(map[string]any)
				if auth["type"] != "bearer" || auth["token"] != "t" {
					t.Fatalf("嵌套失败: %#v", body)
				}
			},
		},
		{
			name: "数组下标路径 a.0.b",
			mappings: []Mapping{
				{Target: "items.0.name", SourceType: SourceLiteral, Source: "n1", ValueType: ValueString},
				{Target: "items.1.name", SourceType: SourceLiteral, Source: "n2", ValueType: ValueString},
			},
			check: func(t *testing.T, body map[string]any) {
				items := body["items"].([]any)
				if items[0].(map[string]any)["name"] != "n1" ||
					items[1].(map[string]any)["name"] != "n2" {
					t.Fatalf("下标路径失败: %#v", body)
				}
			},
		},
		{
			name: "布尔与浮点数字",
			mappings: []Mapping{
				{Target: "flag", SourceType: SourceLiteral, Source: "true", ValueType: ValueBoolean},
				{Target: "rate", SourceType: SourceLiteral, Source: "0.5", ValueType: ValueNumber},
				{Target: "literal", SourceType: SourceLiteral, Source: "固定值", ValueType: ValueString},
			},
			check: func(t *testing.T, body map[string]any) {
				if body["flag"] != true {
					t.Fatalf("布尔失败: %#v", body["flag"])
				}
				if f, ok := body["rate"].(float64); !ok || f != 0.5 {
					t.Fatalf("浮点失败: %#v", body["rate"])
				}
			},
		},
		{
			name: "重复 target 报错",
			mappings: []Mapping{
				{Target: "a", SourceType: SourceLiteral, Source: "1", ValueType: ValueString},
				{Target: "a", SourceType: SourceLiteral, Source: "2", ValueType: ValueString},
			},
			wantErr: "a",
		},
		{
			name: "容器与叶子冲突（先对象后值）",
			mappings: []Mapping{
				{Target: "a.b", SourceType: SourceLiteral, Source: "1", ValueType: ValueString},
				{Target: "a", SourceType: SourceLiteral, Source: "2", ValueType: ValueString},
			},
			wantErr: "a",
		},
		{
			name: "未知变量报错并带 target 上下文",
			mappings: []Mapping{
				{Target: "x", SourceType: SourceVariable, Source: "nope", ValueType: ValueString},
			},
			wantErr: "nope",
		},
		{
			name: "未知常量报错",
			mappings: []Mapping{
				{Target: "x", SourceType: SourceConst, Source: "nope", ValueType: ValueString},
			},
			wantErr: "nope",
		},
		{
			name: "非法数字",
			mappings: []Mapping{
				{Target: "x", SourceType: SourceLiteral, Source: "12x", ValueType: ValueNumber},
			},
			wantErr: "number",
		},
		{
			name: "非法布尔",
			mappings: []Mapping{
				{Target: "x", SourceType: SourceLiteral, Source: "yes", ValueType: ValueBoolean},
			},
			wantErr: "boolean",
		},
		{
			name: "raw 非法 JSON 报错",
			mappings: []Mapping{
				{Target: "x", SourceType: SourceLiteral, Source: "[1,2", ValueType: ValueRaw},
			},
			wantErr: "JSON",
		},
		{
			name: "非法来源类型",
			mappings: []Mapping{
				{Target: "x", SourceType: "template", Source: "y", ValueType: ValueString},
			},
			wantErr: "source_type",
		},
		{
			name: "非法目标路径",
			mappings: []Mapping{
				{Target: ".bad", SourceType: SourceLiteral, Source: "y", ValueType: ValueString},
			},
			wantErr: "target",
		},
		{
			// 回归：父链含多个切片段时旧 setBack 强转 map 直接 panic。
			name: "连续数组下标 a.0.0.0 不得 panic",
			mappings: []Mapping{
				{Target: "a.0.0.0", SourceType: SourceLiteral, Source: "deep", ValueType: ValueString},
			},
			check: func(t *testing.T, body map[string]any) {
				l0 := body["a"].([]any)
				l1 := l0[0].([]any)
				l2 := l1[0].([]any)
				if l2[0] != "deep" {
					t.Fatalf("深层数组写入失败: %#v", body)
				}
			},
		},
		{
			// 回归：map 与 slice 父节点交替时的扩容挂回。
			name: "数组对象数组混合扩容 rows.0.cells.1",
			mappings: []Mapping{
				{Target: "rows.0.cells.1", SourceType: SourceLiteral, Source: "c2", ValueType: ValueString},
				{Target: "rows.0.cells.0", SourceType: SourceLiteral, Source: "c1", ValueType: ValueString},
			},
			check: func(t *testing.T, body map[string]any) {
				rows := body["rows"].([]any)
				cells := rows[0].(map[string]any)["cells"].([]any)
				if cells[0] != "c1" || cells[1] != "c2" {
					t.Fatalf("混合扩容写入失败: %#v", body)
				}
			},
		},
		{
			name: "负数下标报错",
			mappings: []Mapping{
				{Target: "a.-1", SourceType: SourceLiteral, Source: "y", ValueType: ValueString},
			},
			wantErr: "非负整数",
		},
		{
			// 回归：试渲染期 make 巨型切片可 OOM 杀死进程，须提前拒绝。
			name: "超大下标报错而非 OOM",
			mappings: []Mapping{
				{Target: "list.2147483647.x", SourceType: SourceLiteral, Source: "y", ValueType: ValueString},
			},
			wantErr: "超过上限",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := BuildBody(tt.mappings, vars, consts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("期望错误含 %q，实际 %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("结果必须可 JSON 序列化: %v", err)
			}
			var back map[string]any
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatalf("结果不是合法 JSON 对象: %v", err)
			}
			tt.check(t, body)
		})
	}
}

func TestArrayIndexBoundary(t *testing.T) {
	// 恰好等于上限（10000）允许：切片长度 10001，内存可忽略。
	body, err := BuildBody([]Mapping{
		{Target: "a.10000", SourceType: SourceLiteral, Source: "edge", ValueType: ValueString},
	}, nil, nil)
	if err != nil {
		t.Fatalf("边界下标 10000 应允许: %v", err)
	}
	got := body["a"].([]any)
	if len(got) != 10001 || got[10000] != "edge" {
		t.Fatalf("边界写入异常: len=%d v=%v", len(got), got[10000])
	}

	// 超过 1 即拒绝，且不得分配巨型切片（错误在 make 之前返回）。
	if _, err := BuildBody([]Mapping{
		{Target: "a.10001", SourceType: SourceLiteral, Source: "x", ValueType: ValueString},
	}, nil, nil); err == nil || !strings.Contains(err.Error(), "超过上限") {
		t.Fatalf("下标 10001 应被拒绝，实际 %v", err)
	}
}
