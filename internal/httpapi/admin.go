// 管理 API（/api）装配：系统设置、预置、通道 CRUD/启停/测试发送、
// 场景绑定、发送记录。所有路由挂在 admin 子 mux 上；
// 统一结构化错误体、统一 no-store（见 respond.go）。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"feilian-sms/internal/store"
)

// registerAdminRoutes 注册 /api 下的全部管理路由。
// 子 mux 已在根层 StripPrefix("/api") 挂载，故此处模式相对 /api 根书写，
// {id} 等路径变量经 r.PathValue 读取。
func (s *Server) registerAdminRoutes() {
	m := s.admin

	m.HandleFunc("GET /health", s.handleAdminHealth)

	m.HandleFunc("GET /settings", s.getSettings)
	m.HandleFunc("PUT /settings", s.putSettings)

	m.HandleFunc("GET /presets", s.listPresets)

	m.HandleFunc("GET /channels", s.listChannels)
	m.HandleFunc("POST /channels", s.createChannel)
	// 注意：from-preset 为两段字面路径，与三段的 /channels/{id}/* 不冲突；
	// 且仅 POST，与 GET /channels/{id} 方法维度互斥。
	m.HandleFunc("POST /channels/from-preset", s.createChannelFromPreset)
	m.HandleFunc("GET /channels/{id}", s.getChannel)
	m.HandleFunc("PUT /channels/{id}", s.updateChannel)
	m.HandleFunc("DELETE /channels/{id}", s.deleteChannel)
	m.HandleFunc("POST /channels/{id}/enable", s.enableChannel)
	m.HandleFunc("POST /channels/{id}/disable", s.disableChannel)
	m.HandleFunc("POST /channels/{id}/test", s.testChannel)

	m.HandleFunc("GET /bindings", s.listBindings)
	m.HandleFunc("PUT /bindings", s.replaceBindings)

	m.HandleFunc("GET /records", s.listRecords)
	m.HandleFunc("GET /records/{app_sms_id}", s.getRecord)

	// 未命中任何管理路由时返回统一 JSON 404（覆盖 ServeMux 默认纯文本）。
	m.HandleFunc("/", s.adminNotFound)
}

// secretView 是密钥对外回显形态：掩码值 + 是否已设置。
type secretView struct {
	Value string `json:"value"`
	Set   bool   `json:"set"`
}

// handleAdminHealth 管理面存活探针（与公网 /health 同载荷）。
func (s *Server) handleAdminHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

// adminNotFound 是管理子 mux 的兜底 404。
func (s *Server) adminNotFound(w http.ResponseWriter, _ *http.Request) {
	writeErrorJSON(w, http.StatusNotFound, codeNotFound, "接口不存在")
}

// decodeAdminBody 以 1MiB 上限读取并解析 JSON 请求体；失败已直接写响应。
func decodeAdminBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	raw, ok := readLimitedBody(w, r)
	if !ok {
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, codeBadRequest, "请求体不是合法 JSON")
		return false
	}
	return true
}

// isStoreNotFound 统一判定 store 层 ErrNotFound。
func isStoreNotFound(err error) bool {
	return errors.Is(err, store.ErrNotFound)
}
