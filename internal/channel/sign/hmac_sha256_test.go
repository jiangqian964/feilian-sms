package sign

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

// RFC 4231 HMAC-SHA256 标准测试用例（case 1、case 2）。
func TestHMACSHA256RFC4231(t *testing.T) {
	key1 := make([]byte, 20)
	for i := range key1 {
		key1[i] = 0x0b
	}
	cases := []struct {
		name string
		key  string
		msg  string
		hex  string
	}{
		{
			name: "case1",
			key:  string(key1),
			msg:  "Hi There",
			hex:  "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7",
		},
		{
			name: "case2",
			key:  "Jefe",
			msg:  "what do ya want for nothing?",
			hex:  "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843",
		},
	}

	s, err := New(StrategyHMACSHA256, EncodingHex)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := s.Sign(c.key, c.msg)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.hex {
				t.Fatalf("HMAC-SHA256/hex 不匹配\n got: %s\nwant: %s", got, c.hex)
			}
		})
	}
}

func TestHMACSHA256Base64(t *testing.T) {
	s, err := New(StrategyHMACSHA256, EncodingBase64)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Sign("Jefe", "what do ya want for nothing?")
	if err != nil {
		t.Fatal(err)
	}
	want := base64.StdEncoding.EncodeToString(mustHexDecode(t,
		"5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"))
	if got != want {
		t.Fatalf("HMAC-SHA256/base64 不匹配\n got: %s\nwant: %s", got, want)
	}
}

func TestHMACSHA256DefaultEncodingAndBadEncoding(t *testing.T) {
	s, err := New(StrategyHMACSHA256, "")
	if err != nil {
		t.Fatalf("空 encoding 应默认 hex: %v", err)
	}
	got, err := s.Sign("Jefe", "what do ya want for nothing?")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 64 {
		t.Fatalf("默认应为 64 位 hex，实际 %d", len(got))
	}
	if _, err := New(StrategyHMACSHA256, "ucs2"); err == nil {
		t.Fatal("非法 encoding 必须报错")
	}
}

func mustHexDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
