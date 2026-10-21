package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/plugins/autosignin"
	signinjournal "light-media/internal/plugins/autosignin/durable"
)

func signinCacheSetup(t *testing.T, instances ...string) (*App, map[string]*pluginAutoSignInOwner) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	a := &App{Store: s, pluginStore: p, Dir: filepath.Dir(s.lock.Name())}
	owners := map[string]*pluginAutoSignInOwner{}
	a.pluginAutoSignIn.owners = map[string]*pluginAutoSignInSlot{}
	for _, instance := range instances {
		manifest, _ := json.Marshal(autosignin.Manifest())
		run, err := p.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: instance, PluginID: "AutoSignIn", Version: autosignin.Version, ManifestSchemaVersion: 1, Manifest: manifest}, map[string]any{"enabled": false, "onlyonce": false, "sign_sites": []any{}, "login_sites": []any{}})
		if err != nil {
			t.Fatal(err)
		}
		o, err := claimPluginAutoSignInOwner(context.Background(), a, instance, autoSignInImplementation, "UTC")
		if err != nil {
			t.Fatal(err)
		}
		owners[instance] = o
		t.Cleanup(func() { _ = o.Close() })
		// This synthetic fixture provisions authenticated idle state only. The
		// separate SDK host test executes the actual initialization and sign-in.
		if _, err = s.DB.Exec(`UPDATE plugin_runs SET state='succeeded' WHERE id=?`, run.ID); err != nil {
			t.Fatal(err)
		}
		slot := &pluginAutoSignInSlot{owner: o, ready: make(chan struct{}), gate: make(chan struct{}, 1)}
		close(slot.ready)
		a.pluginAutoSignIn.owners[instance] = slot
	}
	return a, owners
}

