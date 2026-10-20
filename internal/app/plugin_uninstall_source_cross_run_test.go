package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/uninstall"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"testing"
)

func TestPluginUninstallSourceHTTPNewRunUsesWarmOrColdInstalledListAfterCommitFailure(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(map[bool]string{false: "warm_cache", true: "cold_durable"}[cold], func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			a, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			open := true
			t.Cleanup(func() {
				if open {
					_ = a.Close()
				}
			})
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			for _, id := range []string{"CategoryEditor", "PluginUnInstall"} {
				w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"`+id+`"}`, cookie, "")
				if w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
				run, e := a.pluginStore.claimRunForInstance(ctx, id)
				if e != nil {
					t.Fatal(e)
				}
				a.executePluginRun(ctx, run)
			}
			if _, err = a.Store.DB.Exec(`CREATE TRIGGER source_cross_run_failure BEFORE UPDATE ON plugin_uninstall_source_lists BEGIN SELECT RAISE(ABORT,'owned list commit failure'); END`); err != nil {
				t.Fatal(err)
			}
			save := func(revision int64) PluginRun {
				t.Helper()
				w := callTest(t, a.Handler(), "PUT", "/api/plugins/PluginUnInstall/config", `{"revision":`+strconv.FormatInt(revision, 10)+`,"config":{"plugin_ids":["CategoryEditor"],"clear_config":true,"clear_data":true}}`, cookie, "")
				if w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
				r, e := a.pluginStore.claimRunForInstance(ctx, "PluginUnInstall")
				if e != nil {
					t.Fatal(e)
				}
				a.executePluginRun(ctx, r)
				r, e = a.pluginStore.Run(ctx, r.ID)
				if e != nil {
					t.Fatal(e)
				}
				return r
			}
			first := save(1)
			if first.State != "failed" || first.ErrorCode != uninstallSourceFailed {
				t.Fatal(first.State, first.ErrorCode)
			}
			before, _ := a.pluginStore.Installation(ctx, "CategoryEditor")
			if before.Installed || before.Generation != 2 {
				t.Fatal(before)
			}
			if _, err = a.Store.DB.Exec(`DROP TRIGGER source_cross_run_failure`); err != nil {
				t.Fatal(err)
			}
			if cold {
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				open = false
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				open = true
				useBuiltInPluginProcess(t, a)
			}
			second := save(2)
			if second.ID == first.ID || second.State != "succeeded" {
				t.Fatal("independent run", second.State, second.ErrorCode)
			}
			want := []string{"PluginUnInstall"}
			removed := `"removed":[]`
			if cold {
				want = []string{"CategoryEditor", "PluginUnInstall"}
				removed = `"removed":["CategoryEditor"]`
			}
			record, _, err := a.pluginStore.readUninstallSource(ctx, a.Store.DB, second)
			if err != nil || !reflect.DeepEqual(record.InstalledBefore, want) || !bytes.Contains(second.Result, []byte(removed)) {
				t.Fatalf("source global installed list across independent run: cold=%v actual=%v expected=%v result=%s err=%v", cold, record.InstalledBefore, want, second.Result, err)
			}
			after, _ := a.pluginStore.Installation(ctx, "CategoryEditor")
			if after.Installed || after.Generation != 2 {
				t.Fatal("absent class removal changed native generation", after)
			}
		})
	}
}

func TestPluginUninstallSourceNewRunAfterMiddleFailureSeesEarlierRemovedClass(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, id := range []string{"Alpha", "Beta"} {
		uninstallInstall(t, a, id, builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	}
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"Alpha", "Beta"}, "clear_config": true, "clear_data": true}, true)
	if _, err := a.Store.DB.Exec(`INSERT INTO plugin_schedules VALUES('Beta','owned','owned',1,1,x'00','owned',1,NULL,?)`, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER source_middle_schedule BEFORE UPDATE ON plugin_schedules WHEN NEW.instance_id='Beta' BEGIN SELECT RAISE(ABORT,'owned schedule'); END; CREATE TRIGGER source_middle_notice BEFORE INSERT ON records WHEN NEW.kind='notification' BEGIN SELECT RAISE(ABORT,'owned notice'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applyPluginUninstall(ctx, &run); pluginErrorCode(err) != uninstallSourceFailed {
		t.Fatal(err)
	}
	alpha, _ := a.pluginStore.Installation(ctx, "Alpha")
	beta, _ := a.pluginStore.Installation(ctx, "Beta")
	if alpha.Installed || !beta.Installed {
		t.Fatal("initial source cut", alpha.Installed, beta.Installed)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER source_middle_schedule; DROP TRIGGER source_middle_notice; UPDATE plugin_runs SET state='failed' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err := a.pluginStore.SaveConfigAndQueue(ctx, "PluginUnInstall", 1, map[string]any{"plugin_ids": []string{"Beta", "Alpha"}, "clear_config": true, "clear_data": true})
	if err != nil {
		t.Fatal(err)
	}
	next, err := a.pluginStore.claimRunForInstance(ctx, "PluginUnInstall")
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.applyPluginUninstall(ctx, &next)
	if err != nil || !bytes.Contains(result, []byte(`"removed":["Alpha","Beta"]`)) {
		t.Fatal(string(result), err)
	}
	alpha, _ = a.pluginStore.Installation(ctx, "Alpha")
	beta, _ = a.pluginStore.Installation(ctx, "Beta")
	if alpha.Generation != 2 || beta.Generation != 2 || alpha.Installed || beta.Installed {
		t.Fatal("absent class stop is idempotent", alpha.Generation, beta.Generation)
	}
}

func TestPluginUninstallSourceReinstallAfterFailedInstalledCommitUsesSourceWarmOrColdOrder(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(map[bool]string{false: "warm_append", true: "cold_existing"}[cold], func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			a, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			open := true
			t.Cleanup(func() {
				if open {
					_ = a.Close()
				}
			})
			uninstallInstall(t, a, "Alpha", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
			run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"Alpha"}}, true)
			if _, err = a.Store.DB.Exec(`CREATE TRIGGER source_reinstall_failure BEFORE UPDATE ON plugin_source_installed BEGIN SELECT RAISE(ABORT,'owned global list commit'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err = a.applyPluginUninstall(ctx, &run); pluginErrorCode(err) != uninstallSourceFailed {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`DROP TRIGGER source_reinstall_failure`); err != nil {
				t.Fatal(err)
			}
			if cold {
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				open = false
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				open = true
			}
			manifest, _ := json.Marshal(builtin.CategoryEditorManifest())
			if err = a.pluginStore.Install(ctx, PluginInstallation{InstanceID: "Alpha", PluginID: "CategoryEditor", Version: builtin.CategoryEditorManifest().Version, ManifestSchemaVersion: 1, Manifest: manifest, DesiredEnabled: true}); err != nil {
				t.Fatal(err)
			}
			ids, _, err := a.pluginStore.sourceInstalled(ctx, a.Store.DB, true)
			want := []string{"PluginUnInstall", "Alpha"}
			if cold {
				want = []string{"Alpha", "PluginUnInstall"}
			}
			if err != nil || !reflect.DeepEqual(ids, want) {
				t.Fatal("source API appends only an ID absent from its warm/cold installed list", ids, want, err)
			}
			if _, err = a.applyPluginUninstall(ctx, &run); err == nil {
				t.Fatal("old failed run replayed replacement")
			}
			current, _ := a.pluginStore.Installation(ctx, "Alpha")
			if !current.Installed || current.Generation != 3 {
				t.Fatal(current)
			}
		})
	}
}

