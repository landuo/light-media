package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cf "light-media/internal/plugins/cloudflarespeedtest"
	"light-media/internal/plugins/uninstall"
)

func cloudflareTestRunState(t *testing.T, a *App) PluginRun {
	t.Helper()
	if _, e := a.pluginStore.EnqueueRun(context.Background(), cf.PluginID, "run", nil); e != nil {
		t.Fatal(e)
	}
	run := cloudflareTestClaim(t, a, cf.PluginID)
	a.executePluginRun(context.Background(), run)
	actual, e := a.pluginStore.Run(context.Background(), run.ID)
	if e != nil {
		t.Fatal(e)
	}
	return actual
}

func cloudflareTestCold(t *testing.T, a *App) *App {
	t.Helper()
	dir, hosts := a.Dir, a.pluginHostsFile
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = next.Close() })
	next.pluginHostsFile = hosts
	useBuiltInPluginProcess(t, next)
	return next
}

func TestPluginCloudflareUnknownEffectsColdDoNotReplayAndAuthenticatedAbandon(t *testing.T) {
	for _, stage := range []string{"cli", "custom_hosts", "own_config", "custom_reload", "notice"} {
		t.Run(stage, func(t *testing.T) {
			a := newTestApp(t)
			cookie, _ := cloudflareTestInstall(t, a)
			port := cloudflareTestLoopback(t)
			cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig(fmt.Sprintf("-ip 127.0.0.1 -tp %d -dd -n 1 -t 1 -p 0", port)))
			var hit bool
			a.pluginCloudflare.afterEffect = func(kind string) error {
				if kind == stage {
					hit = true
					return errors.New("owned acknowledgement lost")
				}
				return nil
			}
			run := cloudflareTestRunState(t, a)
			if !hit || run.State != "reconcile_pending" {
				t.Fatal("unknown effect was completed", hit, run.State, run.ErrorCode)
			}
			s, _, e := a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1)
			if e != nil {
				t.Fatal(e)
			}
			before := mustCleanSeedJSON(s.Runs[run.ID].Effects)
			config, _ := a.pluginStore.Config(context.Background(), cf.PluginID)
			a = cloudflareTestCold(t, a)
			downloads := cloudflareTestOfficial(t, a)
			if e = a.initializeNativePlugins(context.Background()); e != nil {
				t.Fatal(e)
			}
			if _, e = a.pluginStore.claimRunForInstance(context.Background(), cf.PluginID); !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("startup replayed source", e)
			}
			if _, e = a.retryPluginDeferred(context.Background(), cf.PluginID, run.ID); e == nil {
				t.Fatal("unknown retry admitted")
			}
			blocked := cloudflareTestRunState(t, a)
			if blocked.State != "failed" || blocked.ErrorCode != cloudflareBlockedByUnknown {
				t.Fatal("new run bypassed unknown", blocked.State, blocked.ErrorCode)
			}
			for _, action := range []string{"reset", "uninstall"} {
				if w := callTest(t, a.Handler(), "POST", "/api/plugins/CloudflareSpeedTest/"+action, "", cookie, ""); w.Code == 200 {
					t.Fatal("lifecycle bypassed unknown", action)
				}
			}
			body := fmt.Sprintf(`{"run_id":%q,"confirm_abandon":true}`, run.ID)
			for _, auth := range []struct {
				cookie *http.Cookie
				origin string
			}{{nil, ""}, {cookie, "http://other.invalid"}} {
				if w := callTest(t, a.Handler(), "POST", "/api/plugins/CloudflareSpeedTest/abandon-unknown", body, auth.cookie, auth.origin); w.Code == 200 {
					t.Fatal("unauthorized abandon accepted")
				}
			}
			if w := callTest(t, a.Handler(), "POST", "/api/plugins/CloudflareSpeedTest/abandon-unknown", body, cookie, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			s, _, e = a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1)
			if e != nil || !validCloudflareResolution(s.Runs[run.ID], cf.PluginID, 1) || !bytes.Equal(before, mustCleanSeedJSON(s.Runs[run.ID].Effects)) {
				t.Fatal("unknown evidence changed", e)
			}
			after, _ := a.pluginStore.Config(context.Background(), cf.PluginID)
			if after.Revision != config.Revision || downloads.Load() != 0 {
				t.Fatal("cold/abandon repeated IO", after.Revision, config.Revision, downloads.Load())
			}
			if _, e = a.pluginStore.EnqueueRun(context.Background(), cf.PluginID, "run", nil); e != nil {
				t.Fatal("abandon did not permit new run", e)
			}
		})
	}
}

