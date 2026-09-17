package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
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
	outcomePending = "pending" // 已受理但结果不确定（超时/5xx/429/传输错误），保 pending 待补发
)

const (
	// maxResendAttempts 是 stale 续发认领次数上限（attempts 列）：
	// 首次下发不计，补发最多 3 次（总计 4 次尝试），超过置 resend_exhausted 终态，
	// 防止对一个持续失败的号码/凭证无限重试。
	maxResendAttempts = 3
	// writebackTimeout 是终态回写等 DB 动作的独立超时；与入站请求生命周期解耦。
	writebackTimeout = 10 * time.Second
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
	Pending   int          `json:"pending"`
	Items     []ItemResult `json:"items"`
}

// ForwardService 编排飞连事件 → 通用通道下发 → 状态回写。
type ForwardService struct {
	store  *store.Store
	cache  *store.Cache
	sender Sender
	now    func() int64
	logger *zap.Logger

	// bgCtx 与入站请求生命周期解耦（客户端断连不取消在途下发/终态回写），
	// 但受 Shutdown 控制（关停时取消在途动作，未确认记录保 pending 待重启补发）。
	bgCtx    context.Context
	cancelBg context.CancelFunc
	// wg 跟踪后台补发 worker，Shutdown 时等待其退出。
	wg sync.WaitGroup
}

// NewForwardService 构造转发服务；now 可空（默认系统毫秒时钟）。
// 默认使用空日志，生产装配处通过 WithLogger 注入统一脱敏的 Zap logger。
func NewForwardService(st *store.Store, cache *store.Cache, sender Sender) *ForwardService {
	bgCtx, cancel := context.WithCancel(context.Background())
	return &ForwardService{
		store: st, cache: cache, sender: sender, now: nowMS,
		logger:   zap.NewNop(),
		bgCtx:    bgCtx,
		cancelBg: cancel,
	}
}

// WithLogger 注入结构化日志器并返回同一实例（链式，main 装配时调用一次）。
func (f *ForwardService) WithLogger(logger *zap.Logger) *ForwardService {
	if logger != nil {
		f.logger = logger
	}
	return f
}

// Shutdown 取消在途后台动作并等待补发 worker 退出；ctx 超时则放弃等待。
func (f *ForwardService) Shutdown(ctx context.Context) error {
	f.cancelBg()
	done := make(chan struct{})
	go func() {
		f.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
// 注意：入站 ctx 的取消不会中断处理（客户端断连不丢短信），关停由
// ForwardService 自身的 bgCtx 控制，故此处不向下透传入站 ctx。
func (f *ForwardService) HandleEvent(_ context.Context, eventID string, objects []feilian.SMSObject) *ForwardResult {
	result := &ForwardResult{Total: len(objects)}
	batch := len(objects) > 1
	snap := f.cache.Current()

	for i, o := range objects {
		appSmsID := eventID
		if batch {
			appSmsID = fmt.Sprintf("%s-%d", eventID, i)
		}
		item := f.processOne(snap, eventID, appSmsID, o)
		result.Items = append(result.Items, item)
		f.logItem(item)
		switch item.Outcome {
		case outcomeSuccess:
			result.Succeeded++
		case outcomeFailed:
			result.Failed++
		case outcomePending:
			result.Pending++
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
	case outcomePending:
		if item.ErrorKind != "" {
			fields = append(fields, zap.String("error_kind", item.ErrorKind))
		}
		if item.Reason != "" {
			fields = append(fields, zap.String("reason", item.Reason))
		}
		f.logger.Warn("短信下发结果不确定，保持 pending 等待补发", fields...)
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

func (f *ForwardService) processOne(snap *store.Snapshot, eventID, appSmsID string, o feilian.SMSObject) ItemResult {
	if len(appSmsID) >= 64 {
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: store.ErrorKindInternal, Reason: "app_sms_id 长度达到/超过 64，拒绝下发"}
	}

	now := f.now()
	plan := f.buildPlan(snap, appSmsID, o)

	// 原始对象加密落库，供补发 worker 在无飞连重推时解密重放。
	payloadCT, err := f.sealObject(o)
	if err != nil {
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: store.ErrorKindInternal, Reason: err.Error()}
	}

	// 1) 原子幂等闸门：先插后发，只有插入者才下发；并发重复请求必有一方拿到既有行。
	rec := store.SendRecord{
		AppSmsID:         appSmsID,
		EventID:          eventID,
		Source:           store.SourceFeilian,
		ChannelID:        plan.channelID,
		SMSType:          o.SMSType,
		MobileMasked:     store.MaskSecret(o.MobileNumber),
		ParamsMasked:     maskParams(o.Params),
		TemplateCode:     plan.templateCode,
		Status:           store.StatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
		EncryptedPayload: payloadCT,
	}
	inserted, existing, err := f.store.InsertPendingIfAbsent(f.bgCtx, rec)
	if err != nil {
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: store.ErrorKindInternal, Reason: err.Error()}
	}
	if !inserted {
		// 终态直接跳过；pending 未到 stale 阈值视为在途跳过；stale 走 CAS 单飞续发。
		switch existing.Status {
		case store.StatusSuccess, store.StatusFailed:
			return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeSkipped,
				Reason: "已有终态记录"}
		}
		staleMS := snap.Settings.StalePendingMS
		if staleMS <= 0 {
			staleMS = 120_000
		}
		if now-existing.UpdatedAt < int64(staleMS) {
			return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeSkipped,
				Reason: "在途 pending 未超补发阈值"}
		}
		claimed, skip, terminal := f.claimForResend(snap, existing, plan, o, now)
		if skip != nil {
			return *skip
		}
		if terminal != nil {
			return *terminal
		}
		_ = claimed // 认领副作用（updated_at 前推、attempts+1、路由刷新）已完成
	}

	// 2) 配置降级 / 渲染 / 下发 / 终态回写，与补发 worker 共用同一通道。
	return f.dispatch(snap, appSmsID, o, plan)
}

