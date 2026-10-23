package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/uninstall"
)

func uninstallInstall(t *testing.T, a *App, instance string, manifest plugins.Manifest, config any, invoke bool) PluginRun {
	t.Helper()
	ctx := context.Background()
	raw, _ := json.Marshal(manifest)
	installation := PluginInstallation{InstanceID: instance, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw, DesiredEnabled: true}
	if err := a.pluginStore.Install(ctx, installation); err != nil {
		t.Fatal(err)
	}
	if invoke {
		if _, _, err := a.pluginStore.SaveConfigAndQueue(ctx, instance, 0, config); err != nil {
			t.Fatal(err)
		}
		run, err := a.pluginStore.claimRunForInstance(ctx, instance)
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	if _, err := a.pluginStore.SaveConfig(ctx, instance, 0, config); err != nil {
		t.Fatal(err)
	}
	return PluginRun{}
}

func uninstallApply(t *testing.T, a *App, run *PluginRun) uninstall.Result {
	t.Helper()
	installation, err := a.pluginStore.Installation(context.Background(), run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := a.pluginUninstallHost(context.Background(), installation, run, uninstall.ApplyCapability, json.RawMessage(`{}`))
	var result uninstall.Result
	if err != nil || json.Unmarshal(raw, &result) != nil {
		t.Fatal("actual uninstall host", string(raw), err)
	}
	return result
}

func TestPluginUninstallHostOriginalOrderRetainsDataAndStopsRealLifecycle(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	// The source installed list is insertion-ordered, not alphabetical/form order.
	uninstallInstall(t, a, "z-last-name", builtin.CategoryEditorManifest(), map[string]any{"private": "synthetic-retained-config"}, false)
	uninstallInstall(t, a, "a-first-name", builtin.SystemProcessManifest(), map[string]any{"enable": true}, false)
	uninstallInstall(t, a, "keep", builtin.CustomHostsManifest(), map[string]any{"enabled": false}, false)
	for _, instance := range []string{"z-last-name", "a-first-name", "keep"} {
		if _, err := a.pluginStore.SaveData(ctx, instance, "history", 0, []any{"synthetic-retained-data"}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.pluginStore.EnqueueRun(ctx, instance, "reload", nil); err != nil {
			t.Fatal(err)
		}
		installation, _ := a.pluginStore.Installation(ctx, instance)
		if _, err := a.Store.DB.Exec(`INSERT INTO plugin_event_subscriptions VALUES(?, 'fixture', ?,1,'["import.completed"]',0,1,0)`, instance, installation.Generation); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Store.DB.Exec(`INSERT INTO plugin_schedules VALUES(?,'fixture','fixture',?,1,x'00','fixture',1,NULL,?)`, instance, installation.Generation, now()); err != nil {
			t.Fatal(err)
		}
	}
	config := map[string]any{"plugin_ids": []string{"a-first-name", "missing", "z-last-name", "a-first-name"}, "clear_config": true, "clear_data": true, "obsolete": "synthetic-own-config-field"}
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), config, true)
	result := uninstallApply(t, a, &run)
	if !result.OwnConfigReset || !reflect.DeepEqual(result.Removed, []string{"z-last-name", "a-first-name"}) || !reflect.DeepEqual(result.Remaining, []string{"keep", "PluginUnInstall"}) || !result.ConfigClearRequested || !result.DataClearRequested || len(result.ConfigCleared) != 0 || len(result.DataCleared) != 0 {
		t.Fatal("original requested-vs-applied/order changed", result)
	}
	for _, instance := range []string{"z-last-name", "a-first-name"} {
		installation, _ := a.pluginStore.Installation(ctx, instance)
		config, configErr := a.pluginStore.Config(ctx, instance)
		data, dataErr := a.pluginStore.Data(ctx, instance, "history")
		if installation.Installed || installation.DesiredEnabled || installation.Generation != 2 || configErr != nil || dataErr != nil || config.Revision != 1 || !bytes.Contains(data.Value, []byte("synthetic-retained-data")) {
			t.Fatal("actual manager no-op or generation changed", instance, installation, configErr, dataErr)
		}
		for _, table := range []string{"plugin_event_subscriptions", "plugin_schedules"} {
			var active int
			if err := a.Store.DB.QueryRow(`SELECT active FROM `+table+` WHERE instance_id=?`, instance).Scan(&active); err != nil || active != 0 {
				t.Fatal("uninstalled plugin still active", instance, table, active, err)
			}
		}
		var pending int
		if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND state IN ('pending','running','reconcile_pending')`, instance).Scan(&pending); err != nil || pending != 0 {
			t.Fatal("uninstalled queued execution survived", instance, pending, err)
		}
	}
	retained, _ := a.pluginStore.Config(ctx, "z-last-name")
	if !bytes.Contains(retained.Value, []byte("synthetic-retained-config")) {
		t.Fatal("original non-force config clearing unexpectedly applied")
	}
	own, _ := a.pluginStore.Config(ctx, "PluginUnInstall")
	if string(own.Value) != `{"plugin_ids":[],"clear_config":true,"clear_data":true}` || own.Revision != 2 || run.ConfigRevision != 2 {
		t.Fatal("full own-config reset missing", string(own.Value), own.Revision, run.ConfigRevision)
	}
	kept, _ := a.pluginStore.Installation(ctx, "keep")
	if !kept.Installed || !kept.DesiredEnabled || kept.Generation != 1 {
		t.Fatal("unselected plugin changed")
	}
	if _, err := a.pluginStore.SaveConfig(ctx, "z-last-name", 1, map[string]any{}); err == nil {
		t.Fatal("uninstalled instance still grants write authority")
	}
}

func TestPluginUninstallHostSelfReceiptSurvivesReplyLossAndReinstallation(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	uninstallInstall(t, a, "CategoryEditor", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"PluginUnInstall", "CategoryEditor"}, "clear_config": true, "clear_data": true}, true)
	if _, err := a.pluginStore.SaveData(ctx, "PluginUnInstall", "history", 0, map[string]any{"private": "synthetic-self-retained"}); err != nil {
		t.Fatal(err)
	}
	result := uninstallApply(t, a, &run)
	if !result.OwnConfigReset || !reflect.DeepEqual(result.Removed, []string{"CategoryEditor", "PluginUnInstall"}) {
		t.Fatal(result)
	}
	self, _ := a.pluginStore.Installation(ctx, "PluginUnInstall")
	saved, _ := a.pluginStore.Run(ctx, run.ID)
	if self.Installed || self.DesiredEnabled || saved.State != "running" || saved.ConfigRevision != 2 || saved.Generation != 1 {
		t.Fatal("self removal prevented final runtime receipt", self, saved.State, saved.ConfigRevision)
	}
	if raw, err := a.pluginStore.uninstallReceipt(ctx, saved); err != nil || !bytes.Contains(raw, []byte(`"own_config_reset":true`)) {
		t.Fatal("self committed receipt unavailable", err)
	}
	if data, err := a.pluginStore.Data(ctx, "PluginUnInstall", "history"); err != nil || !bytes.Contains(data.Value, []byte("synthetic-self-retained")) {
		t.Fatal("self config/data guard did not match installed source", err)
	}
	// Reinstall both IDs after a committed host call whose worker reply was
	// lost. Recovery must consume the old receipt rather than uninstall again.
	for _, entry := range []struct {
		id       string
		manifest plugins.Manifest
	}{{"CategoryEditor", builtin.CategoryEditorManifest()}, {"PluginUnInstall", uninstall.Manifest()}} {
		raw, _ := json.Marshal(entry.manifest)
		if err := a.pluginStore.Install(ctx, PluginInstallation{InstanceID: entry.id, PluginID: entry.manifest.PluginID, Version: entry.manifest.Version, ManifestSchemaVersion: 1, Manifest: raw, DesiredEnabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.recoverUninstallRuns(ctx); err != nil {
		t.Fatal(err)
	}
	saved, _ = a.pluginStore.Run(ctx, run.ID)
	if saved.State != "succeeded" || !bytes.Contains(saved.Result, []byte(`"own_config_reset":true`)) {
		t.Fatal("self receipt did not reconcile", saved.State)
	}
	for _, instance := range []string{"CategoryEditor", "PluginUnInstall"} {
		current, _ := a.pluginStore.Installation(ctx, instance)
		if !current.Installed || current.Generation != 3 {
			t.Fatal("receipt replay affected replacement installation", instance, current)
		}
	}
}

func TestPluginUninstallHostRejectsPayloadForgedStaleAndReadOnlyRequests(t *testing.T) {
	for _, scenario := range []string{"payload", "null", "form-only", "forged-run", "config-stale", "generation-stale", "wrong-action"} {
		t.Run(scenario, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			uninstallInstall(t, a, "CategoryEditor", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
			run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"CategoryEditor"}}, true)
			installation, _ := a.pluginStore.Installation(ctx, run.InstanceID)
			workerRun, raw := &run, json.RawMessage(`{}`)
			switch scenario {
			case "payload":
				raw = json.RawMessage(`{"plugin_id":"CategoryEditor","force":true}`)
			case "null":
				raw = json.RawMessage(`null`)
			case "form-only":
				workerRun = nil
			case "forged-run":
				run.ID = "untrusted-run"
			case "config-stale":
				_, _ = a.pluginStore.SaveConfig(ctx, "PluginUnInstall", 1, map[string]any{"plugin_ids": []string{}})
			case "generation-stale":
				_, _ = a.Store.DB.Exec(`UPDATE plugin_installations SET generation=generation+1 WHERE instance_id='PluginUnInstall'`)
			case "wrong-action":
				_, _ = a.Store.DB.Exec(`UPDATE plugin_runs SET action='event' WHERE id=?`, run.ID)
				run.Action = "event"
			}
			if _, err := a.pluginUninstallHost(ctx, installation, workerRun, uninstall.ApplyCapability, raw); err == nil {
				t.Fatal("unbound destructive operation accepted", scenario)
			}
			target, _ := a.pluginStore.Installation(ctx, "CategoryEditor")
			if !target.Installed || target.Generation != 1 {
				t.Fatal("rejected operation changed target", scenario)
			}
		})
	}
}

// Source operations are not rolled back when a later local result projection
// cannot be inserted. The new sealed source journal is the authoritative proof.
func TestPluginUninstallSourceCompletionDoesNotDependOnLegacyReceipt(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	uninstallInstall(t, a, "CategoryEditor", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"CategoryEditor"}, "clear_config": true}, true)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER reject_uninstall_receipt BEFORE INSERT ON plugin_uninstall_operations BEGIN SELECT RAISE(ABORT,'synthetic legacy projection failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applyPluginUninstall(ctx, &run); err != nil {
		t.Fatal(err)
	}
	target, _ := a.pluginStore.Installation(ctx, "CategoryEditor")
	own, _ := a.pluginStore.Config(ctx, "PluginUnInstall")
	if target.Installed || target.Generation != 2 || own.Revision != 2 || run.ConfigRevision != 2 {
		t.Fatal("source completion was lost", target, own.Revision, run.ConfigRevision)
	}
	if _, err := a.pluginStore.uninstallReceipt(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.recoverUninstallRuns(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, _ := a.pluginStore.Run(ctx, run.ID)
	if recovered.State != "succeeded" {
		t.Fatal("known completed source operation was replayed", recovered.State)
	}
}

func TestPluginUninstallReadOnlyOptionsUseInstalledOrderAndDoNotExposeConfiguration(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	uninstallInstall(t, a, "late-name", builtin.SystemProcessManifest(), map[string]any{"private": "synthetic-options-secret"}, false)
	uninstallInstall(t, a, "early-name", builtin.CategoryEditorManifest(), map[string]any{"private": "synthetic-other-secret"}, false)
	uninstallInstall(t, a, "removed", builtin.CustomHostsManifest(), map[string]any{}, false)
	if err := a.pluginStore.Uninstall(ctx, "removed"); err != nil {
		t.Fatal(err)
	}
	uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{}, false)
	installation, _ := a.pluginStore.Installation(ctx, "PluginUnInstall")
	raw, err := a.pluginUninstallHost(ctx, installation, nil, uninstall.OptionsCapability, json.RawMessage(`{}`))
	var inventory struct {
		Plugins []uninstall.LocalPlugin `json:"plugins"`
	}
	if err != nil || json.Unmarshal(raw, &inventory) != nil {
		t.Fatal(err)
	}
	options, err := uninstall.LocalOptions(inventory.Plugins)
	if err != nil || len(options) != 3 || options[0].Value != "early-name" || options[1].Value != "late-name" || options[2].Value != "PluginUnInstall" {
		t.Fatal("original metadata order/self option", options, err)
	}
	if bytes.Contains(raw, []byte("synthetic-")) || bytes.Contains(raw, []byte("private")) || bytes.Contains(raw, []byte(`"removed"`)) {
		t.Fatal("options disclosed configuration or retained uninstalled instance")
	}
	if _, err := a.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("read-only options created work", err)
	}
}

func TestPluginUninstallRestartRestoresOnlyItsUncommittedSameSnapshotDrain(t *testing.T) {
	for _, change := range []string{"unchanged", "config-replaced", "already-uninstalled"} {
		t.Run(change, func(t *testing.T) {
			f := newRefreshFixture(t, 3600)
			a, ctx := f.app, context.Background()
			run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"MediaServerRefresh"}}, true)
			consumers, err := a.pluginStore.activeEventSubscriptions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			target, _ := a.pluginStore.Installation(ctx, "MediaServerRefresh")
			if err := a.pluginStore.recordUninstallDrains(ctx, run, consumers, map[string]bool{"MediaServerRefresh": true}, map[string]PluginInstallation{"MediaServerRefresh": target}); err != nil {
				t.Fatal(err)
			}
			// Emulate loss after drain quiesces the consumer, before the final
			// local uninstall transaction starts. No original service is involved.
			if _, err := a.Store.DB.Exec(`UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id='MediaServerRefresh'`); err != nil {
				t.Fatal(err)
			}
			if change == "config-replaced" {
				if _, err := a.pluginStore.SaveConfig(ctx, "MediaServerRefresh", 2, map[string]any{"enabled": false, "delay": 3600, "mediaservers": []string{"fnos"}}); err != nil {
					t.Fatal(err)
				}
			}
			if change == "already-uninstalled" {
				if err := a.pluginStore.Uninstall(ctx, "MediaServerRefresh"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
				t.Fatal(err)
			}
			if err := a.pluginStore.recoverUninstallRuns(ctx); err != nil {
				t.Fatal(err)
			}
			var active, journals int
			if err := a.Store.DB.QueryRow(`SELECT active FROM plugin_event_subscriptions WHERE instance_id='MediaServerRefresh' AND handler_key='refresh'`).Scan(&active); err != nil {
				t.Fatal(err)
			}
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_uninstall_drains`).Scan(&journals); err != nil {
				t.Fatal(err)
			}
			wantActive := 0
			if change == "unchanged" {
				wantActive = 1
			}
			if active != wantActive || journals != 0 {
				t.Fatal("restart restored a stale consumer or lost a live one", change, active, journals)
			}
		})
	}
}