func TestPluginUninstallSourceEmptySelectionDoesNotReadDamagedGlobalList(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{}, "clear_config": true, "clear_data": true}, true)
	if _, err := a.Store.DB.Exec(`DELETE FROM plugin_source_installed`); err != nil {
		t.Fatal(err)
	}
	result, err := a.applyPluginUninstall(ctx, &run)
	if err != nil || string(result) != "null" {
		t.Fatal(string(result), err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_uninstall_source`).Scan(&count); err != nil || count != 0 {
		t.Fatal("empty selection created source effect", count, err)
	}
	if w := callTest(t, a.Handler(), "GET", "/healthz", "", nil, ""); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
}

func TestPluginUninstallSourceCrossRunInstalledASTOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/uninstall-source-failure/cross-run-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Cases []struct {
			Cold  bool `json:"cold"`
			First struct {
				Cache   []string `json:"cache_installed"`
				Durable []string `json:"durable_installed"`
			} `json:"first"`
			SecondTrace []struct{ Stage, Target string } `json:"second_trace"`
			Cache       []string                         `json:"cache_installed"`
			Durable     []string                         `json:"durable_installed"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil || len(cases.Cases) != 2 {
		t.Fatal(err)
	}
	for _, ref := range cases.Cases {
		t.Run(map[bool]string{false: "warm", true: "cold"}[ref.Cold], func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			a, e := New(Options{DataDir: dir})
			if e != nil {
				t.Fatal(e)
			}
			open := true
			t.Cleanup(func() {
				if open {
					_ = a.Close()
				}
			})
			for _, id := range []string{"Alpha", "Beta"} {
				uninstallInstall(t, a, id, builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
			}
			run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"Alpha", "Beta"}, "clear_config": true, "clear_data": true}, true)
			if _, e = a.Store.DB.Exec(`CREATE TRIGGER source_oracle_commit BEFORE UPDATE ON plugin_source_installed BEGIN SELECT RAISE(ABORT,'owned global commit'); END`); e != nil {
				t.Fatal(e)
			}
			if _, e = a.applyPluginUninstall(ctx, &run); pluginErrorCode(e) != uninstallSourceFailed {
				t.Fatal(e)
			}
			cache, _, e := a.pluginStore.sourceInstalled(ctx, a.Store.DB, true)
			if e != nil || !reflect.DeepEqual(cache, ref.First.Cache) {
				t.Fatal("first source cache", cache, e)
			}
			durable, _, e := a.pluginStore.sourceInstalled(ctx, a.Store.DB, false)
			if e != nil || !reflect.DeepEqual(durable, ref.First.Durable) {
				t.Fatal("first source durable", durable, e)
			}
			if _, e = a.Store.DB.Exec(`DROP TRIGGER source_oracle_commit; UPDATE plugin_runs SET state='failed' WHERE id=?`, run.ID); e != nil {
				t.Fatal(e)
			}
			if ref.Cold {
				if e = a.Close(); e != nil {
					t.Fatal(e)
				}
				open = false
				a, e = New(Options{DataDir: dir})
				if e != nil {
					t.Fatal(e)
				}
				open = true
			}
			if _, _, e = a.pluginStore.SaveConfigAndQueue(ctx, "PluginUnInstall", 1, map[string]any{"plugin_ids": []string{"Beta", "Alpha"}, "clear_config": true, "clear_data": true}); e != nil {
				t.Fatal(e)
			}
			next, e := a.pluginStore.claimRunForInstance(ctx, "PluginUnInstall")
			if e != nil {
				t.Fatal(e)
			}
			result, e := a.applyPluginUninstall(ctx, &next)
			if e != nil {
				t.Fatal(e)
			}
			var actual struct {
				Removed []string `json:"removed"`
			}
			if e = json.Unmarshal(result, &actual); e != nil {
				t.Fatal(e)
			}
			expected := []string{}
			for _, x := range ref.SecondTrace {
				if x.Stage == "remove_jobs_entry" {
					expected = append(expected, x.Target)
				}
			}
			if !reflect.DeepEqual(actual.Removed, expected) {
				t.Fatal("source init/Manager/SystemConfig AST second-run trace", actual.Removed, expected)
			}
			cache, _, e = a.pluginStore.sourceInstalled(ctx, a.Store.DB, true)
			if e != nil || !reflect.DeepEqual(cache, ref.Cache) {
				t.Fatal(cache, e)
			}
			durable, _, e = a.pluginStore.sourceInstalled(ctx, a.Store.DB, false)
			if e != nil || !reflect.DeepEqual(durable, ref.Durable) {
				t.Fatal(durable, e)
			}
		})
	}
}

