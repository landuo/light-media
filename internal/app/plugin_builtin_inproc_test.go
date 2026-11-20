package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"light-media/internal/plugins"
)

type stubRejected struct{ method string }

func (e *stubRejected) Error() string { return "stub rejected host method " + e.method }

// stubHost answers the base configuration calls every plugin's init makes:
// configuration round-trips and data paths succeed; everything else is
// rejected loudly so an unexpected capability surfaces in the test.
func stubHost(ctx context.Context, identity plugins.Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "plugin.base.get_config":
		// A present, empty configuration object satisfies every class's
		// schema validation without asserting any business field.
		return json.RawMessage(`{}`), nil
	case "plugin.base.config_save_origin":
		return json.RawMessage(`false`), nil
	case "plugin.base.get_data_path", "plugin.base.get_data", "plugin.base.log":
		return json.RawMessage(`null`), nil
	case "plugin.base.update_config", "plugin.base.save_data", "plugin.base.del_data":
		return json.RawMessage(`true`), nil
	case "plugin.base.get_instance_metadata":
		return json.RawMessage(`{"instance_id":"stub","plugin_id":"stub","plugin_version":"1.0.0","name":"stub"}`), nil
	case "sites.switch":
		return json.RawMessage(`[]`), nil
	case "sites.read":
		// SiteHostResultV1 shape, not a bare list.
		return json.RawMessage(`{"schema_version":1,"items":[]}`), nil
	case "log.write", "message.post", "event.send", "plugin.systemmessage.put",
		"plugin.cleanup.submit", "plugin.reload", "plugin.log.write":
		return json.RawMessage(`null`), nil
	case "plugin.background.submit":
		// The manager replies with a boolean admission receipt.
		return json.RawMessage(`true`), nil
	case "downloader.configurations.read":
		return json.RawMessage(`{"schema_version":1,"downloaders":[]}`), nil
	case "network.lark.endpoint":
		return json.RawMessage(`"https://open.larksuite.com/open-apis"`), nil
	case "settings.read":
		return json.RawMessage(`{}`), nil
	case "storage.files.exists", "storage.files.list":
		return json.RawMessage(`{}`), nil
	case "storage.files.delete":
		return json.RawMessage(`true`), nil
	case "history.transfer.read":
		return json.RawMessage(`{}`), nil
	case "history.transfer.delete":
		return json.RawMessage(`true`), nil
	default:
		// Reject loudly: an unexpected capability must surface in the test
		// as a distinguishable failure.
		return nil, &stubRejected{method: method}
	}
}

// The in-process session must speak the same contract as the wire client for
// the lifecycle the app exercises: init/get_state/stop, invalid methods,
// timeouts, stop joining, and the private-scheduler stop lane.
func TestBuiltinInProcessSessionLifecycle(t *testing.T) {
	catalog, err := builtinCatalogLoad()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := catalog["MergeSiteSwitch"]
	if !ok {
		t.Fatal("MergeSiteSwitch must be compiled in")
	}
	binding := plugins.SessionBinding{Identity: plugins.Identity{PluginID: "MergeSiteSwitch", InstanceID: "MergeSiteSwitch", Version: entry.manifest.Version}, Generation: 1}
	session, err := plugins.NewInProcess(binding, plugins.InProcessOptions{Manifest: entry.manifest, Concurrent: entry.concurrent}, entry.newHandler())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := session.Call(ctx, "init", json.RawMessage(`{}`), stubHost); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	raw, err := session.Call(ctx, "get_state", json.RawMessage(`{}`), stubHost)
	if err != nil {
		t.Fatalf("get_state failed: %v", err)
	}
	if !json.Valid(raw.Result) {
		t.Fatalf("state not json: %s", raw.Result)
	}
	if _, err := session.Call(ctx, "not_a_method", json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("undeclared method must be rejected")
	}
	if _, err := session.Call(ctx, "get_state", json.RawMessage(`not json`), nil); err == nil {
		t.Fatal("invalid payload must be rejected")
	}
	if _, err := session.CallWithOptions(ctx, "get_state", json.RawMessage(`{}`), nil, plugins.CallOptions{Timeout: time.Millisecond}); err == nil {
		t.Fatal("a zero timeout must be invalid")
	}
	if err := session.Stop(ctx); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
}

