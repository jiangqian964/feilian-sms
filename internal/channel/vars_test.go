package channel

import (
	"encoding/json"
	"regexp"
	"testing"
)

func sampleInput() SendInput {
	return SendInput{
		AppSmsID:     "evt-0001",
		CountryCode:  "+86",
		MobileNumber: "13800001111",
		Mobile:       "8613800001111",
		SMSType:      "code",
		TemplateCode: "SMS_CODE",
		Params:       []string{"654321", "5"},
	}
}

func TestBuildVarsMobilePolicies(t *testing.T) {
	tests := []struct {
		name       string
		policy     string
		mobile     string
		country    string
		number     string
		wantMobile string
	}{
		{"示例厂商 cc_prefix 大陆", MobilePolicyCCPrefix, "", "+86", "13800001111", "8613800001111"},
		{"示例厂商 cc_prefix 香港", MobilePolicyCCPrefix, "", "+852", "91234567", "85291234567"},
		{"strip_plus", MobilePolicyStripPlus, "+8613800001111", "+86", "13800001111", "8613800001111"},
		{"raw 原样", MobilePolicyRaw, "+8613800001111", "+86", "13800001111", "+8613800001111"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := sampleInput()
			in.Mobile = tt.mobile
			in.CountryCode = tt.country
			in.MobileNumber = tt.number
			vars, err := BuildVars(in, tt.policy, "48291", 1740385174957)
			if err != nil {
				t.Fatal(err)
			}
			if vars["mobile"] != tt.wantMobile {
				t.Fatalf("mobile = %q, want %q", vars["mobile"], tt.wantMobile)
			}
		})
	}
}

func TestBuildVarsFields(t *testing.T) {
	vars, err := BuildVars(sampleInput(), MobilePolicyCCPrefix, "48291", 1740385174957)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"appSmsId":     "evt-0001",
		"mobileNumber": "13800001111",
		"countryCode":  "+86",
		"smsType":      "code",
		"templateCode": "SMS_CODE",
		"nonce":        "48291",
		"timestamp":    "1740385174957",
		"param0":       "654321",
		"param1":       "5",
	}
	for k, v := range want {
		if vars[k] != v {
			t.Fatalf("vars[%q] = %q, want %q", k, vars[k], v)
		}
	}
	var params []string
	if err := json.Unmarshal([]byte(vars["params"]), &params); err != nil {
		t.Fatalf("params 不是合法 JSON 数组: %v", err)
	}
	if len(params) != 2 || params[0] != "654321" || params[1] != "5" {
		t.Fatalf("params 内容错误: %v", params)
	}
}

func TestBuildVarsValidation(t *testing.T) {
	in := sampleInput()
	in.MobileNumber = ""
	if _, err := BuildVars(in, MobilePolicyCCPrefix, "48291", 1); err == nil {
		t.Fatal("手机号为空应报错")
	}

	in = sampleInput()
	in.CountryCode = "86"
	if _, err := BuildVars(in, MobilePolicyCCPrefix, "48291", 1); err == nil {
		t.Fatal("cc_prefix 要求国家码带 + 前缀")
	}
}

func TestDefaultMobilePolicy(t *testing.T) {
	vars, err := BuildVars(sampleInput(), "", "48291", 1)
	if err != nil {
		t.Fatal(err)
	}
	if vars["mobile"] != "8613800001111" {
		t.Fatalf("缺省策略应为 cc_prefix，实际 %q", vars["mobile"])
	}
}

func TestNewNonce(t *testing.T) {
	re := regexp.MustCompile(`^\d{5,6}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		n := NewNonce()
		if !re.MatchString(n) {
			t.Fatalf("nonce 应为 5-6 位数字: %q", n)
		}
		seen[n] = true
	}
	if len(seen) < 90 {
		t.Fatalf("nonce 随机性不足: %d/100", len(seen))
	}
}
