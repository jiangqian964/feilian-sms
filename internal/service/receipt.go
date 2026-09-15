package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/channel/mapping"
	"feilian-sms/internal/store"
)

// ErrReceiptChannelNotFound 表示回执指向的通道不存在（HTTP 层回 404）。
var ErrReceiptChannelNotFound = errors.New("回执通道不存在")

// deliveryStatus 归一化后的送达状态（落 sms_send.delivery_status）。
const (
	deliveryDelivered = "delivered"
	deliveryFailed    = "delivery_failed"
)

// defaultReceiptAck 是回执默认的精确成功响应体（可被通道配置覆盖）。
const defaultReceiptAck = `{"status":0,"message":"success"}`

// ReceiptResult 是一次回执处理结果；ResponseBody 必须原样回给厂商。
type ReceiptResult struct {
	Found          bool   // 我方是否有对应 appSmsId 记录（false 仅记日志，仍回成功）
	AppSmsID       string // 我方消息 ID
	VendorMsgID    string // 厂商消息 ID
	Delivered      bool   // 是否命中送达成功值
	DeliveryStatus string // delivered / delivery_failed
	ResponseBody   []byte // 回给厂商的成功响应
	RemoteIP       string // 回执来源（直连 RemoteAddr，供日志留痕）
}

// ReceiptService 处理厂商异步送达回执。
type ReceiptService struct {
	store *store.Store
	cache *store.Cache
	now   func() int64
}

// NewReceiptService 构造回执服务。
func NewReceiptService(st *store.Store, cache *store.Cache) *ReceiptService {
	return &ReceiptService{store: st, cache: cache, now: nowMS}
}

// Handle 解析并回写一条回执。通道不存在返回 ErrReceiptChannelNotFound；
// 报文损坏/缺关键字段返回错误（HTTP 层回 400）；未知 appSmsId 不报错（Found=false）。
func (s *ReceiptService) Handle(ctx context.Context, channelID string, body []byte, remoteIP string) (*ReceiptResult, error) {
	snap := s.cache.Current()

	var rc *store.RuntimeChannel
	for i := range snap.Channels {
		if snap.Channels[i].ID == channelID {
			rc = &snap.Channels[i]
			break
		}
	}
	if rc == nil {
		return nil, fmt.Errorf("%w: %s", ErrReceiptChannelNotFound, channelID)
	}
	cfg, err := channel.ParseConfig(rc.ConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("通道回执配置无法解析: %w", err)
	}
	rcpt := cfg.Receipt

	var decoded any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("回执体不是合法 JSON: %w", err)
	}

	appSmsID, ok := mapping.GetString(decoded, rcpt.AppMsgIDPath)
	if !ok || appSmsID == "" {
		return nil, fmt.Errorf("回执缺少我方消息 ID 路径 %q", rcpt.AppMsgIDPath)
	}
	vendorMsgID, _ := mapping.GetString(decoded, rcpt.MsgIDPath)
	statusVal, statusOK := mapping.GetString(decoded, rcpt.StatusPath)
	message, _ := mapping.GetString(decoded, rcpt.MessagePath)
	seqNo := extractSeqNo(decoded, rcpt.SeqNoPath)

	delivered := statusOK && rcpt.DeliveredValue != "" && statusVal == rcpt.DeliveredValue
	deliveryStatus := deliveryFailed
	if delivered {
		deliveryStatus = deliveryDelivered
	}

	found, err := s.store.ApplyReceipt(ctx, store.ReceiptUpdate{
		AppSmsID:        appSmsID,
		DeliveryStatus:  deliveryStatus,
		DeliveryMessage: message,
		SeqNo:           seqNo,
		ReceiptAtMS:     s.now(),
	})
	if err != nil {
		return nil, err
	}

	return &ReceiptResult{
		Found:          found,
		AppSmsID:       appSmsID,
		VendorMsgID:    vendorMsgID,
		Delivered:      delivered,
		DeliveryStatus: deliveryStatus,
		ResponseBody:   buildAck(rcpt.SuccessBody),
		RemoteIP:       remoteIP,
	}, nil
}

// buildAck 返回回给厂商的成功响应：配置了合法自定义体则用之，否则用内置默认。
func buildAck(custom string) []byte {
	if custom != "" && json.Valid([]byte(custom)) {
		return []byte(custom)
	}
	return []byte(defaultReceiptAck)
}

// extractSeqNo 提取回执序号；缺失或非数字返回 0。
func extractSeqNo(v any, path string) int {
	if path == "" {
		return 0
	}
	s, ok := mapping.GetString(v, path)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