// claimForResend 对一条 stale pending 执行 CAS 认领、补发上限裁决与路由刷新。
// 返回 (claimed, skipItem, terminalItem)：skip/terminal 非空表示调用方应直接
// 返回该结论；两者均空表示赢得续发权、已完成路由刷新，可继续 dispatch。
func (f *ForwardService) claimForResend(snap *store.Snapshot, existing *store.SendRecord,
	plan dispatchPlan, o feilian.SMSObject, now int64) (bool, *ItemResult, *ItemResult) {
	appSmsID := existing.AppSmsID
	// 上限裁决先于 CAS：attempts 已达上限直接终态，不再前推租约/递增计数，
	// 保证 attempts 恰好等于实际续发次数（最大 3）。
	if existing.Attempts >= maxResendAttempts {
		f.markFailed(appSmsID, store.ErrorKindResendExhausted, "",
			fmt.Sprintf("超过最大补发次数 %d 仍未确认", maxResendAttempts), 0, now)
		return false, nil, &ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: store.ErrorKindResendExhausted, Reason: "超过最大补发次数"}
	}
	claimed, err := f.store.ClaimStalePending(f.bgCtx, appSmsID, existing.UpdatedAt, now)
	if err != nil {
		return false, nil, &ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: store.ErrorKindInternal, Reason: err.Error()}
	}
	if !claimed {
		return false, &ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeSkipped,
			Reason: "stale pending 已被并发任务认领"}, nil
	}
	// 续发按当前快照路由（绑定可能已改投通道）；刷新失败不阻断下发，仅留日志。
	if err := f.store.RefreshPendingRoute(f.bgCtx, appSmsID, plan.channelID, plan.templateCode,
		store.MaskSecret(o.MobileNumber), maskParams(o.Params), now); err != nil {
		f.logger.Warn("刷新续发路由失败，按新计划继续下发",
			zap.String("app_sms_id", appSmsID), zap.Error(err))
	}
	return true, nil, nil
}

