package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"light-media/internal/plugins/builtin"
)

func TestPluginOriginalInstallExistingClassReloadsAndPreservesVersion(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	const id = "SystemProcess"
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	if _, err := a.pluginStore.SaveConfig(ctx, id, 0, map[string]any{"enable": false, "owned": "keep"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.SaveData(ctx, id, "history", 0, "keep"); err != nil {
		t.Fatal(err)
	}
	if a.startOriginalPlugin(ctx, id) == nil {
		t.Fatal("start")
	}
	r := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+id, `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	r = callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	before, err := a.pluginStore.Installation(ctx, id)
	if err != nil || before.Installed {
		t.Fatalf("loaded-uninstalled precondition %v", err)
	}
	config, err := a.pluginStore.Config(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := a.pluginStore.Data(ctx, id, "history")
	if err != nil {
		t.Fatal(err)
	}
	var runs int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, id).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	for i, request := range []struct{ method, path, body string }{
		{"GET", "/api/plugins/install/" + id + "?release_version=owned-unavailable-version", ""},
		{"POST", "/api/plugins/install", `{"plugin_id":"SystemProcess","release_version":"owned-unavailable-version"}`},
	} {
		old, ok := a.pluginRunningInstances.Load(id)
		if !ok {
			t.Fatal("missing class object")
		}
		r = callTest(t, a.Handler(), request.method, request.path, request.body, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		current, ok := a.pluginRunningInstances.Load(id)
		if !ok || current == old {
			t.Fatal("install must reload even already installed")
		}
		in, e := a.pluginStore.Installation(ctx, id)
		if e != nil || !in.Installed || in.Version != before.Version {
			t.Fatalf("install/version %v %+v", e, in)
		}
		c, e := a.pluginStore.Config(ctx, id)
		if e != nil || c.Revision != config.Revision || !bytes.Equal(c.Value, config.Value) {
			t.Fatalf("install changed config %v", e)
		}
		d, e := a.pluginStore.Data(ctx, id, "history")
		if e != nil || d.Revision != data.Revision || !bytes.Equal(d.Value, data.Value) {
			t.Fatalf("install changed history %v", e)
		}
		var n int
		if e = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, id).Scan(&n); e != nil || n != runs+i+1 {
			t.Fatalf("exact init per install n=%d %v", n, e)
		}
		if _, ok = a.pluginRegisteredAPIs.Load(id); !ok {
			t.Fatal("install did not register APIs")
		}
	}
}

func TestPluginOriginalInstallUnavailableAcquisitionDoesNotMutate(t *testing.T) {
	for _, tc := range []struct {
		name, id, body    string
		installed, loaded bool
	}{
		{"installed-without-class", "SystemProcess", `{"plugin_id":"SystemProcess"}`, true, false},
		{"unknown", "OwnedUnknownInstall", `{"plugin_id":"OwnedUnknownInstall"}`, false, false},
		{"force-without-repo", "SystemProcess", `{"plugin_id":"SystemProcess","force":true}`, true, true},
		{"force-with-repo", "SystemProcess", `{"plugin_id":"SystemProcess","force":true,"repo_url":"https://example.invalid/owned","release_version":"owned"}`, true, true},
		{"existing-class-repo-unverified", "SystemProcess", `{"plugin_id":"SystemProcess","repo_url":"https://example.invalid/owned"}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			ctx := context.Background()
			cookie := setupTestAdmin(t, a)
			if tc.installed {
				installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
			}
			if tc.loaded {
				if a.startOriginalPlugin(ctx, tc.id) == nil {
					t.Fatal("start")
				}
			}
			before := map[string]int{}
			for _, table := range []string{"plugin_installations", "plugin_values", "plugin_runs"} {
				var n int
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
					t.Fatal(err)
				}
				before[table] = n
			}
			old, wasRunning := a.pluginRunningInstances.Load(tc.id)
			method, path := "POST", "/api/plugins/install"
			if tc.name == "installed-without-class" {
				method, path = "GET", "/api/plugins/install/"+tc.id
			}
			r := callTest(t, a.Handler(), method, path, tc.body, cookie, "")
			var body struct {
				Data struct {
					Success bool   `json:"success"`
					Message string `json:"message"`
				} `json:"data"`
			}
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil || body.Data.Success || body.Data.Message == "" {
				t.Fatalf("unsupported install must not fake success: %d %s", r.Code, r.Body.String())
			}
			a.wg.Wait()
			for table, n := range before {
				var got int
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&got); err != nil || got != n {
					t.Fatalf("failed acquisition changed %s %d -> %d %v", table, n, got, err)
				}
			}
			current, running := a.pluginRunningInstances.Load(tc.id)
			if running != wasRunning || current != old {
				t.Fatal("failed acquisition reloaded object")
			}
			if tc.installed {
				in, e := a.pluginStore.Installation(ctx, tc.id)
				if e != nil || !in.Installed {
					t.Fatalf("failed acquisition changed installed flag %v", e)
				}
			}
		})
	}
}

