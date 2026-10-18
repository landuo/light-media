package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/plugins"
	seed "light-media/internal/plugins/cleaninvalidseed"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
	"light-media/internal/plugins/schedule"
)

func TestPluginCleanInvalidSeedOwnerWorkerFixture(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--clean-seed-owner-worker" {
			if plugins.Serve(context.Background(), os.Stdin, os.Stdout, seed.Manifest(), seed.Handle) != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
}

// Run the actual SDK in a child, then its real persisted deferred handshake and
// native adapter. Catalog/runtime registration is a separate shared App slice.
func cleanSeedOwnerInvoke(t *testing.T, a *App, run PluginRun) PluginRun {
	t.Helper()
	ctx := context.Background()
	i, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := a.preparePluginDeferred(ctx, i, run)
	if err != nil || d == nil {
		t.Fatal("deferred prepare", err)
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.pluginProcess <- struct{}{}
	worker, err := (plugins.Runner{Timeout: 20 * time.Second}).Run(ctx, plugins.Command{Path: path, Args: []string{"-test.run=^TestPluginCleanInvalidSeedOwnerWorkerFixture$", "--", "--clean-seed-owner-worker"}}, seed.Manifest(), run.InstanceID, "invoke", run.Input, d.handshake)
	<-a.pluginProcess
	if err != nil {
		t.Fatal("actual SDK child", err)
	}
	if err = d.ready(worker); err != nil {
		t.Fatal(err)
	}
	if err = d.changePhase(ctx, "prepared", "started", nil); err != nil {
		t.Fatal(err)
	}
	trusted := context.WithValue(ctx, pluginDeferredExecutionKey{}, d)
	_, hostErr := a.pluginCleanInvalidSeedHost(trusted, i, &run, d.record.Method, d.record.Raw)
	result, code, found, proofErr := a.pluginStore.cleanInvalidSeedDeferredOutcome(ctx, run, d.record.Method)
	if proofErr != nil {
		t.Fatal("outcome proof", proofErr)
	}
	phase, state := "confirmed", "succeeded"
	if !found {
		phase, state, result, code = "uncertain", "reconcile_pending", nil, "PLUGIN_DEFERRED_UNCERTAIN"
	} else if code != "" {
		phase, state = "terminal_failed", "failed"
	}
	if state == "succeeded" && hostErr != nil {
		t.Fatal("confirmed adapter failed", hostErr)
	}
	if state == "failed" && hostErr == nil {
		t.Fatal("known failed source was returned as success")
	}
	storedCode := code
	if phase == "uncertain" {
		storedCode = ""
	}
	if err = d.changeOutcome(ctx, "started", phase, result, storedCode); err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.FinishRun(ctx, run.ID, state, code, result); err != nil {
		t.Fatal(err)
	}
	got, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func cleanSeedOwnerClaim(t *testing.T, a *App, instance string) PluginRun {
	t.Helper()
	run, err := a.pluginStore.claimRunForInstance(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	return run
}
func cleanSeedOwnerConfig(extra map[string]any) map[string]any {
	c := map[string]any{"enabled": true, "notify": false, "onlyonce": false, "cron": "0 0 * * *", "downloaders": []string{"Owned qB"}, "min_seeding_days": 0, "detect_invalid_files": false, "delete_invalid_files": false, "delete_invalid_torrents": false, "label_only": false, "download_dirs": "", "exclude_keywords": "", "exclude_categories": "", "exclude_labels": ""}
	for k, v := range extra {
		c[k] = v
	}
	return c
}
func cleanSeedOwnerSave(t *testing.T, a *App, instance string, config map[string]any) PluginRun {
	t.Helper()
	ctx := context.Background()
	i, err := a.pluginStore.Installation(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.prepareCleanInvalidSeedTransition(ctx, i, "init")
	if err != nil {
		t.Fatal(err)
	}
	defer p.release()
	raw, _ := json.Marshal(config)
	if err = p.validateConfig(ctx, raw, "init"); err != nil {
		_ = p.resolve(ctx, false)
		t.Fatal(err)
	}
	old, err := a.pluginStore.Config(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c, err := a.pluginStore.saveValueTx(ctx, tx, instance, "config", "main", old.Revision, raw)
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.enqueueRunTx(ctx, tx, instance, "init", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.publishTx(ctx, tx, seedhost.Binding{InstanceID: instance, Generation: i.Generation, Revision: c.Revision}, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = p.resolve(ctx, true); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestPluginCleanInvalidSeedOwnerActualSDKDisabledAndOnlyOnceAtomicScope(t *testing.T) {
	a := newTestApp(t)
	a.config = Settings{}
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), map[string]any{"enabled": false, "onlyonce": true}, true)
	queued, err := a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "run", nil)
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), run.Input...)
	queuedInput := append([]byte(nil), queued.Input...)
	got := cleanSeedOwnerInvoke(t, a, run)
	if got.State != "succeeded" {
		t.Fatal(got.State, got.ErrorCode)
	}
	c, err := a.pluginStore.Config(context.Background(), run.InstanceID)
	if err != nil || c.Revision != 2 || !bytes.Contains(c.Value, []byte(`"onlyonce":false`)) {
		t.Fatal("onlyonce flag", c.Revision, err)
	}
	actual, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || actual.ConfigRevision != 1 || !bytes.Equal(actual.Input, original) {
		t.Fatal("running original admission changed", err)
	}
	actual, err = a.pluginStore.Run(context.Background(), queued.ID)
	if err != nil || actual.ConfigRevision != 2 || !bytes.Equal(actual.Input, queuedInput) {
		t.Fatal("queued manual scope/input lost", err)
	}
	o := a.pluginCleanInvalidSeed.owners[run.InstanceID]
	s, err := o.j.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var date seedhost.Trigger
	for _, tr := range s.Triggers {
		if tr.Kind == "once" {
			date = tr
		}
	}
	if date.ID == "" || date.Binding.Revision != 2 || len(s.ConfigHistory) != 1 {
		t.Fatal("missing atomic source date/flag", date)
	}
	var definitions int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND group_key='clean_seed' AND config_revision=2 AND active=1`, run.InstanceID).Scan(&definitions); err != nil || definitions != 1 {
		t.Fatal("real source plan was not projected", definitions, err)
	}
	if len(a.pluginProcess) != 0 {
		t.Fatal("process slot leaked into owner")
	}
}

func TestPluginCleanInvalidSeedOwnerConfigPrefixSurvivesLaterNoticeFailure(t *testing.T) {
	a := newTestApp(t)
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(nil), true)
	if got := cleanSeedOwnerInvoke(t, a, run); got.State != "succeeded" {
		t.Fatal(got.State)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_second_seed_notice BEFORE INSERT ON records WHEN NEW.kind='notification' AND (SELECT COUNT(*) FROM records WHERE kind='notification')>=1 BEGIN SELECT RAISE(ABORT,'synthetic second notice failure'); END`); err != nil {
		t.Fatal(err)
	}
	queued, err := a.pluginStore.enqueueCleanInvalidSeedCommand(context.Background(), run.InstanceID, map[string]any{"action": "toggle_notify_all", "channel": "Web", "user": "owned-user"})
	if err != nil {
		t.Fatal(err)
	}
	got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if got.ID != queued.ID || got.State != "failed" || got.ErrorCode != "CLEAN_SEED_SOURCE_FAILED" {
		t.Fatal(got.State, got.ErrorCode)
	}
	c, err := a.pluginStore.Config(context.Background(), run.InstanceID)
	if err != nil || c.Revision != 2 || !bytes.Contains(c.Value, []byte(`"notify_all":true`)) {
		t.Fatal("committed source config prefix lost", c.Revision, err)
	}
	var notices, outbox int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='notification'`).Scan(&notices)
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_clean_seed_outbox`).Scan(&outbox)
	if notices != 1 || outbox != 1 {
		t.Fatal("accepted prefix was erased or failed notice accepted", notices, outbox)
	}
	result, code, found, err := a.pluginStore.cleanInvalidSeedDeferredOutcome(context.Background(), got, seed.ExecuteCapability)
	if err != nil || !found || code != "CLEAN_SEED_SOURCE_FAILED" || len(result) == 0 {
		t.Fatal("durable known failure", code, found, err)
	}
}

type cleanSeedOwnerDaemon struct {
	mu          sync.Mutex
	server      *httptest.Server
	rows        []map[string]any
	deletes     int
	failWrite   bool
	beforeWrite func()
}

func cleanSeedOwnerDaemonNew(t *testing.T) *cleanSeedOwnerDaemon {
	d := &cleanSeedOwnerDaemon{}
	d.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		reply := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned-fixture", Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/info":
			rows := []map[string]any{}
			for _, row := range d.rows {
				if hash := r.URL.Query().Get("hashes"); hash == "" || row["hash"] == hash {
					rows = append(rows, row)
				}
			}
			reply(rows)
		case "/api/v2/torrents/trackers":
			status, message := 4, "torrent not registered with this tracker"
			if r.URL.Query().Get("hash") == strings.Repeat("b", 40) {
				status, message = 2, ""
			}
			reply([]any{map[string]any{"url": "https://tracker.fixture/announce?passkey=synthetic-private", "status": status, "msg": message, "tier": 0}})
		case "/api/v2/torrents/delete":
			_ = r.ParseForm()
			if r.Form.Get("deleteFiles") != "false" {
				http.Error(w, "owned data deletion refused", 400)
				return
			}
			if d.beforeWrite != nil {
				d.beforeWrite()
			}
			d.deletes++
			if d.failWrite {
				http.Error(w, "synthetic ambiguous ACK", 500)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(d.server.Close)
	return d
}
func (d *cleanSeedOwnerDaemon) settings(a *App) {
	s := a.Settings()
	s.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-qb", Kind: "qbittorrent", BaseURL: d.server.URL, Username: "owned", Password: "synthetic-private"}, Name: "Owned qB"}}
	a.cfgMu.Lock()
	a.config = s
	a.cfgMu.Unlock()
}
func TestPluginCleanInvalidSeedOwnerNativeKeepDataUnknownDoesNotReplay(t *testing.T) {
	a := newTestApp(t)
	d := cleanSeedOwnerDaemonNew(t)
	d.settings(a)
	path := filepath.Join(t.TempDir(), "keep-data.mkv")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	d.rows = []map[string]any{{"hash": strings.Repeat("a", 40), "name": "owned", "content_path": path, "save_path": filepath.Dir(path), "category": "movies", "tags": "", "state": "uploading", "size": 4, "total_size": 4, "completed": 4, "progress": 1, "ratio": 1}}
	d.rows = append(d.rows, map[string]any{"hash": strings.Repeat("b", 40), "name": "working-same-tracker", "content_path": path + "-good", "save_path": filepath.Dir(path), "category": "movies", "tags": "", "state": "uploading", "size": 4, "total_size": 4, "completed": 4, "progress": 1, "ratio": 1})
	d.failWrite = true
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"delete_invalid_torrents": true}), true)
	cleanSeedOwnerInvoke(t, a, run)
	queued, err := a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "run", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if got.ID != queued.ID || got.State != "reconcile_pending" {
		t.Fatal(got.State, got.ErrorCode)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("keep-data source lost", err)
	}
	d.mu.Lock()
	count := d.deletes
	d.mu.Unlock()
	if count != 1 {
		state, readErr := a.pluginCleanInvalidSeed.owners[run.InstanceID].j.Read(context.Background())
		if readErr == nil {
			for _, x := range state.Runs {
				t.Log("journal run", x.Phase, x.ErrorCode)
			}
			for _, e := range state.Effects {
				t.Log("journal effect", e.Kind, e.Phase, e.Operation, e.ErrorCode)
			}
		}
		t.Fatal("native mutation not exercised", count)
	}
	if _, err = a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "run", nil); err != nil {
		t.Fatal(err)
	}
	blocked := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if blocked.State != "reconcile_pending" {
		t.Fatal("unknown state was bypassed", blocked.State)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.deletes != 1 {
		t.Fatal("unknown mutation replayed", d.deletes)
	}
}

