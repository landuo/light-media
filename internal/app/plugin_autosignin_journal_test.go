package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/autosignin"
	signinjournal "light-media/internal/plugins/autosignin/durable"
)

const signinSQLImplementation = "synthetic-fixed-implementation-sha"

type signinSQLFixture struct {
	a       *App
	o       *pluginAutoSignInOwner
	b       *pluginAutoSignInBackend
	j       *signinjournal.StateJournal
	run     PluginRun
	release func()
}

func signinSQLSetup(t *testing.T, configs ...map[string]any) *signinSQLFixture {
	t.Helper()
	s, p := pluginStoreFixture(t)
	a := &App{Store: s, pluginStore: p, Dir: filepath.Dir(s.lock.Name()), autoSignInEnvironment: func() []string { return nil }}
	manifest, _ := json.Marshal(autosignin.Manifest())
	config := map[string]any{"enabled": false, "onlyonce": false, "sign_sites": []any{}, "login_sites": []any{}}
	if len(configs) > 0 {
		config = configs[0]
	}
	run, err := p.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: "AutoSignIn", PluginID: "AutoSignIn", Version: autosignin.Version, ManifestSchemaVersion: 1, Manifest: manifest}, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	run, err = p.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	o, err := claimPluginAutoSignInOwner(context.Background(), a, run.InstanceID, signinSQLImplementation, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	b, err := o.backend(&run)
	if err != nil {
		t.Fatal(err)
	}
	j, err := signinjournal.NewStateJournal(b)
	if err != nil {
		t.Fatal(err)
	}
	f := &signinSQLFixture{a: a, o: o, b: b, j: j, run: run}
	t.Cleanup(func() {
		if f.release != nil {
			f.release()
			f.release = nil
		}
		_ = f.o.Close()
	})
	return f
}
func (f *signinSQLFixture) execute(t *testing.T) {
	t.Helper()
	var err error
	f.release, err = f.j.AcquireExecution(context.Background(), f.o.binding)
	if err != nil {
		t.Fatal(err)
	}
}
func (f *signinSQLFixture) start(t *testing.T) {
	t.Helper()
	f.execute(t)
	state, err := f.j.State(context.Background(), f.o.binding)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.j.Start(context.Background(), f.o.binding, f.run.ID, "init", json.RawMessage(`{}`), signinjournal.Baseline{Runtime: state.Runtime, History: json.RawMessage(`{}`), Inventory: json.RawMessage(`{}`), Config: state.Config, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
}
func (f *signinSQLFixture) state(t *testing.T) signinjournal.State {
	t.Helper()
	s, err := f.j.State(context.Background(), f.o.binding)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func signinSQLRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPluginAutoSignInSQLConfigReceiptsPreserveCASAndOriginalRunAdmission(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	ctx := context.Background()
	raw := json.RawMessage(`{"enabled":false,"onlyonce":false,"sign_sites":[],"login_sites":[],"queue_cnt":5}`)
	e := signinjournal.Effect{RunID: f.run.ID, Lane: "main", Kind: "config", Digest: "synthetic-original-effect", Value: raw, Mutation: signinjournal.Mutation{Kind: "config", Value: raw}}
	if _, err := f.j.Apply(ctx, f.o.binding, e); err != nil {
		t.Fatal(err)
	}
	c, err := f.a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil || c.Revision != 2 || !bytes.Equal(c.Value, raw) {
		t.Fatal("core config was not atomically projected with a new public revision", c, err)
	}
	if _, err = f.a.pluginStore.SaveConfig(ctx, "AutoSignIn", 1, map[string]any{"stale": true}); pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
		t.Fatal("stale form overwrote the internal source update", err)
	}
	if f.o.binding.Revision != 1 || f.b.run.ConfigRevision != 1 {
		t.Fatal("immutable admission identity changed")
	}
	if _, err = f.j.Apply(ctx, f.o.binding, e); err != nil {
		t.Fatal(err)
	}
	c, _ = f.a.pluginStore.Config(ctx, "AutoSignIn")
	if c.Revision != 2 {
		t.Fatal("receipt replay saved config twice")
	}
	e.Index = 1
	e.Digest = "second-identical-config-save"
	if _, err = f.j.Apply(ctx, f.o.binding, e); err != nil {
		t.Fatal(err)
	}
	c, _ = f.a.pluginStore.Config(ctx, "AutoSignIn")
	if c.Revision != 3 {
		t.Fatal("new same-value source save did not advance CAS")
	}
	if _, err = f.j.State(ctx, f.o.binding); err != nil {
		t.Fatal("owner rejected its own exact config receipt chain", err)
	}
	forged := f.run
	forged.ConfigRevision = 3
	b, _ := f.o.backend(&forged)
	if _, err = b.AcquireExecution(ctx, f.o.binding); !errors.Is(err, signinjournal.ErrBusy) {
		t.Fatal("a parallel forged admission acquired execution", err)
	}
	f.release()
	f.release = nil
	if _, err = b.AcquireExecution(ctx, f.o.binding); !errors.Is(err, signinjournal.ErrStale) {
		t.Fatal("worker-like revision substitution admitted", err)
	}
	if err = f.o.Close(); err != nil {
		t.Fatal(err)
	}
	o, err := claimPluginAutoSignInOwner(ctx, f.a, "AutoSignIn", signinSQLImplementation, "UTC")
	if err != nil {
		t.Fatal("new native owner could not validate its predecessor's config chain", err)
	}
	f.o = o
	if o.binding.Revision != 1 || o.binding.Epoch == f.b.owner.binding.Epoch {
		t.Fatal("owner claim rewrote admission or reused epoch")
	}
}

func TestPluginAutoSignInSQLProjectionFailureRollsBackEffectAndPublicConfig(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	ctx := context.Background()
	before := f.state(t)
	if _, err := f.a.Store.DB.Exec(`CREATE TRIGGER signin_config_failure BEFORE INSERT ON plugin_signin_config_receipts BEGIN SELECT RAISE(ABORT,'synthetic private receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"enabled":false,"onlyonce":false}`)
	_, err := f.j.Apply(ctx, f.o.binding, signinjournal.Effect{RunID: f.run.ID, Lane: "main", Kind: "config", Digest: "config-write", Value: raw, Mutation: signinjournal.Mutation{Kind: "config", Value: raw}})
	if err == nil {
		t.Fatal("synthetic SQL failure was hidden")
	}
	after := f.state(t)
	cfg, err := f.a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil || cfg.Revision != 1 || !reflect.DeepEqual(before, after) {
		t.Fatal("journal/core config escaped rollback", err)
	}
}

func TestPluginAutoSignInSQLIntentCapacityIsReservedBeforeAdmission(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	ctx := context.Background()
	f.o.testCapacity = 128 << 10
	f.o.testReserve = 96 << 10
	if err := f.b.Update(ctx, f.o.binding, func(s *signinjournal.State) error {
		s.Cache["owned-fill"] = bytes.Repeat([]byte{'x'}, 32<<10)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := f.state(t)
	_, _, err := f.j.BeginNetwork(ctx, f.o.binding, signinjournal.NetworkIntent{RunID: f.run.ID, Lane: "site", Kind: "http", Digest: "never-submit", Guard: "site-action"})
	if !errors.Is(err, signinjournal.ErrCapacity) {
		t.Fatal("request intent exceeded reserved receipt capacity", err)
	}
	after := f.state(t)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("denied network admission changed state")
	}
}

func TestPluginAutoSignInSQLClaimAndSameOwnerRecoveryFenceUncertainOperations(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	ctx := context.Background()
	n := signinjournal.NetworkIntent{RunID: f.run.ID, Lane: "site", Kind: "http", Digest: "synthetic-request", Guard: "site-action"}
	if _, _, err := f.j.BeginNetwork(ctx, f.o.binding, n); err != nil {
		t.Fatal(err)
	}
	if err := f.o.Close(); !errors.Is(err, signinjournal.ErrBusy) {
		t.Fatal("owner closed with an active execution", err)
	}
	if _, err := claimPluginAutoSignInOwner(ctx, f.a, "AutoSignIn", signinSQLImplementation, "UTC"); !errors.Is(err, signinjournal.ErrBusy) {
		t.Fatal("second owner stole a live instance", err)
	}
	f.release()
	f.release = nil
	if err := f.o.recoverUnknown(ctx, f.run); err != nil {
		t.Fatal("same-owner recovery failed", err)
	}
	s := f.state(t)
	if s.Runs[f.run.ID].Lanes["site"][0].Phase != "uncertain" || s.Blocks[n.Guard] != f.run.ID {
		t.Fatal("unacknowledged request became retryable")
	}
	old := f.o
	oldEpoch := old.binding.Epoch
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	o, err := claimPluginAutoSignInOwner(ctx, f.a, "AutoSignIn", signinSQLImplementation, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	f.o = o
	b, _ := o.backend(&f.run)
	j, _ := signinjournal.NewStateJournal(b)
	s, err = j.State(ctx, o.binding)
	if err != nil || o.binding.Epoch == oldEpoch || s.Runs[f.run.ID].Epoch != oldEpoch {
		t.Fatal("restart lost original run epoch or owner fence", err)
	}
	if _, err = f.b.AcquireExecution(ctx, old.binding); !errors.Is(err, signinjournal.ErrStale) {
		t.Fatal("closed native backend revived", err)
	}
	if err = j.Reconcile(ctx, o.binding, f.run.ID, n.Lane, 0, json.RawMessage(`{"observed":"owned-proof"}`)); !errors.Is(err, signinjournal.ErrStale) {
		t.Fatal("reconciliation without execution proof accepted", err)
	}
	release, err := j.AcquireExecution(ctx, o.binding)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = j.Reconcile(ctx, o.binding, f.run.ID, n.Lane, 0, json.RawMessage(`{"observed":"owned-proof"}`)); err != nil {
		t.Fatal(err)
	}
	if _, replay, err := j.BeginNetwork(ctx, o.binding, n); err != nil || !replay {
		t.Fatal("trusted reconciliation could not replay", err)
	}
}

func TestPluginAutoSignInSQLNormalizedRowsPreserveOpaqueBytesAndWriteOnlyDelta(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	ctx := context.Background()
	private := bytes.Repeat([]byte("synthetic-private-cache-marker\x00\xff"), 600000) // >16MiB total, no whole-state blob.
	if len(private) <= 16<<20 {
		t.Fatal("fixture no longer exceeds old blob cap")
	}
	if err := f.b.Update(ctx, f.o.binding, func(s *signinjournal.State) error {
		s.Cache["private-cache-key"] = private
		s.History["exact-raw-history"] = json.RawMessage(" { \"n\": 9007199254740993123456789, \"fraction\":1e-100 } ")
		s.Keys = []string{"exact-raw-history"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var cacheRows int
	if err := f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_rows WHERE collection='cache'`).Scan(&cacheRows); err != nil || cacheRows < 100 {
		t.Fatal("large cache was not independently chunked", cacheRows, err)
	}
	before := signinSQLSealedRows(t, f.a.Store)
	if err := f.b.Update(ctx, f.o.binding, func(s *signinjournal.State) error { s.CacheReady["one-small-change"] = true; return nil }); err != nil {
		t.Fatal(err)
	}
	after := signinSQLSealedRows(t, f.a.Store)
	for k, v := range before {
		if strings.Contains(k, "cache/") && !bytes.Equal(v, after[k]) {
			t.Fatal("small effect reencrypted unchanged large cache", k)
		}
	}
	var restored signinjournal.State
	if err := f.b.View(ctx, f.o.binding, func(s *signinjournal.State) error { restored = *s; return nil }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Cache["private-cache-key"], private) || string(restored.History["exact-raw-history"]) != " { \"n\": 9007199254740993123456789, \"fraction\":1e-100 } " {
		t.Fatal("raw bytes/numeric spelling changed")
	}
	for _, suffix := range []string{"", "-wal"} {
		raw, e := os.ReadFile(filepath.Join(f.a.Dir, "light-media.db"+suffix))
		if e != nil && !os.IsNotExist(e) {
			t.Fatal(e)
		}
		for _, needle := range []string{"synthetic-private-cache-marker", "private-cache-key", "exact-raw-history", "9007199254740993123456789"} {
			if bytes.Contains(raw, []byte(needle)) {
				t.Fatal("journal private value/identifier leaked", needle)
			}
		}
	}
}
func signinSQLSealedRows(t *testing.T, s *Store) map[string][]byte {
	t.Helper()
	rows, err := s.DB.Query(`SELECT collection,token,part,sealed FROM plugin_signin_rows`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		var c, k string
		var part int
		var sealed []byte
		if err = rows.Scan(&c, &k, &part, &sealed); err != nil {
			t.Fatal(err)
		}
		out[c+"/"+k+"/"+string(rune(part))] = sealed
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPluginAutoSignInSQLAuthenticatedManifestRejectsRowDeletionAndTransplant(t *testing.T) {
	for _, mutate := range []string{"delete", "sealed", "size"} {
		t.Run(mutate, func(t *testing.T) {
			f := signinSQLSetup(t)
			f.start(t)
			if err := f.b.Update(context.Background(), f.o.binding, func(s *signinjournal.State) error {
				s.Cache["one"] = []byte("private-one")
				s.Cache["two"] = []byte("private-two")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			query := map[string]string{"delete": `DELETE FROM plugin_signin_rows WHERE rowid=(SELECT MIN(rowid) FROM plugin_signin_rows WHERE collection='cache')`, "sealed": `UPDATE plugin_signin_rows SET sealed=(SELECT sealed FROM plugin_signin_rows WHERE collection='cache' ORDER BY rowid LIMIT 1) WHERE rowid=(SELECT MAX(rowid) FROM plugin_signin_rows WHERE collection='cache')`, "size": `UPDATE plugin_signin_rows SET size=size+1 WHERE collection='cache'`}[mutate]
			if _, err := f.a.Store.DB.Exec(query); err != nil {
				t.Fatal(err)
			}
			if _, err := f.j.State(context.Background(), f.o.binding); err == nil {
				t.Fatal("corrupt private row accepted")
			}
		})
	}
}

func TestPluginAutoSignInSQLRealRunAndLifecycleChangesRevokeCapability(t *testing.T) {
	for _, change := range []string{"external-config", "terminal-run", "run-input", "uninstall", "generation", "owner-proof"} {
		t.Run(change, func(t *testing.T) {
			f := signinSQLSetup(t)
			f.start(t)
			ctx := context.Background()
			switch change {
			case "external-config":
				_, err := f.a.pluginStore.SaveConfig(ctx, "AutoSignIn", 1, map[string]any{"external": true})
				if err != nil {
					t.Fatal(err)
				}
			case "terminal-run":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='succeeded' WHERE id=?`, f.run.ID)
			case "run-input":
				sealed, err := f.a.pluginStore.seal("run", "AutoSignIn", f.run.ID, []byte(`{"action":"init","input":"different"}`))
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?`, sealed, f.run.ID)
			case "uninstall":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_installations SET installed=0 WHERE instance_id='AutoSignIn'`)
			case "generation":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_installations SET generation=generation+1 WHERE instance_id='AutoSignIn'`)
			case "owner-proof":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_signin_owners SET sealed=x'01' WHERE instance_id='AutoSignIn'`)
			}
			if _, err := f.j.Apply(ctx, f.o.binding, signinjournal.Effect{RunID: f.run.ID, Lane: "main", Kind: "local", Digest: "must-not-commit", Value: json.RawMessage(`null`)}); err == nil {
				t.Fatal("revoked host capability wrote a new receipt")
			}
		})
	}
}