// The Go market POST explicitly selects a bundled artifact; this does not
// assert remote Python package acquisition or change the original GET contract.
func TestPluginOriginalBundledPOSTFirstInstallWithoutFormDefaults(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	const id = "SystemProcess"
	const other = "CustomHosts"
	installCloneSource(t, a.pluginStore, builtin.CustomHostsManifest())
	if _, err := a.pluginStore.SaveConfig(ctx, other, 0, map[string]any{"owned": "unchanged", "enabled": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.SaveData(ctx, other, "history", 0, "unchanged"); err != nil {
		t.Fatal(err)
	}
	otherBefore, err := a.pluginStore.Installation(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	configBefore, err := a.pluginStore.Config(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	dataBefore, err := a.pluginStore.Data(ctx, other, "history")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.Installation(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("first install fixture: %v", err)
	}
	if _, ok := a.pluginOriginalClasses.Load(id); ok {
		t.Fatal("fixture already has class")
	}
	// A non-existent selected artifact version must fail before namespace creation.
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"SystemProcess","release_version":"owned-unavailable-version"}`, cookie, "")
	var response struct {
		Data struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		} `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil || response.Data.Success || response.Data.Message == "" {
		t.Fatalf("unknown bundled release: %d %s", r.Code, r.Body.String())
	}
	for _, table := range []string{"plugin_installations", "plugin_values", "plugin_runs"} {
		var n int
		if err = a.Store.DB.QueryRow(`SELECT count(*) FROM `+table+` WHERE instance_id=?`, id).Scan(&n); err != nil || n != 0 {
			t.Fatalf("unavailable version wrote %s: %d %v", table, n, err)
		}
	}
	if _, ok := a.pluginRunningInstances.Load(id); ok {
		t.Fatal("unavailable version created running object")
	}
	r = callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"SystemProcess"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	in, err := a.pluginStore.Installation(ctx, id)
	if err != nil || !in.Installed || in.Version != builtin.SystemProcessManifest().Version {
		t.Fatalf("bundled install: %+v %v", in, err)
	}
	if _, ok := a.pluginOriginalClasses.Load(id); !ok {
		t.Fatal("bundled artifact has no class")
	}
	if _, ok := a.pluginRunningInstances.Load(id); !ok {
		t.Fatal("bundled artifact not initialized")
	}
	if _, ok := a.pluginRegisteredAPIs.Load(id); !ok {
		t.Fatal("bundled install did not register")
	}
	if _, err = a.pluginStore.Config(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("install must not synthesize Form defaults: %v", err)
	}
	var total, succeeded int
	if err = a.Store.DB.QueryRow(`SELECT count(*),coalesce(sum(CASE WHEN action='init' AND state='succeeded' THEN 1 ELSE 0 END),0) FROM plugin_runs WHERE instance_id=?`, id).Scan(&total, &succeeded); err != nil || total != 1 || succeeded != 1 {
		t.Fatalf("exact real first init: total=%d succeeded=%d %v", total, succeeded, err)
	}
	otherAfter, err := a.pluginStore.Installation(ctx, other)
	if err != nil || otherAfter.Installed != otherBefore.Installed || otherAfter.Version != otherBefore.Version {
		t.Fatalf("other installation changed: %v", err)
	}
	c, err := a.pluginStore.Config(ctx, other)
	if err != nil || c.Revision != configBefore.Revision || !bytes.Equal(c.Value, configBefore.Value) {
		t.Fatalf("other config changed: %v", err)
	}
	d, err := a.pluginStore.Data(ctx, other, "history")
	if err != nil || d.Revision != dataBefore.Revision || !bytes.Equal(d.Value, dataBefore.Value) {
		t.Fatalf("other history changed: %v", err)
	}
	if _, ok := a.pluginRunningInstances.Load(other); ok {
		t.Fatal("install initialized unrelated instance")
	}
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id<>?`, id).Scan(&total); err != nil || total != 0 {
		t.Fatalf("unrelated runs: %d %v", total, err)
	}
}
