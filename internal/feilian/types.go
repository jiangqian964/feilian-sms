// Package feilian 负责飞连事件订阅 Webhook 的入站协议：
// 事件信封解析、url_verification 握手、加密信封识别与 AES-256-CBC 解密。
package feilian

// Envelope 是飞连事件推送的通用信封（schema=1.0）。
type Envelope struct {
	Schema string `json:"schema"`
	Header Header `json:"header"`
	Data   Data   `json:"data"`
}

// Header 为事件头；CreateTime 为毫秒时间戳字符串。
type Header struct {
	EventID    string `json:"event_id"`
	Token      string `json:"token"`
	CreateTime string `json:"create_time"`
	EventType  string `json:"event_type"`
	AppID      string `json:"app_id"`
}

// Data 承载事件列表，批量推送时含多个元素。
type Data struct {
	Events []Event `json:"events"`
}

// Event 是 data.events[] 的元素，短信事件的业务字段包一层 object。
type Event struct {
	Object SMSObject `json:"object"`
}

// SMSObject 是 notify.v1.sms 的短信对象。
type SMSObject struct {
	// CountryCode 国家区号，形如 "+86"。
	CountryCode string `json:"country_code"`
	// MobileNumber 不含区号的手机号。
	MobileNumber string `json:"mobile_number"`
	// Mobile 含区号手机号，形如 "+86133xxxx3333"。
	Mobile string `json:"mobile"`
	// SMSType 场景类型：code/init_password/... 共 9 种。
	SMSType string `json:"sms_type"`
	// Template 文案模板，占位符为 %s。
	Template string `json:"template"`
	// Params 按模板从左到右顺序的参数值。
	Params []string `json:"params"`
	// ExpiredTime 过期时间（秒级 Unix 时间戳）。
	ExpiredTime int64 `json:"expired_time"`
}
