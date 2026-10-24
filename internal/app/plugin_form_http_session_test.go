package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"os"
	"strings"
	"testing"
	"time"
)

// Hostile output is exercised through the real SDK and an existing compiled
// manifest, without registering any production capability or Form writer.
func TestPluginFormSessionSDKFixture(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--" || i+3 >= len(os.Args) || os.Args[i+1] != "form-session" {
			continue
		}
		manifest, _, ok := builtin.Lookup(os.Args[i+2])
		scenario := os.Args[i+3]
		if !ok {
			os.Exit(2)
		}
		err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			if method == "stop" {
				return json.RawMessage(`null`), nil
			}
			if scenario == "ignored_host" {
				_, _ = host(ctx, "sites.controls.read", json.RawMessage(`{}`))
			}
			if scenario == "illegal_write" {
				_, _ = host(ctx, "sites.controls.configure", json.RawMessage(`{}`))
			}
			if scenario == "fake_error" {
				return nil, errors.New("PLUGIN_REVISION_CONFLICT synthetic-worker-private-detail")
			}
			return json.RawMessage(`{"conf":[],"model":{"sentinel":"worker","enable":false,"file_urls":"https://worker.invalid/?key=synthetic-worker-private-detail","token":"synthetic-worker-private-detail","rss_urls":["synthetic-worker-private-detail"]},"revision":9007199254740991,"form_model_policy":{"installed":"source","preview":"source"}}`), nil
		})
		if err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func pluginFormSessionCommand(t *testing.T, pluginID, scenario string) plugins.Command {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return plugins.Command{Path: executable, Args: []string{"-test.run=^TestPluginFormSessionSDKFixture$", "--", "form-session", pluginID, scenario}}
}

func pluginFormHTTPInstallation(t *testing.T, a *App, pluginID string, config json.RawMessage) PluginInstallation {
	t.Helper()
	manifest, _, ok := builtin.Lookup(pluginID)
	if !ok {
		t.Fatal("compiled fixture missing")
	}
	installCloneSource(t, a.pluginStore, manifest)
	if config != nil {
		if _, err := a.pluginStore.SaveConfig(context.Background(), pluginID, 0, config); err != nil {
			t.Fatal(err)
		}
	}
	return lifecycleInstallation(t, a.pluginStore, pluginID)
}

func TestPluginFormSessionCanonicalTypedHostErrorThroughSDK(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"typed_wrapped", fmt.Errorf("synthetic-host-private-detail: %w", &APIError{Code: "PLUGIN_REVISION_CONFLICT", Message: "synthetic-host-private-detail", Status: 599}), "PLUGIN_REVISION_CONFLICT"},
		{"merge_invalid", &APIError{Code: "MERGE_MESSAGE_STATE_INVALID", Message: "synthetic-host-private-detail", Status: 599}, "MERGE_MESSAGE_STATE_INVALID"},
		{"merge_missing", &APIError{Code: "MERGE_MESSAGE_STATE_MISSING", Message: "synthetic-host-private-detail", Status: 599}, "MERGE_MESSAGE_STATE_MISSING"},
		{"untyped", errors.New("PLUGIN_REVISION_CONFLICT synthetic-host-private-detail"), "PLUGIN_FORM_FAILED"},
		{"unknown_typed", &APIError{Code: "SYNTHETIC_UNKNOWN", Message: "synthetic-host-private-detail", Status: 599}, "PLUGIN_FORM_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Every real App-binary startup gets its own unchanged fixture budget.
			p, ctx, in := pluginFormSessionFixture(t, builtin.MergeSiteSwitchManifest(), json.RawMessage(`{}`))
			session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
			if err != nil {
				t.Fatal(err)
			}
			result, err := (plugins.Runner{Timeout: 3 * time.Second}).Run(ctx, pluginFormSessionCommand(t, in.PluginID, "ignored_host"), builtin.MergeSiteSwitchManifest(), in.InstanceID, "config_form", nil, session.wrapHost(func(context.Context, plugins.Identity, string, json.RawMessage) (json.RawMessage, error) {
				return nil, tc.err
			}))
			if err != nil || len(result.Result) == 0 {
				t.Fatal("SDK did not exercise swallowed HOST_DENIED", err)
			}
			finalErr := session.validateFinal(ctx)
			if pluginErrorCode(finalErr) != tc.want {
				t.Fatal("typed canonical allowlist mismatch")
			}
			if finalErr != nil {
				var api *APIError
				if !errors.As(finalErr, &api) || api.Status != 400 || strings.Contains(api.Message, "synthetic-host-private-detail") {
					t.Fatal("host error details/status leaked")
				}
				if raw, _ := json.Marshal(api); bytes.Contains(raw, []byte("synthetic-host-private-detail")) {
					t.Fatal("host error serialized private details")
				}
			}
		})
	}
}

