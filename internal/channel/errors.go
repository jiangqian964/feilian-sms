package channel

import "errors"

// 渲染阶段错误哨兵：编排层据此把失败归入 invalid_mobile / render。
var (
	// ErrInvalidMobile 手机号缺失或无法按通道策略归一化。
	ErrInvalidMobile = errors.New("手机号无效")
	// ErrRender 映射组装/签名计算等配置类渲染错误。
	ErrRender = errors.New("请求渲染失败")
	// ErrSecretNotSet 表示配置引用了尚未填写的通道密钥（脚手架状态可保存，
	// 但尚不能真实下发）；管理 API 据此与结构性配置错误区分。
	ErrSecretNotSet = errors.New("通道密钥未设置")
)

// RenderError 包装渲染失败并标注阶段（mobile/sign/mapping）。
type RenderError struct {
	Stage string // mobile / sign / mapping
	Err   error
}

func (e *RenderError) Error() string { return e.Err.Error() }
func (e *RenderError) Unwrap() error { return e.Err }

// TransportError 包装出站 HTTP 传输错误，标注是否超时。
type TransportError struct {
	Timeout bool
	Err     error
}

func (e *TransportError) Error() string { return e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }
