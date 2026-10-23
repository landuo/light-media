package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/builtin"
	seed "light-media/internal/plugins/cleaninvalidseed"
	lark "light-media/internal/plugins/larkmessager"
	"light-media/internal/plugins/removelink"
)

// These are real authenticated Handler requests and existing native idle-owner
// fixtures. Disabled configuration keeps the test local: no filesystem watcher,
// downloader, source deletion, network operation or new execution budget.
func lifecycleNativeHTTPFixture(t *testing.T, plugin string) (*App, *http.Cookie) {
	t.Helper()
	a := newTestApp(t)
	a.config = Settings{}
	switch plugin {
	case "RemoveLink":
		run := uninstallInstall(t, a, plugin, removelink.Manifest(), map[string]any{"enabled": false}, true)
		removeLinkOwnerWorker(t, a, run)
	case "CleanInvalidSeed":
		run := uninstallInstall(t, a, plugin, seed.Manifest(), map[string]any{"enabled": false}, true)
		if got := cleanSeedOwnerInvoke(t, a, run); got.State != "succeeded" {
			t.Fatal("native idle fixture did not configure", got.State, got.ErrorCode)
		}
	case "ScrapeFileClean":
		run := scrapeActualInstall(t, a, map[string]any{"enabled": false})
		scrapeActualWorker(t, a, run)
	default:
		t.Fatal("unknown native fixture", plugin)
	}
	return a, setupTestAdmin(t, a)
}

