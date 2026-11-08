package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"light-media/internal/plugins"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

func sourceUninstallFixture(t *testing.T) (*App, *http.Cookie) {
	t.Helper()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	for _, variable := range []string{"SOURCE_UNINSTALL_BINARY", "SOURCE_UNINSTALL_FUTURE_BINARY"} {
		path := os.Getenv(variable)
		if path == "" {
			t.Fatalf("%s must name an actual independently compiled target worker", variable)
		}
		installOwnedResourceProcessWorker(t, a, cookie, path)
	}
	t.Cleanup(a.closeOriginalSessions)
	return a, cookie
}
func sourceUninstallSave(t *testing.T, a *App, id string, raw json.RawMessage) {
	t.Helper()
	in, err := a.pluginStore.Installation(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.saveOriginalPluginConfiguration(context.Background(), in, raw); err != nil {
		t.Fatal(err)
	}
	a.invalidateOriginalBaseConfig(id)
}
func sourceUninstallConfigure(t *testing.T, a *App, cookie *http.Cookie, id string, selection []string, wantSuccess bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"plugin_ids": selection, "clear_config": true, "clear_data": true, "unknown": "preserved-until-tail"})
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(body), cookie, "")
	if wantSuccess {
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	} else if r.Code < 400 {
		t.Fatal("source unexpected fault was swallowed", r.Body.String())
	}
	a.wg.Wait()
}
func sourceUninstallOracle(t *testing.T, name string) struct {
	Classes, Running, Installed []string
	Config                      map[string]json.RawMessage
	State                       map[string]bool
} {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/plugin-source-uninstall-20261105/lifecycle-source21.json")
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Cases []struct {
			Name  string
			After struct {
				Classes, Running []string
				Installed        []string                   `json:"installed_cache"`
				Config           map[string]json.RawMessage `json:"public_config"`
				State            map[string]bool            `json:"public_state"`
			}
		}
	}
	if json.Unmarshal(raw, &root) != nil || len(root.Cases) != 21 {
		t.Fatal("fixed actual source21")
	}
	for _, c := range root.Cases {
		if c.Name == name {
			return struct {
				Classes, Running, Installed []string
				Config                      map[string]json.RawMessage
				State                       map[string]bool
			}{c.After.Classes, c.After.Running, c.After.Installed, c.After.Config, c.After.State}
		}
	}
	t.Fatal("source case missing", name)
	panic("unreachable")
}
func sourceUninstallProjection(t *testing.T, a *App, name string) {
	t.Helper()
	want := sourceUninstallOracle(t, name)
	keep := func(ids []string) []string {
		v := []string{}
		for _, id := range ids {
			if id != "Alpha" {
				v = append(v, id)
			}
		}
		return v
	}
	a.pluginOriginalRunningMu.Lock()
	classes := append([]string{}, a.pluginOriginalClassOrder...)
	running := append([]string{}, a.pluginOriginalRunningOrder...)
	a.pluginOriginalRunningMu.Unlock()
	if !slices.Equal(classes, keep(want.Classes)) || !slices.Equal(running, keep(want.Running)) {
		t.Fatal("source class/running split", classes, running, want.Classes, want.Running)
	}
	ids, err := a.originalInstalledPluginIDs(context.Background())
	if err != nil || !slices.Equal(ids, keep(want.Installed)) {
		t.Fatal("source membership", ids, want.Installed, err)
	}
	for _, id := range []string{"PluginUnInstall", "PluginUnInstallowned", "MPServerStatus"} {
		if expected, ok := want.Config[id]; ok {
			got, err := a.originalManagerConfiguration(context.Background(), id)
			if err != nil || !originalBaseJSONEqual(got, expected) {
				t.Fatal("source public class-gated config", id, string(got), string(expected), err)
			}
		}
		if object, ok := a.pluginRunningInstances.Load(id); ok {
			state, err := object.(*originalPluginInstance).getState(context.Background())
			if err != nil || state != want.State[id] {
				t.Fatal("source real running state", id, state, want.State[id], err)
			}
		}
	}
}
func TestSourceUninstallActualWorkersReinitSelfAndFuture(t *testing.T) {
	for _, c := range []struct {
		name      string
		selection []string
	}{{"loaded-reinit-self", []string{"PluginUnInstall"}}, {"loaded-reinit-future99", []string{"MPServerStatus"}}, {"loaded-reinit-self-and-future99", []string{"PluginUnInstall", "MPServerStatus"}}} {
		t.Run(c.name, func(t *testing.T) {
			a, cookie := sourceUninstallFixture(t)
			sourceUninstallSave(t, a, "MPServerStatus", json.RawMessage(`{"enable":true,"retain":"mps"}`))
			if err := a.initOriginalLoadedPlugin(context.Background(), "MPServerStatus"); err != nil {
				t.Fatal(err)
			}
			sourceUninstallConfigure(t, a, cookie, "PluginUnInstall", c.selection, true)
			sourceUninstallProjection(t, a, c.name)
			for _, id := range c.selection {
				if _, err := os.Stat(filepath.Join(a.pluginPackageRoot(), id)); !os.IsNotExist(err) {
					t.Fatal("own code not removed", id, err)
				}
			}
		})
	}
}
func TestSourceUninstallActualColdPreimportedFutureAndSelf(t *testing.T) {
	for _, c := range []struct {
		name      string
		selection []string
	}{{"cold-self", []string{"PluginUnInstall"}}, {"cold-future99", []string{"MPServerStatus"}}, {"cold-self-and-future99", []string{"PluginUnInstall", "MPServerStatus"}}} {
		t.Run(c.name, func(t *testing.T) {
			a, _ := sourceUninstallFixture(t)
			sourceUninstallSave(t, a, "MPServerStatus", json.RawMessage(`{"enable":true,"retain":"mps"}`))
			raw, _ := json.Marshal(map[string]any{"plugin_ids": c.selection, "clear_config": true, "clear_data": true, "unknown": "preserved-until-tail"})
			sourceUninstallSave(t, a, "PluginUnInstall", raw)
			dir := a.Dir
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			cold, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer cold.Close()
			if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			cold.wg.Wait()
			sourceUninstallProjection(t, cold, c.name)
			// A second genuine cold process has no pre-imported classes left for removed
			// code. This must not become a durable continuation or a code resurrection.
			if err = cold.Close(); err != nil {
				t.Fatal(err)
			}
			again, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			if err = again.initializeOriginalPlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, id := range c.selection {
				if _, ok := again.pluginRunningInstances.Load(id); ok {
					t.Fatal("removed code replayed on next cold", id)
				}
			}
		})
	}
}
func TestSourceUninstallInstalledCacheFailureAndDeepCopies(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, id := range []string{"Alpha", "Beta", "Gamma"} {
		if err := a.pluginStore.Install(ctx, PluginInstallation{InstanceID: id, PluginID: id, Version: "1", ManifestSchemaVersion: 1, Manifest: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`["Alpha","Beta","Gamma"]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_uninstall_reject BEFORE UPDATE ON records WHEN NEW.kind='plugin-original-installed-order-v1' BEGIN SELECT RAISE(FAIL,'owned source DB failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`["Gamma"]`)); err == nil {
		t.Fatal("real SQLite failure not reached")
	}
	got, err := a.originalInstalledPluginIDs(ctx)
	if err != nil || !slices.Equal(got, []string{"Gamma"}) {
		t.Fatal("failed DB write rolled source cache back", got, err)
	}
	got[0] = "tampered"
	got, _ = a.originalInstalledPluginIDs(ctx)
	if !slices.Equal(got, []string{"Gamma"}) {
		t.Fatal("get leaked cache alias")
	}
	p, err := NewPluginStore(ctx, a.Store)
	if err != nil {
		t.Fatal(err)
	}
	db, err := p.originalInstalledPluginIDs(ctx, a.Store.DB)
	if err != nil || !reflect.DeepEqual(db, []string{"Alpha", "Beta", "Gamma"}) {
		t.Fatal("real DB changed after failed write", db, err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_uninstall_reject`); err != nil {
		t.Fatal(err)
	}
	if err = a.setOriginalPluginInstalled(ctx, "Gamma", false); err != nil {
		t.Fatal(err)
	}
	got, err = a.originalInstalledPluginIDs(ctx)
	if err != nil || len(got) != 0 {
		t.Fatal("ordinary writer did not invalidate finite source cache", got, err)
	}
}
func TestSourceUninstallActualOpaqueFailurePreservesEarlierEffects(t *testing.T) {
	a, cookie := sourceUninstallFixture(t)
	// Real SQL failure after removing the first target leaves source effects and
	// cache ahead of storage; config tail is not applied or replayed.
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_uninstall_reject BEFORE UPDATE ON records WHEN NEW.kind='plugin-original-installed-order-v1' BEGIN SELECT RAISE(FAIL,'owned source DB failure'); END`); err != nil {
		t.Fatal(err)
	}
	sourceUninstallConfigure(t, a, cookie, "PluginUnInstall", []string{"MPServerStatus"}, false)
	if _, ok := a.pluginRunningInstances.Load("MPServerStatus"); ok {
		t.Fatal("earlier manager effect rolled back")
	}
	ids, _ := a.originalInstalledPluginIDs(context.Background())
	if !slices.Equal(ids, []string{"PluginUnInstall"}) {
		t.Fatal(ids)
	}
	config, err := a.pluginStore.Config(context.Background(), "PluginUnInstall")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		IDs []string `json:"plugin_ids"`
	}
	_ = json.Unmarshal(config.Value, &c)
	if !slices.Equal(c.IDs, []string{"MPServerStatus"}) {
		t.Fatal("failed tail silently cleared source selection", string(config.Value))
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_uninstall_reject`); err != nil {
		t.Fatal(err)
	}
}
func TestSourceUninstallCodeOnlyFinitePathsAndSourceResidues(t *testing.T) {
	a := newTestApp(t)
	root := a.pluginPackageRoot()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"directory", "file", "symlink", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			id := "Owned" + kind
			p := filepath.Join(root, id)
			outside := t.TempDir()
			if kind == "directory" {
				if err := os.Mkdir(p, 0700); err != nil {
					t.Fatal(err)
				}
			} else if kind == "file" {
				if err := os.WriteFile(p, []byte("own"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				target := outside
				if kind == "dangling" {
					target = filepath.Join(outside, "missing")
				}
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
			}
			got, err := a.sourceOriginalCode(context.Background(), id, "code.exists")
			if err != nil || string(got) != fmt.Sprint(kind != "dangling") {
				t.Fatal(kind, string(got), err)
			}
			if _, err = a.sourceOriginalCode(context.Background(), id, "code.remove"); err != nil {
				t.Fatal(err)
			}
			_, err = os.Lstat(p)
			if (err == nil) != (kind != "directory") {
				t.Fatal("source rmtree residue", kind, err)
			}
			if _, err = os.Stat(outside); err != nil {
				t.Fatal("symlink target touched")
			}
		})
	}
	for _, id := range []string{"../escape", "/tmp/escape", "Owned/child", "..", ""} {
		if _, err := a.sourceOriginalCode(context.Background(), id, "code.remove"); err == nil {
			t.Fatal("worker selected arbitrary package path", id)
		}
	}
}

