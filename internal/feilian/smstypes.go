package feilian

// KnownSMSTypes 是飞连 notify.v1.sms 当前支持的 9 种短信场景标识，
// 与飞连开放平台事件文档保持一致（注意 accout_expire 为飞连官方拼写）。
// 管理 API 与绑定表单以此为校验/展示的唯一目录。
var KnownSMSTypes = []string{
	"code",
	"init_password",
	"reset_password",
	"alert",
	"guest_wifi",
	"password_expiration",
	"accout_expire",
	"wifi_info",
	"exchange_mfa",
}

// IsKnownSMSType 判断场景标识是否在飞连支持目录内。
func IsKnownSMSType(s string) bool {
	for _, k := range KnownSMSTypes {
		if k == s {
			return true
		}
	}
	return false
}
