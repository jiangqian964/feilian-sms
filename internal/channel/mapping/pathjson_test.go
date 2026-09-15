package mapping

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGetPath(t *testing.T) {
	var v any
	// 数字保持 json.Number，模拟带 UseNumber 的响应解析
	dec := json.NewDecoder(strings.NewReader(`{
		"status": 0,
		"message": "success",
		"data": {"id": 123456, "deep": {"flag": true}},
		"list": [{"smsId": "s-1"}, {"smsId": "s-2"}]
	}`))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path    string
		want    any
		wantOK  bool
		wantStr string
	}{
		{path: "status", wantOK: true, want: json.Number("0"), wantStr: "0"},
		{path: "data.id", wantOK: true, want: json.Number("123456"), wantStr: "123456"},
		{path: "data.deep.flag", wantOK: true, want: true},
		{path: "list.1.smsId", wantOK: true, want: "s-2", wantStr: "s-2"},
		{path: "message", wantOK: true, want: "success", wantStr: "success"},
		{path: "missing", wantOK: false},
		{path: "data.nope.x", wantOK: false},
		{path: "list.9.smsId", wantOK: false},
		{path: "status.x", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, ok := GetPath(v, tt.path)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Fatalf("GetPath = %#v(%T), want %#v(%T)", got, got, tt.want, tt.want)
			}
			if tt.wantStr != "" {
				s, ok := GetString(v, tt.path)
				if !ok || s != tt.wantStr {
					t.Fatalf("GetString = %q,%v want %q", s, ok, tt.wantStr)
				}
			}
		})
	}
}

func TestLooseEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b any
		want bool
	}{
		{"数字 0 与字符串 0", json.Number("0"), "0", true},
		{"float 0 与字符串 0", float64(0), "0", true},
		{"int64 与字符串", int64(50001), "50001", true},
		{"数字不等", json.Number("0"), "1", false},
		{"字符串数字不等", "0", "50001", false},
		{"字符串相等", "success", "success", true},
		{"布尔与字符串 true", true, "true", true},
		{"布尔不等", false, "true", false},
		{"nil 与缺失", nil, nil, true},
		{"数字与非数字字符串", json.Number("0"), "abc", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooseEqual(tt.a, tt.b); got != tt.want {
				t.Fatalf("LooseEqual(%#v,%#v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