func TestPluginCloudflareConfirmedOwnerReceiptFinishesColdWithoutCLIOrHTTP(t *testing.T) {
	a := newTestApp(t)
	cookie, _ := cloudflareTestInstall(t, a)
	port := cloudflareTestLoopback(t)
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig(fmt.Sprintf("-ip 127.0.0.1 -tp %d -dd -n 1 -t 1 -p 0", port)))
	run := cloudflareTestRunState(t, a)
	if run.State != "succeeded" {
		t.Fatal(run.State, run.ErrorCode)
	}
	// Simulate power loss after owner+effects were durably confirmed but before
	// the outer deferred/run acknowledgement was published. Never replay IO.
	d, prior, e := a.pluginStore.readDeferred(context.Background(), a.Store.DB, run)
	if e != nil {
		t.Fatal(e)
	}
	d.Phase = "started"
	d.Result = nil
	tx, e := a.Store.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = a.pluginStore.writeDeferred(context.Background(), tx, d, prior); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if _, e = tx.Exec(`UPDATE plugin_runs SET state='running',result=NULL WHERE id=?`, run.ID); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	a = cloudflareTestCold(t, a)
	downloads := cloudflareTestOfficial(t, a)
	if e = a.pluginStore.recoverDeferredRuns(context.Background()); e != nil {
		t.Fatal(e)
	}
	recovered, e := a.pluginStore.Run(context.Background(), run.ID)
	if e != nil || recovered.State != "succeeded" || !bytes.Equal(recovered.Result, run.Result) || downloads.Load() != 0 {
		t.Fatal("confirmed receipt did not finish", e, recovered.State, downloads.Load())
	}
	var route string
	if e = a.Store.DB.QueryRow(`SELECT id FROM plugin_notice_routes WHERE instance_id='CloudflareSpeedTest'`).Scan(&route); e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, route); e != nil {
		t.Fatal("cold notice proof", e)
	}
}

func TestPluginCloudflareMissingEvidenceCannotCreateGenesisOrDropUnknown(t *testing.T) {
	for _, damage := range []string{"state", "root", "both", "deferred", "foreign-state", "foreign-root"} {
		t.Run(damage, func(t *testing.T) {
			a := newTestApp(t)
			cookie, _ := cloudflareTestInstall(t, a)
			cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig("-h"))
			run := cloudflareTestRunState(t, a)
			if run.State != "succeeded" {
				t.Fatal(run.State)
			}
			var e error
			switch damage {
			case "state":
				_, e = a.Store.DB.Exec(`DELETE FROM plugin_cloudflare_state`)
			case "root":
				_, e = a.Store.DB.Exec(`DELETE FROM plugin_cloudflare_roots`)
			case "both":
				_, e = a.Store.DB.Exec(`DELETE FROM plugin_cloudflare_roots;DELETE FROM plugin_cloudflare_state`)
			case "deferred":
				_, e = a.Store.DB.Exec(`DELETE FROM plugin_deferred_operations WHERE run_id=?`, run.ID)
			case "foreign-state", "foreign-root":
				key, table := cloudflareKey(1), "plugin_cloudflare_state"
				if damage == "foreign-root" {
					key, table = cloudflareRootKey(1), "plugin_cloudflare_roots"
				}
				var sealed []byte
				sealed, e = a.pluginStore.seal("run_result", "CloudflareSpeedTest.other", key, []byte(`{}`))
				if e == nil {
					_, e = a.Store.DB.Exec(`UPDATE `+table+` SET sealed=?`, sealed)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1); e == nil {
				t.Fatal("missing/foreign evidence was accepted")
			}
			for _, action := range []string{"reset", "uninstall"} {
				if w := callTest(t, a.Handler(), "POST", "/api/plugins/CloudflareSpeedTest/"+action, "", cookie, ""); w.Code == 200 {
					t.Fatal("lifecycle silently created new root", action)
				}
			}
		})
	}
}

