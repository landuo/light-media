package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	iyuu "light-media/internal/plugins/iyuuautoseed"
)

func TestIYUUActualTypedNativeAndCustomIDsRemainDistinct(t *testing.T) {
	a, _, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	installSiteControlPeerFixture(t, a, "CustomSites", map[string]any{"enabled": true, "sites": []map[string]any{
		{"id": "1", "name": "Exact numeric string", "url": "https://custom-one.example/"},
		{"id": "s1", "name": "Exact opaque string", "url": "https://custom-two.example/"},
	}})
	native := identityRequire(t, a, "s1").SourceID
	raw, _ := json.Marshal(map[string]any{"enabled": false, "token": "synthetic-token", "sites": []any{native, "1", "s1"}})
	value, e := iyuuTypedConfig(raw)
	if e != nil {
		t.Fatal(e)
	}
	run := uninstallInstall(t, a, "OwnedIYUUTyped", iyuu.Manifest(), value, true)
	invokeIYUUActualSDK(t, a, run)
	stored, e := a.pluginStore.Config(ctx, run.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	fields, e := iyuuSourceMap(stored.Value)
	if e != nil {
		t.Fatal(e)
	}
	refs, ok := fields["sites"].([]any)
	if !ok || len(refs) != 3 || refs[0] != json.Number(itoaSourceSite(native)) || refs[1] != "1" || refs[2] != "s1" {
		t.Fatalf("typed identities changed: %#v", refs)
	}
	for _, invalid := range []string{`{"sites":[0]}`, `{"sites":[-1]}`, `{"sites":[1.5]}`, `{"sites":[{}]}`, `{"sites":[null]}`} {
		if _, e = iyuuTypedConfig([]byte(invalid)); e == nil {
			t.Fatal("invalid site reference admitted", invalid)
		}
	}
}

func TestIYUUOriginalImportCannotActivateCoincidentNativeID(t *testing.T) {
	a, _, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	m, e := a.pluginStore.BeginMigration(ctx, strings.Repeat("c", 64), "IYUUAutoSeed", "IYUUAutoSeed.original")
	if e != nil {
		t.Fatal(e)
	}
	manifest := iyuu.Manifest()
	encoded, _ := json.Marshal(manifest)
	config := json.RawMessage(`{"enabled":true,"onlyonce":true,"token":"synthetic-token","sites":[1,"1"],"downloaders":[]}`)
	in := PluginImport{RunID: m.ID, Installation: PluginInstallation{InstanceID: m.InstanceID, PluginID: "IYUUAutoSeed", Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: encoded}, Config: config}
	if _, e = a.pluginStore.Import(ctx, in); e != nil {
		t.Fatal(e)
	}
	before, e := a.pluginStore.Config(ctx, m.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.pluginStore.SaveConfigAndQueue(ctx, m.InstanceID, before.Revision, before.Value); pluginErrorCode(e) != "PLUGIN_SITE_PROTOCOL_UNCLAIMED" {
		t.Fatal("ordinary save released original reference namespace", e)
	}
	installation, e := a.pluginStore.Installation(ctx, m.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginIYUUHost(ctx, installation, nil, iyuu.ExecuteCapability, json.RawMessage(`{}`)); pluginErrorCode(e) != "PLUGIN_SITE_PROTOCOL_UNCLAIMED" {
		t.Fatal("Host admitted original reference namespace", e)
	}
	// Simulate an old activated flag: the independently sealed protocol is still
	// required by queue admission and startup. Numeric spelling grants nothing.
	if _, e = a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=1 WHERE instance_id=?`, m.InstanceID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginStore.EnqueueRun(ctx, m.InstanceID, "run", nil); pluginErrorCode(e) != "PLUGIN_SITE_PROTOCOL_UNCLAIMED" {
		t.Fatal("queue admitted original namespace", e)
	}
	if e = a.initializeNativePlugins(ctx); e != nil {
		t.Fatal(e)
	}
	after, e := a.pluginStore.Config(ctx, m.InstanceID)
	if e != nil || !bytes.Equal(before.Value, after.Value) || before.Revision != after.Revision {
		t.Fatal("blocked original import changed configuration", e)
	}
	var count int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, m.InstanceID).Scan(&count); e != nil || count != 0 {
		t.Fatal("unclaimed import was initialized", count, e)
	}
}

func iyuuAssertUnknownConfigWritersBlocked(t *testing.T, a *App, instance string) {
	t.Helper()
	ctx := context.Background()
	before, e := a.pluginStore.Config(ctx, instance)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginStore.SaveConfig(ctx, instance, before.Revision, before.Value); !errors.Is(e, iyuu.ErrUnconfirmed) {
		t.Fatal("direct Store config write crossed unknown", e)
	}
	if _, _, e = a.pluginStore.SaveConfigAndQueue(ctx, instance, before.Revision, before.Value); !errors.Is(e, iyuu.ErrUnconfirmed) {
		t.Fatal("user config save crossed unknown", e)
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, peers, e := a.pluginStore.siteControlPeersTx(ctx, tx)
	if e == nil {
		e = a.writeSiteControlPeersTx(ctx, tx, peers, map[string]map[string]json.RawMessage{"IYUUAutoSeed": {"sites": json.RawMessage(`[]`)}})
	}
	tx.Rollback()
	if !errors.Is(e, iyuu.ErrUnconfirmed) {
		t.Fatal("site switch/follow peer write crossed unknown", e)
	}
	after, e := a.pluginStore.Config(ctx, instance)
	if e != nil || before.Revision != after.Revision || !bytes.Equal(before.Value, after.Value) {
		t.Fatal("rejected config writers changed source state", e)
	}
}

func TestIYUUUserSavePreservesCachesUntilExplicitClear(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	config := map[string]any{"enabled": false, "token": "synthetic-token", "sites": []any{}, "size": 0, "success_caches": []any{"success"}, "error_caches": []any{"error"}, "permanent_error_caches": []any{"permanent"}}
	run := uninstallInstall(t, a, "OwnedIYUUCache", iyuu.Manifest(), config, true)
	invokeIYUUActualSDK(t, a, run)
	for _, clear := range []bool{false, true} {
		before, e := a.pluginStore.Config(ctx, run.InstanceID)
		if e != nil {
			t.Fatal(e)
		}
		// This is the exact public-form shape: internal caches have no form model.
		submitted := map[string]any{"enabled": false, "token": "synthetic-token", "sites": []any{}, "size": 0, "cron": "5 0 * * *", "clearcache": clear}
		_, _, e = a.pluginStore.SaveConfigAndQueue(ctx, run.InstanceID, before.Revision, submitted)
		if e != nil {
			t.Fatal(e)
		}
		next, e := a.pluginStore.claimRunForInstance(ctx, run.InstanceID)
		if e != nil {
			t.Fatal(e)
		}
		invokeIYUUActualSDK(t, a, next)
		after, e := a.pluginStore.Config(ctx, run.InstanceID)
		if e != nil {
			t.Fatal(e)
		}
		fields, e := iyuuSourceMap(after.Value)
		if e != nil {
			t.Fatal(e)
		}
		for _, key := range []string{"success_caches", "error_caches", "permanent_error_caches"} {
			values, ok := fields[key].([]any)
			if !ok || (!clear && len(values) != 1) || (clear && len(values) != 0) {
				t.Fatalf("clearcache=%v %s=%#v", clear, key, fields[key])
			}
		}
	}
}

func iyuuAssertUnknownAdmissionsWarmCold(t *testing.T, a *App, run PluginRun) {
	t.Helper()
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	check := func(app *App, cookie *http.Cookie) {
		t.Helper()
		var runs, occurrences int
		if e := app.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, run.InstanceID).Scan(&runs); e != nil {
			t.Fatal(e)
		}
		if e := app.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE instance_id=?`, run.InstanceID).Scan(&occurrences); e != nil {
			t.Fatal(e)
		}
		// Pending work admitted before the lost ACK must remain pending. It
		// cannot create a fresh deferred/started row when claimed later.
		for range 2 {
			if next, e := app.pluginStore.claimRunForInstance(ctx, run.InstanceID); !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("old pending admission crossed unknown during claim", next.ID, next.State, e)
			}
		}
		for range 2 {
			if _, e := app.pluginStore.EnqueueRun(ctx, run.InstanceID, "run", nil); !errors.Is(e, iyuu.ErrUnconfirmed) || pluginErrorCode(e) != "IYUU_UNCONFIRMED" {
				t.Fatal("direct queue unknown admission", e)
			}
		}
		body, _ := json.Marshal(map[string]string{"instance_id": run.InstanceID, "service_id": "IYUUAutoSeed.IYUUAutoSeed"})
		for range 2 {
			response := callTest(t, app.Handler(), "POST", "/api/plugins/services/run", string(body), cookie, "")
			if response.Code != 400 || !strings.Contains(response.Body.String(), "IYUU_UNCONFIRMED") {
				t.Fatal("public service admitted unknown", response.Code, response.Body.String())
			}
		}
		legacy := "/api/v1/system/runscheduler?jobid=" + url.QueryEscape(run.InstanceID+"_IYUUAutoSeed.IYUUAutoSeed")
		response := callTest(t, app.Handler(), "GET", legacy, "", cookie, "")
		if response.Code != 400 || !strings.Contains(response.Body.String(), "IYUU_UNCONFIRMED") {
			t.Fatal("legacy API admitted unknown", response.Code, response.Body.String())
		}
		var nextRuns, nextOccurrences int
		app.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, run.InstanceID).Scan(&nextRuns)
		app.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE instance_id=?`, run.InstanceID).Scan(&nextOccurrences)
		if nextRuns != runs || nextOccurrences != occurrences {
			t.Fatal("rejected admission left partial run or occurrence", runs, nextRuns, occurrences, nextOccurrences)
		}
		iyuuAssertUnknownConfigWritersBlocked(t, app, run.InstanceID)
	}
	check(a, cookie)
	iyuuAssertUnknownDispatchDoesNotStarve(t, a, cookie, run)
	dir := a.Dir
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal("cold unknown admission", e)
	}
	defer reopened.Close()
	login := callTest(t, reopened.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if login.Code != 200 {
		t.Fatal("owned cold login", login.Code)
	}
	var session *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == "lm_session" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("owned cold session")
	}
	check(reopened, session)
	recovered, e := reopened.pluginStore.Run(ctx, run.ID)
	if e != nil || recovered.State != "reconcile_pending" || recovered.ErrorCode != "IYUU_UNCONFIRMED" {
		t.Fatal("original unknown was lost or relabeled", recovered.State, recovered.ErrorCode, e)
	}
}