func TestPluginCleanInvalidSeedOwnerNativeConfirmedReceiptAndLateCoreFailure(t *testing.T) {
	for _, failCore := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "late_core_receipt_failure"}[failCore], func(t *testing.T) {
			a := newTestApp(t)
			d := cleanSeedOwnerDaemonNew(t)
			d.settings(a)
			row := func(hash string) map[string]any {
				return map[string]any{"hash": hash, "name": "owned", "content_path": "/not-used-for-zero-day-keep-data", "category": "movies", "tags": "", "state": "uploading", "size": 4, "total_size": 4, "completed": 4, "progress": 1, "ratio": 1}
			}
			d.rows = []map[string]any{row(strings.Repeat("a", 40)), row(strings.Repeat("b", 40))}
			run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"delete_invalid_torrents": true}), true)
			cleanSeedOwnerInvoke(t, a, run)
			if failCore {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_seed_native_core BEFORE INSERT ON plugin_clean_seed_core_receipts BEGIN SELECT RAISE(ABORT,'synthetic post-ACK core receipt failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "run", nil); err != nil {
				t.Fatal(err)
			}
			got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
			want := "succeeded"
			if failCore {
				want = "reconcile_pending"
			}
			if got.State != want {
				t.Fatal(got.State, got.ErrorCode)
			}
			d.mu.Lock()
			count := d.deletes
			d.mu.Unlock()
			if count != 1 {
				t.Fatal("actual keep-data POST was not exercised exactly once", count)
			}
			state, err := a.pluginCleanInvalidSeed.owners[run.InstanceID].j.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range state.Effects {
				if e.RunID == got.ID && e.Kind == "native_write" {
					if failCore {
						if e.Phase != "started" {
							t.Fatal("post-ACK SQL failure lost exact started intent", e.Phase)
						}
					} else {
						if e.Phase != "confirmed" {
							t.Fatal("ACK not checkpointed", e.Phase)
						}
						tx, err := a.Store.DB.BeginTx(context.Background(), nil)
						if err != nil {
							t.Fatal(err)
						}
						inv, _ := cleanSeedInvocation(got)
						err = cleanSeedReadEffectReceipt(context.Background(), tx, a.pluginCleanInvalidSeed.owners[run.InstanceID].j, inv, e)
						_ = tx.Rollback()
						if err != nil {
							t.Fatal("real ACK missing same-tx Core receipt", err)
						}
					}
				}
			}
		})
	}
}

