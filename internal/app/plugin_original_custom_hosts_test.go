package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func TestCustomHostsReleaseAdmission(t *testing.T) {
	for _, tc := range []struct {
		version string
		allowed bool
	}{{"1.0.0", true}, {"1.2.2", true}, {"1.0.1", false}} {
		t.Run(tc.version, func(t *testing.T) {
			ctx := t.Context()
			a := newTestApp(t)
			a.pluginCustomHostsTestPath = filepath.Join(t.TempDir(), "hosts")
			const initial = "127.0.0.1\tlocalhost\n"
			if err := os.WriteFile(a.pluginCustomHostsTestPath, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}

			const id = "CustomHosts"
			manifest := plugins.Manifest{SchemaVersion: 1, PluginID: id, Version: tc.version, Capabilities: []string{originalCustomHostsCapability}}
			encoded, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.pluginStore.Install(ctx, PluginInstallation{PluginID: id, InstanceID: id, Version: tc.version, ManifestSchemaVersion: 1, Manifest: encoded}); err != nil {
				t.Fatal(err)
			}
			in, err := a.pluginStore.Installation(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			config := json.RawMessage(`{"enabled":true,"hosts":"192.0.2.1 example.invalid"}`)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, in, config); err != nil {
				t.Fatal(err)
			}
			p := &originalPluginInstance{app: a, installation: in, manifest: manifest, hostContract: pluginResourcesSDKContract}
			identity := plugins.Identity{PluginID: id, InstanceID: id, Version: tc.version}
			initCtx, endInit := p.beginOriginalInitialization(ctx, true)
			defer endInit()
			initialization := originalLiveInitialization(initCtx, p)
			initialization.config = config
			request := json.RawMessage(`{"mode":"replace","lines":["192.0.2.1 example.invalid"]}`)
			assertDenied := func(phase string, payload json.RawMessage) {
				t.Helper()
				host, revoke := p.originalBaseSessionHostBound(initCtx, phase, nil, initialization)
				defer revoke()
				_, err := host(initCtx, identity, originalCustomHostsCapability, payload)
				var denied *APIError
				if !errors.As(err, &denied) || denied.Code != "PLUGIN_CAPABILITY_DENIED" {
					t.Fatalf("%s: expected denial, got %v", phase, err)
				}
			}
			assertDenied("get_form", request)
			assertDenied("init", json.RawMessage(`{"mode":"replace","lines":["192.0.2.2 example.invalid"]}`))
			if tc.allowed {
				host, revoke := p.originalBaseSessionHostBound(initCtx, "init", nil, initialization)
				defer revoke()
				raw, err := host(initCtx, identity, originalCustomHostsCapability, request)
				if err != nil || string(raw) != `{"write_failed":false,"err_hosts":[]}` {
					t.Fatalf("apply returned %s, %v", raw, err)
				}
			} else {
				assertDenied("init", request)
			}
			want := initial
			if tc.allowed {
				want += "# CustomHostsPlugin\n192.0.2.1\texample.invalid\n"
			}
			if data, err := os.ReadFile(a.pluginCustomHostsTestPath); err != nil || string(data) != want {
				t.Fatalf("owned hosts content = %q, %v, want %q", data, err, want)
			}
		})
	}
}