func TestSourceUninstallActualCloneIdentityAndDataRetention(t *testing.T) {
	for _, coldMode := range []bool{false, true} {
		t.Run(fmt.Sprint("cold=", coldMode), func(t *testing.T) {
			a, cookie := sourceUninstallFixture(t)
			ctx := context.Background()
			r := callTest(t, a.Handler(), "POST", "/api/plugins/PluginUnInstall/clone", `{"suffix":"owned","version":"9.4"}`, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			clone := "PluginUnInstallowned"
			if err := a.stopOriginalPlugin(ctx, "PluginUnInstall"); err != nil {
				t.Fatal(err)
			}
			if err := a.setOriginalPluginInstalled(ctx, "PluginUnInstall", false); err != nil {
				t.Fatal(err)
			}
			sourceUninstallSave(t, a, "MPServerStatus", json.RawMessage(`{"enable":true,"retain":"mps"}`))
			if err := a.initOriginalLoadedPlugin(ctx, "MPServerStatus"); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"PluginUnInstall", clone, "MPServerStatus"} {
				if err := a.pluginStore.originalBaseSaveData(ctx, id, "retained", json.RawMessage(`{"owned":true}`)); err != nil {
					t.Fatal(err)
				}
			}
			target := a
			name := "loaded-reinit-clone-self"
			if coldMode {
				body, _ := json.Marshal(map[string]any{"plugin_ids": []string{clone}, "clear_config": true, "clear_data": true, "unknown": "preserved-until-tail"})
				sourceUninstallSave(t, a, clone, body)
				dir := a.Dir
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
				var err error
				target, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				defer target.Close()
				if err = target.initializeOriginalPlugins(ctx); err != nil {
					t.Fatal(err)
				}
				target.wg.Wait()
				name = "cold-clone-self"
			} else {
				sourceUninstallConfigure(t, a, cookie, clone, []string{clone}, true)
			}
			sourceUninstallProjection(t, target, name)
			if _, err := os.Stat(filepath.Join(target.pluginPackageRoot(), "PluginUnInstall")); err != nil {
				t.Fatal("clone removed independent source package", err)
			}
			key := "retained"
			for _, id := range []string{"PluginUnInstall", clone, "MPServerStatus"} {
				v, err := target.pluginStore.originalBaseGetData(ctx, id, &key)
				if err != nil || !originalBaseJSONEqual(v, json.RawMessage(`{"owned":true}`)) {
					t.Fatal("force=false config/data guard lost retained namespace", id, string(v), err)
				}
			}
		})
	}
}
func TestSourceUninstallActualPermissionErrorStopsAfterLogicalRemoval(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("permission oracle requires nonroot owned test process")
	}
	a, cookie := sourceUninstallFixture(t)
	ctx := context.Background()
	sourceUninstallSave(t, a, "MPServerStatus", json.RawMessage(`{"enable":true,"retain":"mps"}`))
	if err := a.initOriginalLoadedPlugin(ctx, "MPServerStatus"); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"plugin_ids":["MPServerStatus"],"clear_config":true,"clear_data":true,"unknown":"preserved-until-tail"}`)
	sourceUninstallSave(t, a, "PluginUnInstall", raw)
	root := a.pluginPackageRoot()
	if err := os.Chmod(root, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0700)
	err := a.initOriginalLoadedPlugin(ctx, "PluginUnInstall")
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	if err == nil {
		t.Fatal("actual stat PermissionError was swallowed")
	}
	a.wg.Wait()
	sourceUninstallProjection(t, a, "loaded-reinit-future99-exists-error")
	_ = cookie
}
func TestSourceUninstallActualOrdinaryRmtreePermissionFailureIsSuppressed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("permission oracle requires nonroot owned test process")
	}
	a, cookie := sourceUninstallFixture(t)
	path := filepath.Join(a.pluginPackageRoot(), "MPServerStatus")
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0700)
	sourceUninstallConfigure(t, a, cookie, "PluginUnInstall", []string{"MPServerStatus"}, true)
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal("ordinary source residue not retained", err)
	}
	ids, err := a.originalInstalledPluginIDs(context.Background())
	if err != nil || !slices.Equal(ids, []string{"PluginUnInstall"}) {
		t.Fatal("suppressed filesystem failure stopped source final tail", ids, err)
	}
}
func TestSourceUninstallActualOrdinaryMethodCannotForgeSelfInitAuthority(t *testing.T) {
	a, _, object := ownedHostServicesFixture(t)
	_, err := ownedHostServicesCall(t, object, plugins.PluginManagerControlCapability, plugins.PluginManagerRequest{InstanceID: object.installation.InstanceID, Action: "remove"})
	if err == nil {
		t.Fatal("ordinary resource forged initializer authority")
	}
	if !originalObjectStillLoaded(object) {
		t.Fatal("rejected ordinary self remove changed maps")
	}
	// A stale private initializer pointer is not live authority, either.
	lease := &originalInitializationCall{object: object}
	ctx := context.WithValue(context.Background(), originalInitializingObjectKey{}, lease)
	callback, revoke := object.originalBaseSessionHost(ctx, "resource", nil)
	revoke()
	raw, _ := json.Marshal(plugins.PluginManagerRequest{InstanceID: object.installation.InstanceID, Action: "remove"})
	if _, err = callback(ctx, plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}, plugins.PluginManagerControlCapability, raw); err == nil {
		t.Fatal("closed callback borrowed expired initializer")
	}
	_ = a
}

func TestSourceUninstallActualHundredPackagesAndFreshInitLineage(t *testing.T) {
	a, cookie := sourceUninstallFixture(t)
	ctx := context.Background()
	selected := []string{}
	all := []string{"PluginUnInstall", "MPServerStatus"}
	for i := range 100 {
		id := fmt.Sprintf("OwnedPackage%03d", i)
		selected = append(selected, id)
		all = append(all, id)
		dir := filepath.Join(a.pluginPackageRoot(), id)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "owned-marker"), []byte("synthetic package only"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, mustCleanSeedJSON(all)); err != nil {
		t.Fatal(err)
	}
	sourceUninstallConfigure(t, a, cookie, "PluginUnInstall", selected, true)
	ids, err := a.originalInstalledPluginIDs(ctx)
	if err != nil || !slices.Equal(ids, []string{"PluginUnInstall", "MPServerStatus"}) {
		t.Fatal("real >64 candidate tail missing", ids, err)
	}
	for _, id := range selected {
		if _, err := os.Stat(filepath.Join(a.pluginPackageRoot(), id)); !os.IsNotExist(err) {
			t.Fatal("actual package not removed", id, err)
		}
	}
	raw, err := a.pluginStore.Config(ctx, "PluginUnInstall")
	if err != nil || !originalBaseJSONEqual(raw.Value, json.RawMessage(`{"plugin_ids":[],"clear_config":true,"clear_data":true}`)) {
		t.Fatal("owner returned before real continuation tail", string(raw.Value), err)
	}
	t.Log("actual App + independent source worker: 100 owned package directories removed; original64 budget unchanged; final installed/config tail awaited")
}
func TestSourceUninstallInstalledWriteRejectsInvalidBeforeCacheOrSQL(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`["Alpha"]`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{}`, `["../outside"]`, `["Alpha",null]`, `[1]`, `[""]`} {
		if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(raw)); err == nil {
			t.Fatal("invalid material accepted", raw)
		}
		ids, err := a.originalInstalledPluginIDs(ctx)
		if err != nil || !slices.Equal(ids, []string{"Alpha"}) {
			t.Fatal("rejected write changed cache", raw, ids, err)
		}
	}
}