func TestPluginCleanInvalidSeedOwnerManualServiceHasNoMisfireAndPreservesDateAnchor(t *testing.T) {
	a := newTestApp(t)
	d := cleanSeedOwnerDaemonNew(t)
	d.settings(a)
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"onlyonce": true}), true)
	cleanSeedOwnerInvoke(t, a, run)
	o := a.pluginCleanInvalidSeed.owners[run.InstanceID]
	state, err := o.j.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var plan seedhost.Trigger
	for _, tr := range state.Triggers {
		if tr.Kind == "once" {
			plan = tr
		}
	}
	if plan.ID == "" {
		t.Fatal("source date plan missing")
	}
	serviceID := cleanSeedServiceID(plan.ID)
	var sealed []byte
	var originalDue string
	if err = a.Store.DB.QueryRow(`SELECT definition,next_due FROM plugin_schedules WHERE instance_id=? AND service_id=?`, run.InstanceID, serviceID).Scan(&sealed, &originalDue); err != nil {
		t.Fatal(err)
	}
	raw, err := a.pluginStore.unseal("schedule", run.InstanceID, serviceID, sealed)
	if err != nil {
		t.Fatal(err)
	}
	var def pluginScheduleDefinition
	if cleanupJournalStrict(raw, &def) != nil {
		t.Fatal("definition")
	}
	ctx := context.Background()
	at := time.Now().Add(-10 * time.Minute).UTC()
	manual, err := a.pluginStore.enqueueManualPluginService(ctx, run.InstanceID, serviceID, at)
	if err != nil {
		t.Fatal(err)
	}
	var occurrenceID string
	if err = a.Store.DB.QueryRow(`SELECT id FROM plugin_schedule_occurrences WHERE run_id=?`, manual.ID).Scan(&occurrenceID); err != nil {
		t.Fatal(err)
	}
	got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if got.State != "succeeded" {
		t.Fatal("delayed manual service incorrectly expired", got.State, got.ErrorCode)
	}
	var due, phase string
	if err = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=? AND service_id=?`, run.InstanceID, serviceID).Scan(&due); err != nil || due != originalDue {
		t.Fatal("manual service consumed date anchor", due, err)
	}
	if err = a.Store.DB.QueryRow(`SELECT state FROM plugin_schedule_occurrences WHERE id=?`, occurrenceID).Scan(&phase); err != nil || phase != "succeeded" {
		t.Fatal("actual manual occurrence not finished", phase, err)
	}
}

func cleanSeedOwnerPlan(t *testing.T, a *App, instance, kind string) seedhost.Trigger {
	t.Helper()
	s, err := a.pluginCleanInvalidSeed.owners[instance].j.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found seedhost.Trigger
	for _, tr := range s.Triggers {
		if tr.Kind == kind && tr.Phase == "ready" {
			if found.ID != "" {
				t.Fatal("ambiguous source plan")
			}
			found = tr
		}
	}
	if found.ID == "" {
		t.Fatal("missing ready source plan", kind)
	}
	return found
}

func TestPluginCleanInvalidSeedOwnerAutomaticProducerAtomicDateCronAndDispatchGrace(t *testing.T) {
	for _, mode := range []string{"once", "cron", "dispatch_missed", "enqueue_rollback"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			d := cleanSeedOwnerDaemonNew(t)
			d.settings(a)
			var clock atomic.Int64
			clock.Store(time.Date(2026, 10, 17, 1, 2, 10, 0, time.UTC).UnixNano())
			a.pluginCleanInvalidSeed.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
			cfg := cleanSeedOwnerConfig(map[string]any{"onlyonce": true, "cron": ""})
			kind := "once"
			if mode == "cron" {
				cfg["onlyonce"], cfg["cron"], kind = false, "* * * * *", "service"
			}
			run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cfg, true)
			if got := cleanSeedOwnerInvoke(t, a, run); got.State != "succeeded" {
				t.Fatal(got.State, got.ErrorCode)
			}
			plan := cleanSeedOwnerPlan(t, a, run.InstanceID, kind)
			at := plan.Due.Add(200 * time.Millisecond)
			clock.Store(at.UnixNano())
			ctx := context.Background()
			if mode == "enqueue_rollback" {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_seed_enqueue BEFORE UPDATE ON plugin_clean_seed_meta BEGIN SELECT RAISE(ABORT,'synthetic enqueue transaction failure'); END`); err != nil {
					t.Fatal(err)
				}
				if n, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at); err == nil || n != 0 {
					t.Fatal("source event transaction unexpectedly committed", n, err)
				}
				var events, occurrences int
				var due string
				_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND action='event'`, run.InstanceID).Scan(&events)
				_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE instance_id=?`, run.InstanceID).Scan(&occurrences)
				if err := a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=? AND service_id=?`, run.InstanceID, cleanSeedServiceID(plan.ID)).Scan(&due); err != nil || due != schedule.FormatTime(plan.Due) || events != 0 || occurrences != 0 {
					t.Fatal("failed enqueue consumed source time or leaked execution", events, occurrences, due, err)
				}
				if _, err := a.Store.DB.Exec(`DROP TRIGGER fail_seed_enqueue`); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at); err != nil || n != 1 {
				t.Fatal("real common scheduler did not enqueue source event", n, err)
			}
			queued := cleanSeedOwnerClaim(t, a, run.InstanceID)
			if queued.Action != "event" {
				t.Fatal("automatic producer bypassed source event", queued.Action)
			}
			if mode == "dispatch_missed" {
				clock.Store(plan.Due.Add(2 * time.Second).UnixNano())
			}
			got := cleanSeedOwnerInvoke(t, a, queued)
			if got.State != "succeeded" {
				t.Fatal("automatic source invocation", got.State, got.ErrorCode)
			}
			var occurrence string
			if err := a.Store.DB.QueryRow(`SELECT state FROM plugin_schedule_occurrences WHERE run_id=? AND manual=0`, got.ID).Scan(&occurrence); err != nil {
				t.Fatal(err)
			}
			want := "succeeded"
			if mode == "dispatch_missed" {
				want = "missed"
			}
			if occurrence != want {
				t.Fatal("actual automatic occurrence", occurrence, want)
			}
			s, err := a.pluginCleanInvalidSeed.owners[run.InstanceID].j.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "dispatch_missed" {
				for _, e := range s.Effects {
					if e.RunID == got.ID {
						t.Fatal("expired source event performed an effect", e.Kind)
					}
				}
			}
			var next sql.NullString
			if err = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=? AND service_id=?`, run.InstanceID, cleanSeedServiceID(plan.ID)).Scan(&next); err != nil {
				t.Fatal(err)
			}
			if mode == "cron" {
				if !next.Valid || next.String != schedule.FormatTime(plan.Due.Add(time.Minute)) {
					t.Fatal("cron anchor lost", next)
				}
			} else if next.Valid {
				t.Fatal("once date unexpectedly repeated", next)
			}
			if n, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at); err != nil || n != 0 {
				t.Fatal("same due instant duplicated source event", n, err)
			}
		})
	}
}

