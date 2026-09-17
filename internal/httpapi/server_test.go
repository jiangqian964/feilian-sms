// Package httpapi 是网关入站 HTTP 装配层：飞连事件 webhook（路径热匹配）、
// 厂商回执、健康检查、统一错误体与管理端 CIDR 守卫。本文件覆盖 TR-10.1~10.4。
package httpapi

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"feilian-sms/internal/channel"
	"feilian-sms/internal/service"
	"feilian-sms/internal/store"
)

const (
	testToken = "vtok-test"
	testPath  = "/feilian/sms/events"
)

// fakeSender 记录编排层下发次数，可模拟延迟与 context 超时（不发起真实 HTTP）。
type fakeSender struct {
	mu     sync.Mutex
	calls  int
	delay  time.Duration
	result *channel.SendResult
}

func (f *fakeSender) Do(ctx context.Context, _ *channel.Config, _ *channel.RenderResult) (*channel.SendResult, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			f.mu.Lock()
			f.calls++
			f.mu.Unlock()
			return nil, &channel.TransportError{Timeout: true, Err: ctx.Err()}
		}
	}
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.result, nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestEnv(t *testing.T) (*store.Store, *store.Cache, *fakeSender) {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	st, err := store.Open(t.TempDir()+"/sms.db", key)
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
	return st, cache, sender
}