func TestPluginCloudflareCommandRequiresLiveAuthorityAndOriginalSourceNotification(t *testing.T) {
	a := newTestApp(t)
	cookie, _ := cloudflareTestInstall(t, a)
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig("-h"))
	if _, e := a.queueCloudflareCommand(context.Background(), cf.PluginID, pluginAPIAuthority{Kind: "anonymous"}, "", "user"); e == nil {
		t.Fatal("anonymous command accepted")
	}
	settings := a.Settings()
	settings.APIToken = "owned-command-key"
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	authority := pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(settings.APIToken)}
	if _, e := a.queueCloudflareCommand(context.Background(), cf.PluginID, authority, "Web", "owned-user"); e != nil {
		t.Fatal(e)
	}
	run := cloudflareTestExecute(t, a, cloudflareTestClaim(t, a, cf.PluginID))
	var n int
	if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_cloudflare_notices WHERE instance_id='CloudflareSpeedTest'`).Scan(&n); e != nil || n != 1 {
		t.Fatal("source command start notice missing", n, e)
	}
	if !strings.Contains(string(run.Result), "invalid_result") {
		t.Fatal("real --help synthetic result", string(run.Result))
	}
	if _, e := a.queueCloudflareCommand(context.Background(), cf.PluginID, authority, "Web", "owned-user"); e != nil {
		t.Fatal(e)
	}
	settings.APIToken = "owned-command-rotated"
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	denied := cloudflareTestClaim(t, a, cf.PluginID)
	a.executePluginRun(context.Background(), denied)
	denied, e := a.pluginStore.Run(context.Background(), denied.ID)
	if e != nil || denied.State != "failed" || denied.ErrorCode != "CLOUDFLARE_SOURCE_FAILED" {
		t.Fatal("revoked command not known zero-effect rejection", denied.State, denied.ErrorCode, e)
	}
	if _, _, e = a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1); e != nil {
		t.Fatal("denied source poisoned owner", e)
	}
}

func TestPluginCloudflareSingleAndBulkLifecycleRetainHistoricalEvidence(t *testing.T) {
	for _, action := range []string{"reset", "uninstall", "bulk"} {
		t.Run(action, func(t *testing.T) {
			a := newTestApp(t)
			cookie, _ := cloudflareTestInstall(t, a)
			cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig("-h"))
			run := cloudflareTestRunState(t, a)
			if run.State != "succeeded" {
				t.Fatal(run.State)
			}
			if action == "bulk" {
				init := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{cf.PluginID}}, true)
				cloudflareTestExecute(t, a, init)
			} else if w := callTest(t, a.Handler(), "POST", "/api/plugins/CloudflareSpeedTest/"+action, "", cookie, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			in, e := a.pluginStore.Installation(context.Background(), cf.PluginID)
			if e != nil || in.Generation != 2 || (action != "reset" && in.Installed) {
				t.Fatal("lifecycle generation", in.Generation, in.Installed, e)
			}
			if _, _, e = a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1); e != nil {
				t.Fatal("old proof lost", e)
			}
			if _, _, e = a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 2); e != nil {
				t.Fatal("new generation root", e)
			}
			if _, e = os.Stat(a.pluginHostsFile); e != nil {
				t.Fatal("unrelated owned hosts removed", e)
			}
			var count int
			if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_cloudflare_state`).Scan(&count); e != nil || count != 2 {
				t.Fatal("owner evidence count", count, e)
			}
		})
	}
}

