// TR-11.2 发送记录：分页边界（默认 50/上限 200/offset 翻页/双字段排序）、
// 筛选组合（status/sms_type/channel_id/source/from/to）、详情 200/404、非法 query 400 带 field。
package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"feilian-sms/internal/store"
)

// mustInsertPending 直接经 store 幂等闸门插入一条 pending 记录（夹具）。
func mustInsertPending(t *testing.T, st *store.Store, rec store.SendRecord) {
	t.Helper()
	inserted, _, err := st.InsertPendingIfAbsent(context.Background(), rec)
	if err != nil {
		t.Fatalf("插入发送记录 %s 失败: %v", rec.AppSmsID, err)
	}
	if !inserted {
		t.Fatalf("发送记录 %s 已存在，夹具数据冲突", rec.AppSmsID)
	}
}

// recordIDs 按顺序提取分页结果中的 app_sms_id，便于排序/筛选断言。
func recordIDs(t *testing.T, resp recordsResponse) []string {
	t.Helper()
	ids := make([]string, 0, len(resp.Records))
	for _, r := range resp.Records {
		ids = append(ids, r.AppSmsID)
	}
	return ids
}

func assertIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("记录 ID 序列 = %v, 期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("记录 ID 序列 = %v, 期望 %v", got, want)
		}
	}
}

func TestAdminListRecords_Empty(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	w := adminJSON(t, srv, http.MethodGet, "/api/records", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/records = %d, body=%s", w.Code, w.Body.String())
	}
	assertNoStore(t, w)
	var resp recordsResponse
	jdecode(t, w, &resp)
	if resp.Records == nil || len(resp.Records) != 0 {
		t.Fatalf("空列表应回显为空数组，实际 %#v", resp.Records)
	}
	if resp.Total != 0 || resp.Limit != 50 || resp.Offset != 0 {
		t.Fatalf("空列表分页元信息异常: %+v", resp)
	}
}

func TestAdminListRecords_PagingAndOrder(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	// created_at 100..500 五条，app_sms_id evt-a..evt-e。
	for i, ms := range []int64{100, 200, 300, 400, 500} {
		mustInsertPending(t, st, store.SendRecord{
			AppSmsID:     fmt.Sprintf("evt-%c", 'a'+rune(i)),
			Source:       store.SourceFeilian,
			ChannelID:    "ch-1",
			SMSType:      "code",
			MobileMasked: "138****0000",
			CreatedAt:    ms,
			UpdatedAt:    ms,
		})
	}

	// 默认分页：created_at DESC 全量、limit 回显 50。
	w := adminJSON(t, srv, http.MethodGet, "/api/records", nil)
	var resp recordsResponse
	jdecode(t, w, &resp)
	if resp.Total != 5 || resp.Limit != 50 || resp.Offset != 0 {
		t.Fatalf("默认分页元信息异常: %+v", resp)
	}
	assertIDs(t, recordIDs(t, resp), []string{"evt-e", "evt-d", "evt-c", "evt-b", "evt-a"})

	// limit/offset 翻页边界。
	cases := []struct {
		name       string
		query      string
		wantIDs    []string
		wantLimit  int
		wantOffset int
	}{
		{"首页 2 条", "/api/records?limit=2&offset=0", []string{"evt-e", "evt-d"}, 2, 0},
		{"次页 2 条", "/api/records?limit=2&offset=2", []string{"evt-c", "evt-b"}, 2, 2},
		{"尾页 1 条", "/api/records?limit=2&offset=4", []string{"evt-a"}, 2, 4},
		{"越界空页", "/api/records?limit=2&offset=5", []string{}, 2, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := adminJSON(t, srv, http.MethodGet, tc.query, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
			}
			var p recordsResponse
			jdecode(t, w, &p)
			if p.Total != 5 || p.Limit != tc.wantLimit || p.Offset != tc.wantOffset {
				t.Fatalf("分页元信息异常: %+v", p)
			}
			assertIDs(t, recordIDs(t, p), tc.wantIDs)
		})
	}
}