func signinCacheRaw(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signinCacheSource(t *testing.T, a *App, instance string, data []byte) AutoSignInTjuptCacheTransfer {
	t.Helper()
	out, err := a.ExportAutoSignInTjuptCache(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	out.Cache = AutoSignInTjuptCachePayload{Mode: "source-bytes", SourceSHA256: AutoSignInTjuptSourceSHA256, Key: "tjupt", Present: true, ReadyPresent: true, Ready: true, Bytes: data, SHA256: sourceSettingsDigest(data)}
	return out
}

func signinCacheState(t *testing.T, o *pluginAutoSignInOwner) signinjournal.State {
	t.Helper()
	tx, err := o.a.Store.DB.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	loaded, err := o.readTx(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	return loaded.State
}

func signinCacheMutate(t *testing.T, o *pluginAutoSignInOwner, mutate func(*signinjournal.State)) {
	t.Helper()
	tx, err := o.a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	loaded, err := o.readTx(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	state := loaded.State
	mutate(&state)
	if err = o.writeTx(context.Background(), tx, loaded, state); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Compare exact SQL bytes, including ciphertext, not just decoded cache values.
// A rejected operation must not even reseal the owner or initialize metadata.
func signinCacheSQL(t *testing.T, a *App, excludeJournal bool) []byte {
	t.Helper()
	rows, err := a.Store.DB.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'plugin_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		if !excludeJournal || (table != "plugin_signin_meta" && table != "plugin_signin_rows") {
			tables = append(tables, table)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	all := map[string][][]any{}
	for _, table := range tables {
		rows, err = a.Store.DB.Query(`SELECT * FROM "` + table + `" ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for n := range values {
				pointers[n] = &values[n]
			}
			if err = rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			all[table] = append(all[table], values)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return signinCacheRaw(t, all)
}

func TestPluginAutoSignInTjuptCacheTransferPreservesSourceBytesAndOnlyExistingCache(t *testing.T) {
	a, owners := signinCacheSetup(t, "AutoSignIn", "AutoSignInClone")
	for _, data := range [][]byte{
		[]byte(" {\n  \"z\": \"答 案\", \"a\":9007199254740993123456789, \"z\":null } \n"),
		[]byte("{not-json"), []byte("[1,2]"), []byte("null"), []byte("42"), []byte{}, {0xff, 0, 0xfe},
	} {
		in := signinCacheSource(t, a, "AutoSignIn", data)
		before, untouched := signinCacheState(t, owners["AutoSignIn"]), signinCacheSQL(t, a, true)
		receipt, err := a.ImportAutoSignInTjuptCache(context.Background(), "AutoSignIn", signinCacheRaw(t, in))
		if err != nil || receipt.State != "imported" || receipt.InputTrust != "caller-supplied-source-bytes-not-authenticated" || receipt.Bytes != len(data) || receipt.JobsQueued != 0 || receipt.ExternalCalls != 0 || receipt.ConfigChanged || receipt.RuntimeChanged {
			t.Fatal("source bytes did not import without execution", receipt, err)
		}
		before.Cache["tjupt"], before.CacheReady["tjupt"] = signinTypedClone(data), true
		if !reflect.DeepEqual(before, signinCacheState(t, owners["AutoSignIn"])) || !bytes.Equal(untouched, signinCacheSQL(t, a, true)) {
			t.Fatal("import changed state outside existing tjupt cache cells")
		}
		exported, err := a.ExportAutoSignInTjuptCache(context.Background(), "AutoSignIn")
		if err != nil || !bytes.Equal(exported.Cache.Bytes, data) || (exported.Cache.Bytes == nil) != (data == nil) || exported.Cache.Proof == "" {
			t.Fatal("export normalized opaque source bytes", err)
		}
		if _, exists := signinCacheState(t, owners["AutoSignInClone"]).Cache["tjupt"]; exists {
			t.Fatal("clone inherited original global cache")
		}
	}
}

func TestPluginAutoSignInTjuptCacheTransferSealedRestoreAndScopeTamper(t *testing.T) {
	a, owners := signinCacheSetup(t, "AutoSignIn", "AutoSignInClone")
	ctx := context.Background()
	in := signinCacheSource(t, a, "AutoSignIn", []byte(`{"original":"answer"}`))
	if _, err := a.ImportAutoSignInTjuptCache(ctx, "AutoSignIn", signinCacheRaw(t, in)); err != nil {
		t.Fatal(err)
	}
	saved, err := a.ExportAutoSignInTjuptCache(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	in = signinCacheSource(t, a, "AutoSignIn", []byte(`{"later":"answer"}`))
	if _, err = a.ImportAutoSignInTjuptCache(ctx, "AutoSignIn", signinCacheRaw(t, in)); err != nil {
		t.Fatal(err)
	}
	for _, which := range []string{"stale-destination", "changed-sealed-bytes", "cross-instance", "wrong-source-sha", "wrong-owner-epoch", "wrong-generation", "wrong-config", "wrong-plugin", "wrong-implementation", "wrong-state", "wrong-proof"} {
		t.Run(which, func(t *testing.T) {
			x, e := a.ExportAutoSignInTjuptCache(ctx, "AutoSignIn")
			if e != nil {
				t.Fatal(e)
			}
			x.Cache = saved.Cache
			instance := "AutoSignIn"
			switch which {
			case "stale-destination":
				x.Destination = saved.Destination
			case "changed-sealed-bytes":
				x.Cache.Bytes = []byte(`{"tampered":true}`)
				x.Cache.SHA256 = sourceSettingsDigest(x.Cache.Bytes)
			case "cross-instance":
				clone, e := a.ExportAutoSignInTjuptCache(ctx, "AutoSignInClone")
				if e != nil {
					t.Fatal(e)
				}
				x.Destination, instance = clone.Destination, "AutoSignInClone"
			case "wrong-source-sha":
				x.Cache.SourceSHA256 = strings.Repeat("0", 64)
			case "wrong-owner-epoch":
				x.Destination.OwnerEpoch += "x"
			case "wrong-generation":
				x.Destination.Generation++
			case "wrong-config":
				x.Destination.ConfigRevision++
			case "wrong-plugin":
				x.Destination.PluginID = "Other"
			case "wrong-implementation":
				x.Destination.Implementation += "x"
			case "wrong-state":
				x.Destination.StateDigest = strings.Repeat("0", 64)
			case "wrong-proof":
				x.Destination.Proof = strings.Repeat("0", 64)
			}
			before := signinCacheSQL(t, a, false)
			if _, e := a.ImportAutoSignInTjuptCache(ctx, instance, signinCacheRaw(t, x)); e == nil {
				t.Fatal("tampered or stale proof imported")
			}
			if !bytes.Equal(before, signinCacheSQL(t, a, false)) {
				t.Fatal("rejected import changed SQL")
			}
		})
	}
	fresh, err := a.ExportAutoSignInTjuptCache(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	saved.Destination = fresh.Destination
	r, err := a.ImportAutoSignInTjuptCache(ctx, "AutoSignIn", signinCacheRaw(t, saved))
	if err != nil || r.InputTrust != "local-store-export-authenticated-not-original-source-attestation" || !bytes.Equal(signinCacheState(t, owners["AutoSignIn"]).Cache["tjupt"], saved.Cache.Bytes) {
		t.Fatal("same-scope sealed restore failed", r, err)
	}
}

func TestPluginAutoSignInTjuptCacheTransferRejectsMalformedEnvelopeWithoutWrites(t *testing.T) {
	a, _ := signinCacheSetup(t, "AutoSignIn")
	in := signinCacheSource(t, a, "AutoSignIn", []byte(`{}`))
	raw := string(signinCacheRaw(t, in))
	cases := []string{
		`null`, `{}`, raw + `{}`, strings.Replace(raw, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(raw, `"version":1`, `"Version":1`, 1), strings.Replace(raw, `"present":true`, `"present":null`, 1),
		strings.Replace(raw, `"ready":true`, `"ready": null `, 1), strings.Replace(raw, `"ready_present":true,`, ``, 1),
		strings.Replace(raw, `"bytes":"e30="`, `"bytes":"bad-base64"`, 1), strings.Replace(raw, `"bytes":"e30="`, `"bytes":null`, 1),
		strings.Replace(raw, `"key":"tjupt"`, `"key":"other"`, 1), strings.Replace(raw, `"mode":"source-bytes"`, `"mode":"sealed-export"`, 1),
		strings.Replace(raw, `"mode":"source-bytes"`, `"mode":"source-bytes","path":"/private/source"`, 1),
		strings.Replace(raw, `"present":true`, `"present":false`, 1), strings.Replace(raw, `"proof":""`, `"proof":"caller-forged-proof"`, 1),
	}
	for n, candidate := range cases {
		before := signinCacheSQL(t, a, false)
		if _, err := a.ImportAutoSignInTjuptCache(context.Background(), "AutoSignIn", []byte(candidate)); err == nil {
			t.Fatalf("malformed case %d accepted", n)
		}
		if !bytes.Equal(before, signinCacheSQL(t, a, false)) {
			t.Fatalf("malformed case %d changed SQL", n)
		}
	}
	missing, err := a.ExportAutoSignInTjuptCache(context.Background(), "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.ImportAutoSignInTjuptCache(context.Background(), "AutoSignIn", signinCacheRaw(t, missing)); err == nil {
		t.Fatal("absent-cache preview became a deletion import")
	}
}

func TestPluginAutoSignInTjuptCacheTransferColdRestartAndAtomicRollback(t *testing.T) {
	a, owners := signinCacheSetup(t, "AutoSignIn")
	ctx := context.Background()
	in := signinCacheSource(t, a, "AutoSignIn", bytes.Repeat([]byte("synthetic cache bytes\n"), 20000))
	before := signinCacheSQL(t, a, false)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER signin_cache_failure BEFORE INSERT ON plugin_signin_meta BEGIN SELECT RAISE(ABORT,'synthetic cache publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ImportAutoSignInTjuptCache(ctx, "AutoSignIn", signinCacheRaw(t, in)); err == nil {
		t.Fatal("SQL failure hidden")
	}
	if !bytes.Equal(before, signinCacheSQL(t, a, false)) {
		t.Fatal("partial cache chunks escaped rollback")
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER signin_cache_failure`); err != nil {
		t.Fatal(err)
	}
	if err := owners["AutoSignIn"].Close(); err != nil {
		t.Fatal(err)
	}
	delete(a.pluginAutoSignIn.owners, "AutoSignIn")
	epoch := owners["AutoSignIn"].binding.Epoch
	if _, err := a.ImportAutoSignInTjuptCache(ctx, "AutoSignIn", signinCacheRaw(t, in)); err != nil {
		t.Fatal("cold import failed", err)
	}
	before = signinCacheSQL(t, a, false)
	exported, err := a.ExportAutoSignInTjuptCache(ctx, "AutoSignIn")
	if err != nil || exported.Destination.OwnerEpoch != epoch || !bytes.Equal(exported.Cache.Bytes, in.Cache.Bytes) {
		t.Fatal("cold export lost cache or claimed owner", err)
	}
	if !bytes.Equal(before, signinCacheSQL(t, a, false)) {
		t.Fatal("cold export wrote SQL")
	}
	// A real future owner can still claim, and receives the exact same cache.
	o, err := claimPluginAutoSignInOwner(ctx, a, "AutoSignIn", autoSignInImplementation, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if o.binding.Epoch == epoch || !bytes.Equal(signinCacheState(t, o).Cache["tjupt"], in.Cache.Bytes) {
		t.Fatal("later owner did not retain imported cache")
	}
}

func TestPluginAutoSignInTjuptCacheTransferRejectsInflightAndMalformedState(t *testing.T) {
	for _, which := range []string{"slot", "execution", "pending-core-run", "journal-active", "journal-uncertain", "outbox-pending", "malformed-runtime", "tampered-row", "generation", "config", "closed-owner"} {
		t.Run(which, func(t *testing.T) {
			a, owners := signinCacheSetup(t, "AutoSignIn")
			o, ctx := owners["AutoSignIn"], context.Background()
			in := signinCacheSource(t, a, "AutoSignIn", []byte(`{"synthetic":"answer"}`))
			switch which {
			case "slot":
				slot := a.pluginAutoSignIn.owners["AutoSignIn"]
				slot.gate <- struct{}{}
				defer func() { <-slot.gate }()
			case "execution":
				b, _ := o.backend(nil)
				release, err := b.Hold(ctx, o.binding)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			case "pending-core-run":
				if _, err := a.pluginStore.EnqueueRun(ctx, "AutoSignIn", "service", json.RawMessage(`{}`)); err != nil {
					t.Fatal(err)
				}
			case "journal-active":
				signinCacheMutate(t, o, func(s *signinjournal.State) { s.ActiveRun = "synthetic-active" })
			case "journal-uncertain":
				signinCacheMutate(t, o, func(s *signinjournal.State) {
					s.Runs["synthetic"] = &signinjournal.Run{ID: "synthetic", Scope: s.Scope, Phase: "completed", Lanes: map[string][]signinjournal.Entry{"site": {{Kind: "http", Digest: "synthetic", Phase: "uncertain"}}}}
				})
			case "outbox-pending":
				signinCacheMutate(t, o, func(s *signinjournal.State) {
					s.Outbox = []signinjournal.OutboxItem{{ID: "synthetic", Phase: "pending"}}
				})
			case "malformed-runtime":
				signinCacheMutate(t, o, func(s *signinjournal.State) { s.Runtime = json.RawMessage(`[]`) })
			case "tampered-row":
				if _, err := a.Store.DB.Exec(`UPDATE plugin_signin_rows SET digest=zeroblob(32) WHERE instance_id='AutoSignIn' AND collection='runtime'`); err != nil {
					t.Fatal(err)
				}
			case "generation":
				if _, err := a.Store.DB.Exec(`UPDATE plugin_installations SET generation=generation+1 WHERE instance_id='AutoSignIn'`); err != nil {
					t.Fatal(err)
				}
			case "config":
				if _, err := a.pluginStore.SaveConfig(ctx, "AutoSignIn", 1, map[string]any{"enabled": false, "changed": true}); err != nil {
					t.Fatal(err)
				}
			case "closed-owner":
				if err := o.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before := signinCacheSQL(t, a, false)
			if _, err := a.ImportAutoSignInTjuptCache(ctx, "AutoSignIn", signinCacheRaw(t, in)); err == nil {
				t.Fatal("unsafe import admitted")
			}
			if _, err := a.ExportAutoSignInTjuptCache(ctx, "AutoSignIn"); err == nil {
				t.Fatal("unsafe export admitted")
			}
			if !bytes.Equal(before, signinCacheSQL(t, a, false)) {
				t.Fatal("rejection mutated unsafe state")
			}
		})
	}
}

func TestPluginAutoSignInTjuptCacheTransferCapacityAndForeignOwner(t *testing.T) {
	a, owners := signinCacheSetup(t, "AutoSignIn")
	in := signinCacheSource(t, a, "AutoSignIn", bytes.Repeat([]byte("x"), 100000))
	owners["AutoSignIn"].testCapacity = 1024
	before := signinCacheSQL(t, a, false)
	if _, err := a.ImportAutoSignInTjuptCache(context.Background(), "AutoSignIn", signinCacheRaw(t, in)); !errors.Is(err, signinjournal.ErrCapacity) {
		t.Fatal("capacity not enforced", err)
	}
	if !bytes.Equal(before, signinCacheSQL(t, a, false)) {
		t.Fatal("capacity rejection mutated state")
	}
	owners["AutoSignIn"].testCapacity = 0
	// No runtime-map ownership means the still-locked owner belongs elsewhere;
	// knowing its SQL binding must not grant this caller the flock.
	delete(a.pluginAutoSignIn.owners, "AutoSignIn")
	if _, err := a.ImportAutoSignInTjuptCache(context.Background(), "AutoSignIn", signinCacheRaw(t, in)); !errors.Is(err, signinjournal.ErrBusy) {
		t.Fatal("foreign owner bypassed", err)
	}
	if !bytes.Equal(before, signinCacheSQL(t, a, false)) {
		t.Fatal("foreign owner rejection changed SQL")
	}
}

func TestPluginAutoSignInTjuptCacheTransferCommitReadbackAndNilBytes(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "known-unapplied", true: "lost-ack"}[committed], func(t *testing.T) {
			a, owners := signinCacheSetup(t, "AutoSignIn")
			in := signinCacheSource(t, a, "AutoSignIn", []byte(`{"synthetic":"committed cache"}`))
			before := signinCacheSQL(t, a, false)
			calls := 0
			a.pluginLifecycleMu.Lock()
			receipt, err := a.importAutoSignInTjuptCacheCommitLocked(context.Background(), "AutoSignIn", signinCacheRaw(t, in), func(tx *sql.Tx) error {
				calls++
				if committed {
					if err := tx.Commit(); err != nil {
						return err
					}
				}
				return errors.New("synthetic lost commit acknowledgment")
			})
			a.pluginLifecycleMu.Unlock()
			if calls != 1 {
				t.Fatal("commit retried", calls)
			}
			if committed {
				if err != nil || receipt.State != "imported" || !bytes.Equal(signinCacheState(t, owners["AutoSignIn"]).Cache["tjupt"], in.Cache.Bytes) {
					t.Fatal("committed import mislabeled unapplied", receipt, err)
				}
			} else if err == nil || !bytes.Equal(before, signinCacheSQL(t, a, false)) {
				t.Fatal("uncommitted import escaped rollback", err)
			}
		})
	}
	a, owners := signinCacheSetup(t, "AutoSignIn")
	signinCacheMutate(t, owners["AutoSignIn"], func(s *signinjournal.State) { s.Cache["tjupt"] = nil })
	saved, err := a.ExportAutoSignInTjuptCache(context.Background(), "AutoSignIn")
	if err != nil || !saved.Cache.Present || saved.Cache.Bytes != nil || saved.Cache.ReadyPresent {
		t.Fatal("nil cache shape changed", err)
	}
	in := signinCacheSource(t, a, "AutoSignIn", []byte{})
	if _, err = a.ImportAutoSignInTjuptCache(context.Background(), "AutoSignIn", signinCacheRaw(t, in)); err != nil {
		t.Fatal(err)
	}
	fresh, err := a.ExportAutoSignInTjuptCache(context.Background(), "AutoSignIn")
	if err != nil || fresh.Cache.Bytes == nil {
		t.Fatal("empty bytes became missing", err)
	}
	saved.Destination = fresh.Destination
	if _, err = a.ImportAutoSignInTjuptCache(context.Background(), "AutoSignIn", signinCacheRaw(t, saved)); err != nil {
		t.Fatal(err)
	}
	after := signinCacheState(t, owners["AutoSignIn"])
	value, present := after.Cache["tjupt"]
	_, readyPresent := after.CacheReady["tjupt"]
	if !present || value != nil || readyPresent {
		t.Fatal("sealed restore collapsed missing/nil/empty or cache readiness")
	}
}

func TestPluginAutoSignInTjuptCacheTransferDoesNotInitializeUnclaimedInstance(t *testing.T) {
	a, _ := signinCacheSetup(t, "AutoSignIn")
	manifest, _ := json.Marshal(autosignin.Manifest())
	_, err := a.pluginStore.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: "AutoSignInUnclaimed", PluginID: "AutoSignIn", Version: autosignin.Version, ManifestSchemaVersion: 1, Manifest: manifest}, map[string]any{"enabled": false})
	if err != nil {
		t.Fatal(err)
	}
	before := signinCacheSQL(t, a, false)
	if _, err = a.ExportAutoSignInTjuptCache(context.Background(), "AutoSignInUnclaimed"); err == nil {
		t.Fatal("unclaimed export initialized state")
	}
	if !bytes.Equal(before, signinCacheSQL(t, a, false)) {
		t.Fatal("unclaimed export changed SQL")
	}
}