func TestPluginCleanInvalidSeedOwnerInternalConfigRevisionMigratesOnlyPendingSourceOccurrence(t *testing.T) {
	a := newTestApp(t)
	d := cleanSeedOwnerDaemonNew(t)
	d.settings(a)
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 17, 1, 2, 10, 0, time.UTC).UnixNano())
	a.pluginCleanInvalidSeed.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	initial := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"cron": "* * * * *"}), true)
	cleanSeedOwnerInvoke(t, a, initial)
	ctx := context.Background()
	command, err := a.pluginStore.enqueueCleanInvalidSeedCommand(ctx, initial.InstanceID, map[string]any{"action": "toggle_notify_all", "channel": "Web", "user": "owned"})
	if err != nil {
		t.Fatal(err)
	}
	command = cleanSeedOwnerClaim(t, a, initial.InstanceID)
	plan := cleanSeedOwnerPlan(t, a, initial.InstanceID, "service")
	at := plan.Due.Add(200 * time.Millisecond)
	clock.Store(at.UnixNano())
	if n, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var pendingID string
	if err = a.Store.DB.QueryRow(`SELECT run_id FROM plugin_schedule_occurrences WHERE instance_id=? AND state='queued'`, initial.InstanceID).Scan(&pendingID); err != nil {
		t.Fatal(err)
	}
	pending, err := a.pluginStore.Run(ctx, pendingID)
	if err != nil {
		t.Fatal(err)
	}
	beforeInput := append([]byte(nil), pending.Input...)
	if got := cleanSeedOwnerInvoke(t, a, command); got.State != "succeeded" {
		t.Fatal("source command", got.State, got.ErrorCode)
	}
	after, err := a.pluginStore.Run(ctx, pendingID)
	if err != nil || after.ConfigRevision != pending.ConfigRevision+1 || after.State != "pending" || !bytes.Equal(after.Input, beforeInput) {
		t.Fatal("private pending source admission did not preserve original input", after.ConfigRevision, after.State, err)
	}
	var revision int64
	if err = a.Store.DB.QueryRow(`SELECT config_revision FROM plugin_schedule_occurrences WHERE run_id=?`, pendingID).Scan(&revision); err != nil || revision != after.ConfigRevision {
		t.Fatal("actual queued occurrence not rebound atomically", revision, err)
	}
	got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, initial.InstanceID))
	if got.ID != pendingID || got.State != "succeeded" {
		t.Fatal("pending source event was stranded by internal revision", got.ID, got.State, got.ErrorCode)
	}
	actual, err := a.pluginStore.Run(ctx, command.ID)
	if err != nil || actual.ConfigRevision != command.ConfigRevision || !bytes.Equal(actual.Input, command.Input) {
		t.Fatal("running original command admission was rewritten", err)
	}
}

