package app

import (
	"context"
	"testing"

	"light-media/internal/notice"
)

// Synthetic receipt-level tests, not a claim of live site or delivery coverage.
func TestSiteMessageHistoryExecutionSurvivesBusinessDeletionOnly(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	p := a.pluginStore
	identity := siteMessageIdentity{Collection: "owned-receipt-fixture", Ordinal: 0}
	r := pluginNoticeRoute{Version: 2, ID: "owned-route", AcceptedSHA: tokenHash("owned-input"), Count: 2, BusinessV2: &pluginNoticeV2Business{Binding: pluginNoticeV2Binding{Kind: "site_message", SiteMessage: &identity}}}
	fields := notice.Object{"title": "owned fixture", "source": "shared-source"}
	n := Notification{ID: "notice_" + p.noticeID("business-history", r.ID, 2), Title: "owned fixture", Source: "shared-source", CreatedAt: now()}
	saved := pluginNoticeV2History{History: n, Digest: p.noticeID("business-history-value", n)}
	step := pluginNoticeStep{RouteID: r.ID, Ordinal: 2, Kind: "history", InputSHA: tokenHash(string(mustCleanSeedJSON(fields)))}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = txPut(ctx, tx, "notification", n.ID, n); e != nil {
		t.Fatal(e)
	}
	if e = p.saveSiteMessageHistoryExecutionTx(ctx, tx, r, 2, fields, saved); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	check := func() {
		t.Helper()
		if e := p.validateSiteMessageHistoryExecution(ctx, a.Store.DB, r, step, saved); e != nil {
			t.Fatal(e)
		}
	}
	check()
	n.Read = true
	if e = a.Store.Put(ctx, "notification", n.ID, n); e != nil {
		t.Fatal(e)
	}
	check()
	n.Title = "mutated"
	if e = a.Store.Put(ctx, "notification", n.ID, n); e != nil {
		t.Fatal(e)
	}
	if e = p.validateSiteMessageHistoryExecution(ctx, a.Store.DB, r, step, saved); e == nil {
		t.Fatal("present changed business history accepted")
	}
	if _, e = a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='notification' AND id=?`, n.ID); e != nil {
		t.Fatal(e)
	}
	check()
	var count int
	if e = a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM records WHERE kind='notification' AND json_extract(payload,'$.source')=?`, n.Source).Scan(&count); e != nil || count != 0 {
		t.Fatal("execution receipt became a source reservation", count, e)
	}
	changed := step
	changed.InputSHA = tokenHash("forged-input")
	if e = p.validateSiteMessageHistoryExecution(ctx, a.Store.DB, r, changed, saved); e == nil {
		t.Fatal("receipt accepted changed input")
	}
	changedRoute := r
	changedRoute.AcceptedSHA = tokenHash("other-business")
	if e = p.validateSiteMessageHistoryExecution(ctx, a.Store.DB, changedRoute, step, saved); e == nil {
		t.Fatal("receipt accepted changed business")
	}
	if _, e = a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='site_message_history_execution'`); e != nil {
		t.Fatal(e)
	}
	if e = p.validateSiteMessageHistoryExecution(ctx, a.Store.DB, r, step, saved); e == nil {
		t.Fatal("missing immutable execution proof accepted")
	}
}
