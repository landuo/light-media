package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
	"light-media/internal/plugins/autosignin/durable"
)

func autoSignInSDKExecute(t *testing.T, a *App, instance string) PluginRun {
	t.Helper()
	ctx := context.Background()
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if run.InstanceID != instance {
		t.Fatalf("wrong instance %q, expected %q", run.InstanceID, instance)
	}
	a.executePluginRun(ctx, run)
	final, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || final.State != "succeeded" {
		t.Fatalf("SDK run %s %s: %s %s %v", instance, run.Action, final.State, final.ErrorCode, err)
	}
	return final
}
func autoSignInSDKInstall(t *testing.T, a *App, instance string, config map[string]any) {
	t.Helper()
	manifest, _ := json.Marshal(autosignin.Manifest())
	_, err := a.pluginStore.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: instance, PluginID: "AutoSignIn", Version: autosignin.Version, ManifestSchemaVersion: 1, Manifest: manifest}, config)
	if err != nil {
		t.Fatal(err)
	}
	autoSignInSDKExecute(t, a, instance)
}
func autoSignInSDKState(t *testing.T, a *App, instance string) durable.State {
	t.Helper()
	in, err := a.pluginStore.Installation(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	slot, err := a.autoSignInSlot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { <-slot.gate }()
	b, err := slot.owner.backend(nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := durable.NewStateJournal(b)
	if err != nil {
		t.Fatal(err)
	}
	state, err := j.State(context.Background(), slot.owner.binding)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// Actual child executable, handshake, terminal reverse call, native SQL owner,
// trusted local HTTP fixture and source history. No production credentials.
func TestPluginAutoSignInSDKClonesRealHTTPAndReadOnlyPage(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Cookie") != "sid=synthetic-native" {
			t.Error("missing trusted site cookie")
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
	}))
	defer server.Close()
	settings := a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
	signinSitesPublish(t, a, settings)
	config := map[string]any{"enabled": true, "onlyonce": false, "notify": false, "cron": "0 0 * * *", "sign_sites": []any{"all"}, "login_sites": []any{}, "queue_cnt": 1}
	for _, instance := range []string{"AutoSignIn", "AutoSignIn_clone"} {
		autoSignInSDKInstall(t, a, instance, config)
	}
	if requests.Load() != 0 {
		t.Fatal("init performed site requests")
	}
	if _, err := a.queueAutoSignInCommand(ctx, "AutoSignIn", pluginAPIAuthority{Kind: "command"}, "飞书", "owned-user"); err != nil {
		t.Fatal(err)
	}
	autoSignInSDKExecute(t, a, "AutoSignIn")
	afterA := requests.Load()
	if afterA == 0 {
		t.Fatal("actual SDK source command made no HTTP request")
	}
	first := autoSignInSDKState(t, a, "AutoSignIn")
	second := autoSignInSDKState(t, a, "AutoSignIn_clone")
	if len(first.History) == 0 || len(second.History) != 0 {
		t.Fatal("clone history isolation failed", len(first.History), len(second.History))
	}
	for _, instance := range []string{"AutoSignIn", "AutoSignIn_clone"} {
		page := callTest(t, a.Handler(), "GET", "/api/plugins/"+instance+"/page", "", cookie, "")
		if page.Code != 200 || !json.Valid(page.Body.Bytes()) {
			t.Fatal("SDK page", instance, page.Code, page.Body.String())
		}
		if strings.Contains(page.Body.String(), "synthetic-native") || strings.Contains(page.Body.String(), server.URL) {
			t.Fatal("page leaked transport credentials")
		}
		form := callTest(t, a.Handler(), "GET", "/api/plugins/"+instance+"/form", "", cookie, "")
		if form.Code != 200 {
			t.Fatal("SDK form", instance, form.Code, form.Body.String())
		}
	}
	if requests.Load() != afterA {
		t.Fatal("read-only SDK calls performed HTTP")
	}
	if _, err := a.queueAutoSignInCommand(ctx, "AutoSignIn_clone", pluginAPIAuthority{Kind: "command"}, "飞书", "owned-user"); err != nil {
		t.Fatal(err)
	}
	autoSignInSDKExecute(t, a, "AutoSignIn_clone")
	if requests.Load() <= afterA || len(autoSignInSDKState(t, a, "AutoSignIn_clone").History) == 0 {
		t.Fatal("second installed clone did not execute independently")
	}
}