func TestPluginUninstallRealSDKOriginalFormSelfRemovalAndSharedExecutableSurvives(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	for _, plugin := range []string{"CategoryEditor", "PluginUnInstall"} {
		response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"`+plugin+`"}`, cookie, "")
		if response.Code != http.StatusOK {
			t.Fatal("real installation unavailable", plugin, response.Code, response.Body.String())
		}
		run, err := a.pluginStore.claimRunForInstance(context.Background(), plugin)
		if err != nil {
			t.Fatal(err)
		}
		a.executePluginRun(context.Background(), run)
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/PluginUnInstall/form", "", cookie, "")
	for _, field := range []string{"清除配置(配置信息)", "清除数据(运行数据)", "卸载插件", "插件彻底卸载 v2.2"} {
		if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), field) {
			t.Fatal("source form/options not wired", field, form.Code)
		}
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/PluginUnInstall/config", `{"revision":1,"config":{"plugin_ids":["PluginUnInstall","CategoryEditor"],"clear_config":true,"clear_data":true}}`, cookie, "")
	if response.Code != http.StatusOK {
		t.Fatal("real config/save/init missing", response.Code, response.Body.String())
	}
	run, err := a.pluginStore.claimRunForInstance(context.Background(), "PluginUnInstall")
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := os.Executable()
	before, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	run, err = a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || run.State != "succeeded" || !bytes.Contains(run.Result, []byte(`"removed":["CategoryEditor","PluginUnInstall"]`)) {
		t.Fatal("real SDK self removal/receipt failed", run.State, run.ErrorCode, err)
	}
	after, err := os.Stat(executable)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("uninstaller removed shared executable", err)
	}
	for _, instance := range []string{"CategoryEditor", "PluginUnInstall"} {
		current, _ := a.pluginStore.Installation(context.Background(), instance)
		if current.Installed {
			t.Fatal("SDK returned success without actual removal", instance)
		}
	}
	response = callTest(t, a.Handler(), "GET", "/api/plugins/PluginUnInstall/runs", "", cookie, "")
	if strings.Contains(response.Body.String(), "clear_config") || strings.Contains(response.Body.String(), "own_config_reset") {
		t.Fatal("public run listing disclosed config/result")
	}
}

func TestPluginUninstallHTTPFreshInstallAndReinstallPreviewKeepsExistingFormsPinned(t *testing.T) {
	assertUninstallHTTPInstallReinstallForms(t, useUninstallSDKProcess, false)
}

// Always exercise the same full assertions through owned loopback HTTP and
// the actual normal cmd/server --plugin-worker entry, not only a reduced SDK.
func TestPluginUninstallHTTPFreshInstallAndReinstallActualServer(t *testing.T) {
	assertUninstallHTTPInstallReinstallForms(t, useUninstallServerProcess, true)
}

func assertUninstallHTTPInstallReinstallForms(t *testing.T, useProcess func(*testing.T, *App), loopback bool) {
	t.Helper()
	a := newTestApp(t)
	useProcess(t, a)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		return callTest(t, a.Handler(), method, path, body, cookie, "")
	}
	if loopback {
		request = uninstallOwnedAPIRequests(t, a, cookie)
	}
	var first PluginInstallation
	for attempt := 0; attempt < 2; attempt++ {
		response := request("POST", "/api/plugins/install", `{"plugin_id":"PluginUnInstall"}`)
		if response.Code != http.StatusOK {
			t.Fatal("catalog preview blocked HTTP install", attempt, response.Code, response.Body.String())
		}
		run, err := a.pluginStore.claimRunForInstance(ctx, "PluginUnInstall")
		if err != nil {
			t.Fatal(err)
		}
		a.executePluginRun(ctx, run)
		saved, err := a.pluginStore.Run(ctx, run.ID)
		if err != nil || saved.State != "succeeded" {
			t.Fatal("HTTP install init failed", attempt, saved.State, saved.ErrorCode, err)
		}
		current, err := a.pluginStore.Installation(ctx, "PluginUnInstall")
		if err != nil || !current.Installed || current.Generation != int64(1+attempt*2) {
			t.Fatal("unexpected persisted installation", current, err)
		}
		if attempt == 0 {
			first = current
		}
		response = request("GET", "/api/plugins/PluginUnInstall/form", "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "插件彻底卸载 v2.2") {
			t.Fatal("installed form must include itself", response.Code, response.Body.String())
		}
		// A fabricated generation-zero preview must not bypass the ordinary
		// pinned form check once this ID has an active persisted installation.
		manifest, _ := json.Marshal(uninstall.Manifest())
		preview := PluginInstallation{InstanceID: "PluginUnInstall", PluginID: "PluginUnInstall", Version: "2.2", ManifestSchemaVersion: 1, Manifest: manifest}
		if _, err := a.pluginUninstallHost(ctx, preview, nil, uninstall.OptionsCapability, json.RawMessage(`{}`)); err == nil {
			t.Fatal("preview impersonated an active installation")
		}
		if attempt == 1 {
			if _, err := a.pluginUninstallHost(ctx, first, nil, uninstall.OptionsCapability, json.RawMessage(`{}`)); err == nil {
				t.Fatal("stale installed form survived uninstall/reinstall")
			}
			break
		}
		response = request("POST", "/api/plugins/PluginUnInstall/uninstall", `{}`)
		if response.Code != http.StatusOK {
			t.Fatal("ordinary fixture uninstall failed", response.Code, response.Body.String())
		}
		if _, err := a.pluginUninstallHost(ctx, first, nil, uninstall.OptionsCapability, json.RawMessage(`{}`)); err == nil {
			t.Fatal("ordinary installed form accepted a removed instance")
		}
	}
}

// This is a real child-process SDK peer. It receives a successful host reply,
// then exits before emitting the terminal invoke result. Deferred operations
// have not started at this boundary. No application or user config
// is loaded by the child; the parent fixture owns the bound synthetic store.
func TestPluginUninstallReplyLossFixture(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--" || i+1 >= len(os.Args) || os.Args[i+1] != "synthetic-uninstall-reply-loss" {
			continue
		}
		handler := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			result, err := uninstall.Handle(ctx, method, raw, host)
			if method == "invoke" && err == nil && !bytes.Equal(result, []byte(`null`)) {
				os.Exit(3)
			}
			return result, err
		}
		if plugins.Serve(context.Background(), os.Stdin, os.Stdout, uninstall.Manifest(), handler) != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func TestPluginUninstallRealSDKFailureBeforeTerminalCompletionCannotUninstall(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	uninstallInstall(t, a, "CategoryEditor", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"CategoryEditor", "PluginUnInstall"}, "clear_config": true, "clear_data": true}, true)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.pluginCommand = func(pluginID string) (plugins.Command, error) {
		if pluginID != "PluginUnInstall" {
			return plugins.Command{}, errors.New("unexpected fixture worker")
		}
		return plugins.Command{Path: executable, Args: []string{"-test.run=^TestPluginUninstallReplyLossFixture$", "--", "synthetic-uninstall-reply-loss"}}, nil
	}
	a.executePluginRun(ctx, run)
	saved, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || saved.State != "failed" || saved.ConfigRevision != 1 {
		t.Fatal("incomplete SDK worker was allowed to start uninstall", saved.State, saved.ErrorCode, saved.ConfigRevision, err)
	}
	for _, instance := range []string{"CategoryEditor", "PluginUnInstall"} {
		installation, err := a.pluginStore.Installation(ctx, instance)
		if err != nil || !installation.Installed || installation.Generation != 1 {
			t.Fatal("SDK EOF performed an unauthorized uninstall", instance, installation.Installed, installation.Generation, err)
		}
	}
	var receipts int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_uninstall_operations WHERE run_id=?`, run.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("failed terminal handshake committed an uninstall receipt", receipts, err)
	}
}