func saveSettings(t *testing.T, st *store.Store, s store.Settings) {
	t.Helper()
	if err := st.UpdateSettings(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func buildServer(t *testing.T, st *store.Store, cache *store.Cache, sender service.Sender, cidrs ...string) *Server {
	t.Helper()
	srv, err := NewServer(Deps{
		Settings:   service.NewSettingsRuntime(cache),
		Forward:    service.NewForwardService(st, cache, sender),
		Receipts:   service.NewReceiptService(st, cache),
		Store:      st,
		AdminCIDRs: cidrs,
		Logger:     zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// do 通过 httptest 直连 handler；remote 显式指定以模拟直连 RemoteAddr。
func do(srv *Server, method, path string, body []byte, remote string, headers map[string]string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if remote != "" {
		req.RemoteAddr = remote
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func challengeBody(token, value string) []byte {
	return []byte(fmt.Sprintf(`{"challenge":%q,"token":%q,"type":"url_verification"}`, value, token))
}

func smsEvent(eventID, token, smsType, mobile string, params []string) []byte {
	rawParams, _ := json.Marshal(params)
	return []byte(fmt.Sprintf(
		`{"schema":"1.0","header":{"event_id":%q,"token":%q,`+
			`"create_time":"1740000000000","event_type":"notify.v1.sms","app_id":"app1"},`+
			`"data":{"events":[{"object":{"country_code":"+86","mobile_number":%q,`+
			`"mobile":"+86%s","sms_type":%q,"template":"code is %%s","params":%s,`+
			`"expired_time":0}}]}}`,
		eventID, token, mobile, mobile, smsType, string(rawParams)))
}

// sealEncrypted 按飞连 AES-256-CBC 规范在测试侧构造加密信封 {"encrypt":"..."}。
func sealEncrypted(t *testing.T, encryptKey string, plain []byte) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	encoded := base64.StdEncoding.EncodeToString(append(iv, ct...))
	body, err := json.Marshal(map[string]string{"encrypt": encoded})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// ---- TR-10.1：challenge/错 token/坏报文/正常事件/错路径/超限 ----

func TestChallengeEchoUnderOneSecond(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)

	start := time.Now()
	w := do(srv, http.MethodPost, testPath, challengeBody(testToken, "ch-abc-123"), "10.0.0.1:5000", nil)
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("challenge 应回 200，实际 %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "ch-abc-123" {
		t.Fatalf("challenge 必须原样回显，实际 %q", w.Body.String())
	}
	if elapsed >= time.Second {
		t.Fatalf("challenge 回显耗时 %v，超过 1 秒要求", elapsed)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("challenge 响应必须 no-store，实际 %q", got)
	}
	if sender.count() != 0 {
		t.Fatal("challenge 握手不得触发任何下发")
	}
}

func TestWrongToken401AndZeroDispatch(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)

	w := do(srv, http.MethodPost, testPath, smsEvent("evt-bad", "wrong-token", "code", "13800000000", []string{"123456"}),
		"10.0.0.1:5000", nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应 401，实际 %d", w.Code)
	}
	if sender.count() != 0 {
		t.Fatalf("token 校验失败必须零下发，实际调用 %d 次", sender.count())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("错误响应也必须 no-store，实际 %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["code"] == "" {
		t.Fatalf("401 必须返回统一错误体，实际 %s", w.Body.String())
	}
}

func TestMalformedBodies400(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)

	cases := map[string][]byte{
		"非 JSON":               []byte("not-json"),
		"缺少 schema":            []byte(`{"header":{},"data":{"events":[]}}`),
		"加密信封但未配置 Encrypt Key": []byte(`{"encrypt":"YWJjZA=="}`),
	}
	for name, body := range cases {
		w := do(srv, http.MethodPost, testPath, body, "10.0.0.1:5000", nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s：应 400，实际 %d（%s）", name, w.Code, w.Body.String())
		}
	}
}

func TestValidEventAlways200(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)

	// code 场景未绑定任何通道：编排层降级留痕，但入站层仍恒回 200。
	w := do(srv, http.MethodPost, testPath,
		smsEvent("evt-ok-1", testToken, "code", "13800000000", []string{"123456"}),
		"10.0.0.1:5000", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("正常事件必须恒回 200，实际 %d: %s", w.Code, w.Body.String())
	}
	if sender.count() != 0 {
		t.Fatal("未绑定场景不应下发到通道")
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("事件响应必须 no-store，实际 %q", got)
	}
}

func TestUnknownWebhookPath404(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)

	w := do(srv, http.MethodPost, "/somewhere/else",
		smsEvent("evt-x", testToken, "code", "13800000000", nil), "10.0.0.1:5000", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("非订阅路径应 404，实际 %d", w.Code)
	}
}

func TestWebhookBodyTooLarge413(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)

	huge := bytes.Repeat([]byte("a"), (1<<20)+1024)
	w := do(srv, http.MethodPost, testPath, huge, "10.0.0.1:5000", nil)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超过 1MiB 应 413，实际 %d", w.Code)
	}
}

func TestEncryptKeyRejectsPlaintextAndAcceptsSealedChallenge(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)

	s := cache.Current().Settings
	s.EncryptKey = "enc-key-0123456789"
	saveSettings(t, st, s)

	// 配置了 Encrypt Key 后，明文信封一律拒绝。
	w := do(srv, http.MethodPost, testPath, challengeBody(testToken, "ch-plain"), "10.0.0.1:5000", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("配置 key 后的明文信封应 400，实际 %d", w.Code)
	}

	// 合法加密信封：先解密再走 challenge 回显。
	sealed := sealEncrypted(t, "enc-key-0123456789", challengeBody(testToken, "ch-secret-9"))
	w = do(srv, http.MethodPost, testPath, sealed, "10.0.0.1:5000", nil)
	if w.Code != http.StatusOK || w.Body.String() != "ch-secret-9" {
		t.Fatalf("加密 challenge 应解密并原样回显，实际 %d: %s", w.Code, w.Body.String())
	}
}

func TestNonSMSEvent200WithoutDispatch(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)

	body := []byte(`{"schema":"1.0","header":{"event_id":"evt-other","token":"` + testToken +
		`","event_type":"device.v1.thing"},"data":{"events":[{"object":{"foo":1}}]}}`)
	w := do(srv, http.MethodPost, testPath, body, "10.0.0.1:5000", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("非短信事件记日志后应 200，实际 %d: %s", w.Code, w.Body.String())
	}
	if sender.count() != 0 {
		t.Fatal("非短信事件不得触发下发")
	}

	// 回归 #5：未通过 token 校验的非短信事件必须先被 401 拒绝，
	// 不能借「非短信事件 200」分支探测 webhook 有效性。
	bad := bytes.ReplaceAll(body, []byte(`"`+testToken+`"`), []byte(`"wrong-token"`))
	if w := do(srv, http.MethodPost, testPath, bad, "10.0.0.1:5000", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("非短信事件错 token 应先被 401，实际 %d", w.Code)
	}
	if sender.count() != 0 {
		t.Fatal("鉴权失败不得触发下发")
	}
}