func TestPluginCloudflareAlreadyQueuedRunGetsProvenZeroEffectRejection(t *testing.T) {
	a := newTestApp(t)
	cookie, downloads := cloudflareTestInstall(t, a)
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig("-h"))
	if warm := cloudflareTestRunState(t, a); warm.State != "succeeded" {
		t.Fatal("official CLI preflight", warm.State)
	}
	for n := 0; n < 2; n++ {
		if _, e := a.pluginStore.EnqueueRun(context.Background(), cf.PluginID, "run", nil); e != nil {
			t.Fatal(e)
		}
	}
	var cli int
	a.pluginCloudflare.afterEffect = func(kind string) error {
		if kind == "cli" {
			cli++
			return errors.New("owned CLI acknowledgement lost")
		}
		return nil
	}
	first := cloudflareTestClaim(t, a, cf.PluginID)
	a.executePluginRun(context.Background(), first)
	first, e := a.pluginStore.Run(context.Background(), first.ID)
	if e != nil || first.State != "reconcile_pending" {
		t.Fatal(first.State, e)
	}
	second := cloudflareTestClaim(t, a, cf.PluginID)
	a.executePluginRun(context.Background(), second)
	second, e = a.pluginStore.Run(context.Background(), second.ID)
	if e != nil || second.State != "failed" || second.ErrorCode != cloudflareBlockedByUnknown || cli != 1 || downloads.Load() != 1 {
		t.Fatal("queued source repeated IO", second.State, second.ErrorCode, cli, downloads.Load(), e)
	}
	s, _, e := a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1)
	if e != nil || len(s.Runs[second.ID].Effects) != 0 || !bytes.Equal(s.Runs[second.ID].Result, cloudflareBlockedResult()) || s.Runs[first.ID].Phase != "started" {
		t.Fatal("zero-effect rejection poisoned old proof", e)
	}
	if e = a.pluginStore.recoverDeferredRuns(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestPluginCloudflareCustomHostsConcurrentEditIsNotOverwritten(t *testing.T) {
	a := newTestApp(t)
	cookie, _ := cloudflareTestInstall(t, a)
	port := cloudflareTestLoopback(t)
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig(fmt.Sprintf("-ip 127.0.0.1 -tp %d -dd -n 1 -t 1 -p 0", port)))
	var edited bool
	a.pluginCloudflare.afterEffect = func(kind string) error {
		if kind != "cli" {
			return nil
		}
		current, e := a.pluginStore.Config(context.Background(), "CustomHosts")
		if e != nil {
			return e
		}
		_, e = a.pluginStore.SaveConfig(context.Background(), "CustomHosts", current.Revision, map[string]any{"hosts": "192.0.2.99 synthetic-new.example", "enabled": false})
		edited = e == nil
		return e
	}
	run := cloudflareTestRunState(t, a)
	if !edited || run.State != "failed" || run.ErrorCode != "CLOUDFLARE_SOURCE_FAILED" {
		t.Fatal("CAS conflict not known failure", edited, run.State, run.ErrorCode)
	}
	config, e := a.pluginStore.Config(context.Background(), "CustomHosts")
	if e != nil || !strings.Contains(string(config.Value), "192.0.2.99") {
		t.Fatal("concurrent edit overwritten", e)
	}
	if _, _, e = a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1); e != nil {
		t.Fatal("CAS failure poisoned proof", e)
	}
}

func TestPluginCloudflareStartedAndOwnerFactAreAtomicBeforeHostEntry(t *testing.T) {
	a := newTestApp(t)
	cookie, downloads := cloudflareTestInstall(t, a)
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig("-h"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var saw bool
	a.pluginCloudflare.beforeOwnerAdmission = func(_ context.Context, active PluginRun) {
		s, _, e := a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, active.InstanceID, active.Generation)
		if e != nil || s.Runs[active.ID].Phase != "started" || s.Runs[active.ID].Entered || len(s.Runs[active.ID].Effects) != 0 {
			t.Error("committed started missing its exact owner fact", e)
		}
		saw = true
		cancel()
	}
	if _, e := a.pluginStore.EnqueueRun(context.Background(), cf.PluginID, "run", nil); e != nil {
		t.Fatal(e)
	}
	run := cloudflareTestClaim(t, a, cf.PluginID)
	a.executePluginRun(ctx, run)
	if !saw || downloads.Load() != 0 {
		t.Fatal("host-entry fixture performed native IO")
	}
	a = cloudflareTestCold(t, a)
	if e := a.pluginStore.recoverDeferredRuns(context.Background()); e != nil {
		t.Fatal(e)
	}
	actual, e := a.pluginStore.Run(context.Background(), run.ID)
	if e != nil || actual.State != "reconcile_pending" {
		t.Fatal("cold startup forgot admitted unknown", actual.State, e)
	}
	s, _, e := a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1)
	if e != nil || s.Runs[run.ID].Entered || len(s.Runs[run.ID].Effects) != 0 {
		t.Fatal("cold owner fact missing/IO started", e)
	}
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/CloudflareSpeedTest/abandon-unknown", fmt.Sprintf(`{"run_id":%q,"confirm_abandon":true}`, run.ID), cookie, ""); w.Code != 200 {
		t.Fatal("authenticated unknown could not be resolved", w.Code, w.Body.String())
	}
}

