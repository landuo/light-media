package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/autosignin"
)

func signinConcurrentConfig(enabled bool) map[string]any {
	return map[string]any{"enabled": enabled, "onlyonce": false, "notify": false, "cron": "0 0 * * *", "sign_sites": []any{"all"}, "login_sites": []any{"all"}, "queue_cnt": 1}
}
func signinConcurrentClaim(t *testing.T, a *App, instance string) PluginRun {
	t.Helper()
	queued, err := a.queueAutoSignInCommand(context.Background(), instance, pluginAPIAuthority{Kind: "command"}, "飞书", "owned-user")
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.claimRunForInstance(context.Background(), instance)
	if err != nil || run.ID != queued.ID {
		t.Fatalf("claim %s: %s %v", instance, run.ID, err)
	}
	return run
}
func signinConcurrentWait(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

// Both invocations pass through the actual SDK executable. The first host HTTP
// request remains blocked until the second clone has finished real business IO.
// This catches global execution/process locks that serial-only tests cannot.
func TestPluginAutoSignInSDKBlockedCloneDoesNotBlockOtherClone(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
			<-release
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
	}))
	defer server.Close()
	defer unblock()
	settings := a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
	signinSitesPublish(t, a, settings)
	for _, instance := range []string{"AutoSignIn", "AutoSignIn_clone"} {
		autoSignInSDKInstall(t, a, instance, signinConcurrentConfig(true))
	}
	first := signinConcurrentClaim(t, a, "AutoSignIn")
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); a.executePluginRun(context.Background(), first) }()
	defer func() { unblock(); signinConcurrentWait(t, firstDone, "first clone cleanup") }()
	signinConcurrentWait(t, entered, "first clone HTTP entry")
	second := signinConcurrentClaim(t, a, "AutoSignIn_clone")
	secondDone := make(chan struct{})
	go func() { defer close(secondDone); a.executePluginRun(context.Background(), second) }()
	defer func() { unblock(); signinConcurrentWait(t, secondDone, "second clone cleanup") }()
	signinConcurrentWait(t, secondDone, "second clone business completion while first is blocked")
	final, err := a.pluginStore.Run(context.Background(), second.ID)
	if err != nil || final.State != "succeeded" {
		t.Fatalf("second clone: %s %s %v", final.State, final.ErrorCode, err)
	}
	if requests.Load() < 2 || len(autoSignInSDKState(t, a, "AutoSignIn_clone").History) == 0 {
		t.Fatal("second clone did not establish independent HTTP/history progress")
	}
	select {
	case <-firstDone:
		t.Fatal("first clone unexpectedly finished before HTTP release")
	default:
	}
	firstStored, err := a.pluginStore.Run(context.Background(), first.ID)
	if err != nil || firstStored.State != "running" {
		t.Fatal("blocked clone lost its independent running scope", firstStored.State, err)
	}
	unblock()
	signinConcurrentWait(t, firstDone, "first clone business completion")
	firstStored, err = a.pluginStore.Run(context.Background(), first.ID)
	if err != nil || firstStored.State != "succeeded" || len(autoSignInSDKState(t, a, "AutoSignIn").History) == 0 {
		t.Fatal("first clone did not finish after release", firstStored.State, err)
	}
}

