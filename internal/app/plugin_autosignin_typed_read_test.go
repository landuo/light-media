package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/autosignin"
	signinjournal "light-media/internal/plugins/autosignin/durable"
)

// Every fixture here is the actual App PluginStore/SQL schema, owner OS lock and
// immutable core PluginRun. No draft App/PluginStore shim is compiled here.
func signinTypedSetup(t *testing.T, bodyBytes int) (*signinSQLFixture, *pluginAutoSignInTypedBackend, *signinjournal.StateJournal) {
	t.Helper()
	f := signinSQLSetup(t)
	f.start(t)
	ctx := context.Background()
	if _, err := f.j.Apply(ctx, f.o.binding, signinjournal.Effect{RunID: f.run.ID, Lane: "main", Kind: "clock", Digest: "clock-identity", Value: json.RawMessage(`"2026-01-02T12:00:00Z"`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.j.Apply(ctx, f.o.binding, signinjournal.Effect{RunID: f.run.ID, Lane: "raw-local", Kind: "local", Digest: "raw-json-identity", Value: json.RawMessage(" {\"html\":\"<>&\", \"n\":1e-100} ")}); err != nil {
		t.Fatal(err)
	}
	n := signinjournal.NetworkIntent{RunID: f.run.ID, Lane: "large-reply", Kind: "http", Digest: "owned-request", Guard: "large-reply-action"}
	if _, _, err := f.j.BeginNetwork(ctx, f.o.binding, n); err != nil {
		t.Fatal(err)
	}
	if err := f.j.CompleteNetwork(ctx, f.o.binding, n, signinSQLRaw(t, map[string]string{"body": string(bytes.Repeat([]byte{'x'}, bodyBytes))}), ""); err != nil {
		t.Fatal(err)
	}
	n.Lane, n.Digest, n.Guard = "unknown-site", "owned-unknown-request", "unknown-site-action"
	if _, _, err := f.j.BeginNetwork(ctx, f.o.binding, n); err != nil {
		t.Fatal(err)
	}
	if err := f.j.Unknown(ctx, f.o.binding, n); err != nil {
		t.Fatal(err)
	}
	if _, err := f.j.Apply(ctx, f.o.binding, signinjournal.Effect{RunID: f.run.ID, Lane: "notices", Kind: "notify", Digest: "owned-pending-notice", Value: json.RawMessage(`{"title":"synthetic pending source notice"}`), Mutation: signinjournal.Mutation{Kind: "outbox"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.b.Update(ctx, f.o.binding, func(s *signinjournal.State) error {
		s.Cache["tjupt"] = []byte(`{"owned":"cache"}`)
		s.Cache["nil"] = nil
		s.Cache["empty"] = []byte{}
		s.Cache["unrelated"] = bytes.Repeat([]byte{'z'}, bodyBytes)
		s.History["private-history"] = json.RawMessage(" {\"n\":9007199254740993123456789,\"fraction\":1e-100} ")
		s.History["unknown legacy key"] = json.RawMessage(`{"retained":true}`)
		s.Keys = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b := &pluginAutoSignInTypedBackend{f.b}
	j, err := newPluginAutoSignInTypedJournal(f.b)
	if err != nil {
		t.Fatal(err)
	}
	return f, b, j
}

func TestPluginAutoSignInTypedReadsMatchActualFullBackendAndNeverWrite(t *testing.T) {
	f, b, typed := signinTypedSetup(t, 1<<20)
	ctx := context.Background()
	before := signinSQLSealedRows(t, f.a.Store)
	state := f.state(t)
	for _, key := range []string{"tjupt", "nil", "empty", "absent"} {
		want, err := f.j.ReadCache(ctx, f.o.binding, key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := typed.ReadCache(ctx, f.o.binding, key)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("cache absence/nil/empty changed", key, got, want, err)
		}
	}
	for _, target := range []struct {
		lane  string
		index int
	}{{"main", 0}, {"main", 1}, {"unknown-site", 0}, {"large-reply", 0}, {"raw-local", 0}} {
		want, wantExists, err := f.j.ReadEntry(ctx, f.o.binding, f.run.ID, target.lane, target.index)
		if err != nil {
			t.Fatal(err)
		}
		got, exists, err := typed.ReadEntry(ctx, f.o.binding, f.run.ID, target.lane, target.index)
		if err != nil || exists != wantExists || !reflect.DeepEqual(got, want) {
			t.Fatal("entry changed", target, err)
		}
	}
	if _, _, err := typed.ReadEntry(ctx, f.o.binding, "absent-run", "main", 0); err == nil {
		t.Fatal("absent run became an allowed append position")
	}
	if _, _, err := typed.ReadEntry(ctx, f.o.binding, f.run.ID, "main", 2); !errors.Is(err, signinjournal.ErrConflict) {
		t.Fatal("noncontiguous lookup accepted", err)
	}
	for _, id := range []string{f.run.ID, "new-run"} {
		want, err := f.j.ReadRunStartHead(ctx, f.o.binding, id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := typed.ReadRunStartHead(ctx, f.o.binding, id)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("baseline/head changed", id, got, want, err)
		}
		if !got.Exists && (got.Baseline.Keys == nil || !bytes.Contains(got.Baseline.History, []byte("unknown legacy key"))) {
			t.Fatal("new-run baseline lost empty Keys or unknown history")
		}
	}
	reader, err := b.reader(f.o.binding)
	if err != nil {
		t.Fatal(err)
	}
	cost, err := reader.read(ctx, f.o.binding, func(tx *sql.Tx, p *pluginAutoSignInTypedProjection) error {
		_, _, e := p.entryTx(ctx, tx, f.run.ID, "main", 0)
		return e
	})
	if err != nil || cost.DirectoryRows < 25 || cost.JournalDecryptedBytes > 32<<10 {
		t.Fatal("small lookup decrypted unrelated large reply/cache", cost, err)
	}
	if !reflect.DeepEqual(before, signinSQLSealedRows(t, f.a.Store)) || !reflect.DeepEqual(state, f.state(t)) {
		t.Fatal("read mutated unknown/outbox/source facts or ciphertext")
	}
	// Returned data is owned by the caller, never a mutable view of storage.
	cache, _ := typed.ReadCache(ctx, f.o.binding, "tjupt")
	cache.Value[0] ^= 1
	again, err := typed.ReadCache(ctx, f.o.binding, "tjupt")
	if err != nil || string(again.Value) != `{"owned":"cache"}` {
		t.Fatal("caller changed persistent cache", err)
	}
}

func TestPluginAutoSignInTypedAuthenticationRejectsDirectoryAndRequestedBodyCorruption(t *testing.T) {
	for _, mutation := range []string{"directory-delete", "directory-size", "requested-body", "lazy-body"} {
		t.Run(mutation, func(t *testing.T) {
			f, _, typed := signinTypedSetup(t, 256)
			ctx := context.Background()
			switch mutation {
			case "directory-delete":
				_, _ = f.a.Store.DB.Exec(`DELETE FROM plugin_signin_rows WHERE collection='cache' AND token=?`, f.o.token("cache", "unrelated"))
			case "directory-size":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_signin_rows SET size=size+1 WHERE collection='cache' AND token=?`, f.o.token("cache", "unrelated"))
			default:
				key := "tjupt"
				if mutation == "lazy-body" {
					key = "unrelated"
				}
				var sealed []byte
				if err := f.a.Store.DB.QueryRow(`SELECT sealed FROM plugin_signin_rows WHERE collection='cache' AND token=? AND part=0`, f.o.token("cache", key)).Scan(&sealed); err != nil {
					t.Fatal(err)
				}
				sealed[len(sealed)-1] ^= 1
				if _, err := f.a.Store.DB.Exec(`UPDATE plugin_signin_rows SET sealed=? WHERE collection='cache' AND token=? AND part=0`, sealed, f.o.token("cache", key)); err != nil {
					t.Fatal(err)
				}
			}
			before := signinSQLSealedRows(t, f.a.Store)
			_, err := typed.ReadCache(ctx, f.o.binding, "tjupt")
			if mutation == "lazy-body" {
				if err != nil {
					t.Fatal("unrequested body was eagerly read", err)
				}
				if _, err = typed.ReadCache(ctx, f.o.binding, "unrelated"); err == nil {
					t.Fatal("requested corrupt body became empty/success")
				}
			} else if err == nil {
				t.Fatal("corrupt authenticated directory/requested body accepted")
			}
			if !reflect.DeepEqual(before, signinSQLSealedRows(t, f.a.Store)) {
				t.Fatal("failed read repaired/replaced private facts")
			}
		})
	}
}

func TestPluginAutoSignInTypedReadsRequireActualRunConfigAndOwner(t *testing.T) {
	for _, change := range []string{"external-config", "terminal-run", "run-input", "run-revision", "uninstall", "generation", "owner-proof", "foreign-binding"} {
		t.Run(change, func(t *testing.T) {
			f, _, typed := signinTypedSetup(t, 64)
			ctx := context.Background()
			binding := f.o.binding
			switch change {
			case "external-config":
				if _, err := f.a.pluginStore.SaveConfig(ctx, "AutoSignIn", 1, map[string]any{"external": true}); err != nil {
					t.Fatal(err)
				}
			case "terminal-run":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='succeeded' WHERE id=?`, f.run.ID)
			case "run-input":
				sealed, err := f.a.pluginStore.seal("run", "AutoSignIn", f.run.ID, []byte(`{"action":"init","config":{"different":true}}`))
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?`, sealed, f.run.ID)
			case "run-revision":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_runs SET config_revision=config_revision+1 WHERE id=?`, f.run.ID)
			case "uninstall":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_installations SET installed=0 WHERE instance_id='AutoSignIn'`)
			case "generation":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_installations SET generation=generation+1 WHERE instance_id='AutoSignIn'`)
			case "owner-proof":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_signin_owners SET sealed=x'01' WHERE instance_id='AutoSignIn'`)
			case "foreign-binding":
				binding.Epoch = "worker-supplied-epoch"
			}
			if _, err := typed.ReadCache(ctx, binding, "tjupt"); err == nil {
				t.Fatal("revoked cache capability accepted")
			}
			if _, _, err := typed.ReadEntry(ctx, binding, f.run.ID, "main", 0); err == nil {
				t.Fatal("revoked receipt capability accepted")
			}
			if _, err := typed.ReadRunStartHead(ctx, binding, f.run.ID); err == nil {
				t.Fatal("revoked run head capability accepted")
			}
		})
	}
}

func TestPluginAutoSignInTypedConfigChainPreservesOriginalAdmissionAndRejectsCorruptProof(t *testing.T) {
	f, _, typed := signinTypedSetup(t, 256)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		raw := json.RawMessage(" { \"enabled\": false, \"onlyonce\":false, \"sign_sites\":[], \"login_sites\":[], \"queue_cnt\":1, \"label\":\"<>&\" } ")
		if _, err := f.j.Apply(ctx, f.o.binding, signinjournal.Effect{RunID: f.run.ID, Lane: "config", Index: i, Kind: "config", Digest: "owned-config-" + string(rune('a'+i)), Value: raw, Mutation: signinjournal.Mutation{Kind: "config", Value: raw}}); err != nil {
			t.Fatal(err)
		}
		if _, err := typed.ReadCache(ctx, f.o.binding, "tjupt"); err != nil {
			t.Fatal("exact source config chain was rejected", err)
		}
		want, err := f.j.ReadRunStartHead(ctx, f.o.binding, "new-config-seed")
		if err != nil {
			t.Fatal(err)
		}
		got, err := typed.ReadRunStartHead(ctx, f.o.binding, "new-config-seed")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("returned config DTO differs from old JSON copy", err)
		}
		stored, err := f.a.pluginStore.Config(ctx, "AutoSignIn")
		if err != nil || !bytes.Equal(stored.Value, raw) {
			t.Fatal("DTO normalization changed stored config/proof bytes", err)
		}
	}
	config, err := f.a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil || config.Revision != 3 || f.run.ConfigRevision != 1 || f.o.binding.Revision != 1 {
		t.Fatal("typed path changed public CAS/admission", config.Revision, err)
	}
	if _, err = f.a.Store.DB.Exec(`UPDATE plugin_signin_config_receipts SET sealed=x'01' WHERE instance_id='AutoSignIn' AND to_revision=2`); err != nil {
		t.Fatal(err)
	}
	if _, err = typed.ReadRunStartHead(ctx, f.o.binding, f.run.ID); err == nil {
		t.Fatal("private config chain corruption ignored")
	}
}

func TestPluginAutoSignInTypedReplacementOwnerRetainsUnknownAndUncertainOutbox(t *testing.T) {
	f, _, oldTyped := signinTypedSetup(t, 256)
	ctx := context.Background()
	f.release()
	f.release = nil
	maintenance, _ := f.o.backend(nil)
	mj, err := newPluginAutoSignInTypedJournal(maintenance)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mj.DispatchOne(ctx, f.o.binding, func(context.Context, signinjournal.OutboxItem) (signinjournal.Acknowledgement, error) {
		return signinjournal.Acknowledgement{}, errors.New("owned synthetic uncertain sink")
	}); !errors.Is(err, signinjournal.ErrUncertain) {
		t.Fatal(err)
	}
	old := f.o
	oldEpoch := old.binding.Epoch
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	o, err := claimPluginAutoSignInOwner(ctx, f.a, "AutoSignIn", signinSQLImplementation, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	f.o = o
	backend, err := o.backend(&f.run)
	if err != nil {
		t.Fatal(err)
	}
	j, err := newPluginAutoSignInTypedJournal(backend)
	if err != nil {
		t.Fatal(err)
	}
	entry, exists, err := j.ReadEntry(ctx, o.binding, f.run.ID, "unknown-site", 0)
	if err != nil || !exists || entry.Phase != "uncertain" || o.binding.Epoch == oldEpoch {
		t.Fatal("restart lost/freed unknown request", entry, err)
	}
	head, err := j.ReadRunStartHead(ctx, o.binding, f.run.ID)
	if err != nil || head.Epoch != oldEpoch {
		t.Fatal("old run epoch was rebound", head, err)
	}
	if _, err = oldTyped.ReadCache(ctx, old.binding, "tjupt"); !errors.Is(err, signinjournal.ErrStale) {
		t.Fatal("old typed owner revived", err)
	}
	state, err := j.State(ctx, o.binding)
	if err != nil || state.Blocks["unknown-site-action"] != f.run.ID || len(state.Outbox) != 1 || state.Outbox[0].Phase != "uncertain" {
		t.Fatal("read/recovery changed unknown/outbox facts", err)
	}
}

func TestPluginAutoSignInTypedControllerUsesActualSQLAndResumesWithoutSecondVisit(t *testing.T) {
	ctx := context.Background()
	f := signinSQLSetup(t, map[string]any{"enabled": true, "notify": true, "onlyonce": false, "sign_sites": []any{1}, "login_sites": []any{}, "queue_cnt": 1, "cron": "2"})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/attendance.php" || r.Header.Get("Cookie") != "sid=owned-typed-synthetic" {
			t.Error("native fixture request escaped its grant")
		}
		_, _ = w.Write([]byte(`<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`))
	}))
	defer server.Close()
	grant, err := autosignin.BindSiteHTTP(autosignin.SiteHTTPBinding{URL: server.URL + "/", Cookie: "sid=owned-typed-synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := newPluginAutoSignInTypedJournal(f.b)
	if err != nil {
		t.Fatal(err)
	}
	c := signinjournal.Controller{Journal: j, Binding: f.o.binding, DigestKey: [32]byte{1, 2, 3}, Dependencies: signinjournal.Dependencies{
		Inventory: func(context.Context) (autosignin.Inventory, error) {
			site := autosignin.Object{"id": 1, "name": "Owned typed loopback"}
			return autosignin.Inventory{Configured: []autosignin.Object{site}, Indexers: []autosignin.Object{site}}, nil
		},
		Factory: func(context.Context, autosignin.Object) (signinjournal.BoundSite, error) {
			return signinjournal.BoundSite{Site: autosignin.Object{"id": 1, "name": "Owned typed loopback", "url": server.URL + "/", "cookie": "sid=owned-typed-synthetic"}, Hooks: autosignin.SiteVisitHooks{HTTP: grant}, Revision: "owned-typed-native-grant-v1"}, nil
		},
		Timezone: "UTC", Now: func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }, RandomInt: func(a, b int) (int, error) { return a, nil },
	}}
	if _, err = c.Execute(ctx, f.run.ID, signinjournal.Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
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
	j, err = newPluginAutoSignInTypedJournal(backend)
	if err != nil {
		t.Fatal(err)
	}
	c.Journal = j
	if _, err = f.a.Store.DB.Exec(`CREATE TRIGGER signin_typed_notify_failure BEFORE INSERT ON plugin_signin_rows WHEN NEW.collection='outbox.head' BEGIN SELECT RAISE(ABORT,'owned typed source boundary'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Execute(ctx, run.ID, signinjournal.Command{Action: "service"}); err == nil || requests.Load() != 1 {
		t.Fatal("source interruption/visit prefix missing", err, requests.Load())
	}
	if _, err = f.a.Store.DB.Exec(`DROP TRIGGER signin_typed_notify_failure`); err != nil {
		t.Fatal(err)
	}
	c.Dependencies.Inventory = func(context.Context) (autosignin.Inventory, error) {
		return autosignin.Inventory{}, errors.New("current inventory deliberately unavailable")
	}
	result, err := c.Execute(ctx, run.ID, signinjournal.Command{Action: "service"})
	if err != nil || requests.Load() != 1 || result["data"] == nil {
		t.Fatal("native replay refetched inventory/network or lost history", err, requests.Load())
	}
	state, err := j.State(ctx, f.o.binding)
	if err != nil || state.Stats[server.Listener.Addr().String()].Success != 1 || len(state.Outbox) != 1 || state.ActiveRun != "" {
		t.Fatal("source effects were not completed", err)
	}
	config, err := f.a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil || config.Revision != 3 || run.ConfigRevision != 2 || f.o.binding.Revision != 1 {
		t.Fatal("typed execution changed immutable admission/public CAS", config.Revision, err)
	}
}