func TestPluginCloudflareTwoRealSDKReadyTasksHoldPreparedUntilOwnerLease(t *testing.T) {
	a := newTestApp(t)
	cookie, _ := cloudflareTestInstall(t, a)
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig("-h"))
	if warm := cloudflareTestRunState(t, a); warm.State != "succeeded" {
		t.Fatal(warm.State)
	}
	for n := 0; n < 2; n++ {
		if _, e := a.pluginStore.EnqueueRun(context.Background(), cf.PluginID, "run", nil); e != nil {
			t.Fatal(e)
		}
	}
	first, second := cloudflareTestClaim(t, a, cf.PluginID), cloudflareTestClaim(t, a, cf.PluginID)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var cli atomic.Int64
	a.pluginCloudflare.afterEffect = func(kind string) error {
		if kind == "cli" {
			cli.Add(1)
			once.Do(func() { close(entered); <-release })
			return errors.New("owned lost CLI reply")
		}
		return nil
	}
	done1, done2 := make(chan struct{}), make(chan struct{})
	go func() { defer close(done1); a.executePluginRun(context.Background(), first) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first CLI did not run")
	}
	go func() { defer close(done2); a.executePluginRun(context.Background(), second) }()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-done1
		<-done2
	}()
	prepared := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		d, _, e := a.pluginStore.readDeferred(context.Background(), a.Store.DB, second)
		if e == nil {
			if d.Phase != "prepared" {
				t.Fatal("second started before owner release", d.Phase)
			}
			prepared = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !prepared {
		t.Fatal("second SDK never reached prepared")
	}
	// This path needs pluginMu; a real long CLI must not retain the global lock.
	saved := make(chan error, 1)
	go func() { saved <- a.saveSettings(context.Background(), a.Settings()) }()
	select {
	case e := <-saved:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner wait/CLI held global lock")
	}
	close(release)
	<-done1
	<-done2
	one, _ := a.pluginStore.Run(context.Background(), first.ID)
	two, _ := a.pluginStore.Run(context.Background(), second.ID)
	if one.State != "reconcile_pending" || two.State != "failed" || two.ErrorCode != cloudflareBlockedByUnknown || cli.Load() != 1 {
		t.Fatal("queued ready task redispatched or poisoned", one.State, two.State, two.ErrorCode, cli.Load())
	}
	if _, _, e := a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1); e != nil {
		t.Fatal("exact blocked proof lost", e)
	}
}

func TestPluginCloudflareRootFailureRollsBackStartedAndKeepsPrepared(t *testing.T) {
	a := newTestApp(t)
	cookie, _ := cloudflareTestInstall(t, a)
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig("-h"))
	if _, e := a.pluginStore.EnqueueRun(context.Background(), cf.PluginID, "run", nil); e != nil {
		t.Fatal(e)
	}
	run := cloudflareTestClaim(t, a, cf.PluginID)
	if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_fail_cf_root BEFORE UPDATE ON plugin_cloudflare_roots BEGIN SELECT RAISE(ABORT,'owned root failure'); END`); e != nil {
		t.Fatal(e)
	}
	a.executePluginRun(context.Background(), run)
	d, _, e := a.pluginStore.readDeferred(context.Background(), a.Store.DB, run)
	if e != nil || d.Phase != "prepared" {
		t.Fatal("root failure published started", d.Phase, e)
	}
	if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_fail_cf_root`); e != nil {
		t.Fatal(e)
	}
	s, _, e := a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1)
	if _, exists := s.Runs[run.ID]; e != nil || exists {
		t.Fatal("root failure published partial owner fact", exists, e)
	}
}