func TestPluginCleanInvalidSeedOwnerRealSourceDeletionKeepsMediaAndMediaDeletionPreservesIndex(t *testing.T) {
	a := newTestApp(t)
	d := cleanSeedOwnerDaemonNew(t)
	d.settings(a)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, library := filepath.Join(base, "source"), filepath.Join(base, "library")
	for _, p := range []string{source, library} {
		if err = os.Mkdir(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	src, target := filepath.Join(source, "owned.mkv"), filepath.Join(library, "owned.mkv")
	if err = os.WriteFile(src, []byte("owned-hardlink-video"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(src, target); err != nil {
		t.Fatal(err)
	}
	snap, err := snapshotPath(target)
	if err != nil {
		t.Fatal(err)
	}
	asset := Asset{ID: "owned-asset", Media: catalog.Media{MediaType: "movie", ID: 1}, Path: target, RootID: "owned-library", State: "available"}
	if err = a.Store.Put(context.Background(), "asset", asset.ID, asset); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Put(context.Background(), "managed_file", tokenHash(target), managedFile{Path: target, RootID: asset.RootID, AssetID: asset.ID, MediaKey: mediaKey(asset.Media), Kind: "video", Snapshot: snap, SourceSnapshot: snap}); err != nil {
		t.Fatal(err)
	}
	s := a.Settings()
	s.AllowedRoots = []string{base}
	s.Roots = []Root{{ID: asset.RootID, Path: library, Kind: "local"}}
	a.cfgMu.Lock()
	a.config = s
	a.cfgMu.Unlock()
	cfg := cleanSeedOwnerConfig(map[string]any{"detect_invalid_files": true, "delete_invalid_files": true, "download_dirs": source + ":" + source})
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cfg, true)
	cleanSeedOwnerInvoke(t, a, run)
	if _, err = a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "run", nil); err != nil {
		t.Fatal(err)
	}
	got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if got.State != "succeeded" {
		t.Fatal("source cleanup", got.State, got.ErrorCode)
	}
	if _, err = os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source was not removed", err)
	}
	if _, err = os.Stat(target); err != nil {
		t.Fatal("surviving hardlink target lost", err)
	}
	var current Asset
	if err = a.Store.Get(context.Background(), "asset", asset.ID, &current); err != nil || current.State != "available" {
		t.Fatal("source-only deletion marked media missing", current.State, err)
	}
	cfg["download_dirs"] = library + ":" + library
	cleanSeedOwnerSave(t, a, run.InstanceID, cfg)
	cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if _, err = a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "run", nil); err != nil {
		t.Fatal(err)
	}
	got = cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if got.State != "succeeded" {
		t.Fatal("media cleanup", got.State, got.ErrorCode)
	}
	if _, err = os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("media was not removed", err)
	}
	if err = a.Store.Get(context.Background(), "asset", asset.ID, &current); err != nil || current.State != "missing_pending" {
		t.Fatal("confirmed removal not projected", current.State, err)
	}
	var own managedFile
	if err = a.Store.Get(context.Background(), "managed_file", tokenHash(target), &own); err != nil || own.AssetID != asset.ID {
		t.Fatal("audit ownership record was deleted", err)
	}
}

