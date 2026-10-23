package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// The fixture uses the real compiled grant manifest and SDK transport. Its only
// side effect is the host's owned category.yaml write. Unlike the source handler,
// it deliberately does not clear enabled: that would hide the startup replay gap.
func TestPluginResultChannelFixture(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--plugin-result-channel" || i+1 >= len(os.Args) {
			continue
		}
		scenario := os.Args[i+1]
		manifest, _, exists := builtin.Lookup("CategoryEditor")
		if !exists {
			os.Exit(2)
		}
		err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			if method == "stop" {
				return json.RawMessage(`null`), nil
			}
			if method != "invoke" {
				return nil, errors.New("unexpected result-channel fixture method")
			}
			var input plugins.Invocation
			var config struct {
				Enabled bool   `json:"enabled"`
				Content string `json:"content"`
			}
			if json.Unmarshal(raw, &input) != nil || (input.Action != "init" && input.Action != "reload") || json.Unmarshal(input.Config, &config) != nil || !config.Enabled {
				return nil, errors.New("invalid result-channel fixture invocation")
			}
			if scenario == "business_failure" {
				return nil, errors.New("synthetic ordinary business failure")
			}
			if scenario != "oversize_before_host" {
				request, _ := json.Marshal(map[string]string{"content": config.Content})
				if _, err := host(ctx, "category.write", request); err != nil {
					return nil, err
				}
			}
			switch scenario {
			case "oversize_after_host", "oversize_before_host":
				// Exceed the whole logical envelope too, rather than accidentally
				// testing an App storage rejection within the transport allowance.
				return json.RawMessage(`"` + strings.Repeat("r", plugins.MaxLogicalMessageBytes) + `"`), nil
			case "exit_after_host":
				// host() already received the real persisted-write response. Drop
				// the final invocation reply without asking the SDK to fabricate it.
				os.Exit(23)
			case "write_ok":
				return json.RawMessage(`{"written":true}`), nil
			}
			return nil, errors.New("unknown result-channel fixture scenario")
		})
		if err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func usePluginResultChannelProcess(t *testing.T, a *App, scenario string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.pluginCommand = func(pluginID string) (plugins.Command, error) {
		if pluginID != "CategoryEditor" {
			return plugins.Command{}, fmt.Errorf("unexpected fixture plugin")
		}
		return plugins.Command{Path: executable, Args: []string{"-test.run=^TestPluginResultChannelFixture$", "--", "--plugin-result-channel", scenario}}, nil
	}
}

func installPluginResultChannelFixture(t *testing.T, a *App) PluginRun {
	t.Helper()
	manifest := builtin.CategoryEditorManifest()
	raw, _ := json.Marshal(manifest)
	queued, err := a.pluginStore.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}, map[string]any{"enabled": true, "content": customJapaneseMovieCategory})
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil || run.ID != queued.ID || run.Action != "init" {
		t.Fatal("actual fixture invocation not claimed", err)
	}
	return run
}

func resultChannelStartupBlocks(t *testing.T, a *App, runID string) int {
	t.Helper()
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_startup_blocks WHERE run_id=?`, runID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPluginResultChannelSideEffectBlocksStartupAndAllowsExplicitRevision(t *testing.T) {
	for _, scenario := range []struct {
		name, worker, code string
		rejectMarker       bool
	}{
		{"oversize_after_host", "oversize_after_host", "message_limit", false},
		{"exit_after_host", "exit_after_host", "worker_io_failed", false},
		{"exit_after_host_marker_rejected", "exit_after_host", "PLUGIN_RESULT_SAVE_FAILED", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			run := installPluginResultChannelFixture(t, a)
			usePluginResultChannelProcess(t, a, scenario.worker)
			wantBlocks := 1
			if scenario.rejectMarker {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER synthetic_startup_marker_failure BEFORE INSERT ON plugin_run_startup_blocks BEGIN SELECT RAISE(ABORT,'synthetic marker write failure'); END`); err != nil {
					t.Fatal(err)
				}
				// The transactional finish rolls back; its state-only fallback must
				// retain the older SAVE_FAILED startup protection without a marker.
				wantBlocks = 0
			}
			a.executePluginRun(ctx, run)
			written, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
			if err != nil || string(written) != customJapaneseMovieCategory {
				t.Fatal("fixture never crossed the actual host side-effect boundary", err)
			}
			failed, err := a.pluginStore.Run(ctx, run.ID)
			if err != nil || failed.State != "failed" || failed.ErrorCode != scenario.code || resultChannelStartupBlocks(t, a, run.ID) != wantBlocks {
				t.Fatal("uncertain result lost its error or startup block", failed.State, failed.ErrorCode, err)
			}
			if !scenario.rejectMarker && string(failed.Result) != "null" || scenario.rejectMarker && len(failed.Result) != 0 {
				t.Fatal("result bytes escaped the rejected marker transaction", len(failed.Result))
			}
			stored, err := a.pluginStore.Config(ctx, "CategoryEditor")
			if err != nil || stored.Revision != run.ConfigRevision || !bytes.Contains(stored.Value, []byte(`"enabled":true`)) {
				t.Fatal("a cleared one-shot flag masked the replay gap", stored.Revision, err)
			}
			dir := a.Dir
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err := reopened.initializeNativePlugins(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := reopened.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("startup created a replacement invocation after an actual side effect", err)
			}
			if kept, err := reopened.pluginStore.Run(ctx, run.ID); err != nil || kept.ErrorCode != scenario.code || resultChannelStartupBlocks(t, reopened, run.ID) != wantBlocks {
				t.Fatal("reopen lost the original protocol failure/block", err)
			}
			// The block belongs to the failed generation/revision, not the whole
			// installation. A reviewed explicit save must actually run a new child.
			newContent := customJapaneseMovieCategory + "\n# explicit reviewed revision\n"
			updated, next, err := reopened.pluginStore.SaveConfigAndQueue(ctx, "CategoryEditor", stored.Revision, map[string]any{"enabled": true, "content": newContent})
			if err != nil || updated.Revision <= stored.Revision {
				t.Fatal("explicit revision could not be saved", err)
			}
			claimed, err := reopened.pluginStore.ClaimRun(ctx)
			if err != nil || claimed.ID != next.ID || claimed.ConfigRevision != updated.Revision {
				t.Fatal("old result block prevented an explicit new revision", err)
			}
			usePluginResultChannelProcess(t, reopened, "write_ok")
			reopened.executePluginRun(ctx, claimed)
			completed, err := reopened.pluginStore.Run(ctx, claimed.ID)
			written, fileErr := os.ReadFile(filepath.Join(dir, "category.yaml"))
			if err != nil || completed.State != "succeeded" || string(completed.Result) != `{"written":true}` || fileErr != nil || string(written) != newContent || resultChannelStartupBlocks(t, reopened, claimed.ID) != 0 {
				t.Fatal("explicit new revision did not execute the real host write", completed.State, completed.ErrorCode, err, fileErr)
			}
		})
	}
}

