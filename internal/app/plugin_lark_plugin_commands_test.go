package app

import (
	"context"
	"strings"
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
		"/site_signin":          "LARK_PLUGIN_COMMAND_CONSUMER_PENDING",
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
