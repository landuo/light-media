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
		{notificationListSQL, "notification_display_order", []any{100}},
		{notificationUnreadSQL, "notification_unread", nil},
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
	if _, err = s.DB.ExecContext(ctx, `DROP INDEX notification_display_order; DROP INDEX notification_unread; INSERT INTO records VALUES('encrypted-fixture','main',x'FFFEFD','2020')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"b", "a"} {
		if err = s.Put(ctx, "notification", id, Notification{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE records SET updated_at='2026' WHERE kind='notification'`); err != nil {
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
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='notification' AND id='b'`); err != nil {
		t.Fatal(err)
	}
	raw, err = a.notifications(ctx)
	if err != nil || raw.(map[string]any)["unread"] != 0 {
		t.Fatalf("delete index stale: %v %v", raw, err)
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
	if _, err := a.Store.DB.ExecContext(ctx, `DROP INDEX notification_display_order; DROP INDEX notification_unread;
 WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000)
 INSERT INTO records(kind,id,payload,updated_at) SELECT 'notification','notice-'||x,json_object('id','notice-'||x,'read',json('false'),'message',printf('%0500d',0)),printf('%020d',x) FROM n`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := a.Store.List(ctx, "notification", 100, 0); err != nil {
		t.Fatal(err)
	}
	var unread int
	if err := a.Store.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='notification' AND json_extract(payload,'$.read')=0`).Scan(&unread); err != nil {
		t.Fatal(err)
	}
	baseline := time.Since(start)
	start = time.Now()
	if _, err := a.Store.DB.ExecContext(ctx, `CREATE INDEX notification_display_order ON records(kind,updated_at DESC,id) WHERE kind='notification'; CREATE INDEX notification_unread ON records(json_extract(payload,'$.read')) WHERE kind='notification'`); err != nil {
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
