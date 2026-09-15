package channel

import (
	"feilian-sms/internal/channel/mapping"
	"feilian-sms/internal/channel/sign"
)

// PresetBuiltin 是内置「通用 HTTP JSON + SHA-1 加盐」示例预置通道标识。
const PresetBuiltin = "http_json_v1"

// BuiltinPresetConfig 生成通用 JSON over HTTP 短信通道的示例预置配置
// （基址、凭证与模板码均留空待填，直接作为对接新厂商的脚手架）。
// 报文为 9 个平铺字段：
// appCode/appSmsId/mobile/nonce/orgCode/params/sign/templateCode/timestamp。
func BuiltinPresetConfig() *Config {
	return &Config{
		Request: RequestConfig{
			BaseURL:     "https://sms.example.com:1443",
			URL:         "${base}/sms/send",
			Method:      "POST",
			ContentType: "application/json",
		},
		Constants: map[string]ConstantSpec{
			"appCode":   {Secret: false}, // 待填：厂商分配的应用编码
			"orgCode":   {Secret: false}, // 待填：厂商分配的组织编码（无则留空）
			"appSecret": {Secret: true},  // 待填：厂商分配的签名密钥，仅密文存储
		},
		BodyMappings: []mapping.Mapping{
			{Target: "appCode", SourceType: mapping.SourceConst, Source: "appCode", ValueType: mapping.ValueString},
			{Target: "appSmsId", SourceType: mapping.SourceVariable, Source: "appSmsId", ValueType: mapping.ValueString},
			{Target: "mobile", SourceType: mapping.SourceVariable, Source: "mobile", ValueType: mapping.ValueString},
			{Target: "nonce", SourceType: mapping.SourceVariable, Source: "nonce", ValueType: mapping.ValueString},
			{Target: "orgCode", SourceType: mapping.SourceConst, Source: "orgCode", ValueType: mapping.ValueString},
			{Target: "params", SourceType: mapping.SourceVariable, Source: "params", ValueType: mapping.ValueRaw},
			{Target: "sign", SourceType: mapping.SourceVariable, Source: "sign", ValueType: mapping.ValueString},
			{Target: "templateCode", SourceType: mapping.SourceVariable, Source: "templateCode", ValueType: mapping.ValueString},
			{Target: "timestamp", SourceType: mapping.SourceVariable, Source: "timestamp", ValueType: mapping.ValueNumber},
		},
		Sign: SignConfig{
			Strategy:    sign.StrategySHA1Salt,
			SecretConst: "appSecret",
			Segments: []sign.Segment{
				{Kind: sign.SegmentLiteral, Value: "timestamp="},
				{Kind: sign.SegmentVariable, Value: "timestamp"},
				{Kind: sign.SegmentLiteral, Value: "&nonce="},
				{Kind: sign.SegmentVariable, Value: "nonce"},
				{Kind: sign.SegmentLiteral, Value: "&signData="},
				{Kind: sign.SegmentVariable, Value: "appSmsId"},
			},
		},
		Response: ResponseConfig{
			SuccessPath:  "status",
			SuccessValue: "0",
			MsgIDPath:    "data.id",
			MessagePath:  "message",
		},
		MobilePolicy: MobilePolicyCCPrefix,
		Receipt: ReceiptConfig{
			MsgIDPath:      "smsId",
			AppMsgIDPath:   "appSmsId",
			StatusPath:     "status",
			DeliveredValue: "DELIVRD",
			MessagePath:    "statusMessage",
			SeqNoPath:      "seqNo",
		},
	}
}
