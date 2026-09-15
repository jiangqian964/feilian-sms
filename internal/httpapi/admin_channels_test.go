// TR-11.1 预置与通道：CRUD/启停/from-preset/试渲染 400/掩码/测试发送 200/409。
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/channel/mapping"
	"feilian-sms/internal/channel/sign"
	"feilian-sms/internal/service"
	"feilian-sms/internal/store"
)

// newTestEnvAt 与 newTestEnv 相同，但额外返回数据库文件路径（明文扫描用）。
func newTestEnvAt(t *testing.T) (string, *store.Store, *store.Cache, *fakeSender) {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	dbPath := t.TempDir() + "/sms.db"
	st, err := store.Open(dbPath, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cache, err := store.NewCache(st)
	if err != nil {
		t.Fatal(err)
	}
	saveSettings(t, st, store.Settings{
		VerificationToken:   testToken,
		WebhookPath:         testPath,
		DownstreamTimeoutMS: 2000,
		StalePendingMS:      120000,
	})
	sender := &fakeSender{result: &channel.SendResult{Success: true, HTTPCode: 200, VendorID: "vid-1"}}
	return dbPath, st, cache, sender
}

// signNoneConfig 构造一个无签名、可通过试渲染的最小自定义通道配置。
func signNoneConfig() *channel.Config {
	return &channel.Config{
		Request: channel.RequestConfig{
			BaseURL: "http://127.0.0.1:19999", URL: "${base}/send",
			Method: "POST", ContentType: "application/json",
		},
		Constants: map[string]channel.ConstantSpec{
			"fixed": {Value: "v1", Secret: false},
		},
		BodyMappings: []mapping.Mapping{
			{Target: "appSmsId", SourceType: mapping.SourceVariable, Source: "appSmsId", ValueType: mapping.ValueString},
			{Target: "mobile", SourceType: mapping.SourceVariable, Source: "mobile", ValueType: mapping.ValueString},
			{Target: "ts", SourceType: mapping.SourceVariable, Source: "timestamp", ValueType: mapping.ValueNumber},
			{Target: "params", SourceType: mapping.SourceVariable, Source: "params", ValueType: mapping.ValueRaw},
			{Target: "fixed", SourceType: mapping.SourceConst, Source: "fixed", ValueType: mapping.ValueString},
		},
		Sign: channel.SignConfig{Strategy: "none"},
		Response: channel.ResponseConfig{
			SuccessPath: "status", SuccessValue: "0",
			MsgIDPath: "data.id", MessagePath: "message",
		},
		MobilePolicy: channel.MobilePolicyCCPrefix,
		Receipt: channel.ReceiptConfig{
			MsgIDPath: "smsId", AppMsgIDPath: "appSmsId", StatusPath: "status",
			DeliveredValue: "DELIVRD", MessagePath: "statusMessage", SeqNoPath: "seqNo",
		},
	}
}

func rawConfig(t *testing.T, cfg *channel.Config) json.RawMessage {
	t.Helper()
	return jbody(t, cfg)
}

func createChannelReq(name string, cfg *channel.Config, secrets map[string]string) channelRequest {
	raw, _ := json.Marshal(cfg)
	return channelRequest{
		Name:    name,
		Config:  raw,
		Secrets: secrets,
	}
}

func mustCreateChannel(t *testing.T, srv *Server, name string, cfg *channel.Config) channelResponse {
	t.Helper()
	w := adminJSON(t, srv, http.MethodPost, "/api/channels", createChannelReq(name, cfg, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("创建通道 = %d, body=%s", w.Code, w.Body.String())
	}
	var resp channelResponse
	jdecode(t, w, &resp)
	if resp.ID == "" {
		t.Fatal("创建响应缺少 id")
	}
	return resp
}

func TestAdminPresets(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	w := adminJSON(t, srv, http.MethodGet, "/api/presets", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/presets = %d", w.Code)
	}
	var resp struct {
		Presets []presetInfo `json:"presets"`
	}
	jdecode(t, w, &resp)
	if len(resp.Presets) != 1 || resp.Presets[0].ID != channel.PresetBuiltin {
		t.Fatalf("预置列表异常: %+v", resp.Presets)
	}
	if resp.Presets[0].Name == "" {
		t.Fatal("预置名称不能为空")
	}
}

func TestAdminChannelLifecycle(t *testing.T) {
	dbPath, st, cache, sender := newTestEnvAt(t)
	srv := buildServer(t, st, cache, sender)
	cfg := signNoneConfig()

	// 创建：201，密钥视图按声明回显（未设置）。
	body := createChannelReq("自定义通道", cfg, nil)
	body.Description = "测试用"
	w := adminJSON(t, srv, http.MethodPost, "/api/channels", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建 = %d, body=%s", w.Code, w.Body.String())
	}
	var created channelResponse
	jdecode(t, w, &created)
	if !created.Enabled || created.Name != "自定义通道" || created.Description != "测试用" {
		t.Fatalf("创建回显异常: %+v", created)
	}
	if created.ReceiptURL == "" || !strings.HasSuffix(created.ReceiptURL, "/receipts/"+created.ID) {
		t.Fatalf("回执 URL 异常: %q", created.ReceiptURL)
	}

	// 列表与详情。
	w = adminJSON(t, srv, http.MethodGet, "/api/channels", nil)
	var list struct {
		Channels []channelResponse `json:"channels"`
	}
	jdecode(t, w, &list)
	if len(list.Channels) != 1 || list.Channels[0].ID != created.ID {
		t.Fatalf("通道列表异常: %+v", list.Channels)
	}
	w = adminJSON(t, srv, http.MethodGet, "/api/channels/"+created.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("详情 = %d", w.Code)
	}

	// PUT 改名 + 设置密钥：密钥仅回掩码，响应不含明文。
	secret := "s3cr3et-appSecret-99"
	cfg.Constants["appSecret"] = channel.ConstantSpec{Secret: true}
	cfg.Sign = channel.SignConfig{Strategy: "hmac_sha256", Encoding: "hex", SecretConst: "appSecret",
		Segments: []sign.Segment{
			{Kind: sign.SegmentLiteral, Value: "ts="},
			{Kind: sign.SegmentVariable, Value: "timestamp"},
		}}
	upd := channelRequest{Name: "改名通道", Config: rawConfig(t, cfg), Secrets: map[string]string{
		"appSecret": secret,
	}}
	w = adminJSON(t, srv, http.MethodPut, "/api/channels/"+created.ID, upd)
	if w.Code != http.StatusOK {
		t.Fatalf("更新 = %d, body=%s", w.Code, w.Body.String())
	}
	var updated channelResponse
	jdecode(t, w, &updated)
	view, ok := updated.SecretsMasked["appSecret"]
	if !ok || !view.Set || view.Value == "****" {
		t.Fatalf("密钥掩码异常: %+v", updated.SecretsMasked)
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatalf("更新响应泄露密钥明文: %s", w.Body.String())
	}

	// 省略密钥 / 显式空串：原值保留。
	upd2 := channelRequest{Name: "改名通道", Config: rawConfig(t, cfg)}
	w = adminJSON(t, srv, http.MethodPut, "/api/channels/"+created.ID, upd2)
	jdecode(t, w, &updated)
	if !updated.SecretsMasked["appSecret"].Set {
		t.Fatal("省略密钥提交不应清空原值")
	}
	upd2.Secrets = map[string]string{"appSecret": ""}
	w = adminJSON(t, srv, http.MethodPut, "/api/channels/"+created.ID, upd2)
	jdecode(t, w, &updated)
	if !updated.SecretsMasked["appSecret"].Set {
		t.Fatal("空串密钥不应清空原值")
	}

	// DB 文件中 grep 不到明文（AC-9②）。
	if err := st.Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertDBHasNoPlaintext(t, dbPath, secret)

	// 启停。
	w = adminJSON(t, srv, http.MethodPost, "/api/channels/"+created.ID+"/disable", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("停用 = %d, body=%s", w.Code, w.Body.String())
	}
	jdecode(t, w, &updated)
	if updated.Enabled {
		t.Fatal("停用后 enabled 应为 false")
	}
	w = adminJSON(t, srv, http.MethodPost, "/api/channels/"+created.ID+"/enable", nil)
	jdecode(t, w, &updated)
	if !updated.Enabled {
		t.Fatal("启用后 enabled 应为 true")
	}

	// 删除：204，随后详情 404。
	w = adminJSON(t, srv, http.MethodDelete, "/api/channels/"+created.ID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("删除 = %d, body=%s", w.Code, w.Body.String())
	}
	if w = adminJSON(t, srv, http.MethodGet, "/api/channels/"+created.ID, nil); w.Code != http.StatusNotFound {
		t.Fatalf("删除后详情 = %d, 期望 404", w.Code)
	}
}

func TestAdminChannelSaveValidation(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	// 名称为空。
	w := adminJSON(t, srv, http.MethodPost, "/api/channels",
		createChannelReq("", signNoneConfig(), nil))
	if w.Code != http.StatusBadRequest || decodeError(t, w).Field != "name" {
		t.Fatalf("空名称 = %d %+v", w.Code, decodeError(t, w))
	}

	// 外层合法但 config 不是 JSON 对象：field=config（区别于外层坏 JSON 的无 field）。
	w = adminRaw(t, srv, http.MethodPost, "/api/channels",
		[]byte(`{"name":"坏配置","config":"not-an-object"}`))
	if w.Code != http.StatusBadRequest || decodeError(t, w).Field != "config" {
		t.Fatalf("坏配置 = %d %+v", w.Code, decodeError(t, w))
	}

	// 外层整体不是 JSON：400 且不带 field。
	w = adminRaw(t, srv, http.MethodPost, "/api/channels", []byte(`{bad-json`))
	if w.Code != http.StatusBadRequest || decodeError(t, w).Field != "" {
		t.Fatalf("外层坏 JSON = %d %+v", w.Code, decodeError(t, w))
	}

	cases := []struct {
		name   string
		mutate func(cfg *channel.Config)
		field  string
	}{
		{"未知变量名", func(c *channel.Config) {
			c.BodyMappings = append(c.BodyMappings, mapping.Mapping{
				Target: "bogus", SourceType: mapping.SourceVariable, Source: "notAVar",
			})
		}, "config.body_mappings"},
		{"重复 target", func(c *channel.Config) {
			c.BodyMappings = append(c.BodyMappings, mapping.Mapping{
				Target: "mobile", SourceType: mapping.SourceLiteral, Source: "x",
			})
		}, "config.body_mappings"},
		{"number 转换失败", func(c *channel.Config) {
			c.BodyMappings = append(c.BodyMappings, mapping.Mapping{
				Target: "n", SourceType: mapping.SourceLiteral, Source: "abc", ValueType: mapping.ValueNumber,
			})
		}, "config.body_mappings"},
		{"未知签名策略", func(c *channel.Config) {
			c.Sign = channel.SignConfig{Strategy: "mystery"}
		}, "config.sign"},
		{"URL 为空", func(c *channel.Config) {
			c.Request.URL = ""
		}, "config.request.url"},
		{"未知手机号策略", func(c *channel.Config) {
			c.MobilePolicy = "martian"
		}, "config.mobile_policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := signNoneConfig()
			tc.mutate(cfg)
			w := adminJSON(t, srv, http.MethodPost, "/api/channels",
				createChannelReq("校验通道", cfg, nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d, 期望 400, body=%s", w.Code, w.Body.String())
			}
			if e := decodeError(t, w); e.Field != tc.field {
				t.Fatalf("field = %q, 期望 %q（错误: %s）", e.Field, tc.field, e.Message)
			}
		})
	}

	// 提交未声明的密钥名：400 field=secrets。
	cfg := signNoneConfig()
	w = adminJSON(t, srv, http.MethodPost, "/api/channels",
		createChannelReq("密钥通道", cfg, map[string]string{"ghost": "v"}))
	if w.Code != http.StatusBadRequest || decodeError(t, w).Field != "secrets" {
		t.Fatalf("未声明密钥 = %d %+v", w.Code, decodeError(t, w))
	}
}

func TestAdminChannelFromPreset(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	// 未知预置 / 空名称：400 字段级。
	w := adminJSON(t, srv, http.MethodPost, "/api/channels/from-preset",
		map[string]string{"preset": "nope", "name": "x"})
	if w.Code != http.StatusBadRequest || decodeError(t, w).Field != "preset" {
		t.Fatalf("未知预置 = %d %+v", w.Code, decodeError(t, w))
	}
	w = adminJSON(t, srv, http.MethodPost, "/api/channels/from-preset",
		map[string]string{"preset": channel.PresetBuiltin, "name": ""})
	if w.Code != http.StatusBadRequest || decodeError(t, w).Field != "name" {
		t.Fatalf("空名称 = %d %+v", w.Code, decodeError(t, w))
	}

	// 正常从预置建脚手架：密钥声明存在但未设置，保存不被试渲染拦截。
	w = adminJSON(t, srv, http.MethodPost, "/api/channels/from-preset", map[string]string{
		"preset": channel.PresetBuiltin, "name": "示例厂商生产", "description": "d",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("from-preset = %d, body=%s", w.Code, w.Body.String())
	}
	var ch channelResponse
	jdecode(t, w, &ch)
	sec, ok := ch.SecretsMasked["appSecret"]
	if !ok || sec.Set {
		t.Fatalf("预置脚手架密钥视图异常: %+v", ch.SecretsMasked)
	}

	// 补齐密钥后 PUT：试渲染完整通过。
	var cfg channel.Config
	if err := json.Unmarshal(ch.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	w = adminJSON(t, srv, http.MethodPut, "/api/channels/"+ch.ID, channelRequest{
		Name:    "示例厂商生产",
		Config:  ch.Config,
		Secrets: map[string]string{"appSecret": "demo-secret-123456"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("补密钥后更新 = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestAdminChannelTestSend(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)
	ch := mustCreateChannel(t, srv, "测试通道", signNoneConfig())

	payload := testSendRequest{
		TemplateCode: "TPL-CODE", SMSType: "code",
		CountryCode: "+86", MobileNumber: "13800000000", Mobile: "8613800000000",
		Params: []string{"123456"},
	}
	w := adminJSON(t, srv, http.MethodPost, "/api/channels/"+ch.ID+"/test", payload)
	if w.Code != http.StatusOK {
		t.Fatalf("测试发送 = %d, body=%s", w.Code, w.Body.String())
	}
	var res service.TestSendResult
	jdecode(t, w, &res)
	if !res.Success || !strings.HasPrefix(res.AppSmsID, "test-") || res.ProviderMsgID != "vid-1" {
		t.Fatalf("测试发送结果异常: %+v", res)
	}
	if sender.count() != 1 {
		t.Fatalf("真实下发次数 = %d, 期望 1", sender.count())
	}

	// 缺手机号 / 非法 sms_type：400 字段级。
	bad := payload
	bad.MobileNumber = ""
	w = adminJSON(t, srv, http.MethodPost, "/api/channels/"+ch.ID+"/test", bad)
	if w.Code != http.StatusBadRequest || decodeError(t, w).Field != "mobile_number" {
		t.Fatalf("缺手机号 = %d %+v", w.Code, decodeError(t, w))
	}
	bad = payload
	bad.SMSType = "nope"
	w = adminJSON(t, srv, http.MethodPost, "/api/channels/"+ch.ID+"/test", bad)
	if w.Code != http.StatusBadRequest || decodeError(t, w).Field != "sms_type" {
		t.Fatalf("非法 sms_type = %d %+v", w.Code, decodeError(t, w))
	}

	// 通道不存在：404；通道停用：409。
	w = adminJSON(t, srv, http.MethodPost, "/api/channels/missing/test", payload)
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在通道测试发送 = %d, 期望 404", w.Code)
	}
	if w := adminJSON(t, srv, http.MethodPost, "/api/channels/"+ch.ID+"/disable", nil); w.Code != 200 {
		t.Fatalf("停用失败: %d", w.Code)
	}
	w = adminJSON(t, srv, http.MethodPost, "/api/channels/"+ch.ID+"/test", payload)
	if w.Code != http.StatusConflict || decodeError(t, w).Code != "conflict" {
		t.Fatalf("停用通道测试发送 = %d %+v, 期望 409", w.Code, decodeError(t, w))
	}

	// 厂商业务失败仍为 200（测试发送的同步结论），success=false 带分类。
	_ = adminJSON(t, srv, http.MethodPost, "/api/channels/"+ch.ID+"/enable", nil)
	sender.result = &channel.SendResult{Success: false, HTTPCode: 200, Message: "模板不存在"}
	w = adminJSON(t, srv, http.MethodPost, "/api/channels/"+ch.ID+"/test", payload)
	if w.Code != http.StatusOK {
		t.Fatalf("业务失败状态码 = %d", w.Code)
	}
	jdecode(t, w, &res)
	if res.Success || res.ErrorKind == "" {
		t.Fatalf("业务失败结果异常: %+v", res)
	}
}

func TestAdminChannelMissingRoutes(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	if w := adminJSON(t, srv, http.MethodPut, "/api/channels/missing",
		createChannelReq("x", signNoneConfig(), nil)); w.Code != http.StatusNotFound {
		t.Fatalf("PUT 不存在通道 = %d, 期望 404", w.Code)
	}
	if w := adminJSON(t, srv, http.MethodDelete, "/api/channels/missing", nil); w.Code != http.StatusNotFound {
		t.Fatalf("DELETE 不存在通道 = %d, 期望 404", w.Code)
	}
	if w := adminJSON(t, srv, http.MethodPost, "/api/channels/missing/enable", nil); w.Code != http.StatusNotFound {
		t.Fatalf("enable 不存在通道 = %d, 期望 404", w.Code)
	}
}