// ---- TR-10.2：webhook 路径与 token 同进程热切换 ----

func TestWebhookPathAndTokenHotReload(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	srv := buildServer(t, st, cache, sender)

	// 旧路径/旧 token 正常。
	if w := do(srv, http.MethodPost, testPath, challengeBody(testToken, "ch-old"), "10.0.0.1:5000", nil); w.Code != 200 {
		t.Fatalf("旧路径应 200，实际 %d", w.Code)
	}

	s := cache.Current().Settings
	s.WebhookPath = "/new/hook"
	s.VerificationToken = "vtok-new"
	saveSettings(t, st, s)

	// 旧路径立即 404（不重启）。
	if w := do(srv, http.MethodPost, testPath, challengeBody("vtok-new", "ch-old"), "10.0.0.1:5000", nil); w.Code != http.StatusNotFound {
		t.Fatalf("热改后旧路径应 404，实际 %d", w.Code)
	}
	// 新路径 + 旧 token 立即 401。
	if w := do(srv, http.MethodPost, "/new/hook", smsEvent("evt-h", testToken, "code", "13800000000", nil),
		"10.0.0.1:5000", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("新路径旧 token 应 401，实际 %d", w.Code)
	}
	// 新路径 + 新 token 正常。
	if w := do(srv, http.MethodPost, "/new/hook", challengeBody("vtok-new", "ch-new"), "10.0.0.1:5000", nil); w.Code != 200 ||
		w.Body.String() != "ch-new" {
		t.Fatalf("新路径新 token 应 200 且回显，实际 %d: %s", w.Code, w.Body.String())
	}
}

// ---- TR-10.3：3 秒预算、2 秒超时仍 200、health no-store ----

func channelWithBinding(t *testing.T, st *store.Store) {
	t.Helper()
	cfg := channel.BuiltinPresetConfig()
	cfg.Request.BaseURL = ""
	cfg.Request.URL = "http://vendor.test/sms/send"
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := st.CreateChannel(context.Background(), "demo", "预置", string(raw),
		map[string]string{"appSecret": "demo-app-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceBindings(context.Background(), []store.Binding{{
		SMSType: "code", ChannelID: ch.ID, TemplateCode: "TPL_CODE", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestDownstream1500msWithinBudget(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	sender.delay = 1500 * time.Millisecond
	channelWithBinding(t, st)
	srv := buildServer(t, st, cache, sender)

	start := time.Now()
	w := do(srv, http.MethodPost, testPath,
		smsEvent("evt-slow-1", testToken, "code", "13800000000", []string{"123456"}),
		"10.0.0.1:5000", nil)
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("下游慢响应时 webhook 仍恒 200，实际 %d", w.Code)
	}
	if sender.count() != 1 {
		t.Fatalf("应恰好下发 1 次，实际 %d", sender.count())
	}
	if elapsed >= 3*time.Second {
		t.Fatalf("下游 1.5s 时总耗时 %v 超过 3 秒预算", elapsed)
	}
}

func TestDownstream3sTimesOutAt2sStill200(t *testing.T) {
	st, cache, sender := newTestEnv(t)
	sender.delay = 3 * time.Second
	channelWithBinding(t, st)
	srv := buildServer(t, st, cache, sender)

	start := time.Now()
	w := do(srv, http.MethodPost, testPath,
		smsEvent("evt-slow-2", testToken, "code", "13800000000", []string{"123456"}),
		"10.0.0.1:5000", nil)
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("下游超时时 webhook 仍必须 200（失败只留痕），实际 %d", w.Code)
	}
	if elapsed < 1800*time.Millisecond {
		t.Fatalf("应在约 2s 超时处返回，实际仅 %v，疑似超时未生效", elapsed)
	}
	if elapsed >= 3*time.Second {
		t.Fatalf("2s 超时后总耗时 %v 仍突破 3 秒预算", elapsed)
	}
}