func TestAdminListRecords_TieBreakAppSmsID(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	mustInsertPending(t, st, store.SendRecord{AppSmsID: "rec-alpha", CreatedAt: 42, UpdatedAt: 42})
	mustInsertPending(t, st, store.SendRecord{AppSmsID: "rec-zulu", CreatedAt: 42, UpdatedAt: 42})

	w := adminJSON(t, srv, http.MethodGet, "/api/records", nil)
	var resp recordsResponse
	jdecode(t, w, &resp)
	assertIDs(t, recordIDs(t, resp), []string{"rec-zulu", "rec-alpha"})
}

func TestAdminListRecords_LimitCap(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	for i := 0; i < 201; i++ {
		ms := int64(1000 + i)
		mustInsertPending(t, st, store.SendRecord{
			AppSmsID:  fmt.Sprintf("cap-%03d", i),
			CreatedAt: ms,
			UpdatedAt: ms,
		})
	}

	// limit 超过 200：服务端归一化为 200，响应回显有效值。
	w := adminJSON(t, srv, http.MethodGet, "/api/records?limit=500", nil)
	var resp recordsResponse
	jdecode(t, w, &resp)
	if resp.Total != 201 || resp.Limit != 200 || len(resp.Records) != 200 {
		t.Fatalf("上限截断异常: total=%d limit=%d len=%d", resp.Total, resp.Limit, len(resp.Records))
	}
	if resp.Records[0].AppSmsID != "cap-200" {
		t.Fatalf("首页首条应为最新记录，实际 %s", resp.Records[0].AppSmsID)
	}

	// offset=200 取到最后一条。
	w = adminJSON(t, srv, http.MethodGet, "/api/records?limit=200&offset=200", nil)
	jdecode(t, w, &resp)
	if len(resp.Records) != 1 || resp.Records[0].AppSmsID != "cap-000" {
		t.Fatalf("翻页到末尾异常: %+v", resp.Records)
	}
}

func TestAdminListRecords_Filters(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)
	ctx := context.Background()

	// evt-1: code/ch-1/feilian/pending/100；evt-2: alert/ch-2/feilian/success/200；
	// evt-3: code/ch-1/test/failed/300。
	mustInsertPending(t, st, store.SendRecord{AppSmsID: "evt-1", Source: store.SourceFeilian, ChannelID: "ch-1", SMSType: "code", CreatedAt: 100, UpdatedAt: 100})
	mustInsertPending(t, st, store.SendRecord{AppSmsID: "evt-2", Source: store.SourceFeilian, ChannelID: "ch-2", SMSType: "alert", CreatedAt: 200, UpdatedAt: 200})
	mustInsertPending(t, st, store.SendRecord{AppSmsID: "evt-3", Source: store.SourceTest, ChannelID: "ch-1", SMSType: "code", CreatedAt: 300, UpdatedAt: 300})
	if err := st.MarkSuccess(ctx, "evt-2", store.MarkSuccess{NowMS: 201, ProviderStatus: "DELIVRD", LatencyMS: 42}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkFailed(ctx, "evt-3", store.MarkFailed{NowMS: 301, ErrorKind: store.ErrorKindVendor}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		query     string
		wantIDs   []string
		wantTotal int64
	}{
		{"status=success", "/api/records?status=success", []string{"evt-2"}, 1},
		{"status=failed", "/api/records?status=failed", []string{"evt-3"}, 1},
		{"status=pending", "/api/records?status=pending", []string{"evt-1"}, 1},
		{"sms_type=code", "/api/records?sms_type=code", []string{"evt-3", "evt-1"}, 2},
		{"channel_id=ch-1", "/api/records?channel_id=ch-1", []string{"evt-3", "evt-1"}, 2},
		{"source=test", "/api/records?source=test", []string{"evt-3"}, 1},
		{"source=feilian", "/api/records?source=feilian", []string{"evt-2", "evt-1"}, 2},
		{"from=200（含边界）", "/api/records?from=200", []string{"evt-3", "evt-2"}, 2},
		{"to=200（含边界）", "/api/records?to=200", []string{"evt-2", "evt-1"}, 2},
		{"from+to 窗口", "/api/records?from=150&to=250", []string{"evt-2"}, 1},
		{"全条件组合", "/api/records?status=failed&sms_type=code&channel_id=ch-1&source=test&from=250&to=350", []string{"evt-3"}, 1},
		{"互斥组合无结果", "/api/records?status=success&sms_type=code", []string{}, 0},
		{"未知通道无结果", "/api/records?channel_id=ghost", []string{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := adminJSON(t, srv, http.MethodGet, tc.query, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
			}
			var p recordsResponse
			jdecode(t, w, &p)
			if p.Total != tc.wantTotal {
				t.Fatalf("total = %d, 期望 %d（%s）", p.Total, tc.wantTotal, tc.query)
			}
			assertIDs(t, recordIDs(t, p), tc.wantIDs)
		})
	}
}