func TestPluginUninstallRealSDKExitsBeforeRefreshDrainRetainsSingleLiveWorkerSlot(t *testing.T) {
	f := newRefreshFixture(t, 3600)
	a := f.app
	f.importMovie(t)
	if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	// A fixture-only reverse proxy pauses the real refresh HTTP operation so
	// the nested refresh child's process slot is observable. The uninstall SDK
	// process has already exited; no extra worker bypasses the global cap.
	upstream, err := url.Parse(a.config.FNOS.Host)
	if err != nil {
		t.Fatal(err)
	}
	upstream.Path = ""
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/mdb/scan/") {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	a.config.FNOS.Host = server.URL + "/v"
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"MediaServerRefresh"}, "clear_config": true, "clear_data": true}, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { a.executePluginRun(ctx, run); close(done) }()
	select {
	case <-entered:
	case <-done:
		t.Fatal("uninstall finished before original delayed refresh")
	case <-ctx.Done():
		t.Fatal("nested refresh deadlocked on outer worker semaphore")
	}
	if len(a.pluginProcess) != 1 || cap(a.pluginProcess) != 1 {
		t.Fatal("nested refresh did not retain global process exclusion")
	}
	installation, _ := a.pluginStore.Installation(context.Background(), "MediaServerRefresh")
	busyCtx, busyCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, busyErr := a.pluginForm(busyCtx, installation)
	busyCancel()
	if busyErr == nil {
		t.Fatal("another worker/form bypassed the process semaphore")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("uninstall did not finish after nested refresh")
	}
	saved, _ := a.pluginStore.Run(context.Background(), run.ID)
	target, _ := a.pluginStore.Installation(context.Background(), "MediaServerRefresh")
	if saved.State != "succeeded" || target.Installed || f.scans.Load() != 1 || len(a.pluginProcess) != 0 {
		t.Fatal("original drain/removal not applied", saved.State, saved.ErrorCode, target.Installed, f.scans.Load(), len(a.pluginProcess))
	}
}

func TestPluginUninstallCanceledRefreshDrainKeepsTargetAndRestoresConsumer(t *testing.T) {
	f := newRefreshFixture(t, 3600)
	a := f.app
	f.importMovie(t)
	if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	upstream, err := url.Parse(a.config.FNOS.Host)
	if err != nil {
		t.Fatal(err)
	}
	upstream.Path = ""
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/mdb/scan/") {
			close(entered)
			<-r.Context().Done()
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	a.config.FNOS.Host = server.URL + "/v"
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"MediaServerRefresh"}}, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.executePluginRun(ctx, run); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("nested scan was not started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not release nested workers")
	}
	target, _ := a.pluginStore.Installation(context.Background(), "MediaServerRefresh")
	var active int
	if err := a.Store.DB.QueryRow(`SELECT active FROM plugin_event_subscriptions WHERE instance_id='MediaServerRefresh' AND handler_key='refresh'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if !target.Installed || active != 1 || len(a.pluginProcess) != 0 {
		t.Fatal("canceled drain partially uninstalled/quiesced target", target.Installed, active, len(a.pluginProcess))
	}
}
