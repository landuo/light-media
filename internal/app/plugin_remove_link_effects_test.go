package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

type removeLinkEffectsFixture struct {
	a        *App
	j        *pluginRemoveLinkJournal
	e        *pluginRemoveLinkEffects
	b        cleanuphost.Binding
	run      PluginRun
	roots    []cleanuphost.RootStamp
	config   json.RawMessage
	history  transferHistoryItem
	eventRun string
	watcher  *cleanupEffectsIdleWatcher
}

func removeLinkEffectsSetup(t *testing.T, a *App, item transferHistoryItem, config map[string]any) *removeLinkEffectsFixture {
	t.Helper()
	ctx := context.Background()
	config["enabled"] = true
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.InstallAndQueue(ctx, PluginInstallation{InstanceID: "RemoveLink", PluginID: "RemoveLink", Version: "2.17", ManifestSchemaVersion: 1, Manifest: json.RawMessage(`{"plugin_id":"RemoveLink","name":"fixture","runtime":"go-executable"}`)}, json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	run, err = a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := cleanuphost.Binding{InstanceID: run.InstanceID, Generation: run.Generation, Revision: run.ConfigRevision}
	j, err := newPluginRemoveLinkJournal(ctx, a.pluginStore, b.InstanceID, b.Generation)
	if err != nil {
		t.Fatal(err)
	}
	f := &removeLinkEffectsFixture{a: a, j: j, b: b, run: run, config: raw, history: item}
	mediaRoot := a.Settings().Roots[0]
	for _, r := range []cleanuphost.Root{{ID: "source", Path: filepath.Dir(item.ResourceSource), Role: "source"}, {ID: mediaRoot.ID, Path: mediaRoot.Path, Role: "media"}} {
		stamp, err := cleanupEffectStamp(r.Path)
		if err != nil {
			t.Fatal(err)
		}
		f.roots = append(f.roots, cleanuphost.RootStamp{Root: r, File: stamp})
	}
	f.e, err = newPluginRemoveLinkEffects(ctx, a, j)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func removeLinkEffectsNew(t *testing.T, config map[string]any) *removeLinkEffectsFixture {
	t.Helper()
	a, item := historyRemovalFixture(t)
	return removeLinkEffectsSetup(t, a, item, config)
}
func (f *removeLinkEffectsFixture) pin(t *testing.T, path string) cleanuphost.Pin {
	t.Helper()
	r, err := cleanupEffectRoot(f.roots, path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := cleanupEffectStamp(path)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := cleanupEffectStamp(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	return cleanuphost.Pin{RootID: r.Root.ID, ParentPath: filepath.Dir(path), Parent: parent, File: file, Epoch: 1}
}
func (f *removeLinkEffectsFixture) prepare(t *testing.T, path, kind string) cleanuphost.Effect {
	t.Helper()
	r, err := cleanupEffectRoot(f.roots, path)
	if err != nil {
		t.Fatal(err)
	}
	x := cleanuphost.Effect{ID: id("effect_"), Scope: "fixture-timer", Kind: kind, Path: path, Role: r.Root.Role, Binding: f.b, Payload: json.RawMessage(`null`), State: "planned"}
	if kind == "notification" {
		x.Payload = json.RawMessage(`{"title":"private notice title","text":"private notice body"}`)
	} else {
		x.Expected = f.pin(t, path)
	}
	if kind == "download_file_deleted" {
		x.Payload, _ = json.Marshal(map[string]string{"src": path})
	}
	if err = f.j.Update(context.Background(), func(s *cleanuphost.Durable) error {
		s.Binding = f.b
		s.Roots = f.roots
		s.Config = f.config
		s.Active = &cleanuphost.Event{ID: x.Scope, Kind: "timer", At: time.Now()}
		if s.Effects == nil {
			s.Effects = map[string]cleanuphost.Effect{}
		}
		s.Effects[x.ID] = x
		if s.Frozen == nil {
			s.Frozen = map[string]cleanuphost.Pin{}
		}
		if kind != "notification" {
			s.Frozen[path] = x.Expected
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return x
}
func (f *removeLinkEffectsFixture) authorize(t *testing.T, ctx context.Context) func() {
	t.Helper()
	release, err := f.e.authorize(ctx, f.b, "timer", "", f.roots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return release
}
func cleanupEffectsCount(t *testing.T, a *App, table string) int {
	t.Helper()
	var n int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestPluginRemoveLinkEffectsHistoryAtomicStableExecutionAndReplyRecovery(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"delete_history": true})
	ctx := context.Background()
	x := f.prepare(t, f.history.Source, "history_src")
	f.authorize(t, ctx)
	var before []byte
	if err := f.a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='import_plan' AND id=?`, f.history.PlanID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.Store.DB.Exec(`CREATE TRIGGER fail_cleanup_receipt BEFORE INSERT ON plugin_cleanup_core_receipts BEGIN SELECT RAISE(FAIL,'fixture receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.Commit(ctx, x); err == nil {
		t.Fatal("receipt failure returned success")
	}
	if cleanupEffectsCount(t, f.a, "plugin_cleanup_core_receipts") != 0 {
		t.Fatal("partial receipt")
	}
	var n int
	_ = f.a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='transfer_history_hidden'`).Scan(&n)
	if n != 0 {
		t.Fatal("history hide escaped receipt rollback")
	}
	if _, err := f.a.Store.DB.Exec(`DROP TRIGGER fail_cleanup_receipt`); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.e.Commit(ctx, x); err != nil || !ok {
		t.Fatal(ok, err)
	}
	var hidden struct {
		FileID       string `json:"file_id"`
		ExecutionKey string `json:"execution_key"`
	}
	if err := f.a.Store.Get(ctx, "transfer_history_hidden", f.history.ID, &hidden); err != nil || hidden.FileID != f.history.executionFileID() || hidden.ExecutionKey != f.history.ExecutionKey {
		t.Fatal(hidden, err)
	}
	// A lost host reply can mark its journal uncertain after our committed SQL.
	if err := f.j.Update(ctx, func(s *cleanuphost.Durable) error {
		p := s.Effects[x.ID]
		p.State = "uncertain"
		s.Effects[x.ID] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.e.Commit(ctx, x); err != nil || !ok {
		t.Fatal("durable receipt could not recover reply loss", ok, err)
	}
	if cleanupEffectsCount(t, f.a, "plugin_cleanup_core_receipts") != 1 {
		t.Fatal("replay created another receipt")
	}
	var after []byte
	_ = f.a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='import_plan' AND id=?`, f.history.PlanID).Scan(&after)
	if !bytes.Equal(before, after) {
		t.Fatal("import audit mutated")
	}
	for _, p := range []string{f.history.Source, f.history.Target} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("history effect deleted file", err)
		}
	}
}

func TestPluginRemoveLinkEffectsOutboxesAreSealedAtomicAndNeverDelivered(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"notify": true, "delete_torrents": true})
	ctx := context.Background()
	x := f.prepare(t, f.roots[0].Root.Path, "notification")
	f.authorize(t, ctx)
	if _, err := f.a.Store.DB.Exec(`CREATE TRIGGER fail_outbox_receipt BEFORE INSERT ON plugin_cleanup_core_receipts BEGIN SELECT RAISE(FAIL,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.Commit(ctx, x); err == nil {
		t.Fatal("outbox rollback failure accepted")
	}
	if cleanupEffectsCount(t, f.a, "plugin_cleanup_private_outbox") != 0 {
		t.Fatal("outbox escaped transaction")
	}
	_, _ = f.a.Store.DB.Exec(`DROP TRIGGER fail_outbox_receipt`)
	if ok, err := f.e.Commit(ctx, x); err != nil || !ok {
		t.Fatal(ok, err)
	}
	y := f.prepare(t, f.history.Source, "download_file_deleted")
	if ok, err := f.e.Commit(ctx, y); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := f.e.Commit(ctx, y); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if cleanupEffectsCount(t, f.a, "plugin_cleanup_private_outbox") != 2 {
		t.Fatal("outbox duplicate or missing")
	}
	rows, err := f.a.Store.DB.Query(`SELECT token,sealed FROM plugin_cleanup_private_outbox`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var token string
		var sealed []byte
		if err = rows.Scan(&token, &sealed); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(sealed, []byte("private notice")) || bytes.Contains(sealed, []byte(f.history.Source)) {
			t.Fatal("private outbox leaked plaintext")
		}
		raw, err := f.a.pluginStore.unseal("cleanup_journal", f.b.InstanceID, f.e.aad("outbox", token), sealed)
		if err != nil {
			t.Fatal(err)
		}
		var box pluginRemoveLinkPrivateOutbox
		if json.Unmarshal(raw, &box) != nil || box.State != "pending" {
			t.Fatal("outbox falsely delivered", string(raw))
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var n int
	_ = f.a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&n)
	if n != 0 {
		t.Fatal("notification sent/claimed delivered")
	}
}

type cleanupUncomparableContext struct {
	context.Context
	extra []int
}

func TestPluginRemoveLinkEffectsRequireLeaseFullPlanAndCurrentInstallation(t *testing.T) {
	for _, which := range []string{"no_lease", "different_context", "cancelled", "uncomparable", "released", "revision", "generation", "version", "flag", "payload", "pin", "scope", "unplanned", "uncertain", "settings_root"} {
		t.Run(which, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"delete_history": true})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			x := f.prepare(t, f.history.Source, "history_src")
			if which == "uncomparable" {
				if _, err := f.e.authorize(cleanupUncomparableContext{ctx, []int{1}}, f.b, "timer", "", f.roots); err == nil {
					t.Fatal("uncomparable context accepted")
				}
				return
			}
			if which != "no_lease" {
				release := f.authorize(t, ctx)
				if which == "released" {
					release()
				}
			}
			switch which {
			case "different_context":
				ctx = context.WithValue(ctx, struct{}{}, "other")
			case "cancelled":
				cancel()
			case "revision":
				_, _ = f.a.pluginStore.SaveConfig(context.Background(), f.b.InstanceID, f.b.Revision, map[string]any{"delete_history": true})
			case "generation":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_installations SET generation=generation+1 WHERE instance_id=?`, f.b.InstanceID)
			case "version":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_installations SET version='other' WHERE instance_id=?`, f.b.InstanceID)
			case "flag":
				_ = f.j.Update(ctx, func(s *cleanuphost.Durable) error { s.Config = json.RawMessage(`{"delete_history":false}`); return nil })
			case "payload":
				x.Payload = json.RawMessage(`{}`)
			case "pin":
				x.Expected.File.Inode++
			case "scope":
				x.Scope = "different"
			case "unplanned":
				_ = f.j.Update(ctx, func(s *cleanuphost.Durable) error { delete(s.Effects, x.ID); return nil })
			case "uncertain":
				_ = f.j.Update(ctx, func(s *cleanuphost.Durable) error {
					p := s.Effects[x.ID]
					p.State = "uncertain"
					s.Effects[x.ID] = p
					return nil
				})
			case "settings_root":
				f.a.cfgMu.Lock()
				f.a.config.Roots[0].Path = f.roots[1].Root.Path + "-new"
				f.a.cfgMu.Unlock()
			}
			if ok, err := f.e.Commit(ctx, x); err == nil || ok {
				t.Fatal("forged/stale effect accepted", ok, err)
			}
			if cleanupEffectsCount(t, f.a, "plugin_cleanup_core_receipts") != 0 {
				t.Fatal("rejected effect recorded")
			}
		})
	}
}

func TestPluginRemoveLinkEffectsConfirmedParentRemovalNeedsFreshRmdirChain(t *testing.T) {
	for _, proof := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_parent_receipt", true: "confirmed_parent_receipt"}[proof], func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{})
			ctx := context.Background()
			f.prepare(t, f.history.Source, "history_src")
			filePin := f.pin(t, f.history.Target)
			parentPath := filePin.ParentPath
			parentPin := f.pin(t, parentPath)
			f.authorize(t, ctx)
			if err := os.Remove(f.history.Target); err != nil {
				t.Fatal(err)
			}
			f.fileFact(t, f.history.Target, filePin)
			if err := os.Remove(parentPath); err != nil {
				t.Fatal("fixture parent was not empty", err)
			}
			if proof {
				fact := f.fileFact(t, parentPath, parentPin)
				if err := f.j.Update(ctx, func(s *cleanuphost.Durable) error { fact.Kind = "rmdir"; s.Effects[fact.ID] = fact; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			err := f.e.ReconcileConfirmedFiles(ctx)
			if (err == nil) != proof {
				t.Fatal("wrong parent authorization", err)
			}
			var plan ImportPlan
			_ = f.a.Store.Get(ctx, "import_plan", f.history.PlanID, &plan)
			var asset Asset
			_ = f.a.Store.Get(ctx, "asset", plan.Files[0].AssetID, &asset)
			if (asset.State == "missing_pending") != proof {
				t.Fatal("unproven parent absence changed asset", asset.State)
			}
		})
	}
}

func (f *removeLinkEffectsFixture) fileFact(t *testing.T, path string, pin cleanuphost.Pin) cleanuphost.Effect {
	t.Helper()
	r, err := cleanupEffectRoot(f.roots, path)
	if err != nil {
		t.Fatal(err)
	}
	x := cleanuphost.Effect{ID: id("confirmed_file_"), Scope: "fixture-timer", Kind: "unlink", Path: path, Role: r.Root.Role, Expected: pin, Binding: f.b, State: "confirmed"}
	if err = f.j.Update(context.Background(), func(s *cleanuphost.Durable) error { s.Effects[x.ID] = x; return nil }); err != nil {
		t.Fatal(err)
	}
	return x
}
func TestPluginRemoveLinkEffectsConfirmedFilesKeepSlotsAndAuditAndRejectOldFacts(t *testing.T) {
	for _, which := range []string{"new_media", "source_only", "replacement", "old_fact", "new_owner", "root_symlink", "receipt_failure"} {
		t.Run(which, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{})
			ctx := context.Background()
			f.prepare(t, f.history.Source, "history_src")
			path := f.history.Target
			if which == "source_only" {
				path = f.history.Source
			}
			pin := f.pin(t, path)
			var plan ImportPlan
			_ = f.a.Store.Get(ctx, "import_plan", f.history.PlanID, &plan)
			assetID := plan.Files[0].AssetID
			var slotsBefore int
			_ = f.a.Store.DB.QueryRow(`SELECT count(*) FROM asset_slots WHERE asset_id=?`, assetID).Scan(&slotsBefore)
			if which == "old_fact" {
				_ = os.Remove(path)
				f.fileFact(t, path, pin)
			}
			f.authorize(t, ctx)
			if which != "old_fact" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				f.fileFact(t, path, pin)
			}
			if which == "replacement" {
				importWrite(t, path, []byte("new owner file"))
			}
			if which == "new_owner" {
				var own managedFile
				_ = f.a.Store.Get(ctx, "managed_file", tokenHash(path), &own)
				own.AssetID = "different"
				own.Snapshot.Inode++
				_ = f.a.Store.Put(ctx, "managed_file", tokenHash(path), own)
			}
			if which == "root_symlink" {
				root := f.roots[1].Root.Path
				renamed := root + "-replaced"
				if err := os.Rename(root, renamed); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(renamed, root); err != nil {
					t.Fatal(err)
				}
			}
			if which == "receipt_failure" {
				_, _ = f.a.Store.DB.Exec(`CREATE TRIGGER fail_file_receipt BEFORE INSERT ON plugin_cleanup_core_receipts BEGIN SELECT RAISE(FAIL,'failure'); END`)
			}
			err := f.e.ReconcileConfirmedFiles(ctx)
			wantErr := which == "replacement" || which == "old_fact" || which == "root_symlink" || which == "receipt_failure"
			if (err != nil) != wantErr {
				t.Fatal("reconcile result", err)
			}
			var asset Asset
			if err := f.a.Store.Get(ctx, "asset", assetID, &asset); err != nil {
				t.Fatal(err)
			}
			if (asset.State == "missing_pending") != (which == "new_media") {
				t.Fatal("wrong missing owner/state", asset.State)
			}
			var slotsAfter int
			_ = f.a.Store.DB.QueryRow(`SELECT count(*) FROM asset_slots WHERE asset_id=?`, assetID).Scan(&slotsAfter)
			if slotsBefore == 0 || slotsBefore != slotsAfter {
				t.Fatal("slot policy changed", slotsBefore, slotsAfter)
			}
			var own managedFile
			if err := f.a.Store.Get(ctx, "managed_file", tokenHash(f.history.Target), &own); err != nil {
				t.Fatal("managed audit lost", err)
			}
			if _, err := f.a.transferHistoryItem(ctx, f.history.ID); err != nil {
				t.Fatal("index reconciliation changed history", err)
			}
			if which == "new_media" {
				if err := f.e.ReconcileConfirmedFiles(ctx); err != nil {
					t.Fatal("reconcile not idempotent", err)
				}
			}
		})
	}
}

type cleanupEffectsIdleWatcher struct {
	events chan cleanuphost.Event
	errs   chan error
	once   sync.Once
}

func (w *cleanupEffectsIdleWatcher) Events() <-chan cleanuphost.Event { return w.events }
func (w *cleanupEffectsIdleWatcher) Errors() <-chan error             { return w.errs }
func (w *cleanupEffectsIdleWatcher) Close() error {
	w.once.Do(func() { close(w.events); close(w.errs) })
	return nil
}
func (f *removeLinkEffectsFixture) openHost(t *testing.T, now func() time.Time) *cleanuphost.Host {
	t.Helper()
	var cfg map[string]any
	_ = json.Unmarshal(f.config, &cfg)
	cfg["monitor_dirs"] = f.roots[0].Root.Path + "\n" + f.roots[1].Root.Path
	f.config, _ = json.Marshal(cfg)
	if _, err := f.a.pluginStore.SaveConfig(context.Background(), f.b.InstanceID, f.b.Revision, f.config); err != nil {
		t.Fatal(err)
	}
	stored, _ := f.a.pluginStore.Config(context.Background(), f.b.InstanceID)
	f.b.Revision = stored.Revision
	_, _ = f.a.Store.DB.Exec(`UPDATE plugin_runs SET config_revision=? WHERE id=?`, f.b.Revision, f.run.ID)
	opts := cleanuphost.Options{Binding: f.b, Roots: []cleanuphost.Root{f.roots[0].Root, f.roots[1].Root}, Journal: f.j, Effects: f.e, AfterAction: f.e.afterAction, MediaExtensions: []string{".mkv"}, Watch: func(context.Context, []removelink.Watch, func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
		f.watcher = &cleanupEffectsIdleWatcher{events: make(chan cleanuphost.Event), errs: make(chan error)}
		return f.watcher, nil
	}}
	opts.Guard = func(ctx context.Context, b cleanuphost.Binding, action string) (func(), error) {
		f.a.pluginLifecycleMu.Lock()
		f.a.downloadMu.Lock()
		f.a.importMu.Lock()
		runID := ""
		if action == "configure" {
			runID = f.run.ID
		}
		if action == "apply" {
			runID = f.eventRun
		}
		release, err := f.e.authorize(ctx, b, action, runID, f.roots)
		if err != nil {
			f.a.importMu.Unlock()
			f.a.downloadMu.Unlock()
			f.a.pluginLifecycleMu.Unlock()
			return nil, err
		}
		return func() {
			release()
			f.a.importMu.Unlock()
			f.a.downloadMu.Unlock()
			f.a.pluginLifecycleMu.Unlock()
		}, nil
	}
	opts.Now = now
	h, err := cleanuphost.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = h.Configure(ctx, f.b, f.config); err != nil {
		_ = h.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Suspend(context.Background()) })
	return h
}
func TestPluginRemoveLinkEffectsActualHostDeletesAndReconcilesAllFlagsFalse(t *testing.T) {
	for _, flags := range []bool{false, true} {
		t.Run(map[bool]string{false: "all_optional_false", true: "history_events_notice"}[flags], func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"delete_history": flags, "delete_torrents": flags, "notify": flags, "delayed_deletion": false})
			h := f.openHost(t, nil)
			ctx := context.Background()
			var err error
			if err = os.Remove(f.history.Source); err != nil {
				t.Fatal(err)
			}
			event := cleanuphost.Event{ID: "owned-external-delete", Kind: "deleted", Path: f.history.Source, Mode: "hardlink", At: time.Now()}
			if flags {
				eventInput, _ := json.Marshal(map[string]string{"event_id": event.ID})
				run, runErr := f.a.pluginStore.EnqueueRun(ctx, f.b.InstanceID, "event", eventInput)
				if runErr != nil {
					t.Fatal(runErr)
				}
				if _, runErr = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); runErr != nil {
					t.Fatal(runErr)
				}
				f.eventRun = run.ID
				if err = h.RecordEvent(ctx, event); err == nil {
					err = h.Apply(ctx, f.b, event.ID)
				}
			} else {
				err = h.Observe(ctx, event)
			}
			if err != nil {
				_ = h.Close(ctx)
				t.Fatal("actual host", err)
			}
			if _, err = os.Stat(f.history.Target); !os.IsNotExist(err) {
				t.Fatal("follower not actually deleted", err)
			}
			var plan ImportPlan
			_ = f.a.Store.Get(ctx, "import_plan", f.history.PlanID, &plan)
			var asset Asset
			_ = f.a.Store.Get(ctx, "asset", plan.Files[0].AssetID, &asset)
			if asset.State != "missing_pending" {
				t.Fatal("real deleted asset not reconciled", asset.State)
			}
			if flags {
				if cleanupEffectsCount(t, f.a, "plugin_cleanup_private_outbox") != 3 {
					t.Fatal("real event/notice outbox missing")
				}
				if _, err = f.a.transferHistoryItem(ctx, f.history.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("history not hidden", err)
				}
			} else {
				if cleanupEffectsCount(t, f.a, "plugin_cleanup_private_outbox") != 0 {
					t.Fatal("disabled effects enqueued")
				}
			}
			if err = h.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPluginRemoveLinkEffectsReorganizationAliasIsFrozenAncestry(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "proven", true: "wrong_snapshot"}[corrupt], func(t *testing.T) {
			a, source, old := reorgFixture(t)
			ctx := context.Background()
			first := historyParityReadPage(t, a, "").Items[0]
			for n := 0; n < 2; n++ {
				target := filepath.Join(filepath.Dir(filepath.Dir(old.Path)), "effect-reorg-"+strings.Repeat("x", n+1))
				if err := os.MkdirAll(target, 0750); err != nil {
					t.Fatal(err)
				}
				scrape := false
				plan, err := a.previewImport(ctx, ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: old.Media.MediaType, TMDBID: old.Media.ID, TargetPath: target, Mode: "copy", Scraping: &scrape, Files: []ImportFileRequest{{Path: old.Path}}, HistorySource: source})
				if err != nil {
					t.Fatal(err)
				}
				job, err := a.executeImport(ctx, plan.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				reorgRunJob(t, a, job)
				reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", plan.ID))
				if err = a.assetAtPath(ctx, plan.Files[0].Target, &old); err != nil {
					t.Fatal(err)
				}
			}
			latest, err := a.transferHistoryItem(ctx, first.ID)
			if err != nil {
				t.Fatal(err)
			}
			f := removeLinkEffectsSetup(t, a, latest, map[string]any{"delete_history": true})
			x := f.prepare(t, source, "history_src")
			f.authorize(t, ctx)
			if corrupt {
				x.Expected.File.Inode++
				_ = f.j.Update(ctx, func(s *cleanuphost.Durable) error { s.Effects[x.ID] = x; s.Frozen[source] = x.Expected; return nil })
			}
			ok, err := f.e.Commit(ctx, x)
			if corrupt {
				if err == nil || ok {
					t.Fatal("alias wrong snapshot accepted", ok, err)
				}
			} else {
				if err != nil || !ok {
					t.Fatal("proven multi-copy alias rejected", ok, err)
				}
				var hidden struct {
					FileID       string `json:"file_id"`
					ExecutionKey string `json:"execution_key"`
				}
				if err = a.Store.Get(ctx, "transfer_history_hidden", first.ID, &hidden); err != nil || hidden.FileID != latest.executionFileID() || hidden.ExecutionKey != latest.ExecutionKey {
					t.Fatal(hidden, err)
				}
			}
		})
	}
}

func TestPluginRemoveLinkEffectsFailedSubmittedHistoryNeedsNoAsset(t *testing.T) {
	a, source, _ := importTestApp(t)
	importFixture(t, a, "movie")
	ctx := context.Background()
	path := filepath.Join(source, "Submitted.But.Not.Imported.2024.mkv")
	importWrite(t, path, []byte("submitted failure fixture"))
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='failed',error_message='fixture before any publication' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	item, err := a.transferHistoryItem(ctx, plan.Files[0].ID)
	if err != nil || item.Status != "failed" {
		t.Fatal(item, err)
	}
	f := removeLinkEffectsSetup(t, a, item, map[string]any{"delete_history": true})
	x := f.prepare(t, path, "history_src")
	f.authorize(t, ctx)
	if ok, err := f.e.Commit(ctx, x); err != nil || !ok {
		t.Fatal("failed execution with exact source was not hidden", ok, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("history removal touched source", err)
	}
	var n int
	_ = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&n)
	if n != 0 {
		t.Fatal("history deletion invented asset")
	}
}

func TestPluginRemoveLinkEffectsReceiptRestartAndConcurrentReplay(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"delete_history": true})
	ctx := context.Background()
	x := f.prepare(t, f.history.Source, "history_src")
	release := f.authorize(t, ctx)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := f.e.Commit(ctx, x)
			if err == nil && !ok {
				err = errors.New("false history result")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if cleanupEffectsCount(t, f.a, "plugin_cleanup_core_receipts") != 1 {
		t.Fatal("concurrent reply retries duplicated receipt")
	}
	release()
	dir := f.a.Dir
	if err := f.a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	j, err := newPluginRemoveLinkJournal(ctx, reopened.pluginStore, f.b.InstanceID, f.b.Generation)
	if err != nil {
		t.Fatal(err)
	}
	e, err := newPluginRemoveLinkEffects(ctx, reopened, j)
	if err != nil {
		t.Fatal(err)
	}
	// Recovery reads the durable core receipt directly; it does not execute a
	// restored Active action, and startup cannot fabricate a new write lease.
	r, err := e.receipt(ctx, reopened.Store.DB, x)
	if err != nil || !r.Result || r.FileID != f.history.executionFileID() {
		t.Fatal("receipt restart lost exact execution", r, err)
	}
	if _, err = e.Commit(ctx, x); err == nil {
		t.Fatal("restart implicitly authorized replay")
	}
}

func TestPluginRemoveLinkEffectsAfterActionSQLFailureBlocksActualHostAndLifecycle(t *testing.T) {
	for _, action := range []string{"event", "native_observer", "apply", "timer", "configure", "stop", "retirement_drain", "reload"} {
		t.Run(action, func(t *testing.T) {
			delayed := action != "event" && action != "native_observer" && action != "apply"
			f := removeLinkEffectsNew(t, map[string]any{"delete_history": false, "delete_torrents": false, "notify": false, "delayed_deletion": delayed, "delay_seconds": 30})
			var clock atomic.Int64
			clock.Store(time.Now().UnixNano())
			h := f.openHost(t, func() time.Time { return time.Unix(0, clock.Load()) })
			ctx := context.Background()
			if err := os.Remove(f.history.Source); err != nil {
				t.Fatal(err)
			}
			event := cleanuphost.Event{ID: "sql-failure-bound-event", Kind: "deleted", Path: f.history.Source, Mode: "hardlink", At: time.Unix(0, clock.Load())}
			if delayed {
				if err := h.Observe(ctx, event); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(f.history.Target); err != nil {
					t.Fatal("queue did not retain target", err)
				}
			}
			if action == "apply" {
				raw, _ := json.Marshal(map[string]string{"event_id": event.ID})
				run, err := f.a.pluginStore.EnqueueRun(ctx, f.b.InstanceID, "event", raw)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
				f.eventRun = run.ID
				if err = h.RecordEvent(ctx, event); err != nil {
					t.Fatal(err)
				}
			}
			var ticket cleanuphost.TransitionTicket
			if action == "retirement_drain" || action == "reload" {
				kind := "uninstall"
				if action == "reload" {
					kind = "reload"
				}
				var err error
				ticket, err = h.BeginTransition(ctx, f.b, "actual-sql-failure-"+kind, kind)
				if err != nil {
					t.Fatal(err)
				}
				if action == "reload" {
					run, err := f.a.pluginStore.EnqueueRun(ctx, f.b.InstanceID, "reload", nil)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
						t.Fatal(err)
					}
					f.run = run
					if err = f.j.Update(ctx, func(s *cleanuphost.Durable) error { return cleanuphost.PublishTransition(s, ticket, f.b, run.ID) }); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := f.a.Store.DB.Exec(`CREATE TRIGGER fail_actual_cleanup_core_receipt BEFORE INSERT ON plugin_cleanup_core_receipts BEGIN SELECT RAISE(FAIL,'owned actual core receipt failure'); END`); err != nil {
				t.Fatal(err)
			}
			var err error
			switch action {
			case "event":
				err = h.Observe(ctx, event)
			case "native_observer":
				select {
				case f.watcher.events <- event:
				case <-time.After(3 * time.Second):
					t.Fatal("observer did not receive fixture event")
				}
				deadline := time.NewTimer(3 * time.Second)
				defer deadline.Stop()
				poll := time.NewTicker(10 * time.Millisecond)
				defer poll.Stop()
				waiting := true
				for waiting {
					state, readErr := f.j.Read(ctx)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if state.Blocked != "" {
						waiting = false
						break
					}
					select {
					case <-deadline.C:
						t.Fatal("asynchronous core failure was not blocked")
					case <-poll.C:
					}
				}
				err = h.Tick(ctx)
			case "apply":
				err = h.Apply(ctx, f.b, event.ID)
			case "timer":
				clock.Add(int64(31 * time.Second))
				err = h.Tick(ctx)
			case "configure":
				err = h.Configure(ctx, f.b, f.config)
			case "stop":
				err = h.Stop(ctx, "actual-core-failure")
			case "retirement_drain":
				err = h.DrainTransition(ctx, ticket)
			case "reload":
				err = h.ApplyTransition(ctx, ticket, f.run.ID, f.config)
			}
			if err == nil {
				t.Fatal("actual core SQL failure returned success")
			}
			state, readErr := f.j.Read(ctx)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if state.Active == nil || state.Blocked == "" {
				t.Fatal("SQL failure lost exact active scope", state.Active, state.Blocked)
			}
			if _, err = os.Stat(f.history.Target); !os.IsNotExist(err) {
				t.Fatal("fixture did not reach file deletion", err)
			}
			for _, x := range state.Effects {
				if x.Kind == "lifecycle_applied" {
					t.Fatal("SQL rollback emitted lifecycle success")
				}
			}
			if cleanupEffectsCount(t, f.a, "plugin_cleanup_core_receipts") != 0 {
				t.Fatal("failed SQL persisted receipt")
			}
			var plan ImportPlan
			_ = f.a.Store.Get(ctx, "import_plan", f.history.PlanID, &plan)
			var asset Asset
			_ = f.a.Store.Get(ctx, "asset", plan.Files[0].AssetID, &asset)
			if asset.State == "missing_pending" {
				t.Fatal("index update escaped failed receipt transaction")
			}
			if action == "retirement_drain" && state.Transition.Phase != "prepared" {
				t.Fatal("failed drain allowed final publication")
			}
			if action == "reload" && state.Transition.Phase != "published" {
				t.Fatal("failed reload falsely applied")
			}
			if err = h.Tick(ctx); !errors.Is(err, cleanuphost.ErrUncertain) && !errors.Is(err, cleanuphost.ErrTransition) {
				t.Fatal("host replayed failed core effect", err)
			}
			if err = h.Suspend(ctx); err != nil {
				t.Fatal(err)
			}
			reopened, err := cleanuphost.Open(cleanuphost.Options{Binding: f.b, Roots: []cleanuphost.Root{f.roots[0].Root, f.roots[1].Root}, Journal: f.j, Effects: f.e, Guard: func(context.Context, cleanuphost.Binding, string) (func(), error) { return func() {}, nil }, AfterAction: f.e.afterAction})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Suspend(ctx)
			if err = reopened.Recover(ctx, f.b); !errors.Is(err, cleanuphost.ErrUncertain) && !errors.Is(err, cleanuphost.ErrTransition) {
				t.Fatal("restart blindly replayed failed effect", err)
			}
		})
	}
}

func TestPluginRemoveLinkEffectsApplyRequiresActualBoundEventInvocation(t *testing.T) {
	for _, which := range []string{"missing_run", "wrong_action", "wrong_event", "terminal_run"} {
		t.Run(which, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"delete_history": true})
			ctx := context.Background()
			x := f.prepare(t, f.history.Source, "history_src")
			if err := f.j.Update(ctx, func(s *cleanuphost.Durable) error {
				event := cleanuphost.Event{ID: x.Scope, Kind: "deleted", Path: x.Path, Mode: "hardlink", At: time.Now()}
				s.Active = &event
				s.Events = map[string]cleanuphost.EventReceipt{x.Scope: {Event: event, Binding: f.b, State: "planned", Sequence: 1}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			runID := ""
			if which == "wrong_action" {
				runID = f.run.ID
			} else if which != "missing_run" {
				id := x.Scope
				if which == "wrong_event" {
					id = "different-persisted-event"
				}
				raw, _ := json.Marshal(map[string]string{"event_id": id})
				run, err := f.a.pluginStore.EnqueueRun(ctx, f.b.InstanceID, "event", raw)
				if err != nil {
					t.Fatal(err)
				}
				state := "running"
				if which == "terminal_run" {
					state = "succeeded"
				}
				if _, err = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state=? WHERE id=?`, state, run.ID); err != nil {
					t.Fatal(err)
				}
				runID = run.ID
			}
			release, err := f.e.authorize(ctx, f.b, "apply", runID, f.roots)
			if err == nil {
				defer release()
				if ok, commitErr := f.e.Commit(ctx, x); commitErr == nil || ok {
					t.Fatal("apply accepted unbound actual event", ok, commitErr)
				}
			}
			if cleanupEffectsCount(t, f.a, "plugin_cleanup_core_receipts") != 0 {
				t.Fatal("rejected event wrote receipt")
			}
		})
	}
}

