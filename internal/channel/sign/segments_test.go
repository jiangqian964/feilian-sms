package sign

import "testing"

func TestBuildRaw(t *testing.T) {
	vars := map[string]string{
		"timestamp": "1740385174957",
		"nonce":     "48291",
		"appSmsId":  "evt-0001",
	}
	tests := []struct {
		name     string
		segments []Segment
		want     string
		wantErr  string
	}{
		{
			name: "示例厂商 6 片段有序拼接",
			segments: []Segment{
				{Kind: SegmentLiteral, Value: "timestamp="},
				{Kind: SegmentVariable, Value: "timestamp"},
				{Kind: SegmentLiteral, Value: "&nonce="},
				{Kind: SegmentVariable, Value: "nonce"},
				{Kind: SegmentLiteral, Value: "&signData="},
				{Kind: SegmentVariable, Value: "appSmsId"},
			},
			want: "timestamp=1740385174957&nonce=48291&signData=evt-0001",
		},
		{
			name:     "空片段",
			segments: nil,
			want:     "",
		},
		{
			name: "纯字面量",
			segments: []Segment{
				{Kind: SegmentLiteral, Value: "abc"},
				{Kind: SegmentLiteral, Value: "def"},
			},
			want: "abcdef",
		},
		{
			name: "未知变量报错",
			segments: []Segment{
				{Kind: SegmentLiteral, Value: "x="},
				{Kind: SegmentVariable, Value: "notExist"},
			},
			wantErr: "notExist",
		},
		{
			name: "非法片段类型报错",
			segments: []Segment{
				{Kind: "template", Value: "x"},
			},
			wantErr: "template",
		},
		{
			name: "变量值为空字符串允许（空 nonce 边界）",
			segments: []Segment{
				{Kind: SegmentLiteral, Value: "nonce="},
				{Kind: SegmentVariable, Value: "empty"},
			},
			want: "nonce=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := cloneVars(vars)
			v["empty"] = ""
			got, err := BuildRaw(tt.segments, v)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("期望错误含 %q，实际 nil", tt.wantErr)
				}
				if !contains(err.Error(), tt.wantErr) {
					t.Fatalf("错误 %q 不含 %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("BuildRaw = %q, want %q", got, tt.want)
			}
		})
	}
}

func cloneVars(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
