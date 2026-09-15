// mockvendor 是 headless 全链路冒烟使用的本地厂商短信桩：
//   - POST /sms/send 按通用 JSON 报文形态回成功/业务失败/HTTP 失败；
//   - POST /__control 动态切换模式，供冒烟覆盖厂商失败分支；
//   - 可选把每个请求体顺序落盘（req-001.json … + last.json），便于脚本断言
//     网关真实下发的报文（appSmsId/签名/参数顺序）。
//
// 仅用于测试，不参与生产部署。
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	modeSuccess       = "success"        // HTTP 200 + status=0
	modeBusinessError = "business_error" // HTTP 200 + status=非0（厂商业务拒绝）
	modeHTTPError     = "http_error"     // HTTP 500（基础设施失败）
)

// vendorStub 是线程安全的桩处理器；模式与序号由互斥锁保护。
type vendorStub struct {
	mu          sync.Mutex
	seq         int64
	mode        string
	failCode    int
	failMessage string
	dumpDir     string
	now         func() int64
}

type sendRequest struct {
	AppSmsID string `json:"appSmsId"`
}

type controlRequest struct {
	Mode        string `json:"mode"`
	FailCode    int    `json:"fail_code"`
	FailMessage string `json:"fail_message"`
}

func (v *vendorStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	case r.Method == http.MethodPost && r.URL.Path == "/__control":
		v.handleControl(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/sms/send":
		v.handleSend(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (v *vendorStub) handleControl(w http.ResponseWriter, r *http.Request) {
	var req controlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeStubError(w, http.StatusBadRequest, "控制报文不是合法 JSON")
		return
	}
	if !validModes[req.Mode] {
		writeStubError(w, http.StatusBadRequest,
			fmt.Sprintf("未知模式 %q（可选 success/business_error/http_error）", req.Mode))
		return
	}

	v.mu.Lock()
	v.mode = req.Mode
	if req.FailCode != 0 {
		v.failCode = req.FailCode
	}
	if req.FailMessage != "" {
		v.failMessage = req.FailMessage
	}
	mode := v.mode
	v.mu.Unlock()

	writeStubJSON(w, http.StatusOK, map[string]any{"mode": mode})
}

func (v *vendorStub) handleSend(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeStubError(w, http.StatusBadRequest, "读取请求体失败")
		return
	}

	var req sendRequest
	_ = json.Unmarshal(body, &req) // 报文损坏不影响桩行为，appSmsId 留空回显

	v.mu.Lock()
	v.seq++
	seq := v.seq
	mode := v.mode
	failCode := v.failCode
	failMessage := v.failMessage
	dumpDir := v.dumpDir
	nowMS := v.nowMS()
	v.mu.Unlock()

	if dumpDir != "" {
		if err := dumpRequest(dumpDir, seq, body); err != nil {
			fmt.Fprintf(os.Stderr, "mockvendor 落盘失败: %v\n", err)
		}
	}

	switch mode {
	case modeHTTPError:
		logLine(seq, mode, req.AppSmsID)
		writeStubError(w, http.StatusInternalServerError, "模拟 HTTP 500")
	case modeBusinessError:
		logLine(seq, mode, req.AppSmsID)
		writeStubJSON(w, http.StatusOK, map[string]any{
			"status":  failCode,
			"message": failMessage,
			"data":    nil,
		})
	default:
		logLine(seq, modeSuccess, req.AppSmsID)
		writeStubJSON(w, http.StatusOK, map[string]any{
			"status":  0,
			"message": "success",
			"data": map[string]any{
				"id":       fmt.Sprintf("mock-%d-%d", nowMS, seq),
				"appSmsId": req.AppSmsID,
			},
		})
	}
}

func (v *vendorStub) nowMS() int64 {
	if v.now != nil {
		return v.now()
	}
	return time.Now().UnixMilli()
}

var validModes = map[string]bool{
	modeSuccess:       true,
	modeBusinessError: true,
	modeHTTPError:     true,
}

// dumpRequest 把请求体同时写为序号文件与 last.json，供冒烟脚本断言。
func dumpRequest(dir string, seq int64, body []byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	seqName := filepath.Join(dir, fmt.Sprintf("req-%03d.json", seq))
	if err := os.WriteFile(seqName, body, 0o640); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "last.json"), body, 0o640)
}

func logLine(seq int64, mode, appSmsID string) {
	line, _ := json.Marshal(map[string]any{
		"component": "mockvendor", "seq": seq, "mode": mode, "app_sms_id": appSmsID,
	})
	fmt.Println(string(line))
}

func writeStubJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeStubError(w http.ResponseWriter, code int, message string) {
	writeStubJSON(w, code, map[string]any{"status": -1, "message": message, "data": nil})
}