func TestPluginUninstallSourceGlobalAbsentThenEqualEmptyFollowsSystemConfigSet(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"Unknown"}}, true)
	_, revision, err := a.pluginStore.sourceInstalled(ctx, a.Store.DB, false)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.writeSourceInstalledTx(ctx, tx, revision, []string{}, false); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = a.applyPluginUninstall(ctx, &run); err != nil {
		t.Fatal(err)
	}
	value, firstRevision, err := a.pluginStore.sourceInstalledState(ctx, a.Store.DB)
	if err != nil || !value.Exists || len(value.IDs) != 0 {
		t.Fatal("source conf absent creates an explicit empty list", value, err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='succeeded' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.pluginStore.SaveConfigAndQueue(ctx, "PluginUnInstall", 2, map[string]any{"plugin_ids": []string{"Unknown"}}); err != nil {
		t.Fatal(err)
	}
	second, err := a.pluginStore.claimRunForInstance(ctx, "PluginUnInstall")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.applyPluginUninstall(ctx, &second); err != nil {
		t.Fatal(err)
	}
	value, secondRevision, err := a.pluginStore.sourceInstalledState(ctx, a.Store.DB)
	if err != nil || !value.Exists || len(value.IDs) != 0 || firstRevision != secondRevision {
		t.Fatal("source existing same empty list skips the DB commit", firstRevision, secondRevision, value, err)
	}
}

func TestPluginUninstallSourceOwnWarmCacheReturnsBeforeInventoryOnNextInvocation(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	uninstallInstall(t, a, "Alpha", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"Alpha"}}, true)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER source_own_cache_failure BEFORE UPDATE ON plugin_values WHEN NEW.instance_id='PluginUnInstall' BEGIN SELECT RAISE(ABORT,'owned reset'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applyPluginUninstall(ctx, &run); pluginErrorCode(err) != uninstallSourceFailed {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER source_own_cache_failure; UPDATE plugin_runs SET state='failed' WHERE id=?; DELETE FROM plugin_source_installed`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.EnqueueRun(ctx, "PluginUnInstall", "reload", nil); err != nil {
		t.Fatal(err)
	}
	second, err := a.pluginStore.claimRunForInstance(ctx, "PluginUnInstall")
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.applyPluginUninstall(ctx, &second)
	if err != nil || string(result) != "null" {
		t.Fatal("source reads its warm empty selection before damaged global inventory", string(result), err)
	}
}