// dispatch 对已获得发送权的记录执行 配置降级判定 → 渲染 → 下发 → 终态回写。
// processOne（飞连实时事件）与补发 worker 共用本方法，保证两条路径语义一致。
func (f *ForwardService) dispatch(snap *store.Snapshot, appSmsID string, o feilian.SMSObject, plan dispatchPlan) ItemResult {
	now := f.now()

	// 配置类降级：零下发，直接置失败。
	if plan.degradeKind != "" {
		f.markFailed(appSmsID, plan.degradeKind, "", plan.degradeWhy, 0, now)
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: plan.degradeKind, Reason: plan.degradeWhy}
	}

	// 渲染（号码/映射/签名错误在此分类，均为确定性失败）。
	rendered, err := plan.cfg.Render(plan.secrets, plan.input)
	if err != nil {
		kind := store.ErrorKindRender
		if errors.Is(err, channel.ErrInvalidMobile) {
			kind = store.ErrorKindInvalidMobile
		}
		f.markFailed(appSmsID, kind, "", err.Error(), 0, now)
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
			ErrorKind: kind, Reason: err.Error()}
	}

	// 下发：超时以当前设置为准（热生效）；ctx 与入站请求解耦、受关停控制。
	sendCtx, cancel := context.WithTimeout(f.bgCtx, timeoutDuration(snap.Settings.DownstreamTimeoutMS))
	defer cancel()
	start := f.now()
	res, err := f.sender.Do(sendCtx, plan.cfg, rendered)
	latency := f.now() - start
	if err != nil {
		// 传输层任何错误（超时/连接/读响应失败/关停取消）都可能是「厂商已收到、
		// 响应丢失」，不得置失败终态，保持 pending 由补发 worker/飞连重推兜底。
		kind := store.ErrorKindNetwork
		var te *channel.TransportError
		if errors.As(err, &te) && te.Timeout {
			kind = store.ErrorKindTimeout
		}
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomePending,
			ErrorKind: kind, Reason: err.Error()}
	}

	if res.Success {
		if mErr := f.markSuccess(appSmsID, store.MarkSuccess{
			ProviderMsgID: res.VendorID, ProviderStatus: strconv.Itoa(res.HTTPCode),
			ProviderMessage: res.Message, LatencyMS: latency, NowMS: f.now(),
		}); mErr != nil {
			return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
				ErrorKind: store.ErrorKindInternal, Reason: mErr.Error()}
		}
		return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeSuccess,
			ProviderMsgID: res.VendorID}
	}

	// 2xx 业务拒绝 → vendor；4xx（429 除外）→ 确定性 network 失败，均为终态。
	// 5xx 与 429 同样可能已被厂商受理，结果不确定 → 保 pending 等待补发。
	if isDefiniteHTTPReject(res.HTTPCode) {
		kind := store.ErrorKindNetwork
		return f.failWithRecord(appSmsID, o, kind, strconv.Itoa(res.HTTPCode), res.Message, latency, now)
	}
	if res.HTTPCode >= 200 && res.HTTPCode < 300 {
		return f.failWithRecord(appSmsID, o, store.ErrorKindVendor, strconv.Itoa(res.HTTPCode),
			res.Message, latency, now)
	}
	return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomePending,
		ErrorKind: store.ErrorKindNetwork,
		Reason:    fmt.Sprintf("厂商 HTTP %d 结果不确定，等待补发", res.HTTPCode)}
}

// failWithRecord 回写 failed 终态并返回失败结论。
func (f *ForwardService) failWithRecord(appSmsID string, o feilian.SMSObject, kind, providerStatus,
	message string, latency, now int64) ItemResult {
	f.markFailed(appSmsID, kind, providerStatus, message, latency, now)
	return ItemResult{AppSmsID: appSmsID, SMSType: o.SMSType, Outcome: outcomeFailed,
		ErrorKind: kind, Reason: message,
		ProviderMsgID: ""}
}

// isDefiniteHTTPReject 判定非 2xx 响应中哪些是确定性拒绝（重试无意义）：
// 4xx（除 429 限流）；5xx 与 429 视为不确定，留 pending 补发。
func isDefiniteHTTPReject(httpCode int) bool {
	return httpCode >= 400 && httpCode < 500 && httpCode != 429
}

// sealObject 序列化并加密原始短信对象（空对象产出空密文，历史行为兼容）。
func (f *ForwardService) sealObject(o feilian.SMSObject) (string, error) {
	b, err := json.Marshal(o)
	if err != nil {
		return "", fmt.Errorf("序列化补发载荷失败: %w", err)
	}
	ct, err := f.store.SealPayload(b)
	if err != nil {
		return "", fmt.Errorf("加密补发载荷失败: %w", err)
	}
	return ct, nil
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

// markFailed 回写 failed 终态；DB 错误不再被静默吞掉（#10），打 Error 日志，
// 且使用独立超时上下文，入站客户端断连不影响终态落库。
func (f *ForwardService) markFailed(appSmsID, kind, providerStatus, message string, latency, now int64) {
	ctx, cancel := context.WithTimeout(f.bgCtx, writebackTimeout)
	defer cancel()
	if err := f.store.MarkFailed(ctx, appSmsID, store.MarkFailed{
		ErrorKind: kind, ProviderStatus: providerStatus,
		ProviderMessage: message, LatencyMS: latency, NowMS: now,
	}); err != nil {
		f.logger.Error("failed 终态回写失败",
			zap.String("app_sms_id", appSmsID),
			zap.String("error_kind", kind),
			zap.Error(err))
	}
}

// markSuccess 回写 success 终态；失败返回错误供调用方决定结论。
func (f *ForwardService) markSuccess(appSmsID string, m store.MarkSuccess) error {
	ctx, cancel := context.WithTimeout(f.bgCtx, writebackTimeout)
	defer cancel()
	if err := f.store.MarkSuccess(ctx, appSmsID, m); err != nil {
		f.logger.Error("success 终态回写失败",
			zap.String("app_sms_id", appSmsID), zap.Error(err))
		return err
	}
	return nil
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
