package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestLogDescribesOperationWithoutURLValues(t *testing.T) {
	a := newTestApp(t)
	path := "/api/sites/manage/fixture-private-id/resources?passkey=fixture-private-query"
	w := callTest(t, a.Handler(), "POST", path, "", nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected authentication rejection, got %d", w.Code)
	}
	flushTestLogs(t, a)
	raw, err := os.ReadFile(filepath.Join(a.Dir, "logs", "app.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "fixture-private-") {
		t.Fatal("request path or query values entered the spool")
	}
	page, err := a.queryLogs(context.Background(), map[string]string{"module": "http.sites", "q": "搜索站点资源"})
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("expected one searchable site request, err=%v count=%d", err, len(page.Entries))
	}
	e := page.Entries[0]
	if e["message"] != "搜索站点资源：需要登录认证" || e["error_code"] != "UNAUTHORIZED" || e["http_status"] != float64(401) {
		t.Fatalf("missing request result: %v", e)
	}
}

func TestLegacyExternalAndPerformanceLogsHaveSearchableActions(t *testing.T) {
	a := newTestApp(t)
	flushTestLogs(t, a)
	f, err := os.Create(filepath.Join(a.Dir, "logs", "app-00000000000000000001-test.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	stamp := now()
	for _, entry := range []map[string]any{
		{"time": stamp, "module": "external.downloader", "event": "http_completed", "message": "外部接口请求", "operation": "qb_torrents", "error_code": "transport_error", "http_status": 0},
		{"time": stamp, "module": "performance", "event": "slow_phase", "operation": "settings_save", "message": "writer_wait", "duration_ms": 789},
	} {
		if err := json.NewEncoder(f).Encode(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, expect string }{
		{"qBittorrent", "查询 qBittorrent 种子列表：网络传输失败"},
		{"等待数据库写入", "保存系统设置：等待数据库写入耗时较长"},
	} {
		page, err := a.queryLogs(context.Background(), map[string]string{"q": tc.query})
		if err != nil || len(page.Entries) != 1 || page.Entries[0]["message"] != tc.expect {
			t.Fatalf("query %q did not find a readable record: %v %v", tc.query, page.Entries, err)
		}
	}
}
