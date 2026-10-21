package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/commandrouter"
	seed "light-media/internal/plugins/cleaninvalidseed"
)

func TestLarkPluginCommandQueuesCleanInvalidSeed(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	initial := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(nil), true)
	if got := cleanSeedRuntimeExecute(t, a, initial); got.State != "succeeded" {
		t.Fatal("clean seed init", got.State, got.ErrorCode)
	}
	command, ok := commandrouter.LookupPluginCommand("/detect_invalid_torrents")
	if !ok {
		t.Fatal("command missing from registry")
	}
	if err := a.dispatchLarkPluginCommand(ctx, command, "ou_owned_user"); err != nil {
		t.Fatal(err)
	}
	var runID, action string
	if err := a.Store.DB.QueryRow(`SELECT id,action FROM plugin_runs WHERE instance_id='CleanInvalidSeed' AND action='event' AND state='pending' ORDER BY rowid DESC LIMIT 1`).Scan(&runID, &action); err != nil || runID == "" || action != "event" {
		t.Fatal("plugin command run not queued", runID, action, err)
	}
	run, err := a.pluginStore.Run(ctx, runID)
	if err != nil || !strings.Contains(string(run.Input), "event_id") {
		t.Fatal("command run lost its event binding", string(run.Input), err)
	}
}

func TestLarkPluginCommandRejectsUnreadyTargets(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	command, _ := commandrouter.LookupPluginCommand("/detect_invalid_torrents")
	if err := a.dispatchLarkPluginCommand(ctx, command, "ou_owned_user"); err == nil || pluginErrorCodeInternal(err) != "LARK_PLUGIN_COMMAND_DENIED" {
		t.Fatal("uninstalled target accepted", err)
	}
	if err := a.dispatchLarkPluginCommand(ctx, command, "  "); err == nil || pluginErrorCodeInternal(err) != "LARK_PLUGIN_COMMAND_DENIED" {
		t.Fatal("empty source accepted", err)
	}
	wrong := command
	wrong.Action = "delete_invalid_files"
	if err := a.dispatchLarkPluginCommand(ctx, wrong, "ou_owned_user"); err == nil || pluginErrorCodeInternal(err) != "LARK_PLUGIN_COMMAND_DENIED" {
		t.Fatal("foreign action accepted", err)
	}
	for name, code := range map[string]string{
		"/cloudflare_speedtest": "LARK_PLUGIN_COMMAND_DENIED",
		"/site_signin":          "LARK_PLUGIN_COMMAND_DENIED",
	} {
		pluginCommand, ok := commandrouter.LookupPluginCommand(name)
		if !ok {
			t.Fatal("command missing", name)
		}
		if err := a.dispatchLarkPluginCommand(ctx, pluginCommand, "ou_owned_user"); err == nil || pluginErrorCodeInternal(err) != code {
			t.Errorf("%s returned %v, want %s", name, err, code)
		}
	}
}
func TestLarkPluginCommandQueuesCloudflare(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	cookie, _ := cloudflareTestInstall(t, a)
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig("-h"))
	command, _ := commandrouter.LookupPluginCommand("/cloudflare_speedtest")
	if err := a.dispatchLarkPluginCommand(ctx, command, "ou_owned_user"); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := a.Store.DB.QueryRow(`SELECT id FROM plugin_runs WHERE instance_id='CloudflareSpeedTest' AND action='command' AND state='pending' ORDER BY rowid DESC LIMIT 1`).Scan(&runID); err != nil || runID == "" {
		t.Fatal("cloudflare command run not queued", runID, err)
	}
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_cloudflare_commands WHERE instance_id='CloudflareSpeedTest'`).Scan(&sealed); err != nil || len(sealed) == 0 {
		t.Fatal("cloudflare command fact missing", err)
	}
}

// This crosses the native Lark plugin-command dispatcher, sealed command fact,
// actual SDK child and host HTTP, rather than treating queue insertion as success.
func TestLarkPluginCommandRunsAutoSignInThroughTrustedSDK(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Cookie") != "sid=synthetic-native" {
			t.Error("trusted cookie missing")
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
	}))
	defer server.Close()
	settings := a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
	signinSitesPublish(t, a, settings)
	autoSignInSDKInstall(t, a, "AutoSignIn", map[string]any{"enabled": true, "notify": false, "onlyonce": false, "cron": "0 0 * * *", "queue_cnt": 1, "sign_sites": []any{"all"}, "login_sites": []any{}})
	command, ok := commandrouter.LookupPluginCommand("/site_signin")
	if !ok {
		t.Fatal("source command registry missing")
	}
	if err := a.dispatchLarkPluginCommand(ctx, command, "ou_owned_signin_user"); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := a.Store.DB.QueryRow(`SELECT id FROM plugin_runs WHERE instance_id='AutoSignIn' AND action='event' AND state='pending' ORDER BY rowid DESC LIMIT 1`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.Run(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	event, err := a.readAutoSignInCommand(ctx, run)
	if err != nil || event["action"] != "site_signin" || event["channel"] != "飞书" || event["user"] != "ou_owned_signin_user" {
		t.Fatal("original source command was not sealed", err)
	}
	final := autoSignInSDKExecute(t, a, "AutoSignIn")
	state := autoSignInSDKState(t, a, "AutoSignIn")
	if final.ID != runID || requests.Load() != 1 || len(state.History) == 0 || state.Runs[runID].Phase != "completed" {
		t.Fatal("dispatcher did not complete real source business", requests.Load())
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_source_results`).Scan(&count); err != nil || count < 2 {
		t.Fatal("command start/completion source proofs absent", count, err)
	}
}
