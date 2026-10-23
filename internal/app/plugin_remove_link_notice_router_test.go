package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

// Only owned temporary files and an in-memory sender are used here. These are
// integration tests of the real SQL acceptance/handoff and shared Router.
func removeLinkNoticeBox(t *testing.T, f *removeLinkEffectsFixture, x cleanuphost.Effect) pluginRemoveLinkPrivateOutbox {
	t.Helper()
	var sealed []byte
	token := f.e.token(x.ID)
	if err := f.a.Store.DB.QueryRow(`SELECT sealed FROM plugin_cleanup_private_outbox WHERE instance_id=? AND generation=? AND token=?`, f.b.InstanceID, f.b.Generation, token).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	raw, err := f.a.pluginStore.unseal("cleanup_journal", f.b.InstanceID, f.e.aad("outbox", token), sealed)
	var box pluginRemoveLinkPrivateOutbox
	if err != nil || cleanupJournalStrict(raw, &box) != nil {
		t.Fatal("read accepted notice", err)
	}
	return box
}
func removeLinkNoticeReseal(t *testing.T, f *removeLinkEffectsFixture, x cleanuphost.Effect, kind, table string, value any) {
	t.Helper()
	token := f.e.token(x.ID)
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := f.a.pluginStore.seal("cleanup_journal", f.b.InstanceID, f.e.aad(kind, token), raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.a.Store.DB.Exec(`UPDATE `+table+` SET sealed=? WHERE instance_id=? AND generation=? AND token=?`, sealed, f.b.InstanceID, f.b.Generation, token); err != nil {
		t.Fatal(err)
	}
}
func removeLinkNoticeDomain(t *testing.T, a *App, domain string) {
	t.Helper()
	settings := a.Settings()
	settings.AppDomain = domain
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
}
func removeLinkNoticeDrain(t *testing.T, a *App) {
	t.Helper()
	if err := a.dispatchRemoveLinkOutboxes(context.Background(), 200); err != nil {
		t.Fatal("accepted handoff", err)
	}
	if err := a.resumePluginNoticeRoutes(context.Background(), 200); err != nil {
		t.Fatal("Router resume", err)
	}
}
func TestRemoveLinkNoticeRouterFreezesAcceptanceBeforeHandoff(t *testing.T) {
	for _, domain := range []string{"", "https://accepted.invalid/base/"} {
		t.Run(domain, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"notify": true})
			ctx := context.Background()
			f.authorize(t, ctx)
			removeLinkNoticeDomain(t, f.a, domain)
			stamp := time.Date(2026, 10, 20, 22, 13, 14, 123456789, time.FixedZone("owned-offset", 19800))
			f.a.pluginStore.notices.now = func() time.Time { return stamp }
			x := cleanupOutboxCommit(t, f, "notification")
			first := removeLinkNoticeBox(t, f, x)
			if first.Version != 2 || first.Notice == nil || first.Notice.AppDomain == nil || *first.Notice.AppDomain != domain {
				t.Fatal("missing frozen domain including empty domain", first)
			}
			if first.Notice.CreatedAt != stamp.UTC().Format(time.RFC3339Nano) {
				t.Fatal("acceptance timestamp changed", first.Notice.CreatedAt)
			}
			local := stamp.In(time.Local)
			_, offset := local.Zone()
			if first.Notice.TimezoneOffset != offset || first.Notice.Timezone != time.Local.String() {
				t.Fatal("timezone snapshot not paired with acceptance", first.Notice)
			}
			expected, err := sourcePluginDefaultLink(domain, "RemoveLink", "")
			if err != nil {
				t.Fatal(err)
			}
			var envelope map[string]any
			if err = json.Unmarshal(first.Notice.Envelope, &envelope); err != nil || envelope["link"] != expected {
				t.Fatal("wrong accepted link", envelope, err)
			}
			removeLinkNoticeDomain(t, f.a, "https://later.invalid/new")
			f.a.pluginStore.notices.now = func() time.Time { return stamp.Add(24 * time.Hour) }
			if ok, err := f.e.Commit(ctx, x); err != nil || !ok {
				t.Fatal("repeat Commit", ok, err)
			}
			repeated := removeLinkNoticeBox(t, f, x)
			oldJSON, _ := json.Marshal(first.Notice)
			newJSON, _ := json.Marshal(repeated.Notice)
			if !bytes.Equal(oldJSON, newJSON) {
				t.Fatal("repeat Commit refreshed acceptance")
			}
			var calls atomic.Int32
			f.a.pluginStore.notices.send = func(ctx context.Context, q pluginNoticeQueueEntry) (int, error) {
				calls.Add(1)
				raw, _ := json.Marshal(q.Call)
				if strings.Contains(string(raw), "later.invalid") {
					t.Error("sender used later config", string(raw))
				}
				var count int
				if err := f.a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM plugin_cleanup_outbox_handoffs`).Scan(&count); err != nil {
					t.Error("sender retained SQL transaction", err)
				}
				return 1, nil
			}
			removeLinkNoticeDrain(t, f.a)
			h := cleanupHandoff(t, f, x)
			if h.Notice == nil || h.Notice.RouteID == "" {
				t.Fatal("missing route handoff")
			}
			route, _, err := f.a.pluginStore.readNoticeRoute(ctx, f.a.Store.DB, h.Notice.RouteID)
			if err != nil || route.Kind != "remove_link" || !route.Complete || !bytes.Equal(route.Envelope, first.Notice.Envelope) {
				t.Fatal("route did not preserve accepted envelope", route, err)
			}
			var history Notification
			if err = f.a.Store.Get(ctx, "notification", h.Reference, &history); err != nil || history.Link != func() string { v, _ := expected.(string); return v }() {
				t.Fatal("history changed frozen link", history, err)
			}
			if calls.Load() != 1 {
				t.Fatal("wrong send count", calls.Load())
			}
			if _, err = f.a.pluginStore.Data(ctx, f.b.InstanceID, "last_notify"); !missingPluginValue(err) {
				t.Fatal("RemoveLink acquired SiteStatistic fingerprint semantics", err)
			}
			y := cleanupOutboxCommit(t, f, "notification")
			next := removeLinkNoticeBox(t, f, y)
			if next.Notice == nil || next.Notice.AppDomain == nil || *next.Notice.AppDomain != "https://later.invalid/new" {
				t.Fatal("new acceptance did not use current domain")
			}
		})
	}
}
func TestRemoveLinkNoticeRouterConcurrentHandoffUnknownNoResend(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "consumed", true: "unknown"}[unknown], func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"notify": true})
			ctx := context.Background()
			f.authorize(t, ctx)
			x := cleanupOutboxCommit(t, f, "notification")
			var sends atomic.Int32
			f.a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) {
				sends.Add(1)
				if unknown {
					return 1, seedhost.ErrUnknown
				}
				return 1, nil
			}
			var wg sync.WaitGroup
			results := make(chan error, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); results <- f.a.dispatchRemoveLinkOutboxes(ctx, 200) }()
			}
			wg.Wait()
			close(results)
			for err := range results {
				if err != nil {
					t.Fatal("duplicate handoff", err)
				}
			}
			h := cleanupHandoff(t, f, x)
			if h.Notice == nil {
				t.Fatal("missing router proof")
			}
			for i := 0; i < 3; i++ {
				removeLinkNoticeDrain(t, f.a)
			}
			if sends.Load() != 1 || cleanupEffectsCount(t, f.a, "plugin_notice_routes") != 1 || cleanupEffectsCount(t, f.a, "plugin_notice_publications") != 1 {
				t.Fatal("duplicate route/send", sends.Load())
			}
			ids := noticeRouteTestIDs(t, f.a, "plugin_message_queue")
			if len(ids) != 1 {
				t.Fatal(ids)
			}
			q, err := f.a.pluginStore.readPluginMessage(ctx, f.a.Store.DB, ids[0])
			want := "consumed"
			if unknown {
				want = "unknown"
			}
			if err != nil || q.State != want {
				t.Fatal("wrong durable send state", q.State, err)
			}
		})
	}
}
func TestRemoveLinkNoticeRouterRollbackLeavesOnlyAcceptance(t *testing.T) {
	for _, trigger := range []string{"BEFORE INSERT ON plugin_notice_routes", "BEFORE INSERT ON plugin_cleanup_outbox_handoffs", "BEFORE UPDATE ON plugin_cleanup_private_outbox"} {
		t.Run(trigger, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"notify": true})
			f.authorize(t, context.Background())
			x := cleanupOutboxCommit(t, f, "notification")
			var sends atomic.Int32
			f.a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if _, err := f.a.Store.DB.Exec(`CREATE TRIGGER owned_notice_reject ` + trigger + ` BEGIN SELECT RAISE(ABORT,'owned rollback'); END`); err != nil {
				t.Fatal(err)
			}
			if err := f.a.dispatchRemoveLinkOutboxes(context.Background(), 200); err == nil {
				t.Fatal("failed handoff accepted")
			}
			for _, table := range []string{"plugin_notice_routes", "plugin_cleanup_outbox_handoffs", "plugin_notice_publications", "plugin_message_queue"} {
				if cleanupEffectsCount(t, f.a, table) != 0 {
					t.Fatal("partial handoff", table)
				}
			}
			var n int
			if err := f.a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&n); err != nil || n != 0 {
				t.Fatal("history escaped rollback", n, err)
			}
			if removeLinkNoticeBox(t, f, x).State != "pending" || sends.Load() != 0 {
				t.Fatal("acceptance acknowledged or sender ran")
			}
			if _, err := f.a.Store.DB.Exec(`DROP TRIGGER owned_notice_reject`); err != nil {
				t.Fatal(err)
			}
			removeLinkNoticeDrain(t, f.a)
			if sends.Load() != 1 {
				t.Fatal(sends.Load())
			}
		})
	}
}
func TestRemoveLinkNoticeRouterLegacyPendingStaysLocalOnly(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"notify": true})
	ctx := context.Background()
	f.authorize(t, ctx)
	x := cleanupOutboxCommit(t, f, "notification")
	box := removeLinkNoticeBox(t, f, x)
	// Read the valid receipt before constructing a genuine old-format pair.
	// The production read validates receipt and outbox together.
	receipt, err := f.e.receipt(ctx, f.a.Store.DB, x)
	if err != nil {
		t.Fatal(err)
	}
	box.Version = 1
	box.Notice = nil
	removeLinkNoticeReseal(t, f, x, "outbox", "plugin_cleanup_private_outbox", box)
	receipt.NoticeAcceptanceSHA = ""
	removeLinkNoticeReseal(t, f, x, "receipt", "plugin_cleanup_core_receipts", receipt)
	var sends atomic.Int32
	f.a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	removeLinkNoticeDomain(t, f.a, "https://new.invalid")
	for i := 0; i < 2; i++ {
		removeLinkNoticeDrain(t, f.a)
	}
	h := cleanupHandoff(t, f, x)
	if h.Notice != nil || h.Stage != "history_saved" || sends.Load() != 0 || cleanupEffectsCount(t, f.a, "plugin_notice_routes") != 0 {
		t.Fatal("legacy intent upgraded")
	}
	var legacy Notification
	if err := f.a.Store.Get(ctx, "notification", h.Reference, &legacy); err != nil || legacy.Link != "#/plugins?tab=installed&id=RemoveLink" {
		t.Fatal("legacy fragment changed", legacy, err)
	}
	if removeLinkNoticeBox(t, f, x).Version != 1 {
		t.Fatal("legacy version rewritten")
	}
}
func TestRemoveLinkNoticeRouterResealedAcceptanceCorruptionFailsClosed(t *testing.T) {
	for _, which := range []string{"domain", "nil_domain", "envelope", "clock", "receipt_sha", "legacy_downgrade"} {
		t.Run(which, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"notify": true})
			ctx := context.Background()
			f.authorize(t, ctx)
			x := cleanupOutboxCommit(t, f, "notification")
			box := removeLinkNoticeBox(t, f, x)
			if box.Notice == nil {
				t.Fatal("new acceptance missing")
			}
			switch which {
			case "domain":
				d := "https://forged.invalid"
				box.Notice.AppDomain = &d
			case "nil_domain":
				box.Notice.AppDomain = nil
			case "envelope":
				box.Notice.Envelope = json.RawMessage(`{"title":"forged","text":"forged","mtype":"站点","link":"https://forged.invalid"}`)
			case "clock":
				box.Notice.TimezoneOffset++
			case "receipt_sha":
				r, err := f.e.receipt(ctx, f.a.Store.DB, x)
				if err != nil {
					t.Fatal(err)
				}
				r.NoticeAcceptanceSHA = strings.Repeat("0", 64)
				removeLinkNoticeReseal(t, f, x, "receipt", "plugin_cleanup_core_receipts", r)
			case "legacy_downgrade":
				box.Version = 1
				box.Notice = nil
			}
			removeLinkNoticeReseal(t, f, x, "outbox", "plugin_cleanup_private_outbox", box)
			var sends atomic.Int32
			f.a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err == nil {
				t.Fatal("resealed corrupt acceptance trusted", which)
			}
			if sends.Load() != 0 || cleanupEffectsCount(t, f.a, "plugin_notice_routes") != 0 || cleanupEffectsCount(t, f.a, "plugin_cleanup_outbox_handoffs") != 0 {
				t.Fatal("corruption escaped source gate")
			}
		})
	}
}
func TestRemoveLinkNoticeRouterHandoffHistoryAndRouteRemainAuthenticated(t *testing.T) {
	for _, which := range []string{"handoff_sha", "handoff_route", "history", "route_envelope", "route_kind", "source_domain", "source_receipt"} {
		t.Run(which, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"notify": true})
			ctx := context.Background()
			f.authorize(t, ctx)
			x := cleanupOutboxCommit(t, f, "notification")
			var sends atomic.Int32
			f.a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			removeLinkNoticeDrain(t, f.a)
			h := cleanupHandoff(t, f, x)
			if h.Notice == nil {
				t.Fatal("no handoff proof")
			}
			route, _, err := f.a.pluginStore.readNoticeRoute(ctx, f.a.Store.DB, h.Notice.RouteID)
			if err != nil {
				t.Fatal(err)
			}
			switch which {
			case "handoff_sha":
				h.Notice.InitialSHA = strings.Repeat("0", 64)
				removeLinkNoticeReseal(t, f, x, "handoff", "plugin_cleanup_outbox_handoffs", h)
			case "handoff_route":
				h.Notice.RouteID = strings.Repeat("0", 64)
				removeLinkNoticeReseal(t, f, x, "handoff", "plugin_cleanup_outbox_handoffs", h)
			case "history":
				var n Notification
				if err = f.a.Store.Get(ctx, "notification", h.Reference, &n); err != nil {
					t.Fatal(err)
				}
				n.Link = "https://forged.invalid"
				if err = f.a.Store.Put(ctx, "notification", h.Reference, n); err != nil {
					t.Fatal(err)
				}
			case "route_envelope", "route_kind":
				if which == "route_kind" {
					route.Kind = "clean_seed"
				} else {
					route.Envelope = json.RawMessage(`{"title":"forged","text":"forged"}`)
				}
				sealed, e := f.a.pluginStore.noticeSeal("route", route.ID, route)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.a.Store.DB.Exec(`UPDATE plugin_notice_routes SET sealed=? WHERE id=?`, sealed, route.ID); e != nil {
					t.Fatal(e)
				}
			case "source_domain":
				box := removeLinkNoticeBox(t, f, x)
				d := "https://forged.invalid"
				box.Notice.AppDomain = &d
				removeLinkNoticeReseal(t, f, x, "outbox", "plugin_cleanup_private_outbox", box)
			case "source_receipt":
				r, e := f.e.receipt(ctx, f.a.Store.DB, x)
				if e != nil {
					t.Fatal(e)
				}
				r.NoticeAcceptanceSHA = strings.Repeat("0", 64)
				removeLinkNoticeReseal(t, f, x, "receipt", "plugin_cleanup_core_receipts", r)
			}
			if _, err = f.a.auditRemoveLinkHandoffs(ctx, pluginCleanupAuditCursor{}, 200); err == nil {
				t.Fatal("corrupt completed handoff trusted", which)
			}
			if err = f.a.runNoticeRoute(ctx, route.ID); err == nil {
				t.Fatal("completed route bypassed source revalidation", which)
			}
			if sends.Load() != 1 {
				t.Fatal("corrupt completed route resent", sends.Load())
			}
		})
	}
}
func TestRemoveLinkNoticeRouterRetiredColdAcceptanceDoesNotGrantCleanup(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"notify": true, "delete_history": true})
	ctx := context.Background()
	release := f.authorize(t, ctx)
	removeLinkNoticeDomain(t, f.a, "https://accepted.invalid")
	x := cleanupOutboxCommit(t, f, "notification")
	unaccepted := f.prepare(t, f.history.Source, "history_src")
	release()
	if _, err := f.a.Store.DB.Exec(`UPDATE plugin_installations SET installed=0,desired_enabled=0,generation=generation+1 WHERE instance_id=?`, f.b.InstanceID); err != nil {
		t.Fatal(err)
	}
	removeLinkNoticeDomain(t, f.a, "https://changed.invalid")
	dir := f.a.Dir
	if err := f.a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	removeLinkNoticeDrain(t, a)
	old := f.a
	f.a = a
	defer func() { f.a = old }()
	h := cleanupHandoff(t, f, x)
	if h.Notice == nil {
		t.Fatal("retired acceptance did not recover")
	}
	var n Notification
	if err = a.Store.Get(ctx, "notification", h.Reference, &n); err != nil || !strings.HasPrefix(n.Link, "https://accepted.invalid/") {
		t.Fatal("cold recovery refreshed domain", n, err)
	}
	j, err := newPluginRemoveLinkJournal(ctx, a.pluginStore, f.b.InstanceID, f.b.Generation)
	if err != nil {
		t.Fatal(err)
	}
	e, err := newPluginRemoveLinkEffects(ctx, a, j)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := e.authorize(ctx, f.b, "timer", "", f.roots); err == nil {
		release()
		t.Fatal("retired generation received a new write lease")
	}
	if ok, err := e.Commit(ctx, unaccepted); err == nil || ok {
		t.Fatal("old notice recovery granted new cleanup", ok, err)
	}
	for _, path := range []string{f.history.Source, f.history.Target} {
		if _, err = os.Stat(path); err != nil {
			t.Fatal("notice recovery touched owned media", err)
		}
	}
	removeLinkNoticeDrain(t, a)
	if sends.Load() != 1 {
		t.Fatal("retired notice duplicate", sends.Load())
	}
}
func TestRemoveLinkNoticeRouterCompiledSDKOwnerNativeCleanupToStub(t *testing.T) {
	a, source, library := importTestApp(t)
	useBuiltInPluginProcess(t, a)
	a.pluginRemoveLink.watch = func(context.Context, []removelink.Watch, func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
		return &removeLinkSQLTestWatcher{events: make(chan cleanuphost.Event, 8), errors: make(chan error, 1)}, nil
	}
	origin, target := filepath.Join(source, "owned-router.mkv"), filepath.Join(library, "owned-router.mkv")
	if err := os.WriteFile(origin, []byte("synthetic owned movie"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(origin, target); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	removeLinkHTTPCall(t, a, cookie, "POST", "/api/plugins/install", map[string]any{"plugin_id": "RemoveLink"})
	removeLinkHTTPRun(t, a, "RemoveLink")
	config := map[string]any{"enabled": true, "monitor_dirs": source + "\n" + library, "delayed_deletion": false, "delete_history": false, "delete_scrap_infos": false, "delete_torrents": false, "notify": true}
	removeLinkHTTPCall(t, a, cookie, "PUT", "/api/plugins/RemoveLink/config", map[string]any{"revision": 1, "config": config})
	removeLinkHTTPRun(t, a, "RemoveLink")
	installation, err := a.pluginStore.Installation(context.Background(), "RemoveLink")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := a.removeLinkOwner(context.Background(), installation)
	if err != nil || owner.h == nil {
		t.Fatal("compiled SDK did not create host", err)
	}
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	if err = os.Remove(origin); err != nil {
		t.Fatal(err)
	}
	event := cleanuphost.Event{ID: "owned-router-delete", Kind: "deleted", Path: origin, Mode: "hardlink", At: time.Now()}
	// The compiled SDK installed/configured this owner; the native observer,
	// not worker-provided JSON, supplies the trusted filesystem event.
	if err = owner.h.Observe(context.Background(), event); err != nil {
		t.Fatal("owned native observation", err)
	}
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("actual linked follower was not cleaned", err)
	}
	removeLinkNoticeDrain(t, a)
	if sends.Load() != 1 || cleanupEffectsCount(t, a, "plugin_notice_routes") != 1 {
		t.Fatal("SDK cleanup did not reach Router", sends.Load())
	}
	for _, id := range noticeRouteTestIDs(t, a, "plugin_notice_routes") {
		r, _, e := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, id)
		if e != nil || r.Kind != "remove_link" || r.Instance != "RemoveLink" || !r.Complete {
			t.Fatal("wrong true-path route", r, e)
		}
	}
}
func TestRemoveLinkNoticeRouterCloneSameEffectIDsStayIndependent(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"notify": true})
	ctx := context.Background()
	run, err := f.a.pluginStore.InstallAndQueue(ctx, PluginInstallation{InstanceID: "RemoveLink_owned_clone", PluginID: "RemoveLink", Version: "2.17", ManifestSchemaVersion: 1, Manifest: json.RawMessage(`{"plugin_id":"RemoveLink","name":"fixture","runtime":"go-executable"}`)}, f.config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	run, err = f.a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := cleanuphost.Binding{InstanceID: run.InstanceID, Generation: run.Generation, Revision: run.ConfigRevision}
	j, err := newPluginRemoveLinkJournal(ctx, f.a.pluginStore, b.InstanceID, b.Generation)
	if err != nil {
		t.Fatal(err)
	}
	e, err := newPluginRemoveLinkEffects(ctx, f.a, j)
	if err != nil {
		t.Fatal(err)
	}
	clone := &removeLinkEffectsFixture{a: f.a, j: j, e: e, b: b, run: run, roots: f.roots, config: f.config, history: f.history}
	removeLinkNoticeDomain(t, f.a, "https://owned.invalid")
	var sends atomic.Int32
	f.a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	var routes []string
	for _, fixture := range []*removeLinkEffectsFixture{f, clone} {
		fixture.authorize(t, ctx)
		x := fixture.prepare(t, fixture.roots[0].Root.Path, "notification")
		old := x.ID
		x.ID = "same-owned-effect-id"
		if err = fixture.j.Update(ctx, func(s *cleanuphost.Durable) error { delete(s.Effects, old); s.Effects[x.ID] = x; return nil }); err != nil {
			t.Fatal(err)
		}
		if ok, err := fixture.e.Commit(ctx, x); err != nil || !ok {
			t.Fatal(ok, err)
		}
		removeLinkNoticeDrain(t, f.a)
		h := cleanupHandoff(t, fixture, x)
		if h.Notice == nil {
			t.Fatal("missing clone route")
		}
		routes = append(routes, h.Notice.RouteID)
		route, _, err := f.a.pluginStore.readNoticeRoute(ctx, f.a.Store.DB, h.Notice.RouteID)
		if err != nil || route.Instance != fixture.b.InstanceID || route.EffectID != x.ID {
			t.Fatal("clone binding lost", route, err)
		}
		if strings.Contains(string(route.Envelope), "RemoveLink_owned_clone") || !strings.Contains(string(route.Envelope), "id=RemoveLink") {
			t.Fatal("link used instance instead of plugin class", string(route.Envelope))
		}
	}
	if routes[0] == routes[1] || sends.Load() != 2 {
		t.Fatal("clones collapsed", routes, sends.Load())
	}
}
func TestRemoveLinkNoticeRouterCommittedAcknowledgementLoss(t *testing.T) {
	for _, stage := range []string{"route_step", "route_finish", "queue_intent", "queue_finish"} {
		t.Run(stage, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"notify": true})
			ctx := context.Background()
			f.authorize(t, ctx)
			x := cleanupOutboxCommit(t, f, "notification")
			var lost atomic.Bool
			var sends atomic.Int32
			f.a.pluginStore.notices.afterCommit = func(op string) error {
				if op == stage && lost.CompareAndSwap(false, true) {
					return errors.New("owned committed acknowledgement lost")
				}
				return nil
			}
			f.a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			removeLinkNoticeDrain(t, f.a)
			h := cleanupHandoff(t, f, x)
			if h.Notice == nil {
				t.Fatal("missing handoff")
			}
			for i := 0; i < 3; i++ {
				if err := f.a.runNoticeRoute(ctx, h.Notice.RouteID); err != nil {
					t.Fatal("committed readback failed", err)
				}
			}
			if !lost.Load() || sends.Load() != 1 {
				t.Fatal("fault not reached or send duplicated", lost.Load(), sends.Load())
			}
		})
	}
}