func TestPluginResultChannelNoHostCallsDoesNotBlockStartup(t *testing.T) {
	for _, scenario := range []struct{ name, code string }{
		{"oversize_before_host", "message_limit"},
		{"business_failure", "plugin_operation_failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			run := installPluginResultChannelFixture(t, a)
			usePluginResultChannelProcess(t, a, scenario.name)
			a.executePluginRun(ctx, run)
			failed, err := a.pluginStore.Run(ctx, run.ID)
			if err != nil || failed.State != "failed" || failed.ErrorCode != scenario.code || resultChannelStartupBlocks(t, a, run.ID) != 0 {
				t.Fatal("failure without host calls acquired a startup block", failed.State, failed.ErrorCode, err)
			}
			if _, err := os.Lstat(filepath.Join(a.Dir, "category.yaml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("no-host fixture unexpectedly wrote a category file", err)
			}
			dir := a.Dir
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err := reopened.initializeNativePlugins(ctx); err != nil {
				t.Fatal(err)
			}
			claimed, err := reopened.pluginStore.ClaimRun(ctx)
			if err != nil || claimed.ID == run.ID || claimed.ConfigRevision != run.ConfigRevision || claimed.Action != "init" {
				t.Fatal("ordinary/no-host failure unexpectedly prevented fresh startup init", err)
			}
			usePluginResultChannelProcess(t, reopened, "write_ok")
			reopened.executePluginRun(ctx, claimed)
			completed, err := reopened.pluginStore.Run(ctx, claimed.ID)
			if err != nil || completed.State != "succeeded" || resultChannelStartupBlocks(t, reopened, claimed.ID) != 0 {
				t.Fatal("unblocked startup could not complete the actual SDK invocation", err)
			}
		})
	}
}

func TestPluginResultChannelStartupMarkerSharesFinishTransaction(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	run := installPluginResultChannelFixture(t, a)
	execution := executionLeaseActivate(t, a.pluginStore, run)
	defer run.executionLease.runCleanup()
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER synthetic_startup_marker_atomicity BEFORE INSERT ON plugin_run_startup_blocks BEGIN SELECT RAISE(ABORT,'synthetic marker insertion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.finishRun(execution, run.ID, "failed", "worker_io_failed", json.RawMessage(`null`), true); err == nil || !strings.Contains(err.Error(), "synthetic marker insertion failure") {
		t.Fatal("terminal transaction did not reach the injected marker failure", err)
	}
	current, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || current.State != "running" || current.ErrorCode != "" || len(current.Result) != 0 || resultChannelStartupBlocks(t, a, run.ID) != 0 {
		t.Fatal("marker failure did not roll back the whole terminal transaction", current.State, current.ErrorCode, err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER synthetic_startup_marker_atomicity`); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.finishRun(execution, run.ID, "failed", "worker_io_failed", json.RawMessage(`null`), true); err != nil {
		t.Fatal(err)
	}
	current, err = a.pluginStore.Run(ctx, run.ID)
	if err != nil || current.State != "failed" || current.ErrorCode != "worker_io_failed" || string(current.Result) != "null" || resultChannelStartupBlocks(t, a, run.ID) != 1 {
		t.Fatal("terminal failure and startup marker did not commit together", current.State, current.ErrorCode, err)
	}
	if err := a.pluginStore.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
}