func TestAdminGetRecord(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	mustInsertPending(t, st, store.SendRecord{
		AppSmsID:     "evt-detail",
		EventID:      "feilian-event-1",
		Source:       store.SourceFeilian,
		ChannelID:    "ch-9",
		SMSType:      "code",
		MobileMasked: "138****0000",
		ParamsMasked: `["12***"]`,
		TemplateCode: "TPL-CODE",
		CreatedAt:    123,
		UpdatedAt:    123,
	})

	w := adminJSON(t, srv, http.MethodGet, "/api/records/evt-detail", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET 详情 = %d, body=%s", w.Code, w.Body.String())
	}
	assertNoStore(t, w)
	var rec store.SendRecord
	jdecode(t, w, &rec)
	if rec.AppSmsID != "evt-detail" || rec.EventID != "feilian-event-1" ||
		rec.Status != store.StatusPending || rec.MobileMasked != "138****0000" ||
		rec.TemplateCode != "TPL-CODE" || rec.CreatedAt != 123 {
		t.Fatalf("详情字段异常: %+v", rec)
	}

	// 不存在：统一 404 错误体。
	w = adminJSON(t, srv, http.MethodGet, "/api/records/missing", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET 缺失记录 = %d, 期望 404", w.Code)
	}
	assertNoStore(t, w)
	if e := decodeError(t, w); e.Code != codeNotFound {
		t.Fatalf("404 错误体 = %+v", e)
	}
}

func TestAdminListRecords_InvalidQuery(t *testing.T) {
	st, cache, _ := newTestEnv(t)
	srv := buildServer(t, st, cache, nil)

	cases := []struct {
		name  string
		query string
		field string
	}{
		{"limit 非数字", "/api/records?limit=abc", "limit"},
		{"limit 为零", "/api/records?limit=0", "limit"},
		{"limit 负数", "/api/records?limit=-3", "limit"},
		{"offset 非数字", "/api/records?offset=x", "offset"},
		{"offset 负数", "/api/records?offset=-1", "offset"},
		{"from 非数字", "/api/records?from=soon", "from"},
		{"from 负数", "/api/records?from=-1", "from"},
		{"to 非数字", "/api/records?to=x", "to"},
		{"to 负数", "/api/records?to=-2", "to"},
		{"非法 status", "/api/records?status=done", "status"},
		{"非法 sms_type", "/api/records?sms_type=nope", "sms_type"},
		{"非法 source", "/api/records?source=email", "source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := adminJSON(t, srv, http.MethodGet, tc.query, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d, 期望 400, body=%s", w.Code, w.Body.String())
			}
			assertNoStore(t, w)
			e := decodeError(t, w)
			if e.Code != codeBadRequest || e.Field != tc.field {
				t.Fatalf("错误体 = %+v, 期望 field=%q", e, tc.field)
			}
		})
	}
}