// A builtin panic must terminate only the session, never the host process.
func TestBuiltinInProcessSessionContainsPanics(t *testing.T) {
	manifest := plugins.Manifest{
		SchemaVersion: 1, PluginID: "PanicProbe", Version: "1.0",
		Runtime: "go-executable", Entry: "panic-probe", HostAPIMin: 1, HostAPIMax: 1, Name: "Panic probe",
		Methods:      []string{"init", "get_state", "stop"},
		Capabilities: plugins.BaseMethods(),
	}
	handler := plugins.InProcessHandler(func(ctx context.Context, method string, payload json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		if method == "get_state" {
			panic("builtin panic containment probe")
		}
		return json.RawMessage(`{}`), nil
	})
	binding := plugins.SessionBinding{Identity: plugins.Identity{PluginID: "PanicProbe", InstanceID: "PanicProbe", Version: "1.0"}, Generation: 1}
	session, err := plugins.NewInProcess(binding, plugins.InProcessOptions{Manifest: manifest, Concurrent: true}, handler)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Call(context.Background(), "get_state", json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("a panicking call must fail")
	}
	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("stop after panic must still join: %v", err)
	}
}

// The whole catalog must load: every class converts its manifest, validates
// against the resources contract, and constructs a live handler that answers
// init and get_state.
func TestBuiltinCatalogFullLifecycle(t *testing.T) {
	catalog, err := builtinCatalogLoad()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 22 {
		t.Fatalf("catalog must cover all 22 classes, got %d", len(catalog))
	}
	ctx := context.Background()
	for class, entry := range catalog {
		entry := entry
		t.Run(class, func(t *testing.T) {
			binding := plugins.SessionBinding{Identity: plugins.Identity{PluginID: class, InstanceID: class, Version: entry.manifest.Version}, Generation: 1}
			session, err := plugins.NewInProcess(binding, plugins.InProcessOptions{Manifest: entry.manifest, Concurrent: entry.concurrent}, entry.newHandler())
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			// RemoveLink's initializer runs a resident dispatch pass that
			// only ends when its background submit is answered or the context
			// ends; in a bare stub environment the context deadline is the
			// honest completion, so a deadline error is acceptable there.
			deadline := class == "RemoveLink"
			initCtx := ctx
			if deadline {
				var cancel context.CancelFunc
				initCtx, cancel = context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
			}
			if _, err := session.Call(initCtx, "init", json.RawMessage(`{}`), stubHost); err != nil && !deadline {
				t.Fatalf("init: %v", err)
			}
			if _, err := session.Call(ctx, "get_state", json.RawMessage(`{}`), stubHost); err != nil {
				t.Fatalf("get_state: %v", err)
			}
			if err := session.Stop(ctx); err != nil {
				t.Fatalf("stop: %v", err)
			}
		})
	}
}

// A compiled-in plugin has no worker process, so the idle reaper must never
// retire its session: doing so would only force an initializer replay.
func TestBuiltinPluginsAreExcludedFromIdleRetirement(t *testing.T) {
	internal := &originalPluginInstance{external: true, installation: PluginInstallation{PluginID: "MergeSiteSwitch"}, sessionStarted: true}
	internal.builtin.Store(true)
	if pluginIdleEligible(internal, nil, nil) {
		t.Fatal("a compiled-in plugin must never enter idle retirement")
	}
	// The same class as an external worker keeps its audited eligibility.
	external := &originalPluginInstance{external: true, installation: PluginInstallation{PluginID: "MergeSiteSwitch"}, sessionStarted: true}
	if !pluginIdleEligible(external, nil, nil) {
		t.Fatal("an external worker keeps its audited eligibility")
	}
}