func lifecycleSQLSnapshot(t *testing.T, db *sql.DB, query string, args ...any) []byte {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	result := make([][]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func lifecycleCoreSnapshot(t *testing.T, a *App, instance string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for table, order := range map[string]string{
		"plugin_installations":       "instance_id",
		"plugin_values":              "kind,item_key",
		"plugin_runs":                "id",
		"plugin_schedules":           "service_id",
		"plugin_event_subscriptions": "handler_key",
		"plugin_event_batches":       "id",
	} {
		out[table] = lifecycleSQLSnapshot(t, a.Store.DB, `SELECT * FROM `+table+` WHERE instance_id=? ORDER BY `+order, instance)
	}
	out["plugin_organization"] = lifecycleSQLSnapshot(t, a.Store.DB, `SELECT * FROM plugin_organization ORDER BY owner_id`)
	// Prepare/abort may append legitimate old-generation journal evidence, but
	// a failed publication must not create any new-generation owner authority.
	for _, table := range map[string][]string{
		"RemoveLink":       {"plugin_cleanup_journal_meta", "plugin_cleanup_journal_rows"},
		"CleanInvalidSeed": {"plugin_clean_seed_meta", "plugin_clean_seed_rows"},
		"ScrapeFileClean":  {"plugin_scrape_owner_roots", "plugin_scrape_journal_meta", "plugin_scrape_journal_rows"},
	}[instance] {
		out[table+"/generations"] = lifecycleSQLSnapshot(t, a.Store.DB, `SELECT DISTINCT generation FROM `+table+` WHERE instance_id=? ORDER BY generation`, instance)
	}
	return out
}

func lifecycleNativeOwnerSnapshot(t *testing.T, a *App, plugin string) []byte {
	t.Helper()
	ctx := context.Background()
	var value any
	switch plugin {
	case "RemoveLink":
		o := a.pluginRemoveLink.owners[plugin]
		if o == nil || len(o.gate) != 0 {
			t.Fatal("native RemoveLink owner missing or still locked")
		}
		s, err := o.j.Read(ctx)
		if err != nil || s.Transition != nil && s.Transition.Phase != "applied" {
			t.Fatal("native RemoveLink transition not resolved", err)
		}
		// An applied prior transition may be replaced by prepare/abort, but all
		// business binding, config, serials, receipts and machine state remain.
		s.Transition = nil
		value = s
	case "ScrapeFileClean":
		o := a.pluginScrapeClean.owners[plugin]
		if o == nil || len(o.gate) != 0 {
			t.Fatal("native ScrapeFileClean owner missing or still locked")
		}
		s, err := o.j.Read(ctx)
		if err != nil || s.Transition != nil && s.Transition.Phase != "applied" {
			t.Fatal("native ScrapeFileClean transition not resolved", err)
		}
		s.Transition = nil
		value = s
	case "CleanInvalidSeed":
		o := a.pluginCleanInvalidSeed.owners[plugin]
		if o == nil || len(o.gate) != 0 {
			t.Fatal("native CleanInvalidSeed owner missing or still locked")
		}
		s, meta, err := o.j.read(ctx, a.Store.DB)
		if err != nil || meta.Closed || meta.Transition != nil && meta.Transition.Phase != "applied" {
			t.Fatal("native CleanInvalidSeed transition not resolved", err)
		}
		value = map[string]any{"state": s, "binding": meta.Binding, "config": meta.Config, "applied_run": meta.AppliedRun, "applied_binding": meta.AppliedBinding, "closed": meta.Closed}
	default:
		t.Fatal("unknown native owner", plugin)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPluginLifecycleDedicatedHTTPGenerationCommitAndRollback(t *testing.T) {
	for _, plugin := range []string{"RemoveLink", "CleanInvalidSeed", "ScrapeFileClean"} {
		for _, operation := range []string{"reset", "uninstall"} {
			t.Run(plugin+"/"+operation, func(t *testing.T) {
				a, cookie := lifecycleNativeHTTPFixture(t, plugin)
				ctx := context.Background()
				before := lifecycleInstallation(t, a.pluginStore, plugin)
				lifecycleWantMode(t, a.pluginStore, before, pluginFormInstalledStarted)
				config, err := a.pluginStore.Config(ctx, plugin)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.pluginStore.SaveData(ctx, plugin, "lifecycle-probe", 0, map[string]any{"retained": true}); err != nil {
					t.Fatal(err)
				}
				pending, err := a.pluginStore.EnqueueRun(ctx, plugin, "page", nil)
				if err != nil {
					t.Fatal(err)
				}
				members, _ := json.Marshal([]string{plugin})
				if _, err = a.pluginStore.SaveOrganization(ctx, PluginOrganization{Folders: map[string]json.RawMessage{"probe": members}, Order: json.RawMessage(`["probe"]`)}); err != nil {
					t.Fatal(err)
				}
				for _, query := range []string{
					`INSERT INTO plugin_schedules VALUES(?,'lifecycle-probe','lifecycle-probe',?, ?,x'00','fixture',1,NULL,'fixture')`,
					`INSERT INTO plugin_event_subscriptions VALUES(?,'lifecycle-probe',?,?,'[]',0,1,0)`,
					`INSERT INTO plugin_event_batches(id,instance_id,handler_key,generation,config_revision,due_at,created_at,state) VALUES('lifecycle-probe',?,'lifecycle-probe',?,?,0,'fixture','collecting')`,
				} {
					if _, err = a.Store.DB.Exec(query, plugin, before.Generation, config.Revision); err != nil {
						t.Fatal(err)
					}
				}
				baseline := lifecycleCoreSnapshot(t, a, plugin)
				ownerBefore := lifecycleNativeOwnerSnapshot(t, a, plugin)
				path := "/api/plugins/" + plugin + "/" + operation
				if denied := callTest(t, a.Handler(), "POST", path, `{}`, nil, ""); denied.Code != http.StatusUnauthorized {
					t.Fatal("native lifecycle authorization bypassed", denied.Code)
				}
				for _, failure := range []string{"marker", "after_marker_and_config"} {
					trigger := `CREATE TRIGGER lifecycle_path_failure BEFORE UPDATE OF manifest ON plugin_installations WHEN NEW.instance_id='` + plugin + `' BEGIN SELECT RAISE(ABORT,'synthetic lifecycle marker failure'); END`
					if failure == "after_marker_and_config" {
						// This executes after manifest/gen, config deletion or folder
						// removal and schedule/event changes in the same transaction.
						trigger = `CREATE TRIGGER lifecycle_path_failure BEFORE UPDATE OF state ON plugin_runs WHEN NEW.id='` + pending.ID + `' AND NEW.state='canceled' BEGIN SELECT RAISE(ABORT,'synthetic post-marker lifecycle failure'); END`
					}
					if _, err = a.Store.DB.Exec(trigger); err != nil {
						t.Fatal(err)
					}
					failed := callTest(t, a.Handler(), "POST", path, `{}`, cookie, "")
					if failed.Code != http.StatusInternalServerError {
						t.Fatalf("%s did not fail at injected SQL boundary: %d %s", failure, failed.Code, failed.Body.String())
					}
					if _, err = a.Store.DB.Exec(`DROP TRIGGER lifecycle_path_failure`); err != nil {
						t.Fatal(err)
					}
					if actual := lifecycleCoreSnapshot(t, a, plugin); !reflect.DeepEqual(actual, baseline) {
						for table, want := range baseline {
							if !bytes.Equal(actual[table], want) {
								t.Errorf("%s failed to roll back %s", failure, table)
							}
						}
						t.FailNow()
					}
					if actual := lifecycleNativeOwnerSnapshot(t, a, plugin); !bytes.Equal(actual, ownerBefore) {
						t.Fatal("failed publication changed native business binding or state", failure)
					}
					lifecycleWantMode(t, a.pluginStore, before, pluginFormInstalledStarted)
				}
				succeeded := callTest(t, a.Handler(), "POST", path, `{}`, cookie, "")
				if succeeded.Code != http.StatusOK {
					t.Fatalf("native %s did not commit: %d %s", operation, succeeded.Code, succeeded.Body.String())
				}
				after := lifecycleInstallation(t, a.pluginStore, plugin)
				if after.Generation != before.Generation+1 || after.DesiredEnabled || after.Installed != (operation == "reset") {
					t.Fatal("native lifecycle did not publish exact next identity", after)
				}
				if operation == "reset" {
					lifecycleWantMode(t, a.pluginStore, after, pluginFormInstalledCold)
					if _, err = a.pluginStore.Config(ctx, plugin); !errors.Is(err, sql.ErrNoRows) {
						t.Fatal("reset retained config", err)
					}
					if _, err = a.pluginStore.Data(ctx, plugin, "lifecycle-probe"); !errors.Is(err, sql.ErrNoRows) {
						t.Fatal("reset retained plugin data", err)
					}
				} else {
					if state, err := pluginLifecycleState(after); err != nil || state != pluginLifecycleInactive {
						t.Fatal("uninstall did not stamp inactive", state, err)
					}
					lifecycleWantMode(t, a.pluginStore, lifecyclePreview(after), pluginFormPreview)
					retained, err := a.pluginStore.Config(ctx, plugin)
					if err != nil || retained.Revision != config.Revision || !bytes.Equal(retained.Value, config.Value) {
						t.Fatal("ordinary uninstall changed retained config", err)
					}
					if _, err = a.pluginStore.Data(ctx, plugin, "lifecycle-probe"); err != nil {
						t.Fatal("ordinary uninstall discarded retained data", err)
					}
				}
				actual, err := a.pluginStore.Run(ctx, pending.ID)
				if err != nil || actual.State != "canceled" || actual.ErrorCode != "PLUGIN_SNAPSHOT_STALE" {
					t.Fatal("old pending run survived generation change", actual, err)
				}
				for _, table := range []string{"plugin_schedules", "plugin_event_subscriptions"} {
					var active int
					if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE instance_id=? AND active<>0`, plugin).Scan(&active); err != nil || active != 0 {
						t.Fatal("old generation remained active", table, active, err)
					}
				}
				var batchState string
				if err = a.Store.DB.QueryRow(`SELECT state FROM plugin_event_batches WHERE instance_id=? AND id='lifecycle-probe'`, plugin).Scan(&batchState); err != nil || batchState != "canceled" {
					t.Fatal("old event batch survived", batchState, err)
				}
				org, err := a.pluginStore.Organization(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if operation == "reset" && !bytes.Equal(org.Folders["probe"], members) || operation == "uninstall" && bytes.Contains(org.Folders["probe"], []byte(plugin)) {
					t.Fatal("native folder lifecycle changed", org)
				}
				if a.pluginRemoveLink.owners[plugin] != nil || a.pluginCleanInvalidSeed.owners[plugin] != nil || a.pluginScrapeClean.owners[plugin] != nil {
					t.Fatal("retired native owner remained active")
				}
			})
		}
	}
}

// Occupy only the SDK dispatcher, not SQL or lifecycle locks. The real private
// entry point must commit its claim and marker before it waits for this slot.
// This proves successful claim commit independently from later business work;
// cancellation afterward deliberately avoids launching a worker or doing IO.
func lifecycleObservePrivateClaimCommit(t *testing.T, a *App, parent context.Context, queued PluginRun, invoke func(context.Context) error) {
	t.Helper()
	in := lifecycleInstallation(t, a.pluginStore, queued.InstanceID)
	if state, err := pluginLifecycleState(in); err != nil || state != pluginLifecycleCold {
		t.Fatal("fixture was not authentically cold before claim", state, err)
	}
	previousSlot := a.pluginProcess
	slot := make(chan struct{}, 1)
	slot <- struct{}{}
	a.pluginProcess = slot
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- invoke(ctx) }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("private claim did not stop after dispatcher cancellation")
				return // Do not race a still-running executor by replacing its slot.
			}
		}
		select {
		case <-slot:
		default:
			t.Error("private claim unexpectedly consumed the occupied dispatcher slot")
		}
		a.pluginProcess = previousSlot
	}()
	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		actual, err := a.pluginStore.Run(readCtx, queued.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.State == "running" {
			current, err := a.pluginStore.pluginLifecycleInstallation(readCtx, a.Store.DB, queued.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			if state, err := pluginLifecycleState(current); err != nil || state != pluginLifecycleStarted || current.Generation != queued.Generation {
				t.Fatal("private running commit lacked started evidence", state, err)
			}
			if !bytes.Equal(actual.Input, queued.Input) || actual.ConfigRevision != queued.ConfigRevision || actual.Generation != queued.Generation || actual.Version != queued.Version || actual.Action != queued.Action {
				t.Fatal("private claim changed its frozen admission")
			}
			break
		}
		if actual.State != "pending" {
			t.Fatal("private claim did not stop at the real dispatcher boundary", actual.State, actual.ErrorCode)
		}
		select {
		case err := <-done:
			finished = true
			t.Fatal("private claim exited before successful commit", err)
		case <-readCtx.Done():
			t.Fatal("private claim did not commit", readCtx.Err())
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		finished = true
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal("private dispatcher cancellation failed", err)
		}
	case <-readCtx.Done():
		t.Fatal("committed private claim did not return after cancellation", readCtx.Err())
	}
	current := lifecycleInstallation(t, a.pluginStore, queued.InstanceID)
	if state, err := pluginLifecycleState(current); err != nil || state != pluginLifecycleStarted {
		t.Fatal("post-commit cancellation restored cold evidence", state, err)
	}
}

func TestPluginLifecyclePrivateHTTPClaimsCommitColdToStarted(t *testing.T) {
	for _, plugin := range []string{"BrushFlowLanduo", "SeedRescue"} {
		t.Run(plugin, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			cookie := setupTestAdmin(t, a)
			manifest, _, ok := builtin.Lookup(plugin)
			if !ok {
				t.Fatal(plugin)
			}
			installCloneSource(t, a.pluginStore, manifest)
			var config any = map[string]any{}
			if plugin == "BrushFlowLanduo" {
				config = brushflow.DefaultConfig()
			}
			if _, err := a.pluginStore.SaveConfig(ctx, plugin, 0, config); err != nil {
				t.Fatal(err)
			}
			in := lifecycleInstallation(t, a.pluginStore, plugin)
			request := plugins.HTTPRequest{RequestID: id("ph_"), Key: "status", Method: "GET", Path: "/status", Params: map[string]string{}}
			queued, err := a.enqueuePluginHTTP(ctx, in, request, pluginAPIScope{Key: request.Key, Method: request.Method, RequestID: request.RequestID, Authority: pluginAPIAuthority{Kind: "session", Proof: tokenHash(cookie.Value)}})
			if err != nil {
				t.Fatal(err)
			}
			lifecycleObservePrivateClaimCommit(t, a, ctx, queued, func(ctx context.Context) error {
				if plugin == "BrushFlowLanduo" {
					return a.executeBrushFlowHTTP(ctx, queued)
				}
				return a.executeSeedRescueHTTP(ctx, queued)
			})
		})
	}
}

func TestPluginLifecyclePrivateLarkClaimCommitsColdToStarted(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	manifest := lark.Manifest()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.Install(ctx, PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw, DesiredEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.SaveConfig(ctx, manifest.PluginID, 0, map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	// Existing native-core fixtures create authenticated source admissions with
	// fixture SQL, without using a production plugin claim. Do not forge/reset
	// the lifecycle marker: this real private child is the first such claim.
	core, trusted := lifecycleColdLarkSourceFixture(t, a)
	child, err := a.larkPrepareCoreModule(trusted, core, pluginLarkModuleInput{Name: "message_parser", Source: "Lark", Metadata: core.inbox.Payload})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleObservePrivateClaimCommit(t, a, trusted, child, func(ctx context.Context) error {
		_, err := a.executeLarkSourceRun(ctx, child)
		return err
	})
}

func TestPluginLifecycleManifestCiphertextRejectsOtherInstanceOnly(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	manifest := builtin.SystemProcessManifest()
	installCloneSource(t, p, manifest)
	clone, err := p.Clone(ctx, manifest.PluginID, PluginCloneRequest{Suffix: "aead"})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleWantMode(t, p, clone, pluginFormInstalledCold)
	var sourceCiphertext, cloneCiphertext, cloneMetadata []byte
	if err = s.DB.QueryRow(`SELECT manifest FROM plugin_installations WHERE instance_id=?`, manifest.PluginID).Scan(&sourceCiphertext); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(`SELECT manifest FROM plugin_installations WHERE instance_id=?`, clone.InstanceID).Scan(&cloneCiphertext); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(`SELECT sealed FROM plugin_instance_metadata WHERE instance_id=?`, clone.InstanceID).Scan(&cloneMetadata); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE plugin_installations SET manifest=? WHERE instance_id=?`, sourceCiphertext, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.resolvePluginFormMode(ctx, clone); pluginErrorCode(err) != "PLUGIN_DATA_CORRUPT" {
		t.Fatal("cross-instance ciphertext did not fail AEAD before identity/mode checks", err)
	}
	var unchanged []byte
	if err = s.DB.QueryRow(`SELECT sealed FROM plugin_instance_metadata WHERE instance_id=?`, clone.InstanceID).Scan(&unchanged); err != nil || !bytes.Equal(unchanged, cloneMetadata) {
		t.Fatal("AEAD test changed or removed clone metadata", err)
	}
	if _, err = s.DB.Exec(`UPDATE plugin_installations SET manifest=? WHERE instance_id=?`, cloneCiphertext, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	lifecycleWantMode(t, p, clone, pluginFormInstalledCold)
}

func lifecycleColdLarkInboxFixture(t *testing.T, a *App, source string) (Job, string) {
	t.Helper()
	ctx := context.Background()
	installation, e := a.pluginStore.Installation(ctx, "LarkMessager")
	if e != nil {
		t.Fatal(e)
	}
	request := plugins.HTTPRequest{RequestID: id("ph_"), Key: "webhook", Method: "POST", Path: "/webhook"}
	run, e := a.enqueuePluginHTTP(ctx, installation, request, pluginAPIScope{Key: request.Key, Method: request.Method, RequestID: request.RequestID, Authority: pluginAPIAuthority{Kind: "anonymous"}})
	if e != nil {
		t.Fatal(e)
	}
	// Seed a legacy authenticated source outcome, not an executor completion.
	// This isolated boundary fixture has never claimed a plugin run, so its
	// genuine installed generation remains cold. All regular Lark workflow
	// fixtures use the real webhook claim/finish path instead.
	sealed, e := a.pluginStore.seal("run_result", run.InstanceID, run.ID, json.RawMessage(`null`))
	if e != nil {
		t.Fatal(e)
	}
	changed, e := a.Store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='succeeded',result=?,updated_at=? WHERE id=? AND state='pending'`, sealed, now(), run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if n, e := changed.RowsAffected(); e != nil || n != 1 {
		t.Fatal("legacy source fixture", n, e)
	}
	var attempts int
	if e = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_run_attempts WHERE instance_id=?`, run.InstanceID).Scan(&attempts); e != nil || attempts != 0 {
		t.Fatal("cold fixture accidentally claimed", attempts, e)
	}
	var inv plugins.Invocation
	if e = json.Unmarshal(run.Input, &inv); e != nil {
		t.Fatal(e)
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	inboxID := id("li_")
	j, e := enqueueTx(ctx, tx, "lark_message", map[string]string{"id": inboxID}, inboxID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='running',generation=1 WHERE id=?`, j.ID); e != nil {
		t.Fatal(e)
	}
	j.State, j.Generation = "running", 1
	root, e := a.pluginStore.larkRootRead(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		t.Fatal(e)
	}
	f := pluginLarkInbox{Version: 1, ID: inboxID, Instance: run.InstanceID, Generation: run.Generation, Revision: run.ConfigRevision, OriginRun: run.ID, InputSHA: deferredInputSHA(run.Input), ConfigSHA: tokenHash(string(inv.Config)), Job: j.ID, JobInputSHA: tokenHash(string(j.Payload)), SourceID: source, State: "queued"}
	if e = a.pluginStore.larkSaveInboxTx(ctx, tx, root, f); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return j, inboxID
}
func lifecycleColdLarkSourceFixture(t *testing.T, a *App) (*pluginLarkCoreExecution, context.Context) {
	t.Helper()
	job, inbox := lifecycleColdLarkInboxFixture(t, a, "owned/cold-private-claim")
	core := &pluginLarkCoreExecution{a: a, job: job}
	ctx := context.Background()
	if _, err := a.larkInboxForJob(ctx, job, inbox, core); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(core.release)
	return core, context.WithValue(ctx, pluginLarkCoreKey{}, core)
}
