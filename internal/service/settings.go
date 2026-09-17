// Package service 是网关编排层：把飞连短信事件按配置快照绑定到通用 HTTP
// 通道，完成参数重排、号码归一化、幂等闸门、下发、状态回写与测试发送。
package service

import (
	"strings"

	"feilian-sms/internal/store"
)

// SettingsRuntime 以只读快照方式暴露当前系统设置；所有读均为 O(1) 原子读，
// PUT 后因快照整体替换而立即生效（无需重启）。
type SettingsRuntime struct {
	cache *store.Cache
}

// NewSettingsRuntime 构造设置运行时。
func NewSettingsRuntime(cache *store.Cache) *SettingsRuntime {
	return &SettingsRuntime{cache: cache}
}

// Current 返回当前设置的一份值拷贝。
func (r *SettingsRuntime) Current() store.Settings {
	return r.cache.Current().Settings
}

// WebhookPath 返回飞连事件订阅路径（热改后即时生效）。
func (r *SettingsRuntime) WebhookPath() string {
	return r.cache.Current().Settings.WebhookPath
}

// VerificationToken 返回飞连 Verification Token（恒定时间比较在入站层做）。
func (r *SettingsRuntime) VerificationToken() string {
	return r.cache.Current().Settings.VerificationToken
}

// EncryptKey 返回飞连 Encrypt Key 明文；未配置返回空串（本期不启用加密）。
func (r *SettingsRuntime) EncryptKey() string {
	return r.cache.Current().Settings.EncryptKey
}

// ReceiptAuthToken 返回厂商异步回执的全局鉴权 token；空串表示不校验。
// 恒定时间比较在入站层完成，明文仅存在于内存快照，日志禁止输出。
func (r *SettingsRuntime) ReceiptAuthToken() string {
	return r.cache.Current().Settings.ReceiptAuthToken
}

// DownstreamTimeoutMS 返回单次下游下发超时（毫秒）。
func (r *SettingsRuntime) DownstreamTimeoutMS() int {
	return r.cache.Current().Settings.DownstreamTimeoutMS
}

// StalePendingMS 返回 pending 记录视为僵死、允许补发的阈值（毫秒）。
func (r *SettingsRuntime) StalePendingMS() int {
	return r.cache.Current().Settings.StalePendingMS
}

// PublicBaseURL 返回对外基址。
func (r *SettingsRuntime) PublicBaseURL() string {
	return r.cache.Current().Settings.PublicBaseURL
}

// ReceiptURL 拼接某通道的厂商异步回执地址；未配置基址时返回相对路径。
func (r *SettingsRuntime) ReceiptURL(channelID string) string {
	base := strings.TrimRight(r.PublicBaseURL(), "/")
	return base + "/receipts/" + channelID
}
