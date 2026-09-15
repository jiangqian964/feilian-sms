package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/feilian"
	"feilian-sms/internal/store"
)

// TestSendResult 是 WebUI「测试发送」的同步结果（含失败分类便于页面提示）。
type TestSendResult struct {
	AppSmsID      string `json:"app_sms_id"`
	Success       bool   `json:"success"`
	HTTPCode      int    `json:"http_code"`
	ErrorKind     string `json:"error_kind,omitempty"`
	ProviderMsgID string `json:"provider_msg_id,omitempty"`
	Message       string `json:"message,omitempty"`
}

// ErrChannelDisabled 表示测试发送的目标通道已停用（管理 API 映射为 409）。
var ErrChannelDisabled = errors.New("通道已停用")

// TestSend 从管理端直接对指定通道下发一条测试短信：
// appSmsId=test-<unixms>-<rand>，source=test 全程留痕，同步返回投递结果。
// 通道不存在或停用时直接返回错误（不落记录，供页面即时报错）。
func (f *ForwardService) TestSend(ctx context.Context, channelID, templateCode string, o feilian.SMSObject) (*TestSendResult, error) {
	snap := f.cache.Current()

	var rc *store.RuntimeChannel
	for i := range snap.Channels {
		if snap.Channels[i].ID == channelID {
			rc = &snap.Channels[i]
			break
		}
	}
	if rc == nil {
		return nil, fmt.Errorf("%w: 通道 %s", store.ErrNotFound, channelID)
	}
	if !rc.Enabled {
		return nil, fmt.Errorf("%w: 通道 %s", ErrChannelDisabled, rc.Name)
	}
	cfg, err := channel.ParseConfig(rc.ConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("通道配置无法解析: %w", err)
	}

	now := f.now()
	appSmsID := "test-" + strconv.FormatInt(now, 10) + "-" + randomSuffix()
	in := channel.SendInput{
		AppSmsID:     appSmsID,
		CountryCode:  o.CountryCode,
		MobileNumber: o.MobileNumber,
		Mobile:       o.Mobile,
		SMSType:      o.SMSType,
		TemplateCode: templateCode,
		Params:       o.Params,
	}

	rec := store.SendRecord{
		AppSmsID:     appSmsID,
		Source:       store.SourceTest,
		ChannelID:    channelID,
		SMSType:      o.SMSType,
		MobileMasked: store.MaskSecret(o.MobileNumber),
		ParamsMasked: maskParams(o.Params),
		TemplateCode: templateCode,
		Status:       store.StatusPending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if _, _, err := f.store.InsertPendingIfAbsent(ctx, rec); err != nil {
		return nil, err
	}

	// 渲染阶段失败（号码/配置）同样留痕并同步返回。
	rendered, err := cfg.Render(rc.Secrets, in)
	if err != nil {
		kind := store.ErrorKindRender
		if errors.Is(err, channel.ErrInvalidMobile) {
			kind = store.ErrorKindInvalidMobile
		}
		f.markFailed(ctx, appSmsID, kind, "", err.Error(), 0, now)
		return &TestSendResult{AppSmsID: appSmsID, Success: false, ErrorKind: kind, Message: err.Error()}, nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, timeoutDuration(snap.Settings.DownstreamTimeoutMS))
	defer cancel()
	start := f.now()
	res, err := f.sender.Do(sendCtx, cfg, rendered)
	latency := f.now() - start
	if err != nil {
		kind := store.ErrorKindNetwork
		var te *channel.TransportError
		if errors.As(err, &te) && te.Timeout {
			kind = store.ErrorKindTimeout
		}
		f.markFailed(ctx, appSmsID, kind, "", err.Error(), latency, f.now())
		return &TestSendResult{AppSmsID: appSmsID, Success: false, ErrorKind: kind, Message: err.Error()}, nil
	}

	if res.Success {
		_ = f.store.MarkSuccess(ctx, appSmsID, store.MarkSuccess{
			ProviderMsgID: res.VendorID, ProviderStatus: strconv.Itoa(res.HTTPCode),
			ProviderMessage: res.Message, LatencyMS: latency, NowMS: f.now(),
		})
		return &TestSendResult{AppSmsID: appSmsID, Success: true, HTTPCode: res.HTTPCode,
			ProviderMsgID: res.VendorID, Message: res.Message}, nil
	}

	kind := store.ErrorKindVendor
	if res.HTTPCode < 200 || res.HTTPCode >= 300 {
		kind = store.ErrorKindNetwork
	}
	f.markFailed(ctx, appSmsID, kind, strconv.Itoa(res.HTTPCode), res.Message, latency, f.now())
	return &TestSendResult{AppSmsID: appSmsID, Success: false, HTTPCode: res.HTTPCode,
		ErrorKind: kind, Message: res.Message}, nil
}

func randomSuffix() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 熵源异常时退化为时间戳低位，保证测试发送仍可进行
		return strconv.FormatInt(nowMS()&0xffffff, 16)
	}
	return hex.EncodeToString(b[:])
}