// The local fixture deliberately withholds the first HTTP response. Stop is
// actual invocation-context cancellation; disable uses the real HTTP configuration
// admission change, not a worker-provided flag or a forged run-state UPDATE.
func TestPluginAutoSignInSDKRevocationBlocksSecondHTTPAndSourceWrites(t *testing.T) {
	for _, mode := range []string{"stop", "disable"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					close(entered)
					<-release
				}
				fmt.Fprint(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
			}))
			defer server.Close()
			defer unblock()
			settings := a.Settings()
			settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
			signinSitesPublish(t, a, settings)
			autoSignInSDKInstall(t, a, "AutoSignIn", signinConcurrentConfig(true))
			config, err := a.pluginStore.Config(ctx, "AutoSignIn")
			if err != nil {
				t.Fatal(err)
			}
			run := signinConcurrentClaim(t, a, "AutoSignIn")
			running, cancel := context.WithCancel(ctx)
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); a.executePluginRun(running, run) }()
			defer func() { cancel(); unblock(); signinConcurrentWait(t, done, "revoked run cleanup") }()
			signinConcurrentWait(t, entered, "first HTTP entry")
			sourceCount := func() int {
				var n int
				if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_source_acceptances WHERE instance_id='AutoSignIn'`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			beforeSource := sourceCount()
			expectedRevision := config.Revision
			if mode == "stop" {
				cancel()
			} else {
				payload, err := json.Marshal(map[string]any{"revision": config.Revision, "config": signinConcurrentConfig(false)})
				if err != nil {
					t.Fatal(err)
				}
				response := callTest(t, a.Handler(), "PUT", "/api/plugins/AutoSignIn/config", string(payload), cookie, "")
				if response.Code != 200 {
					t.Fatalf("disable HTTP: %d %s", response.Code, response.Body.String())
				}
				expectedRevision = config.Revision + 1
			}
			unblock()
			signinConcurrentWait(t, done, "revoked run terminal bookkeeping")
			final, err := a.pluginStore.Run(ctx, run.ID)
			if err != nil || final.State != "reconcile_pending" {
				t.Fatalf("in-flight effect was not retained as unknown: %s %s %v", final.State, final.ErrorCode, err)
			}
			if requests.Load() != 1 || sourceCount() != beforeSource {
				t.Fatal("revoked run continued HTTP or source writes", requests.Load())
			}
			after, err := a.pluginStore.Config(ctx, "AutoSignIn")
			if err != nil || after.Revision != expectedRevision {
				t.Fatal("revoked source rewrote configuration", err)
			}
			if mode == "stop" {
				state := autoSignInSDKState(t, a, "AutoSignIn")
				if len(state.History) != 0 || state.ActiveRun != run.ID {
					t.Fatal("unknown HTTP fabricated committed history or cleared active run")
				}
			}
			if _, _, found, err := a.pluginStore.autoSignInDeferredOutcome(ctx, final, autosignin.RunCapability); err == nil && found {
				t.Fatal("unknown effect fabricated a terminal business receipt")
			}
			// Even an explicit attempt to execute the original immutable admission may
			// not resend an unknown request or produce new source acceptances.
			a.executePluginRun(ctx, run)
			if requests.Load() != 1 || sourceCount() != beforeSource {
				t.Fatal("unknown admission was replayed")
			}
		})
	}
}

// An idle source can be disabled through the real HTTP configuration endpoint:
// no speculative site traffic, no fresh once execution, no surviving timer.
func TestPluginAutoSignInSDKHTTPDisableCancelsFutureTimers(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	initial := signinConcurrentConfig(true)
	initial["onlyonce"] = true
	initial["sign_sites"] = []any{}
	initial["login_sites"] = []any{}
	autoSignInSDKInstall(t, a, "AutoSignIn", initial)
	var before int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='AutoSignIn' AND active=1 AND next_due IS NOT NULL`).Scan(&before); err != nil || before == 0 {
		t.Fatal("fixture did not establish future schedules", before, err)
	}
	config, err := a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	disabled := signinConcurrentConfig(false)
	disabled["sign_sites"] = []any{}
	disabled["login_sites"] = []any{}
	payload, err := json.Marshal(map[string]any{"revision": config.Revision, "config": disabled})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/AutoSignIn/config", string(payload), cookie, "")
	if response.Code != 200 {
		t.Fatalf("disable HTTP: %d %s", response.Code, response.Body.String())
	}
	autoSignInSDKExecute(t, a, "AutoSignIn")
	state := autoSignInSDKState(t, a, "AutoSignIn")
	var runtime autosignin.Object
	if autosignin.Decode(state.Runtime, &runtime) != nil || autosignin.Truth(runtime["enabled"]) || len(state.Jobs) != 0 {
		t.Fatal("source disable retained runtime/jobs")
	}
	var active int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='AutoSignIn' AND active=1`).Scan(&active); err != nil || active != 0 {
		t.Fatal("source disable left active timers", active, err)
	}
	var queued int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='AutoSignIn' AND state='pending'`).Scan(&queued); err != nil || queued != 0 {
		t.Fatal("source disable queued unintended execution", queued, err)
	}
}