func removeLinkEffectsRepeatedReorganization(t *testing.T, repeats int) (*removeLinkEffectsFixture, transferHistoryItem, ImportPlan, []ImportPlan, Job) {
	t.Helper()
	ctx := context.Background()
	a, source, old := reorgFixture(t)
	first := historyParityReadPage(t, a, "").Items[0]
	var original ImportPlan
	if err := a.Store.Get(ctx, "import_plan", first.PlanID, &original); err != nil {
		t.Fatal(err)
	}
	var repeated []ImportPlan
	var lastJob Job
	for n := 0; n < repeats; n++ {
		plan, err := a.previewImport(ctx, ImportRequest{MediaType: old.Media.MediaType, TMDBID: old.Media.ID, RootID: old.RootID, Mode: "copy", Files: []ImportFileRequest{{Path: source}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Files) != 1 || plan.Files[0].Status != "already_imported" || plan.Files[0].AssetID != old.ID {
			t.Fatal("actual repeat did not reuse published owner", plan.Files)
		}
		job, err := a.executeImport(ctx, plan.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		reorgRunJob(t, a, job)
		repeated = append(repeated, plan)
		lastJob = job
	}
	target := filepath.Join(filepath.Dir(filepath.Dir(old.Path)), "repeat-effect-new-library")
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	scrape := false
	plan, err := a.previewImport(ctx, ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: old.Media.MediaType, TMDBID: old.Media.ID, TargetPath: target, Mode: "copy", Scraping: &scrape, Files: []ImportFileRequest{{Path: old.Path}}, HistorySource: source})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", plan.ID))
	latest, err := a.transferHistoryItem(ctx, first.ID)
	if err != nil || latest.PlanID != plan.ID || latest.ID != first.ID {
		t.Fatal("actual reorg lost stable history", latest, err)
	}
	return removeLinkEffectsSetup(t, a, latest, map[string]any{"delete_history": true}), first, original, repeated, lastJob
}

func TestPluginRemoveLinkEffectsRepeatedAlreadyImportedAncestryRequiresEquivalentBranches(t *testing.T) {
	for _, which := range []string{"equivalent", "foreign_resource_filtered", "foreign_target_filtered", "wrong_frozen_snapshot", "conflicting_source", "broken_shared_ancestry", "future_execution_filtered", "cycle_stack", "shared_budget"} {
		t.Run(which, func(t *testing.T) {
			repeats := 1
			if which == "shared_budget" {
				repeats = 127
			}
			f, first, original, repeated, lastJob := removeLinkEffectsRepeatedReorganization(t, repeats)
			ctx := context.Background()
			latest := f.history
			x := f.prepare(t, latest.ResourceSource, "history_src")
			f.authorize(t, ctx)
			repeat := repeated[0]
			switch which {
			case "foreign_resource_filtered":
				repeat.Files[0].HistorySource = "/synthetic-other-resource"
				if err := f.a.Store.Put(ctx, "import_plan", repeat.ID, repeat); err != nil {
					t.Fatal(err)
				}
			case "foreign_target_filtered":
				repeat.Files[0].Target += "-unrelated"
				if err := f.a.Store.Put(ctx, "import_plan", repeat.ID, repeat); err != nil {
					t.Fatal(err)
				}
			case "wrong_frozen_snapshot":
				var meta importMetadata
				_ = f.a.Store.Get(ctx, "import_meta", latest.PlanID, &meta)
				meta.Reorganize.Video.SourceSnapshot.Inode++
				if err := f.a.Store.Put(ctx, "import_meta", latest.PlanID, meta); err != nil {
					t.Fatal(err)
				}
			case "conflicting_source":
				repeat.Files[0].HistorySource = latest.ResourceSource
				repeat.Files[0].Source += "-different"
				if err := f.a.Store.Put(ctx, "import_plan", repeat.ID, repeat); err != nil {
					t.Fatal(err)
				}
			case "broken_shared_ancestry":
				for _, p := range []ImportPlan{original, repeat} {
					p.Files[0].HistorySource = latest.ResourceSource
					p.Files[0].Source = latest.Source
					if err := f.a.Store.Put(ctx, "import_plan", p.ID, p); err != nil {
						t.Fatal(err)
					}
				}
			case "future_execution_filtered":
				if _, err := f.a.Store.DB.Exec(`UPDATE jobs SET created_at='2099-01-01T00:00:00Z' WHERE id=?`, lastJob.ID); err != nil {
					t.Fatal(err)
				}
			}
			if which == "future_execution_filtered" || which == "cycle_stack" || which == "foreign_resource_filtered" {
				// Probe the selected immutable execution's proof independently of
				// history ranking: a deliberately future ancestor would itself be the
				// current visible history, and must not be used to prove this past row.
				var plan ImportPlan
				if err := f.a.Store.Get(ctx, "import_plan", latest.PlanID, &plan); err != nil {
					t.Fatal(err)
				}
				tx, err := f.a.Store.DB.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				stack := map[string]bool{}
				if which == "cycle_stack" {
					stack[plan.ID+"/"+plan.Files[0].ID] = true
				}
				err = f.e.sourceProof(ctx, tx, plan, plan.Files[0], x.Path, cleanupEffectSnapshot(x.Expected.File), latest.ExecutionKey, latest.ResourceSource, stack)
				_ = tx.Rollback()
				if (err != nil) != (which == "cycle_stack") {
					t.Fatal("wrong ancestor fence/cycle proof", err)
				}
				if which != "foreign_resource_filtered" {
					return
				}
				// Its proof is excluded correctly, but a foreign resource
				// grouping still exposes another visible row with this same
				// Source. The public history mutation must reject ambiguity.
			}
			ok, err := f.e.Commit(ctx, x)
			wantErr := which == "foreign_resource_filtered" || which == "wrong_frozen_snapshot" || which == "conflicting_source" || which == "broken_shared_ancestry" || which == "shared_budget"
			if (err != nil) != wantErr || ok == wantErr {
				t.Fatal("wrong repeated ancestry result", ok, err)
			}
			if wantErr {
				if cleanupEffectsCount(t, f.a, "plugin_cleanup_core_receipts") != 0 {
					t.Fatal("rejected chain persisted receipt")
				}
				var n int
				_ = f.a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='transfer_history_hidden'`).Scan(&n)
				if n != 0 {
					t.Fatal("failed proof hid history")
				}
			} else {
				var hidden struct {
					FileID       string `json:"file_id"`
					ExecutionKey string `json:"execution_key"`
				}
				if err = f.a.Store.Get(ctx, "transfer_history_hidden", first.ID, &hidden); err != nil || hidden.FileID != latest.executionFileID() || hidden.ExecutionKey != latest.ExecutionKey {
					t.Fatal("wrong actual execution hidden after repeat", hidden, err)
				}
			}
		})
	}
}