func TestPluginAutoSignInSDKRejectsWorkerAuthorityAndRawEvent(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	autoSignInSDKInstall(t, a, "AutoSignIn", map[string]any{"enabled": true, "onlyonce": false, "cron": "0 0 * * *", "sign_sites": []any{}, "login_sites": []any{}})
	ctx := context.Background()
	in, err := a.pluginStore.Installation(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	identity := plugins.Identity{PluginID: in.PluginID, InstanceID: in.InstanceID, Version: in.Version}
	for _, method := range []string{autosignin.ConfigureCapability, autosignin.RunCapability, autosignin.DeleteCapability} {
		if _, err := a.pluginHostCall(ctx, in, nil, identity, method, json.RawMessage(`{}`)); err == nil {
			t.Fatalf("mutation %s accepted without run", method)
		}
	}
	for _, method := range []string{autosignin.OptionsCapability, autosignin.SnapshotCapability} {
		if _, err := a.pluginHostCall(ctx, in, nil, identity, method, json.RawMessage(`{"url":"https://untrusted.invalid"}`)); err == nil {
			t.Fatalf("read %s accepted worker-selected scope", method)
		}
	}
	queued, err := a.pluginStore.EnqueueRun(ctx, "AutoSignIn", "event", json.RawMessage(`{"Data":{"action":"site_signin","channel":"forged","user":"victim"}}`))
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.ID != queued.ID {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	final, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || final.State == "succeeded" {
		t.Fatal("raw caller event was treated as trusted origin", final.State, err)
	}
}

func TestPluginAutoSignInSDKSourceFailureStopsAtExactBoundary(t *testing.T) {
	for _, at := range []string{"start", "category"} {
		t.Run(at, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if at == "category" {
					tx, err := a.Store.DB.BeginTx(ctx, nil)
					if err == nil {
						err = a.pluginStore.putNoticePreferenceTx(ctx, tx, "superuser", "", 123)
						if err == nil {
							err = tx.Commit()
						} else {
							_ = tx.Rollback()
						}
					}
					if err != nil {
						t.Error(err)
					}
				}
				fmt.Fprint(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
			}))
			defer server.Close()
			settings := a.Settings()
			settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
			signinSitesPublish(t, a, settings)
			autoSignInSDKInstall(t, a, "AutoSignIn", map[string]any{"enabled": true, "onlyonce": false, "notify": true, "cron": "0 0 * * *", "sign_sites": []any{"all"}, "login_sites": []any{"all"}, "queue_cnt": 1})
			before, err := a.pluginStore.Config(ctx, "AutoSignIn")
			if err != nil {
				t.Fatal(err)
			}
			if at == "start" {
				noticePreferenceTest(t, a, "superuser", "", 123)
			}
			queued, err := a.queueAutoSignInCommand(ctx, "AutoSignIn", pluginAPIAuthority{Kind: "command"}, "飞书", "owned-user")
			if err != nil {
				t.Fatal(err)
			}
			run, err := a.pluginStore.ClaimRun(ctx)
			if err != nil || run.ID != queued.ID {
				t.Fatal(err)
			}
			a.executePluginRun(ctx, run)
			final, err := a.pluginStore.Run(ctx, run.ID)
			if err != nil || final.State != "failed" || final.ErrorCode != autoSignInSourceFailed {
				t.Fatalf("not a proved source failure: %s %s %v", final.State, final.ErrorCode, err)
			}
			state := autoSignInSDKState(t, a, "AutoSignIn")
			if state.ActiveRun != "" || state.Runs[run.ID].Phase != "failed_source" {
				t.Fatal("known source failure remained unknown")
			}
			if at == "start" {
				if requests.Load() != 0 || len(state.History) != 0 {
					t.Fatal("start failure continued into HTTP/history")
				}
			} else {
				if requests.Load() == 0 || len(state.History) == 0 {
					t.Fatal("category failure lost completed signin history")
				}
				for _, entries := range state.Runs[run.ID].Lanes {
					for _, entry := range entries {
						if entry.Kind == "config" {
							t.Fatal("config write continued after category failure")
						}
					}
				}
				for lane := range state.Runs[run.ID].Lanes {
					if strings.HasPrefix(lane, "登录:") {
						t.Fatal("login continued after category failure")
					}
				}
			}
			after, err := a.pluginStore.Config(ctx, "AutoSignIn")
			if err != nil || after.Revision != before.Revision {
				t.Fatal("failed source changed config", err)
			}
			proof, code, found, err := a.pluginStore.autoSignInDeferredOutcome(ctx, final, autosignin.RunCapability)
			if err != nil || !found || code != autoSignInSourceFailed || !json.Valid(proof) {
				t.Fatal("historical terminal proof missing", code, found, err)
			}
			count := requests.Load()
			// Removing the independent source proof must not recover as known failure.
			if _, err = a.Store.DB.Exec(`DELETE FROM plugin_signin_source_results`); err != nil {
				t.Fatal(err)
			}
			if _, _, found, err = a.pluginStore.autoSignInDeferredOutcome(ctx, final, autosignin.RunCapability); err == nil && found {
				t.Fatal("terminal accepted without independent source evidence")
			}
			if requests.Load() != count {
				t.Fatal("historical inspection resent a request")
			}
		})
	}
}

