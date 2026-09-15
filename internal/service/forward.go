package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/feilian"
	"feilian-sms/internal/store"
)

// 单条处理结论。
const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
	outcomeSkipped = "skipped" // 幂等命中终态或在途 pending，未重复下发
)

// Sender 抽象通道下发（生产为 *channel.Client，测试可用假实现断言调用次数）。
type Sender interface {
	Do(ctx context.Context, cfg *channel.Config, rendered *channel.RenderResult) (*channel.SendResult, error)
}

// ItemResult 是单个短信 object 的处理结果。
type ItemResult struct {
	AppSmsID      string `json:"app_sms_id"`
	SMSType       string `json:"sms_type"`
	Outcome       string `json:"outcome"`
	ErrorKind     string `json:"error_kind,omitempty"`
	ProviderMsgID string `json:"provider_msg_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// ForwardResult 是一批事件的汇总（HTTP 层据此恒回 200，明细仅留痕/日志）。
type ForwardResult struct {
	Total     int          `json:"total"`
	Succeeded int          `json:"succeeded"`
	Failed    int          `json:"failed"`
	Skipped   int          `json:"skipped"`
	Items     []ItemResult `json:"items"`
}

// ForwardService 编排飞连事件 → 通用通道下发 → 状态回写。
type ForwardService struct {
	store  *store.Store
	cache  *store.Cache
	sender Sender
	now    func() int64
	logger *zap.Logger
}

// NewForwardService 构造转发服务；now 可空（默认系统毫秒时钟）。
// 默认使用空日志，生产装配处通过 WithLogger 注入统一脱敏的 Zap logger。
func NewForwardService(st *store.Store, cache *store.Cache, sender Sender) *ForwardService {
	return &ForwardService{store: st, cache: cache, sender: sender, now: nowMS, logger: zap.NewNop()}
}

// WithLogger 注入结构化日志器并返回同一实例（链式，main 装配时调用一次）。
func (f *ForwardService) WithLogger(logger *zap.Logger) *ForwardService {
	if logger != nil {
		f.logger = logger
	}
	return f
}

func nowMS() int64 { return time.Now().UnixMilli() }

// dispatchPlan 是单条 object 的配置解析结果（降级时 degradeKind 非空）。
type dispatchPlan struct {
	channelID    string
	templateCode string
	cfg          *channel.Config
	secrets      map[string]string
	input        channel.SendInput
	degradeKind  string
	degradeWhy   string
}

// HandleEvent 处理一个飞连事件（批量时每个 object 一条）。
// 任何单条失败都不影响其余条目，且不向上返回错误（入站层恒回 200）。
func (f *ForwardService) HandleEvent(ctx context.Context, eventID string, objects []feilian.SMSObject) *ForwardResult {
	result := &ForwardResult{Total: len(objects)}
	batch := len(objects) > 1
	snap := f.cache.Current()

	for i, o := range objects {
		appSmsID := eventID
		if batch {
			appSmsID = fmt.Sprintf("%s-%d", eventID, i)
		}
		item := f.processOne(ctx, snap, eventID, appSmsID, o)
		result.Items = append(result.Items, item)
		f.logItem(item)
		switch item.Outcome {
		case outcomeSuccess:
			result.Succeeded++
		case outcomeFailed:
			result.Failed++
		case outcomeSkipped:
			result.Skipped++
		}
	}
	return result
}

// logItem 为每条短信打一条结构化结论日志；只含标识 ID/场景/错误分类，
// 绝不携带手机号、params、密钥与签名（reason 文本另由日志 core 正则兜底）。
func (f *ForwardService) logItem(item ItemResult) {
	fields := []zap.Field{
		zap.String("app_sms_id", item.AppSmsID),
		zap.String("sms_type", item.SMSType),
		zap.String("outcome", item.Outcome),
	}
	if item.ProviderMsgID != "" {
		fields = append(fields, zap.String("provider_msg_id", item.ProviderMsgID))
	}
	switch item.Outcome {
	case outcomeSuccess:
		f.logger.Info("短信已下发", fields...)
	case outcomeSkipped:
		if item.Reason != "" {
			fields = append(fields, zap.String("reason", item.Reason))
		}
		f.logger.Debug("短信跳过未重复下发", fields...)
	default:
		if item.ErrorKind != "" {
			fields = append(fields, zap.String("error_kind", item.ErrorKind))
		}
		if item.Reason != "" {
			fields = append(fields, zap.String("reason", item.Reason))
		}
		f.logger.Warn("短信下发失败", fields...)
	}
}

func (f *ForwardService) processOne(ctx context.Context, snap *store.Snapshot,
	eventID, appSmsID string, o feilian.SMSObject) ItemResult {
	if len(appSmsID) >= 64 {
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: store.ErrorKindInternal, Reason: "app_sms_id 长度达到/超过 64，拒绝下发"}
	}

	now := f.now()

	// 1) 幂等闸门：终态直接跳过；在途 pending 未到 stale 阈值也跳过。
	if existing, err := f.store.GetSend(ctx, appSmsID); err == nil {
		if existing.Status == store.StatusSuccess || existing.Status == store.StatusFailed {
			return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeSkipped,
				Reason: "已有终态记录"}
		}
		if now-existing.CreatedAt < int64(snap.Settings.StalePendingMS) {
			return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeSkipped,
				Reason: "在途 pending 未超补发阈值"}
		}
		// stale pending：落到后续流程续发（不新建行）。
	} else if !errors.Is(err, store.ErrNotFound) {
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: store.ErrorKindInternal, Reason: err.Error()}
	}

	// 2) 解析当前配置快照（绑定→通道→参数重排→输入）。
	plan := f.buildPlan(snap, appSmsID, o)

	// 3) 新记录落 pending（stale 续发时行已存在）。
	if _, err := f.store.GetSend(ctx, appSmsID); errors.Is(err, store.ErrNotFound) {
		rec := store.SendRecord{
			AppSmsID:     appSmsID,
			EventID:      eventID,
			Source:       store.SourceFeilian,
			ChannelID:    plan.channelID,
			SMSType:      o.SMSType,
			MobileMasked: store.MaskSecret(o.MobileNumber),
			ParamsMasked: maskParams(o.Params),
			TemplateCode: plan.templateCode,
			Status:       store.StatusPending,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if _, _, err := f.store.InsertPendingIfAbsent(ctx, rec); err != nil {
			return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
				ErrorKind: store.ErrorKindInternal, Reason: err.Error()}
		}
	}

	// 4) 配置类降级：零下发，直接置失败。
	if plan.degradeKind != "" {
		f.markFailed(ctx, appSmsID, plan.degradeKind, "", plan.degradeWhy, 0, now)
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: plan.degradeKind, Reason: plan.degradeWhy}
	}

	// 5) 渲染（号码/映射/签名错误在此分类）。
	rendered, err := plan.cfg.Render(plan.secrets, plan.input)
	if err != nil {
		kind := store.ErrorKindRender
		if errors.Is(err, channel.ErrInvalidMobile) {
			kind = store.ErrorKindInvalidMobile
		}
		f.markFailed(ctx, appSmsID, kind, "", err.Error(), 0, now)
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: kind, Reason: err.Error()}
	}

	// 6) 下发（超时取当前设置，天然热生效）。
	sendCtx, cancel := context.WithTimeout(ctx, timeoutDuration(snap.Settings.DownstreamTimeoutMS))
	defer cancel()
	start := f.now()
	res, err := f.sender.Do(sendCtx, plan.cfg, rendered)
	latency := f.now() - start
	if err != nil {
		kind := store.ErrorKindNetwork
		var te *channel.TransportError
		if errors.As(err, &te) && te.Timeout {
			kind = store.ErrorKindTimeout
		}
		f.markFailed(ctx, appSmsID, kind, "", err.Error(), latency, now)
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: kind, Reason: err.Error()}
	}

	if res.Success {
		if mErr := f.store.MarkSuccess(ctx, appSmsID, store.MarkSuccess{
			ProviderMsgID: res.VendorID, ProviderStatus: strconv.Itoa(res.HTTPCode),
			ProviderMessage: res.Message, LatencyMS: latency, NowMS: f.now(),
		}); mErr != nil {
			return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
				ErrorKind: store.ErrorKindInternal, Reason: mErr.Error()}
		}
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeSuccess,
			ProviderMsgID: res.VendorID}
	}

	// 2xx 业务拒绝 → vendor；非 2xx 基础设施错误 → network。
	kind := store.ErrorKindVendor
	if res.HTTPCode < 200 || res.HTTPCode >= 300 {
		kind = store.ErrorKindNetwork
	}
	f.markFailed(ctx, appSmsID, kind, strconv.Itoa(res.HTTPCode), res.Message, latency, now)
	return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
		ErrorKind: kind, Reason: res.Message}
}

// buildPlan 从快照解析绑定/通道并完成参数重排；任何前置不满足都转为降级计划。
func (f *ForwardService) buildPlan(snap *store.Snapshot, appSmsID string, o feilian.SMSObject) dispatchPlan {
	plan := dispatchPlan{}

	var binding *store.Binding
	for i := range snap.Bindings {
		if snap.Bindings[i].SMSType == o.SMSType {
			binding = &snap.Bindings[i]
			break
		}
	}
	if binding == nil {
		plan.degradeKind = store.ErrorKindUnbound
		plan.degradeWhy = fmt.Sprintf("场景 %s 未绑定通道", o.SMSType)
		return plan
	}
	if !binding.Enabled {
		plan.degradeKind = store.ErrorKindBindingDisabled
		plan.degradeWhy = fmt.Sprintf("场景 %s 的绑定已停用", o.SMSType)
		return plan
	}
	plan.channelID = binding.ChannelID
	plan.templateCode = binding.TemplateCode

	var rc *store.RuntimeChannel
	for i := range snap.Channels {
		if snap.Channels[i].ID == binding.ChannelID {
			rc = &snap.Channels[i]
			break
		}
	}
	if rc == nil {
		plan.degradeKind = store.ErrorKindChannelNotFound
		plan.degradeWhy = "绑定指向的通道不存在"
		return plan
	}
	if !rc.Enabled {
		plan.degradeKind = store.ErrorKindChannelDisabled
		plan.degradeWhy = fmt.Sprintf("通道 %s 已停用", rc.Name)
		return plan
	}

	cfg, err := channel.ParseConfig(rc.ConfigJSON)
	if err != nil {
		plan.degradeKind = store.ErrorKindRender
		plan.degradeWhy = err.Error()
		return plan
	}

	params, err := reorderParams(o.Params, binding.ParamIndex)
	if err != nil {
		plan.degradeKind = store.ErrorKindRender
		plan.degradeWhy = err.Error()
		return plan
	}

	plan.cfg = cfg
	plan.secrets = rc.Secrets
	plan.input = channel.SendInput{
		AppSmsID:     appSmsID,
		CountryCode:  o.CountryCode,
		MobileNumber: o.MobileNumber,
		Mobile:       o.Mobile,
		SMSType:      o.SMSType,
		TemplateCode: binding.TemplateCode,
		Params:       params,
	}
	return plan
}

func (f *ForwardService) markFailed(ctx context.Context, appSmsID, kind, providerStatus, message string, latency, now int64) {
	_ = f.store.MarkFailed(ctx, appSmsID, store.MarkFailed{
		ErrorKind: kind, ProviderStatus: providerStatus,
		ProviderMessage: message, LatencyMS: latency, NowMS: now,
	})
}

// reorderParams 按绑定的下标表重排飞连模板参数；空表保持原序；越界报错。
func reorderParams(params []string, idx []int) ([]string, error) {
	if len(idx) == 0 {
		return params, nil
	}
	out := make([]string, 0, len(idx))
	for _, i := range idx {
		if i < 0 || i >= len(params) {
			return nil, fmt.Errorf("参数下标 %d 越界（飞连事件共 %d 个参数）", i, len(params))
		}
		out = append(out, params[i])
	}
	return out, nil
}

// maskParams 对参数逐个脱敏后以 JSON 数组落库。
func maskParams(params []string) string {
	masked := make([]string, len(params))
	for i, p := range params {
		masked[i] = store.MaskSecret(p)
	}
	b, _ := json.Marshal(masked)
	return string(b)
}

// timeoutDuration 把毫秒设置转为超时；非正值回落到 2000ms。
func timeoutDuration(ms int) time.Duration {
	if ms <= 0 {
		ms = 2000
	}
	return time.Duration(ms) * time.Millisecond
}