func TestPluginAutoSignInSQLOutboxRequiresOwnedMaintenanceAndKeepsSourceOrder(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	ctx := context.Background()
	for i, title := range []string{"one", "two"} {
		raw := signinSQLRaw(t, map[string]string{"title": title})
		if _, err := f.j.Apply(ctx, f.o.binding, signinjournal.Effect{RunID: f.run.ID, Lane: "main", Index: i, Kind: "notify", Digest: title, Value: raw, Mutation: signinjournal.Mutation{Kind: "outbox"}}); err != nil {
			t.Fatal(err)
		}
	}
	f.release()
	f.release = nil
	maintenance, _ := f.o.backend(nil)
	j, _ := signinjournal.NewStateJournal(maintenance)
	var first string
	sent := 0
	if _, err := j.DispatchOne(ctx, f.o.binding, func(_ context.Context, item signinjournal.OutboxItem) (signinjournal.Acknowledgement, error) {
		sent++
		first = item.ID
		return signinjournal.Acknowledgement{}, errors.New("owned synthetic lost acknowledgment")
	}); !errors.Is(err, signinjournal.ErrUncertain) {
		t.Fatal(err)
	}
	if _, err := j.DispatchOne(ctx, f.o.binding, func(context.Context, signinjournal.OutboxItem) (signinjournal.Acknowledgement, error) {
		sent++
		return signinjournal.Acknowledgement{}, nil
	}); !errors.Is(err, signinjournal.ErrUncertain) || sent != 1 {
		t.Fatal("uncertain message/later queue item was resent", err)
	}
	release, err := maintenance.Hold(ctx, f.o.binding)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.ReconcileOutbox(ctx, f.o.binding, first, signinjournal.Acknowledgement{ID: first, Committed: true}); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err = j.DispatchOne(ctx, f.o.binding, func(_ context.Context, item signinjournal.OutboxItem) (signinjournal.Acknowledgement, error) {
		sent++
		if string(item.Payload) != `{"title":"two"}` {
			t.Error("source FIFO changed")
		}
		return signinjournal.Acknowledgement{ID: item.ID, Committed: true}, nil
	}); err != nil || sent != 2 {
		t.Fatal(err, sent)
	}
}