func TestPluginCleanInvalidSeedOwnerPreflightAndPublicationRollbackKeepOldSchedulesUsable(t *testing.T) {
	a := newTestApp(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.config = Settings{AllowedRoots: []string{base}}
	cfg := cleanSeedOwnerConfig(map[string]any{"download_dirs": base + ":" + base})
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cfg, true)
	if got := cleanSeedOwnerInvoke(t, a, run); got.State != "succeeded" {
		t.Fatal(got.State)
	}
	ctx := context.Background()
	i, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.prepareCleanInvalidSeedTransition(ctx, i, "init")
	if err != nil {
		t.Fatal(err)
	}
	badConfig := cleanSeedOwnerConfig(map[string]any{"download_dirs": filepath.Join(base, "does-not-exist") + ":/owned"})
	raw, _ := json.Marshal(badConfig)
	if err = p.validateConfig(ctx, raw, "init"); err == nil {
		t.Fatal("nonexistent cleanup directory was published")
	}
	if err = p.resolve(ctx, false); err != nil {
		t.Fatal(err)
	}
	p.release()
	var active int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`, run.InstanceID).Scan(&active); err != nil || active != 1 {
		t.Fatal("preflight abort did not resume old real schedule", active, err)
	}
	p, err = a.prepareCleanInvalidSeedTransition(ctx, i, "init")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER fail_seed_publication BEFORE UPDATE ON plugin_clean_seed_meta BEGIN SELECT RAISE(ABORT,'synthetic late publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	old, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := a.pluginStore.saveValueTx(ctx, tx, run.InstanceID, "config", "main", old.Revision, old.Value)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := a.pluginStore.enqueueRunTx(ctx, tx, run.InstanceID, "init", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.publishTx(ctx, tx, seedhost.Binding{InstanceID: run.InstanceID, Generation: run.Generation, Revision: next.Revision}, queued.ID); err == nil {
		t.Fatal("late publication unexpectedly committed")
	}
	_ = tx.Rollback()
	if _, err = a.Store.DB.Exec(`DROP TRIGGER fail_seed_publication`); err != nil {
		t.Fatal(err)
	}
	if err = p.resolve(ctx, false); err != nil {
		t.Fatal(err)
	}
	p.release()
	after, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil || after.Revision != old.Revision || !bytes.Equal(after.Value, old.Value) {
		t.Fatal("public config escaped transaction rollback", err)
	}
	if _, err = a.pluginStore.Run(ctx, queued.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed publication leaked actual SDK task", err)
	}
	cleanSeedOwnerSave(t, a, run.InstanceID, cfg)
	if got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID)); got.State != "succeeded" {
		t.Fatal("corrected normal save remained blocked", got.State, got.ErrorCode)
	}
}

func TestPluginCleanInvalidSeedOwnerCleanStopReopenUsesFreshInitAndCron(t *testing.T) {
	data := t.TempDir()
	a, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.config = Settings{}
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(nil), true)
	cleanSeedOwnerInvoke(t, a, run)
	a.stopCleanInvalidSeedPlugins()
	o := a.pluginCleanInvalidSeed.owners[run.InstanceID]
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s, m, err := o.j.read(context.Background(), tx)
	_ = tx.Rollback()
	if err != nil || !m.Closed {
		t.Fatal("clean StopService not checkpointed", err)
	}
	for _, tr := range s.Triggers {
		if tr.Phase == "ready" {
			t.Fatal("clean stop kept source scheduler running")
		}
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	i, err := a.pluginStore.Installation(context.Background(), run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.pluginStore.Config(context.Background(), run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	initialize, err := a.shouldInitializeCleanInvalidSeed(context.Background(), i, c)
	if err != nil || !initialize {
		t.Fatal("clean stop suppressed required source init", initialize, err)
	}
	if _, err = a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "init", nil); err != nil {
		t.Fatal(err)
	}
	got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if got.State != "succeeded" {
		t.Fatal("fresh source object did not re-register cron", got.State, got.ErrorCode)
	}
	var active int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`, run.InstanceID).Scan(&active); err != nil || active != 1 {
		t.Fatal("cron not restored after clean stop", active, err)
	}
}

