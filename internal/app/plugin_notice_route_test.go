package app

import (
	"context"
	"encoding/json"
	"light-media/internal/notice"
	"strings"
	"testing"
	"time"
)

func noticePreferenceTest(t *testing.T, a *App, kind, key string, value any) {
	t.Helper()
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = a.pluginStore.putNoticePreferenceTx(context.Background(), tx, kind, key, value); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func noticeRouteTestIDs(t *testing.T, a *App, table string) []string {
	t.Helper()
	if table != "plugin_notice_routes" && table != "plugin_message_queue" {
		t.Fatal("fixture table")
	}
	rows, err := a.Store.DB.Query(`SELECT id FROM ` + table + ` ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestPluginNoticeQueueUsesExplicitReloadedWindowSnapshot(t *testing.T) {
	a := newTestApp(t)
	at := time.Date(2026, 10, 18, 10, 0, 0, 0, time.FixedZone("owned", 8*3600))
	before, err := a.pluginStore.noticePeriods(context.Background())
	if err != nil || !notice.Allowed(before, at) {
		t.Fatal(err)
	}
	noticePreferenceTest(t, a, "window", "", []any{map[string]any{"start": "12:00", "end": "12:00"}})
	existing, err := a.pluginStore.noticePeriods(context.Background())
	if err != nil || !notice.Allowed(existing, at) {
		t.Fatal("DB edit silently reinitialized the source queue", err)
	}
	if err = a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatal(err)
	}
	next, err := a.pluginStore.noticePeriods(context.Background())
	if err != nil || notice.Allowed(next, at) {
		t.Fatal("explicit native init_config did not apply", err)
	}
	noticePreferenceTest(t, a, "window", "", json.Number("4"))
	if err = a.pluginStore.reloadNoticePeriods(context.Background()); err == nil {
		t.Fatal("invalid original schedule accepted")
	}
	retained, err := a.pluginStore.noticePeriods(context.Background())
	if err != nil || notice.Allowed(retained, at) {
		t.Fatal("failed reload replaced previous source periods", err)
	}
}

func TestPluginNoticeCoreEventRejectsMissingPrivateFact(t *testing.T) {
	a := newTestApp(t)
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fake := "pnm_" + strings.Repeat("a", 64)
	err = recordDomainEventTx(context.Background(), tx, DomainEvent{Type: "notice.message", EventID: domainEventID("notice.message", fake), NoticeID: fake})
	_ = tx.Rollback()
	if err == nil {
		t.Fatal("public reference without committed private fact accepted")
	}
}