func TestPluginAutoSignInSDKOwnerTakeoverPreservesRuntimeAndSchedules(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	var pageRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageRequests.Add(1)
		fmt.Fprint(w, "page must not request this site")
	}))
	defer server.Close()
	settings := a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
	// This fixture survives New, unlike signinSitesPublish which deliberately
	// updates only the in-process settings and durable identity map.
	a.settingsMu.Lock()
	settingsErr := a.saveSettings(ctx, settings)
	a.settingsMu.Unlock()
	if settingsErr != nil {
		t.Fatal(settingsErr)
	}
	autoSignInSDKInstall(t, a, "AutoSignIn", map[string]any{"enabled": true, "onlyonce": true, "notify": false, "sign_sites": []any{}, "login_sites": []any{}})
	before := autoSignInSDKState(t, a, "AutoSignIn")
	config, err := a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	in, err := a.pluginStore.Installation(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}

	var dueBefore string
	if err = a.Store.DB.QueryRow(`SELECT COALESCE(group_concat(service_id||':'||COALESCE(next_due,''),'|'),'') FROM (SELECT service_id,next_due FROM plugin_schedules WHERE instance_id='AutoSignIn' ORDER BY service_id)`).Scan(&dueBefore); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		dir := a.Dir
		if err = a.Close(); err != nil {
			t.Fatal(err)
		}
		a, err = New(Options{DataDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		useBuiltInPluginProcess(t, a)
		if err = a.initializeNativePlugins(ctx); err != nil {
			t.Fatal(err)
		}
		// Exercise the real SDK page before any helper can claim an owner.
		page := callTest(t, a.Handler(), "GET", "/api/plugins/AutoSignIn/page", "", cookie, "")
		if page.Code != 200 || !json.Valid(page.Body.Bytes()) {
			t.Fatal("cold SDK page", page.Code, page.Body.String())
		}
		a.pluginAutoSignIn.mu.Lock()
		ownerCount := len(a.pluginAutoSignIn.owners)
		a.pluginAutoSignIn.mu.Unlock()
		if ownerCount != 0 || pageRequests.Load() != 0 {
			t.Fatal("read-only page claimed owner or made HTTP", ownerCount, pageRequests.Load())
		}
		tx, readErr := a.Store.DB.BeginTx(ctx, nil)
		if readErr != nil {
			t.Fatal(readErr)
		}
		handled, pageState, readErr := a.pluginStore.autoSignInScheduleStateTx(ctx, tx, in.InstanceID, in.Generation, config.Revision)
		_ = tx.Rollback()
		oldState, _ := json.Marshal(before)
		newState, _ := json.Marshal(pageState)
		if readErr != nil || !handled || string(oldState) != string(newState) {
			t.Fatal("cold page mutated sealed state/epoch/jobs/runs", readErr)
		}
		pageConfig, readErr := a.pluginStore.Config(ctx, in.InstanceID)
		if readErr != nil || pageConfig.Revision != config.Revision || string(pageConfig.Value) != string(config.Value) {
			t.Fatal("cold page changed config", readErr)
		}
		var pageRuns int
		if readErr = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='AutoSignIn'`).Scan(&pageRuns); readErr != nil || pageRuns != len(before.Runs) {
			t.Fatal("cold page enqueued a run", pageRuns, readErr)
		}
		if initialize, err := a.shouldInitializeAutoSignIn(ctx, in, config); err != nil || initialize {
			t.Fatal("restored initialized state would enqueue init", initialize, err)
		}
		after := autoSignInSDKState(t, a, "AutoSignIn")
		if after.RuntimeEpoch == before.RuntimeEpoch || !jsonEqualAutoSignIn(before.Runtime, after.Runtime) || !jsonEqualAutoSignIn(before.Services, after.Services) || !jsonEqualAutoSignIn(before.Config, after.Config) {
			t.Fatal("owner takeover reinitialized source runtime")
		}
		oldJobs, _ := json.Marshal(before.Jobs)
		newJobs, _ := json.Marshal(after.Jobs)
		if string(oldJobs) != string(newJobs) || len(after.Runs) != len(before.Runs) {
			t.Fatal("owner takeover created jobs or runs")
		}

		var dueAfter string
		if err = a.Store.DB.QueryRow(`SELECT COALESCE(group_concat(service_id||':'||COALESCE(next_due,''),'|'),'') FROM (SELECT service_id,next_due FROM plugin_schedules WHERE instance_id='AutoSignIn' ORDER BY service_id)`).Scan(&dueAfter); err != nil || dueAfter != dueBefore {
			t.Fatal("cold New changed actual schedule next_due", err)
		}
		before = after
	}
}
func jsonEqualAutoSignIn(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	left, _ := json.Marshal(x)
	right, _ := json.Marshal(y)
	return string(left) == string(right)
}

func TestPluginAutoSignInSDKColdPageRejectsMissingOrCorruptState(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			autoSignInSDKInstall(t, a, "AutoSignIn", map[string]any{"enabled": true, "onlyonce": false, "sign_sites": []any{}, "login_sites": []any{}})
			config, err := a.pluginStore.Config(ctx, "AutoSignIn")
			if err != nil {
				t.Fatal(err)
			}
			dir := a.Dir
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			useBuiltInPluginProcess(t, a)
			if err = a.initializeNativePlugins(ctx); err != nil {
				t.Fatal(err)
			}
			if damage == "missing" {
				_, err = a.Store.DB.Exec(`UPDATE plugin_signin_meta SET generation=generation+100 WHERE instance_id='AutoSignIn'`)
			} else {
				_, err = a.Store.DB.Exec(`UPDATE plugin_signin_meta SET sealed=x'00' WHERE instance_id='AutoSignIn'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			var beforeOwner, afterOwner []byte
			if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_signin_owners WHERE instance_id='AutoSignIn'`).Scan(&beforeOwner); err != nil {
				t.Fatal(err)
			}
			page := callTest(t, a.Handler(), "GET", "/api/plugins/AutoSignIn/page", "", cookie, "")
			if page.Code == 200 {
				t.Fatal("invalid cold state produced a page", damage)
			}
			a.pluginAutoSignIn.mu.Lock()
			ownerCount := len(a.pluginAutoSignIn.owners)
			a.pluginAutoSignIn.mu.Unlock()
			if ownerCount != 0 {
				t.Fatal("failed page claimed an owner")
			}
			if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_signin_owners WHERE instance_id='AutoSignIn'`).Scan(&afterOwner); err != nil || string(beforeOwner) != string(afterOwner) {
				t.Fatal("failed page changed owner", err)
			}
			afterConfig, err := a.pluginStore.Config(ctx, "AutoSignIn")
			if err != nil || afterConfig.Revision != config.Revision || string(afterConfig.Value) != string(config.Value) {
				t.Fatal("failed page changed config", err)
			}
			var runs, notices int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='AutoSignIn'`).Scan(&runs); err != nil || runs != 1 {
				t.Fatal("failed page enqueued init", runs, err)
			}
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_notice_publications`).Scan(&notices); err != nil || notices != 0 {
				t.Fatal("failed page published notice", notices, err)
			}
		})
	}
}
