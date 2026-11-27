package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func assertNotificationQueryPlans(t *testing.T, s *Store) {
	t.Helper()
	for _, tc := range []struct {
		query, index string
		args         []any
	}{
		{notificationListSQL, "notification_read_time_uid", []any{100}},
		{notificationUnreadSQL, "notification_read_time_uid", nil},
	} {
		rows, err := s.reader().QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+tc.query, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan.String(), tc.index) || strings.Contains(plan.String(), "TEMP B-TREE") {
			t.Fatalf("unexpected notification plan: %s", plan.String())
		}
	}
}
func TestNotificationIndexesUpgradeEncryptedStoreAndReadState(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = s.DB.ExecContext(ctx, `DROP INDEX notification_read_time_uid;
 CREATE INDEX notification_time_uid ON message(updated_at DESC,uid) WHERE record_kind='notification';
 DROP INDEX notification_unread; INSERT INTO plugin_record VALUES('encrypted-fixture','main',x'FFFEFD','2020')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"b", "a"} {
		if err = s.Put(ctx, "notification", id, Notification{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE message SET updated_at='2026' WHERE 1=1`); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal("index upgrade rejected unrelated encrypted payload:", err)
	}
	defer s.Close()
	assertNotificationQueryPlans(t, s)
	a := &App{Store: s}
	raw, err := a.notifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(map[string]any)
	if result["unread"] != 2 {
		t.Fatal(result)
	}
	var first Notification
	if err = json.Unmarshal(result["items"].([]json.RawMessage)[0], &first); err != nil || first.ID != "a" {
		t.Fatalf("tie ordering changed: %+v %v", first, err)
	}
	if _, err = a.notificationAPI(httptest.NewRequest("POST", "/api/notifications/a/read", nil), "a/read"); err != nil {
		t.Fatal(err)
	}
	raw, err = a.notifications(ctx)
	if err != nil || raw.(map[string]any)["unread"] != 1 {
		t.Fatalf("read index stale: %v %v", raw, err)
	}
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM message WHERE uid='b'`); err != nil {
		t.Fatal(err)
	}
	raw, err = a.notifications(ctx)
	if err != nil || raw.(map[string]any)["unread"] != 0 {
		t.Fatalf("delete index stale: %v %v", raw, err)
	}
}

func TestNotificationListIncludesOldUnreadBeforeRecentReadHistory(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	if _, err := a.Store.DB.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<104)
 INSERT INTO message(record_kind,uid,payload,updated_at)
 SELECT 'notification','n'||x,json_object('id','n'||x,'read',json(CASE WHEN x=1 THEN 'false' ELSE 'true' END)),printf('%03d',x) FROM n`); err != nil {
		t.Fatal(err)
	}
	assertNotificationQueryPlans(t, a.Store)
	for _, wantUnread := range []int{1, 0} {
		res := callTest(t, a.Handler(), "GET", "/api/notifications", "", cookie, "")
		var page struct {
			Data struct {
				Items  []Notification
				Unread int
			}
		}
		if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil || res.Code != 200 || page.Data.Unread != wantUnread || len(page.Data.Items) != 100 {
			t.Fatalf("unexpected notifications response: status=%d unread=%d len=%d err=%v", res.Code, page.Data.Unread, len(page.Data.Items), err)
		}
		items := page.Data.Items
		if wantUnread == 1 {
			if items[0].Read || items[0].ID != "n1" || items[1].ID != "n104" || items[99].ID != "n6" {
				t.Fatal("old unread notification missing or read history out of order")
			}
			if ack := callTest(t, a.Handler(), "POST", "/api/notifications/read-all", "{}", cookie, ""); ack.Code != 200 {
				t.Fatal(ack.Code)
			}
		} else if !items[0].Read || items[0].ID != "n104" || items[99].ID != "n5" {
			t.Fatal("all-read history no longer in chronological order")
		}
	}
}

func TestNotificationReadAllAcrossPages(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := t.Context()
	if _, err := a.Store.DB.ExecContext(ctx, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<101)
 INSERT INTO message(record_kind,uid,payload,updated_at) SELECT 'notification','n'||x,json_object('id','n'||x,'read',json('false')),'original' FROM n`); err != nil {
		t.Fatal(err)
	}
	if err := a.saveProtectedNotification(ctx, Notification{ID: "protected", Title: "任务完成"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "message", "other", Notification{ID: "other"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{102, 0} {
		response := callTest(t, a.Handler(), "POST", "/api/notifications/read-all", "{}", cookie, "")
		var ack struct{ Data struct{ Updated int } }
		if err := json.Unmarshal(response.Body.Bytes(), &ack); err != nil || response.Code != 200 || ack.Data.Updated != want {
			t.Fatalf("mark-all returned %s", response.Body.String())
		}
	}
	var record notificationRecord
	if err := a.Store.Get(ctx, "notification", "protected", &record); err != nil {
		t.Fatal(err)
	}
	if n, err := a.openNotification(record); err != nil || !n.Read || n.Title != "任务完成" || len(record.ProtectedContent) == 0 {
		t.Fatalf("protected content or read state changed: %+v %v", n, err)
	}
	var same int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM message WHERE (read=1 AND updated_at='original') OR (uid='other' AND read=0)`).Scan(&same); err != nil || same != 102 {
		t.Fatalf("history ordering or unrelated message was changed: %d %v", same, err)
	}
	if err := a.Store.Put(ctx, "notification", "new", Notification{ID: "new"}); err != nil {
		t.Fatal(err)
	}
	page, err := a.notifications(ctx)
	if err != nil || page.(map[string]any)["unread"] != 1 {
		t.Fatalf("new notification must start unread: %v", err)
	}
}

func TestNotificationPollIndexedLargeHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("large synthetic history")
	}
	a := newTestApp(t)
	ctx := t.Context()
	// Populate a previously deployed database before adding the indexes, so
	// startup migration cost is measured separately from the indexed poll.
	if _, err := a.Store.DB.ExecContext(ctx, `DROP INDEX notification_read_time_uid; DROP INDEX notification_unread;
 WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000)
 INSERT INTO message(record_kind,uid,payload,updated_at) SELECT 'notification','notice-'||x,json_object('id','notice-'||x,'read',json('false'),'message',printf('%0500d',0)),printf('%020d',x) FROM n`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := a.Store.List(ctx, "notification", 100, 0); err != nil {
		t.Fatal(err)
	}
	var unread int
	if err := a.Store.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM message WHERE json_extract(payload,'$.read')=0`).Scan(&unread); err != nil {
		t.Fatal(err)
	}
	baseline := time.Since(start)
	start = time.Now()
	if _, err := a.Store.DB.ExecContext(ctx, notificationOrderIndex+`; CREATE INDEX notification_unread ON message(read)`); err != nil {
		t.Fatal(err)
	}
	migration := time.Since(start)
	assertNotificationQueryPlans(t, a.Store)
	start = time.Now()
	for i := 0; i < 3; i++ {
		out, err := a.notifications(ctx)
		if err != nil {
			t.Fatal(err)
		}
		m := out.(map[string]any)
		if m["unread"] != 100000 || len(m["items"].([]json.RawMessage)) != 100 {
			t.Fatal("notification content/count changed")
		}
	}
	t.Logf("100k notifications, 500B synthetic message: unindexed=%s index-build=%s indexed-average=%s", baseline, migration, time.Since(start)/3)
	// Assert access paths rather than a machine-dependent timing threshold.
}
