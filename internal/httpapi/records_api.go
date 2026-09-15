package httpapi

import (
	"net/http"
	"strconv"

	"feilian-sms/internal/feilian"
	"feilian-sms/internal/store"
)

// 发送记录分页参数（与 store.ListSends 的归一化保持一致）。
const (
	defaultRecordsLimit = 50
	maxRecordsLimit     = 200
)

// recordsResponse 是发送记录分页响应：当前页记录 + 满足筛选条件的总数 + 生效分页参数。
type recordsResponse struct {
	Records []store.SendRecord `json:"records"`
	Total   int64              `json:"total"`
	Limit   int                `json:"limit"`
	Offset  int                `json:"offset"`
}

// listRecords 处理 GET /records：状态/场景/通道/来源/时间窗筛选 + 分页。
// 所有非法 query 参数以 400 + field 形式拒绝；limit 缺省 50、超过 200 截断为 200。
func (s *Server) listRecords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := defaultRecordsLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "limit", "limit 必须为正整数")
			return
		}
		if n > maxRecordsLimit {
			n = maxRecordsLimit
		}
		limit = n
	}

	offset := 0
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "offset", "offset 必须为非负整数")
			return
		}
		offset = n
	}

	fromMS, ok := parseNonNegMSQuery(w, r, "from")
	if !ok {
		return
	}
	toMS, ok := parseNonNegMSQuery(w, r, "to")
	if !ok {
		return
	}

	status := q.Get("status")
	if status != "" &&
		status != string(store.StatusPending) &&
		status != string(store.StatusSuccess) &&
		status != string(store.StatusFailed) {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "status",
			"status 仅支持 pending、success、failed")
		return
	}

	smsType := q.Get("sms_type")
	if smsType != "" && !feilian.IsKnownSMSType(smsType) {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "sms_type", "sms_type 不是受支持的短信场景")
		return
	}

	source := q.Get("source")
	if source != "" && source != store.SourceFeilian && source != store.SourceTest {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "source",
			"source 仅支持 feilian、test")
		return
	}

	rows, total, err := s.deps.Store.ListSends(r.Context(), store.SendFilter{
		Status:    status,
		SMSType:   smsType,
		ChannelID: q.Get("channel_id"),
		Source:    source,
		FromMS:    fromMS,
		ToMS:      toMS,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		s.failInternal(w, r, "查询发送记录失败", err)
		return
	}
	if rows == nil {
		rows = []store.SendRecord{} // 空结果回显 [] 而非 null
	}
	writeJSON(w, recordsResponse{Records: rows, Total: total, Limit: limit, Offset: offset})
}

// parseNonNegMSQuery 解析非负毫秒时间戳 query（缺省=0 表示不限制）；
// 非法值已直接写出 400 field 错误，返回 ok=false。
func parseNonNegMSQuery(w http.ResponseWriter, r *http.Request, field string) (int64, bool) {
	raw := r.URL.Query().Get(field)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, field, field+" 必须为非负毫秒时间戳")
		return 0, false
	}
	return n, true
}

// getRecord 处理 GET /records/{app_sms_id}：按业务幂等键查询单条发送全生命周期留痕。
func (s *Server) getRecord(w http.ResponseWriter, r *http.Request) {
	rec, err := s.deps.Store.GetSend(r.Context(), r.PathValue("app_sms_id"))
	if err != nil {
		if isStoreNotFound(err) {
			writeErrorJSON(w, http.StatusNotFound, codeNotFound, "发送记录不存在")
			return
		}
		s.failInternal(w, r, "查询发送记录失败", err)
		return
	}
	writeJSON(w, rec)
}
