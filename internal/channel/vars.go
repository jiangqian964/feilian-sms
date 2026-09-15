package channel

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// timeNow 便于测试注入（当前仅用于 nonce 降级路径）。
var timeNow = time.Now

// 手机号归一化策略（持久化到 config_json，禁止改名）。
const (
	MobilePolicyRaw       = "raw"        // 原样使用飞连 mobile（含 +）
	MobilePolicyStripPlus = "strip_plus" // 去掉开头的 +
	MobilePolicyCCPrefix  = "cc_prefix"  // 去 + 的国家码 + 本机号码（如 86/852...）
)

// SendInput 是一次短信下发的标准化输入（与厂商无关）。
type SendInput struct {
	AppSmsID     string
	CountryCode  string // 含 +，如 +86
	MobileNumber string // 不含国家码
	Mobile       string // 含国家码，如 86138...（飞连原值，+ 可能保留）
	SMSType      string
	TemplateCode string
	Params       []string
}

// BuildVars 生成映射/签名可用的全部运行时变量。
// nonce 与 timestamp(毫秒) 由调用方传入，保证签名可测试、可重放校验。
func BuildVars(in SendInput, mobilePolicy, nonce string, tsMS int64) (map[string]string, error) {
	if in.MobileNumber == "" {
		return nil, fmt.Errorf("%w: 手机号为空", ErrInvalidMobile)
	}
	mobile, err := normalizeMobile(in, mobilePolicy)
	if err != nil {
		return nil, err
	}

	paramsJSON, err := json.Marshal(in.Params)
	if err != nil {
		return nil, err
	}
	vars := map[string]string{
		"appSmsId":     in.AppSmsID,
		"mobileNumber": in.MobileNumber,
		"countryCode":  in.CountryCode,
		"mobile":       mobile,
		"smsType":      in.SMSType,
		"templateCode": in.TemplateCode,
		"nonce":        nonce,
		"timestamp":    strconv.FormatInt(tsMS, 10),
		"params":       string(paramsJSON),
	}
	for i, p := range in.Params {
		vars["param"+strconv.Itoa(i)] = p
	}
	return vars, nil
}

func normalizeMobile(in SendInput, policy string) (string, error) {
	switch policy {
	case "", MobilePolicyCCPrefix:
		cc := strings.TrimPrefix(in.CountryCode, "+")
		if cc == in.CountryCode || cc == "" {
			return "", fmt.Errorf("%w: cc_prefix 要求国家码形如 +86，实际 %q", ErrInvalidMobile, in.CountryCode)
		}
		return cc + in.MobileNumber, nil
	case MobilePolicyStripPlus:
		m := strings.TrimPrefix(in.Mobile, "+")
		if m == "" {
			return "", fmt.Errorf("%w: mobile 为空", ErrInvalidMobile)
		}
		return m, nil
	case MobilePolicyRaw:
		if in.Mobile == "" {
			return "", fmt.Errorf("%w: mobile 为空", ErrInvalidMobile)
		}
		return in.Mobile, nil
	default:
		return "", fmt.Errorf("%w: 未知手机号策略 %q", ErrInvalidMobile, policy)
	}
}

// NewNonce 生成 5-6 位随机数字串（内置示例预置要求的 nonce 形态）。
func NewNonce() string {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		// crypto/rand 失败意味着系统熵源异常，降级时间戳比 panic 更可观测
		return strconv.FormatInt(10000+timeNow().UnixNano()%900000, 10)
	}
	return strconv.FormatInt(n.Int64()+10000, 10)
}