func TestPluginFormPolicySeparatesPreviewAndInstalledWithoutWriteGrant(t *testing.T) {
	manifest := builtin.SystemProcessManifest()
	manifest.FormModelPolicy = &plugins.FormModelPolicy{Installed: plugins.FormModelSource, Preview: plugins.FormModelHostOverlay}
	for _, mode := range []pluginFormMode{pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		if !pluginFormSourceModel(manifest, mode) {
			t.Fatal("installed policy depended on warm/cold inference")
		}
	}
	if pluginFormSourceModel(manifest, pluginFormPreview) || manifest.ConfigFormProjection != "" {
		t.Fatal("presentation granted writes or bypassed preview overlay")
	}
	manifest.FormModelPolicy = nil
	if pluginFormSourceModel(manifest, pluginFormPreview) || pluginFormSourceModel(manifest, pluginFormInstalledCold) {
		t.Fatal("legacy nil policy no longer overlays")
	}
	merge := builtin.MergeSiteSwitchManifest()
	if !pluginFormSourceModel(merge, pluginFormPreview) || !pluginFormSourceModel(merge, pluginFormInstalledUnknown) || merge.ConfigFormProjection != "" {
		t.Fatal("compiled source/source declaration coupled to writes")
	}
}

func TestPluginFormSessionUninstallDisplayIsNotDurableAuthority(t *testing.T) {
	manifest, _, known := builtin.Lookup("PluginUnInstall")
	if !known {
		t.Fatal("compiled uninstall fixture missing")
	}
	p, ctx, in := pluginFormSessionFixture(t, manifest, json.RawMessage(`{"plugin_ids":["SystemProcess"]}`))
	before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	p.cacheUninstallConfig(in.InstanceID, before.Revision, in.Generation, json.RawMessage(`{"plugin_ids":[]}`))
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil {
		t.Fatal("display sandwich re-entered SQL or lost cache", err)
	}
	assertPluginFormSessionValue(t, session.durable, before)
	if string(session.display.Value) != `{"plugin_ids":[]}` || session.display.Revision != before.Revision {
		t.Fatal("legacy uninstall display overlay lost")
	}
	// This is the intentionally retained memory-only boundary, NOT a cache epoch.
	p.cacheUninstallConfig(in.InstanceID, before.Revision, in.Generation, json.RawMessage(`{"plugin_ids":["CategoryEditor"]}`))
	if err = session.validateFinal(ctx); err != nil || string(session.display.Value) != `{"plugin_ids":[]}` {
		t.Fatal("same-generation/revision cache change was misrepresented", err)
	}
	assertPluginFormDurableConfigUnchanged(t, p, ctx, in.InstanceID, before, sealed)
}

// Config() may intentionally apply an in-memory display projection. A durable
// assertion must read the encrypted storage value directly, not that projection.
func assertPluginFormDurableConfigUnchanged(t *testing.T, p *PluginStore, ctx context.Context, instance string, before PluginStoredValue, beforeSealed []byte) {
	t.Helper()
	after, err := p.readValue(ctx, instance, "config", "main")
	if err != nil {
		t.Fatal(err)
	}
	assertPluginFormSessionValue(t, after, before)
	var sealed []byte
	if err = p.store.DB.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, instance).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sealed, beforeSealed) {
		t.Fatal("Form changed durable sealed config")
	}
}

func TestPluginFormAuditedCompiledHandlersPropagateHostFailure(t *testing.T) {
	// This bounded list documents the current catalog audit. A new compiled
	// adapter must separately decide whether it has an explicit read fallback.
	hostForms := []string{"TorrentTransfer", "TorrentTransfermusic", "AutoBackup", "IYUUAutoSeed", "CleanInvalidSeed", "AutoSignIn", "CategoryEditor", "MergeSiteSwitch", "MediaServerRefresh", "PluginUnInstall"}
	localForms := []string{"CloudflareSpeedTest", "BrushFlowLanduo", "SeedRescue", "ScrapeFileClean", "RemoveLink", "MPServerStatus", "CustomHosts", "SystemProcess", "SiteStatistic", "SubscribeGroup", "RemoteIdentifiers"}
	for _, group := range []struct {
		ids   []string
		calls int
	}{{hostForms, 1}, {localForms, 0}} {
		for _, pluginID := range group.ids {
			t.Run(pluginID, func(t *testing.T) {
				_, handle, known := builtin.Lookup(pluginID)
				if !known {
					t.Fatal("audited compiled handler missing")
				}
				failure := errors.New("synthetic read failure")
				calls := 0
				_, err := handle(context.Background(), "config_form", nil, func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, failure })
				if calls != group.calls || (group.calls == 1 && !errors.Is(err, failure)) || (group.calls == 0 && err != nil) {
					t.Fatal("compiled Form acquired or hid a host-error fallback", calls, err)
				}
			})
		}
	}
}