func TestPluginAutoSignInSQLBlockedExecutionLeavesViewsAndOtherInstanceAvailable(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan error, 1)
	go func() {
		defer wg.Done()
		done <- f.b.View(ctx, f.o.binding, func(*signinjournal.State) error { return nil })
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("execution held SQL/pluginMu for its whole lifetime")
	}
	wg.Wait()
	other, _ := f.o.backend(&f.run)
	if _, err := other.AcquireExecution(ctx, f.o.binding); !errors.Is(err, signinjournal.ErrBusy) {
		t.Fatal("parallel same-instance execution admitted", err)
	}
	var count int
	if err := f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&count); err != nil || count != 1 {
		t.Fatal("journal created another execution queue", count, err)
	}
}

func TestPluginAutoSignInSQLControllerUsesRealCoreRunsAndOwnedHTTPWithoutDuplicateResume(t *testing.T) {
	ctx := context.Background()
	f := signinSQLSetup(t, map[string]any{"enabled": true, "notify": true, "onlyonce": false, "sign_sites": []any{1}, "login_sites": []any{}, "queue_cnt": 1, "cron": "2"})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/attendance.php" || r.Header.Get("Cookie") != "sid=owned-synthetic" {
			t.Error("bounded native request differs from source plan")
		}
		_, _ = w.Write([]byte(`<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`))
	}))
	defer server.Close()
	httpGrant, err := autosignin.BindSiteHTTP(autosignin.SiteHTTPBinding{URL: server.URL + "/", Cookie: "sid=owned-synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	deps := signinjournal.Dependencies{
		Source: newAutoSignInSourcePort(f.b),
		Inventory: func(context.Context) (autosignin.Inventory, error) {
			site := autosignin.Object{"id": 1, "name": "Owned loopback"}
			return autosignin.Inventory{Configured: []autosignin.Object{site}, Indexers: []autosignin.Object{site}}, nil
		},
		Factory: func(context.Context, autosignin.Object) (signinjournal.BoundSite, error) {
			return signinjournal.BoundSite{Site: autosignin.Object{"id": 1, "name": "Owned loopback", "url": server.URL + "/", "cookie": "sid=owned-synthetic"}, Hooks: autosignin.SiteVisitHooks{HTTP: httpGrant}, Revision: "immutable-loopback-grant-v1"}, nil
		},
		Timezone: "UTC", Now: func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }, RandomInt: func(a, b int) (int, error) { return a, nil },
	}
	controller := signinjournal.Controller{Journal: f.j, Binding: f.o.binding, DigestKey: [32]byte{1, 2, 3}, Dependencies: deps}
	if _, err = controller.Execute(ctx, f.run.ID, signinjournal.Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := f.a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil || cfg.Revision != 2 {
		t.Fatal("real cold init did not publish its config save", cfg.Revision, err)
	}
	if err = f.a.pluginStore.FinishRun(ctx, f.run.ID, "succeeded", "", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	run, err := f.a.pluginStore.EnqueueRun(ctx, "AutoSignIn", "service", json.RawMessage(`{}`))
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
	backend, err := f.o.backend(&run)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := signinjournal.NewStateJournal(backend)
	if err != nil {
		t.Fatal(err)
	}
	controller.Journal = journal
	controller.Dependencies.Source = newAutoSignInSourcePort(backend)
	faultJournal := &signinSourceIntentJournal{StateJournal: journal, t: t}
	controller.Journal = faultJournal
	// main[0:2] are committed history writes. Fail the next SQL intent row,
	// before Invoke, not an ambiguous source send or its terminal proof.
	sourceToken := f.o.token("entry.head", signinKey(run.ID, "main", "2"))
	if _, err = f.a.Store.DB.Exec(`CREATE TRIGGER signin_notify_failure BEFORE INSERT ON plugin_signin_rows WHEN NEW.collection='entry.head' AND NEW.token='` + sourceToken + `' BEGIN SELECT RAISE(ABORT,'owned source intent boundary'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.Execute(ctx, run.ID, signinjournal.Command{Action: "service"}); err == nil {
		t.Fatal("source effect failure did not interrupt")
	}
	if requests.Load() != 1 {
		t.Fatal("initial owned source visit count", requests.Load())
	}
	if !faultJournal.attempted {
		t.Fatal("SQL failure did not target source BeginSource")
	}
	signinAssertBeforeSourceIntent(t, f, journal, run.ID, server.Listener.Addr().String())
	if _, err = f.a.Store.DB.Exec(`DROP TRIGGER signin_notify_failure`); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the execution objects; replay cannot rely on warm memory.
	backend, err = f.o.backend(&run)
	if err != nil {
		t.Fatal(err)
	}
	journal, err = signinjournal.NewStateJournal(backend)
	if err != nil {
		t.Fatal(err)
	}
	deps.Source = newAutoSignInSourcePort(backend)
	deps.Inventory = func(context.Context) (autosignin.Inventory, error) {
		return autosignin.Inventory{}, errors.New("current inventory deliberately unavailable")
	}
	controller = signinjournal.Controller{Journal: journal, Binding: f.o.binding, DigestKey: [32]byte{1, 2, 3}, Dependencies: deps}
	result, err := controller.Execute(ctx, run.ID, signinjournal.Command{Action: "service"})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || result["data"] == nil {
		t.Fatal("resumed native flow repeated the network or lost history")
	}
	state, err := journal.State(ctx, f.o.binding)
	if err != nil {
		t.Fatal(err)
	}
	if state.Stats[server.Listener.Addr().String()].Success != 1 || len(state.Outbox) != 0 || state.ActiveRun != "" {
		t.Fatal("source receipt/effect chain was not completed", state.Stats, len(state.Outbox), state.ActiveRun)
	}
	cfg, err = f.a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil || cfg.Revision != 3 || run.ConfigRevision != 2 || f.o.binding.Revision != 1 {
		t.Fatal("run admission or public revision changed incorrectly", cfg.Revision, run.ConfigRevision, f.o.binding.Revision, err)
	}
	signinAssertCompletedSourceProof(t, f, journal, run.ID)
	if _, err = f.a.Store.DB.Exec(`DELETE FROM plugin_signin_source_results`); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.Execute(ctx, run.ID, signinjournal.Command{Action: "service"}); err == nil || requests.Load() != 1 {
		t.Fatal("missing independent source proof became successful replay or repeated HTTP", err, requests.Load())
	}
}

func TestPluginAutoSignInSQLActualStoreRestartRetainsBytesAndRejectsForgedEffectiveRevision(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	ctx := context.Background()
	if err := f.b.Update(ctx, f.o.binding, func(s *signinjournal.State) error {
		s.Cache["zero"] = []byte{}
		s.Cache["missing-like"] = nil
		s.History["raw"] = json.RawMessage(" { \"v\":1e-100 } ")
		s.Keys = []string{"raw"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.release()
	f.release = nil
	if err := f.o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(f.a.Dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	p, err := NewPluginStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	f.a = &App{Store: store, pluginStore: p, Dir: f.a.Dir, autoSignInEnvironment: func() []string { return nil }}
	o, err := claimPluginAutoSignInOwner(ctx, f.a, "AutoSignIn", signinSQLImplementation, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	f.o = o
	b, _ := o.backend(&f.run)
	j, _ := signinjournal.NewStateJournal(b)
	var s signinjournal.State
	err = b.View(ctx, o.binding, func(value *signinjournal.State) error { s = *value; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if s.Cache["zero"] == nil || s.Cache["missing-like"] != nil || string(s.History["raw"]) != " { \"v\":1e-100 } " {
		t.Fatal("restart normalized absent/empty cache or raw history")
	}
	if _, err = store.DB.Exec(`UPDATE plugin_values SET revision=revision+1 WHERE instance_id='AutoSignIn' AND kind='config'`); err != nil {
		t.Fatal(err)
	}
	if _, err = j.State(ctx, o.binding); !errors.Is(err, signinjournal.ErrStale) {
		t.Fatal("public revision alone was accepted as private config authority", err)
	}
}

func TestPluginAutoSignInSQLMaintenanceCannotRewriteHeaderOrOutboxIdentity(t *testing.T) {
	for _, change := range []string{"active", "runtime_epoch", "id", "kind", "run", "lane", "index", "payload", "append", "delete", "skip-intent", "invent-phase"} {
		t.Run(change, func(t *testing.T) {
			f := signinSQLSetup(t)
			f.start(t)
			ctx := context.Background()
			if _, err := f.j.Apply(ctx, f.o.binding, signinjournal.Effect{RunID: f.run.ID, Lane: "main", Kind: "notify", Digest: "source-notice", Value: json.RawMessage(`{"title":"owned source"}`), Mutation: signinjournal.Mutation{Kind: "outbox"}}); err != nil {
				t.Fatal(err)
			}
			f.release()
			f.release = nil
			maintenance, _ := f.o.backend(nil)
			release, err := maintenance.Hold(ctx, f.o.binding)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			var before signinjournal.State
			if err = maintenance.View(ctx, f.o.binding, func(s *signinjournal.State) error { before = *s; return nil }); err != nil {
				t.Fatal(err)
			}
			err = maintenance.Update(ctx, f.o.binding, func(s *signinjournal.State) error {
				switch change {
				case "active":
					s.ActiveRun = "forged-other-run"
				case "runtime_epoch":
					s.RuntimeEpoch = "forged-runtime"
				case "id":
					s.Outbox[0].ID = "another-id"
				case "kind":
					s.Outbox[0].Kind = "emit"
				case "run":
					s.Outbox[0].RunID = "other-run"
				case "lane":
					s.Outbox[0].Lane = "other-lane"
				case "index":
					s.Outbox[0].Index++
				case "payload":
					s.Outbox[0].Payload = json.RawMessage(`{"title":"injected"}`)
				case "append":
					s.Outbox = append(s.Outbox, s.Outbox[0])
				case "delete":
					s.Outbox = nil
				case "skip-intent":
					s.Outbox[0].Phase = "delivered"
				case "invent-phase":
					s.Outbox[0].Phase = "made-up"
				}
				return nil
			})
			if !errors.Is(err, signinjournal.ErrStale) {
				t.Fatal("maintenance exceeded phase-only authority", change, err)
			}
			var after signinjournal.State
			if err = maintenance.View(ctx, f.o.binding, func(s *signinjournal.State) error { after = *s; return nil }); err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected mutation was published", change, err)
			}
		})
	}
}

func TestPluginAutoSignInSQLRunBackendCannotSelectAnotherActiveRunOrRuntimeEpoch(t *testing.T) {
	for _, change := range []string{"active", "runtime_epoch"} {
		t.Run(change, func(t *testing.T) {
			f := signinSQLSetup(t)
			f.start(t)
			err := f.b.Update(context.Background(), f.o.binding, func(s *signinjournal.State) error {
				if change == "active" {
					s.ActiveRun = "other-real-or-invented-run"
				} else {
					s.RuntimeEpoch = "forged-epoch"
				}
				return nil
			})
			if !errors.Is(err, signinjournal.ErrStale) {
				t.Fatal("run selected a different header authority", err)
			}
		})
	}
}

// Keep the real SQL BeginSource implementation (and typed optional readers),
// while proving that the injected row failure targets notification intent.
type signinSourceIntentJournal struct {
	*signinjournal.StateJournal
	t         *testing.T
	attempted bool
}

func (j *signinSourceIntentJournal) BeginSource(ctx context.Context, b signinjournal.Binding, req signinjournal.SourceRequest) (signinjournal.Entry, bool, error) {
	j.t.Helper()
	if req.Identity.Kind != "source.notify" || req.Identity.Lane != "main" || req.Identity.Index != 2 {
		j.t.Fatalf("unexpected source intent targeted by SQL fault: %+v", req.Identity)
	}
	j.attempted = true
	return j.StateJournal.BeginSource(ctx, b, req)
}

func signinAssertBeforeSourceIntent(t *testing.T, f *signinSQLFixture, j *signinjournal.StateJournal, runID, domain string) {
	t.Helper()
	state, err := j.State(context.Background(), f.o.binding)
	if err != nil {
		t.Fatal(err)
	}
	run := state.Runs[runID]
	if run == nil || len(run.Lanes["main"]) != 2 || len(state.History) != 2 || state.Stats[domain].Success != 1 || state.ActiveRun != runID || len(state.Outbox) != 0 {
		t.Fatal("SQL intent failure lost committed HTTP/history prefix or advanced source", state)
	}
	for _, entry := range run.Lanes["main"] {
		if entry.Kind != "history.put" || entry.Phase != "applied" {
			t.Fatal("history prefix changed", entry)
		}
	}
	for _, entries := range run.Lanes {
		for _, entry := range entries {
			if entry.Phase == "intent" || entry.Phase == "uncertain" || strings.HasPrefix(entry.Kind, "source.") {
				t.Fatal("pre-Invoke failure was turned into an unknown source send", entry)
			}
		}
	}
	for _, table := range []string{"plugin_signin_source_acceptances", "plugin_signin_source_results"} {
		var count int
		if err := f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("source invoked despite rejected intent", table, count, err)
		}
	}
}

func signinAssertCompletedSourceProof(t *testing.T, f *signinSQLFixture, j *signinjournal.StateJournal, runID string) {
	t.Helper()
	ctx := context.Background()
	state, err := j.State(ctx, f.o.binding)
	if err != nil {
		t.Fatal(err)
	}
	run := state.Runs[runID]
	if run == nil || run.Phase != "completed" || len(run.Lanes["main"]) != 4 {
		t.Fatal("source chain incomplete", run)
	}
	entries := run.Lanes["main"]
	for i, kind := range []string{"history.put", "history.put", "source.notify", "config"} {
		if entries[i].Kind != kind {
			t.Fatal("source ordering changed", i, entries[i])
		}
	}
	var env signinjournal.SourceEnvelope
	if entries[2].Phase != "receipt" || json.Unmarshal(entries[2].Value, &env) != nil || env.Result == nil || env.Result.Outcome != signinjournal.SourceNormal || signinjournal.ValidateSourceResult(env.Request, *env.Result) != nil {
		t.Fatal("source lacks typed normal receipt", entries[2])
	}
	for _, table := range []string{"plugin_signin_source_acceptances", "plugin_signin_source_results"} {
		var count int
		if err := f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
			t.Fatal("source did not commit exactly once", table, count, err)
		}
	}
	tx, err := f.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	proof, found, err := f.a.pluginStore.autoSignInSourceResultHistoricalTx(ctx, tx, run, env.Request.Identity)
	if err != nil || !found || proof != *env.Result {
		t.Fatal("receipt lacks independent authenticated terminal proof", proof, found, err)
	}
}
