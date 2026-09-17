package feilian

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
)

const supportedSchema = "1.0"

// ParseEnvelope 解析并校验 notify.v1.sms 事件信封。
// 对结构缺失（schema/header 关键字段/events/object/sms_type）返回中文错误，
// 以便 webhook 层快速归类为 400。
func ParseEnvelope(raw []byte) (*Envelope, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("事件请求体为空")
	}

	var probe struct {
		Schema string `json:"schema"`
		Header Header `json:"header"`
		Data   struct {
			Events []json.RawMessage `json:"events"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("解析事件 JSON 失败: %w", err)
	}
	if probe.Schema == "" {
		return nil, fmt.Errorf("事件缺少 schema 字段")
	}
	if probe.Schema != supportedSchema {
		return nil, fmt.Errorf("不支持的事件 schema %q（仅支持 %s）", probe.Schema, supportedSchema)
	}
	if probe.Header.EventID == "" {
		return nil, fmt.Errorf("事件 header 缺少 event_id")
	}
	if probe.Header.Token == "" {
		return nil, fmt.Errorf("事件 header 缺少 token")
	}
	if probe.Header.EventType == "" {
		return nil, fmt.Errorf("事件 header 缺少 event_type")
	}
	if len(probe.Data.Events) == 0 {
		return nil, fmt.Errorf("事件 data.events 为空")
	}

	env := &Envelope{
		Schema: probe.Schema,
		Header: probe.Header,
	}
	env.Data.Events = make([]Event, 0, len(probe.Data.Events))
	for i, rawEvent := range probe.Data.Events {
		var holder struct {
			Object json.RawMessage `json:"object"`
		}
		if err := json.Unmarshal(rawEvent, &holder); err != nil {
			return nil, fmt.Errorf("解析 data.events[%d] 失败: %w", i, err)
		}
		if len(holder.Object) == 0 {
			return nil, fmt.Errorf("data.events[%d] 缺少 object 对象", i)
		}
		var obj SMSObject
		if err := json.Unmarshal(holder.Object, &obj); err != nil {
			return nil, fmt.Errorf("解析 data.events[%d].object 失败: %w", i, err)
		}
		if obj.SMSType == "" {
			return nil, fmt.Errorf("data.events[%d].object 缺少 sms_type", i)
		}
		env.Data.Events = append(env.Data.Events, Event{Object: obj})
	}
	return env, nil
}

// EventHeaderView 是事件信封的头部轻量解析结果（不解析 data.events）。
type EventHeaderView struct {
	Schema string
	Header Header
}

// ParseEventHeader 仅解析 schema 与 header（token/event_type 等），不校验
// data.events 的短信对象结构。用途：webhook 必须先完成 token 鉴权再决定
// 是否忽略非短信事件，而 device.* 等其他事件的 object 本来就不是短信结构，
// 不能用 ParseEnvelope 的短信校验去拦截它们。
func ParseEventHeader(raw []byte) (*EventHeaderView, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("事件请求体为空")
	}
	var probe struct {
		Schema string `json:"schema"`
		Header Header `json:"header"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("解析事件 JSON 失败: %w", err)
	}
	if probe.Schema == "" {
		return nil, fmt.Errorf("事件缺少 schema 字段")
	}
	if probe.Schema != supportedSchema {
		return nil, fmt.Errorf("不支持的事件 schema %q（仅支持 %s）", probe.Schema, supportedSchema)
	}
	if probe.Header.Token == "" {
		return nil, fmt.Errorf("事件 header 缺少 token")
	}
	if probe.Header.EventType == "" {
		return nil, fmt.Errorf("事件 header 缺少 event_type")
	}
	return &EventHeaderView{Schema: probe.Schema, Header: probe.Header}, nil
}

// Challenge 是订阅保存时飞连发来的 url_verification 握手请求。
type Challenge struct {
	Challenge string `json:"challenge"`
	Token     string `json:"token"`
	Type      string `json:"type"`
}

// ParseChallenge 识别 url_verification 请求；非该类型返回 ok=false。
// 类型匹配但 challenge 缺失时返回 ok=true 与错误。
func ParseChallenge(raw []byte) (*Challenge, bool, error) {
	var c Challenge
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, false, fmt.Errorf("解析 url_verification 请求失败: %w", err)
	}
	if c.Type != "url_verification" {
		return nil, false, nil
	}
	if c.Challenge == "" {
		return &c, true, fmt.Errorf("url_verification 请求缺少 challenge 字段")
	}
	return &c, true, nil
}

// ExtractEncrypted 识别加密信封 {"encrypt":"..."}；明文事件返回 ok=false。
func ExtractEncrypted(raw []byte) (string, bool, error) {
	var env struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", false, fmt.Errorf("解析加密信封失败: %w", err)
	}
	if env.Encrypt == "" {
		return "", false, nil
	}
	return env.Encrypt, true, nil
}

// TokenEqual 以恒定时间比较 Verification Token，空期望值一律不通过。
func TokenEqual(got, want string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
