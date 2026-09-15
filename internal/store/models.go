package store

import "errors"

// ErrNotFound 表示按主键查询无此行。
var ErrNotFound = errors.New("记录不存在")

// Settings 是 system_settings 单行的运行时视图；
// EncryptKey 为解密后的明文（仅内存，日志禁止输出）。
type Settings struct {
	VerificationToken   string `json:"verification_token"`
	EncryptKey          string `json:"encrypt_key,omitempty"`
	WebhookPath         string `json:"webhook_path"`
	PublicBaseURL       string `json:"public_base_url"`
	DownstreamTimeoutMS int    `json:"downstream_timeout_ms"`
	StalePendingMS      int    `json:"stale_pending_ms"`
	UpdatedAt           int64  `json:"updated_at"`
}

// Channel 是 channels 表一行；ConfigJSON 为结构化通道配置（T6 定义其结构）。
type Channel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	ConfigJSON  string `json:"config_json"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// RuntimeChannel 是带解密密钥的通道运行时视图，仅存在于内存快照中。
type RuntimeChannel struct {
	Channel
	Secrets map[string]string `json:"-"`
}

// Binding 是短信场景（sms_type）到通道+模板的绑定。
type Binding struct {
	SMSType      string `json:"sms_type"`
	ChannelID    string `json:"channel_id"`
	TemplateCode string `json:"template_code"`
	ParamIndex   []int  `json:"param_index"`
	Enabled      bool   `json:"enabled"`
	UpdatedAt    int64  `json:"updated_at"`
}

// MaskedSecret 是密钥的对外回显形态。
type MaskedSecret struct {
	Value string `json:"value"`
	Set   bool   `json:"set"`
}

// Snapshot 是不可变配置快照；任何写操作后整体替换。
type Snapshot struct {
	Version  int64
	Settings Settings
	Channels []RuntimeChannel
	Bindings []Binding
}