func TestSourceUninstallFailedInstalledWriteCacheControlsSameProcessRestart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("owned filesystem fixture requires nonroot")
	}
	a, cookie := sourceUninstallFixture(t)
	path := filepath.Join(a.pluginPackageRoot(), "MPServerStatus")
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0700)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_uninstall_reject BEFORE UPDATE ON records WHEN NEW.kind='plugin-original-installed-order-v1' BEGIN SELECT RAISE(FAIL,'owned source DB failure'); END`); err != nil {
		t.Fatal(err)
	}
	sourceUninstallConfigure(t, a, cookie, "PluginUnInstall", []string{"MPServerStatus"}, false)
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal("source filesystem residue not retained", err)
	}
	// Persistent installed membership is deliberately still old. The source
	// in-process get() cache must control this ordinary manager init_config.
	if err := a.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	if _, exists := a.pluginRunningInstances.Load("MPServerStatus"); exists {
		t.Fatal("same-process source cache ignored; retained code was restarted")
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_uninstall_reject`); err != nil {
		t.Fatal(err)
	}
}

func TestSourceUninstallActualSelectiveStartReloadAndUninstalledCode(t *testing.T) {
	for _, name := range []string{"start-loaded-self", "reload-self", "selective-start-uninstalled-code", "cold-uninstalled-code-not-loaded"} {
		t.Run(name, func(t *testing.T) {
			a, _ := sourceUninstallFixture(t)
			ctx := context.Background()
			sourceUninstallSave(t, a, "MPServerStatus", json.RawMessage(`{"enable":true,"retain":"mps"}`))
			if err := a.initOriginalLoadedPlugin(ctx, "MPServerStatus"); err != nil {
				t.Fatal(err)
			}
			selected := []string{"PluginUnInstall"}
			if name == "selective-start-uninstalled-code" || name == "cold-uninstalled-code-not-loaded" {
				selected = []string{}
			}
			raw, _ := json.Marshal(map[string]any{"plugin_ids": selected, "clear_config": true, "clear_data": true, "unknown": "preserved-until-tail"})
			sourceUninstallSave(t, a, "PluginUnInstall", raw)
			switch name {
			case "start-loaded-self":
				if a.startOriginalPlugin(ctx, "PluginUnInstall") == nil {
					t.Fatal("source selective start did not reinsert new running self")
				}
			case "reload-self":
				if _, err := a.reloadOriginalPlugin(ctx, "PluginUnInstall"); err != nil {
					t.Fatal(err)
				}
				a.wg.Wait()
			default:
				if err := a.stopOriginalPlugin(ctx, "PluginUnInstall"); err != nil {
					t.Fatal(err)
				}
				if err := a.setOriginalPluginInstalled(ctx, "PluginUnInstall", false); err != nil {
					t.Fatal(err)
				}
				if name == "selective-start-uninstalled-code" {
					// Source fixture enters selective start with empty class/running maps.
					if err := a.stopOriginalPlugin(ctx, "MPServerStatus"); err != nil {
						t.Fatal(err)
					}
					if a.startOriginalPlugin(ctx, "PluginUnInstall") == nil {
						t.Fatal("explicit retained code could not load independently of installed list")
					}
				} else {
					if err := a.initializeOriginalPlugins(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			sourceUninstallProjection(t, a, name)
		})
	}
}

func TestSourceUninstallFalseyInstalledSetRecreatesAbsentSourceRow(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for i, wantPresent := range []bool{true, true} {
		changed, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`[]`))
		if err != nil || changed != (i == 0) {
			t.Fatal("source row-present/equal return", i, changed, err)
		}
		var present bool
		if err = a.Store.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM records WHERE kind=? AND id='main')`, originalInstalledOrderKind).Scan(&present); err != nil || present != wantPresent {
			t.Fatal("source falsey create", present, err)
		}
	}
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`["Alpha"]`)); err != nil {
		t.Fatal(err)
	}
	if changed, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`[]`)); err != nil || !changed {
		t.Fatal("source falsey deletes existing row", changed, err)
	}
	if changed, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`[]`)); err != nil || !changed {
		t.Fatal("source equal cache recreates absent row", changed, err)
	}
}

func TestSourceUninstallPathlib312ExactErrnoBoundary(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-source-uninstall-20261105/pathlib-errno-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Name   string `json:"case"`
			Exists *bool  `json:"exists"`
			Raises string `json:"raises"`
		}
	}
	if json.Unmarshal(raw, &source) != nil || len(source.Cases) != 11 {
		t.Fatal("actual captured pathlib AST oracle")
	}
	values := map[string]error{"ENOENT": syscall.ENOENT, "ENOTDIR": syscall.ENOTDIR, "EBADF": syscall.EBADF, "ELOOP": syscall.ELOOP, "EACCES": syscall.EACCES, "EPERM": syscall.EPERM, "EIO": syscall.EIO, "EINVAL": syscall.EINVAL, "ENAMETOOLONG": syscall.ENAMETOOLONG}
	for _, c := range source.Cases {
		e, ok := values[c.Name]
		if !ok {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			absent := sourceOriginalPathAbsent(&os.PathError{Op: "stat", Path: "/owned/fixture", Err: e})
			if absent != (c.Exists != nil && !*c.Exists) {
				t.Fatal("source missing/error branch", c.Name, absent, c.Raises)
			}
		})
	}
	// Actual target-kernel loop demonstrates the adapter receives ELOOP; source
	// expected classification above is the captured3.12 AST, not a Python kernel run.
	a := newTestApp(t)
	if err := os.MkdirAll(a.pluginPackageRoot(), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.pluginPackageRoot(), "OwnedLoop")
	if err := os.Symlink("OwnedLoop", path); err != nil {
		t.Fatal(err)
	}
	exists, err := a.sourceOriginalCode(context.Background(), "OwnedLoop", "code.exists")
	if err != nil || string(exists) != "false" {
		t.Fatal("owned loop should be source absence", string(exists), err)
	}
}

type sourceInstalledBarrierReader struct {
	*sql.DB
	entered, release chan struct{}
	once             sync.Once
}

func (r *sourceInstalledBarrierReader) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	r.once.Do(func() { close(r.entered) })
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return r.DB.QueryRowContext(ctx, q, args...)
}
func TestSourceUninstallInstalledReaderCannotBlockNativeTransaction(t *testing.T) {
	a := newTestApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`["Alpha"]`)); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reader := &sourceInstalledBarrierReader{DB: a.Store.DB, entered: make(chan struct{}), release: make(chan struct{})}
	readDone := make(chan error, 1)
	go func() { _, err := a.pluginStore.originalInstalledPluginIDs(ctx, reader); readDone <- err }()
	select {
	case <-reader.entered:
	case <-ctx.Done():
		t.Fatal("reader barrier not reached")
	}
	writeDone := make(chan error, 1)
	go func() { writeDone <- a.pluginStore.updateOriginalInstalledOrderTx(ctx, tx, "Alpha", false, true) }()
	blocked := false
	select {
	case err = <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		blocked = true
		_ = tx.Rollback()
	}
	close(reader.release)
	if blocked {
		select {
		case <-writeDone:
		case <-ctx.Done():
			t.Fatal("writer failed to join after barrier cleanup")
		}
	}
	select {
	case err = <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("reader failed to join")
	}
	if blocked {
		t.Fatal("single SQLite connection deadlock: reader held cache mutex while awaiting transaction's connection; native writer awaited that same cache mutex")
	}
}

type sourceInstalledSnapshotReader struct {
	*sql.DB
	entered, release chan struct{}
}

func (r *sourceInstalledSnapshotReader) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	var sealed []byte
	err := r.DB.QueryRowContext(ctx, q, args...).Scan(&sealed)
	if err != nil {
		return r.DB.QueryRowContext(ctx, q, args...)
	}
	// Keep an actual old encrypted-row observation at the query boundary so the
	// race is deterministic without a production hook or a timing assumption.
	close(r.entered)
	<-r.release
	return r.DB.QueryRowContext(ctx, `SELECT ?`, sealed)
}
func TestSourceUninstallOldObservationCannotDiscardNewFailedWriteCache(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`["Alpha"]`)); err != nil {
		t.Fatal(err)
	}
	r := &sourceInstalledSnapshotReader{DB: a.Store.DB, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := a.pluginStore.originalInstalledPluginIDs(ctx, r); done <- err }()
	<-r.entered
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`["Gamma"]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_uninstall_reject BEFORE UPDATE ON records WHEN NEW.kind='plugin-original-installed-order-v1' BEGIN SELECT RAISE(FAIL,'owned failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`["Delta"]`)); err == nil {
		t.Fatal("real failure not reached")
	}
	close(r.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ids, err := a.originalInstalledPluginIDs(ctx)
	if err != nil || !slices.Equal(ids, []string{"Delta"}) {
		t.Fatal("older query discarded the newer source cache after DB failure", ids, err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_uninstall_reject`); err != nil {
		t.Fatal(err)
	}
}

func TestSourceUninstallDuplicateInstalledIDsImportOneActualClass(t *testing.T) {
	a, cookie := sourceUninstallFixture(t)
	binary := os.Getenv("SOURCE_UNINSTALL_COUNTER_BINARY")
	if binary == "" {
		t.Fatal("SOURCE_UNINSTALL_COUNTER_BINARY must identify actual owned Base SDK counter fixture")
	}
	m := installOwnedResourceProcessWorker(t, a, cookie, binary)
	ctx := context.Background()
	sourceUninstallSave(t, a, m.PluginID, json.RawMessage(`{"key":{"owned":"same source class"}}`))
	if err := a.initOriginalLoadedPlugin(ctx, m.PluginID); err != nil {
		t.Fatal(err)
	}
	revision := func() int64 {
		var n int64
		if err := a.Store.DB.QueryRow(`SELECT revision FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='shared'`, m.PluginID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := revision()
	raw, _ := json.Marshal([]string{"PluginUnInstall", "MPServerStatus", m.PluginID, m.PluginID})
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	if got := revision() - before; got != 1 {
		t.Fatal("source package/class scan must initialize once, independent of duplicate installed membership", got)
	}
	ids, err := a.originalInstalledPluginIDs(ctx)
	if err != nil || len(ids) != 4 || ids[2] != ids[3] {
		t.Fatal("class deduplication altered the source installed list", ids, err)
	}
}
