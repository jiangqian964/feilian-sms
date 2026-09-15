package httpapi

import (
	"encoding/json"
	"net/http"
)

// 统一错误码（与管理 API 共用同一套词汇表）。
const (
	codeBadRequest      = "bad_request"
	codeUnauthorized    = "unauthorized"
	codeForbidden       = "forbidden"
	codeNotFound        = "not_found"
	codeConflict        = "conflict"
	codePayloadTooLarge = "payload_too_large"
	codeInternal        = "internal_error"
)

const (
	headerNoStore   = "no-store"
	jsonContentType = "application/json; charset=utf-8"
)

// errorBody 是统一结构化错误响应：{"code","message","field"?}。
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// writeErrorJSON 以统一错误体响应；所有错误响应同样禁止缓存/嗅探。
func writeErrorJSON(w http.ResponseWriter, status int, code, message string) {
	writeJSONStatus(w, status, errorBody{Code: code, Message: message})
}

// writeFieldErrorJSON 以带字段名的结构化错误响应（表单保存前校验用）。
func writeFieldErrorJSON(w http.ResponseWriter, status int, code, field, message string) {
	writeJSONStatus(w, status, errorBody{Code: code, Message: message, Field: field})
}

// writeNoContent 写出无响应体的成功状态（如 DELETE）。
func writeNoContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", headerNoStore)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNoContent)
}

// writeJSON 以 200 写出 JSON 响应。
func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", jsonContentType)
	w.Header().Set("Cache-Control", headerNoStore)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