func TestPluginCleanInvalidSeedOwnerInternalConfigCrashReopenPreservesAppliedObjectAndOnceDeadline(t *testing.T) {
	data := t.TempDir()
	a, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a != nil {
			_ = a.Close()
		}
	})
	d := cleanSeedOwnerDaemonNew(t)
	d.settings(a)
	sealedSettings, err := a.Store.Seal(a.Settings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`INSERT INTO records VALUES('config','main',?,?)`, sealedSettings, now()); err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 17, 1, 2, 10, 0, time.UTC).UnixNano())
	nativeNow := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	a.pluginCleanInvalidSeed.now = nativeNow
	initial := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"enabled": false, "onlyonce": true, "cron": ""}), true)
	if got := cleanSeedOwnerInvoke(t, a, initial); got.State != "succeeded" {
		t.Fatal(got.State, got.ErrorCode)
	}
	plan := cleanSeedOwnerPlan(t, a, initial.InstanceID, "once")
	var originalDue string
	if err = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=? AND service_id=?`, initial.InstanceID, cleanSeedServiceID(plan.ID)).Scan(&originalDue); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.enqueueCleanInvalidSeedCommand(context.Background(), initial.InstanceID, map[string]any{"action": "toggle_notify_all", "channel": "Web", "user": "owned"}); err != nil {
		t.Fatal(err)
	}
	command := cleanSeedOwnerClaim(t, a, initial.InstanceID)
	if got := cleanSeedOwnerInvoke(t, a, command); got.State != "succeeded" {
		t.Fatal("internal config source command", got.State, got.ErrorCode)
	}
	i, err := a.pluginStore.Installation(context.Background(), initial.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.pluginStore.Config(context.Background(), initial.InstanceID)
	if err != nil || c.Revision != 3 {
		t.Fatal("source configuration chain", c.Revision, err)
	}
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, meta, err := a.pluginCleanInvalidSeed.owners[initial.InstanceID].j.read(context.Background(), tx)
	_ = tx.Rollback()
	if err != nil || meta.AppliedRun != initial.ID || meta.AppliedBinding != (seedhost.Binding{InstanceID: i.InstanceID, Generation: i.Generation, Revision: c.Revision}) || meta.Closed {
		t.Fatal("internal publication did not atomically preserve applied object identity", err)
	}
	// This App was never Start'ed: close its actual logs/SQLite resources without
	// invoking source StopService, modelling lost process memory rather than a
	// graceful plugin shutdown that is supposed to cancel source schedules.
	a.logs.close()
	if err = a.Store.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a, err = New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	a.pluginCleanInvalidSeed.now = nativeNow
	i, err = a.pluginStore.Installation(context.Background(), initial.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	c, err = a.pluginStore.Config(context.Background(), initial.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	initialize, err := a.shouldInitializeCleanInvalidSeed(context.Background(), i, c)
	if err != nil || initialize {
		t.Fatal("internal revision caused an accidental fresh init after crash", initialize, err)
	}
	if err = a.initializeNativePlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	var initCount int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND action IN ('init','reload')`, initial.InstanceID).Scan(&initCount); err != nil || initCount != 1 {
		t.Fatal("startup enqueued an init that would erase the pending once date", initCount, err)
	}
	var due string
	if err = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=? AND service_id=? AND active=1`, initial.InstanceID, cleanSeedServiceID(plan.ID)).Scan(&due); err != nil || due != originalDue {
		t.Fatal("persisted once deadline moved across cold reopen", due, err)
	}
	clock.Store(plan.Due.Add(200 * time.Millisecond).UnixNano())
	if n, err := a.pluginStore.enqueueDuePluginSchedules(context.Background(), nativeNow()); err != nil || n != 1 {
		t.Fatal("retained original once date could not enqueue", n, err)
	}
	got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, initial.InstanceID))
	if got.State != "succeeded" || got.Action != "event" {
		t.Fatal("retained date did not execute original source action", got.State, got.ErrorCode)
	}
	var onceCount int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE instance_id=? AND state='succeeded'`, initial.InstanceID).Scan(&onceCount); err != nil || onceCount != 1 {
		t.Fatal("retained once action did not complete exactly once", onceCount, err)
	}
}
