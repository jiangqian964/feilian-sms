package store

// 发送记录状态机：pending → success/failed（终态不可逆）；
// 回执只回写 delivery_* 字段，不改变主 status。
const (
	StatusPending SendStatus = "pending"
	StatusSuccess SendStatus = "success"
	StatusFailed  SendStatus = "failed"
)

// 记录来源。
const (
	SourceFeilian = "feilian" // 飞连事件触发
	SourceTest    = "test"    // WebUI 测试发送
)

// 失败分类（error_kind），供发送记录筛选与运维定位。
const (
	ErrorKindUnbound         = "unbound"           // 场景未绑定通道
	ErrorKindBindingDisabled = "binding_disabled"  // 绑定被停用
	ErrorKindChannelDisabled = "channel_disabled"  // 通道被停用
	ErrorKindChannelNotFound = "channel_not_found" // 绑定指向的通道已删除
	ErrorKindInvalidMobile   = "invalid_mobile"    // 手机号无法归一化
	ErrorKindRender          = "render"            // 映射/签名渲染失败（多为配置错误）
	ErrorKindVendor          = "vendor"            // 厂商明确业务失败
	ErrorKindNetwork         = "network"           // 网络层错误（连接拒绝/DNS/5xx 等）
	ErrorKindTimeout         = "timeout"           // 下游超时
	ErrorKindInternal        = "internal"          // 网关内部错误
)

// SendStatus 发送主状态。
type SendStatus string

// SendRecord 是 sms_send 表一行（发送全生命周期留痕）。
// 手机号/参数仅以脱敏形态落库。
type SendRecord struct {
	AppSmsID        string     `json:"app_sms_id"`
	EventID         string     `json:"event_id"`
	Source          string     `json:"source"`
	ChannelID       string     `json:"channel_id"`
	SMSType         string     `json:"sms_type"`
	MobileMasked    string     `json:"mobile_masked"`
	ParamsMasked    string     `json:"params_masked"`
	TemplateCode    string     `json:"template_code"`
	Status          SendStatus `json:"status"`
	ProviderMsgID   string     `json:"provider_msg_id"`
	ProviderStatus  string     `json:"provider_status"`
	ProviderMessage string     `json:"provider_message"`
	DeliveryStatus  string     `json:"delivery_status"`
	DeliveryMessage string     `json:"delivery_message"`
	SeqNo           int        `json:"seq_no"`
	ReceiptAt       int64      `json:"receipt_at"`
	ErrorKind       string     `json:"error_kind"`
	LatencyMS       int64      `json:"latency_ms"`
	CreatedAt       int64      `json:"created_at"`
	UpdatedAt       int64      `json:"updated_at"`
}

// MarkSuccess 是 pending→success 的回写参数。
type MarkSuccess struct {
	ProviderMsgID   string
	ProviderStatus  string
	ProviderMessage string
	LatencyMS       int64
	NowMS           int64
}

// MarkFailed 是 pending→failed 的回写参数。
type MarkFailed struct {
	ErrorKind       string
	ProviderStatus  string
	ProviderMessage string
	LatencyMS       int64
	NowMS           int64
}

// ReceiptUpdate 是厂商异步回执的回写参数（不改主状态）。
type ReceiptUpdate struct {
	AppSmsID        string
	DeliveryStatus  string
	DeliveryMessage string
	SeqNo           int
	ReceiptAtMS     int64
}

// SendFilter 是发送记录列表筛选与分页条件（零值表示不限制）。
type SendFilter struct {
	Status    string
	SMSType   string
	ChannelID string
	Source    string
	FromMS    int64 // created_at >= FromMS
	ToMS      int64 // created_at <= ToMS
	Limit     int
	Offset    int
}
