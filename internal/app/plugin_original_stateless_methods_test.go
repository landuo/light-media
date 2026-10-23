package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// These are getter unit tests with actual builtin objects and synthetic hosts.
// They do not claim App lifecycle capture; the p.init regression below does.
func originalStatelessUnitInit(t *testing.T, object interface {
	Handle(context.Context, string, json.RawMessage, plugins.HostCaller) (json.RawMessage, error)
}, config string, fail string) {
	t.Helper()
	sentinel := errors.New("synthetic host failure")
	host := func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == fail {
			return nil, sentinel
		}
		return json.RawMessage(`{}`), nil
	}
	raw, err := json.Marshal(plugins.Invocation{Action: "init", Config: json.RawMessage(config)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = object.Handle(context.Background(), "invoke", raw, host)
	if fail == "" && err != nil || fail != "" && !errors.Is(err, sentinel) {
		t.Fatalf("init err=%v", err)
	}
}

func TestOriginalStatelessRetainedMembers(t *testing.T) {
	for _, tc := range []struct {
		id     string
		config string
		fail   string
		want   bool
	}{
		{"CategoryEditor", `{"enabled":true,"content":"synthetic"}`, "category.write", true},
		{"CategoryEditor", `{"enabled":true,"content":"synthetic"}`, "config.patch", false},
		{"MediaServerRefresh", `{"enabled":true}`, "", true},
		{"SiteStatistic", `{"enabled":false,"onlyonce":true}`, "", false},
		{"SiteStatistic", `{"enabled":true,"onlyonce":true}`, "", true},
	} {
		t.Run(tc.id, func(t *testing.T) {
			var native interface {
				Handle(context.Context, string, json.RawMessage, plugins.HostCaller) (json.RawMessage, error)
			}
			switch tc.id {
			case "CategoryEditor":
				native = builtin.NewOriginalCategoryEditor()
			case "MediaServerRefresh":
				native = builtin.NewOriginalMediaServerRefresh()
			case "SiteStatistic":
				native = builtin.NewOriginalSiteStatistic()
			}
			originalStatelessUnitInit(t, native, tc.config, tc.fail)
			p := &originalPluginInstance{installation: PluginInstallation{PluginID: tc.id}, nativeState: native, appliedConfig: json.RawMessage(`{"enabled":false}`)}
			got, handled, err := originalStatelessState(context.Background(), p)
			if err != nil || !handled || got != tc.want {
				t.Fatalf("state=%v handled=%v err=%v", got, handled, err)
			}
			services, handled, err := originalStatelessServices(context.Background(), p)
			if services != nil || !handled || err != nil {
				t.Fatal("public services changed")
			}
			commands, handled, err := originalStatelessCommands(context.Background(), p)
			if commands != nil || !handled || err != nil {
				t.Fatal("commands changed")
			}
		})
	}
}

func TestOriginalStatelessMissingCaptureNeverDecodesConfig(t *testing.T) {
	for _, id := range []string{"CategoryEditor", "MediaServerRefresh", "SiteStatistic", "MergeSiteSwitch"} {
		p := &originalPluginInstance{installation: PluginInstallation{PluginID: id}, appliedConfig: json.RawMessage(`{"enabled":true,"search_follow_enable_sites":true}`)}
		if _, handled, err := originalStatelessState(context.Background(), p); !handled || err == nil {
			t.Fatalf("%s silently reconstructed missing capture", id)
		}
	}
	p := &originalPluginInstance{installation: PluginInstallation{PluginID: "Unknown"}}
	if _, handled, err := originalStatelessState(context.Background(), p); handled || err != nil {
		t.Fatal("claimed unknown state")
	}
	if _, handled, _ := originalStatelessServices(context.Background(), p); handled {
		t.Fatal("claimed unknown services")
	}
	if _, handled, _ := originalStatelessCommands(context.Background(), p); handled {
		t.Fatal("claimed unknown commands")
	}
}

func TestOriginalStatelessMergeLiveDependencies(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	native := builtin.NewOriginalMergeSiteSwitch()
	originalStatelessUnitInit(t, native, `{"signin_follow_enable_sites":true}`, "")
	p := &originalPluginInstance{app: a, installation: PluginInstallation{PluginID: "MergeSiteSwitch"}, nativeState: native}
	check := func(want bool) {
		t.Helper()
		got, handled, err := originalStatelessState(ctx, p)
		if err != nil || !handled || got != want {
			t.Fatalf("state=%v handled=%v err=%v want=%v", got, handled, err, want)
		}
	}
	check(false)
	// Install only a declaration. Never invoke a business plugin.
	uninstallInstall(t, a, "stateless-dependency", plugins.Manifest{SchemaVersion: 1, PluginID: "AutoSignIn", Version: "test", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1}, map[string]any{}, false)
	check(true)
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE plugin_installations SET desired_enabled=0 WHERE instance_id=?`, "stateless-dependency"); err != nil {
		t.Fatal(err)
	}
	check(true) // installed, not enabled, is the source dependency condition
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE plugin_installations SET installed=0 WHERE instance_id=?`, "stateless-dependency"); err != nil {
		t.Fatal(err)
	}
	check(false)
	originalStatelessUnitInit(t, native, `null`, "") // original init(None) replaces Merge config
	check(false)
	originalStatelessUnitInit(t, native, `{"search_follow_enable_sites":true}`, "")
	check(true)
}

func TestOriginalStatelessMergeOriginalDependencyNames(t *testing.T) {
	a := newTestApp(t)
	for _, id := range []string{"CrossSeed", "BrushFlowLanduo"} {
		manifest := plugins.Manifest{SchemaVersion: 1, PluginID: id, Version: "1", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1}
		if id == "BrushFlowLanduo" {
			var ok bool
			manifest, _, ok = builtin.Lookup(id)
			if !ok {
				t.Fatal("actual BrushFlowLanduo manifest missing")
			}
		}
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		// Only create a legitimate installed declaration and its native install
		// genesis. No config save, runtime initialization, fake receipt or task.
		if err = a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: "stateless-" + id, PluginID: id, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw, DesiredEnabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	native := builtin.NewOriginalMergeSiteSwitch()
	originalStatelessUnitInit(t, native, `{"cross_seed_follow_enable_sites":true,"brush_flow_follow_enable_sites":true}`, "")
	p := &originalPluginInstance{app: a, installation: PluginInstallation{PluginID: "MergeSiteSwitch"}, nativeState: native}
	if got, _, err := originalStatelessState(context.Background(), p); err != nil || got {
		t.Fatalf("invented alias/crossseed state=%v err=%v", got, err)
	}
}

// Prepared integration regression: this must fail until the owner captures the
// member during actual init. It is not a fixture that injects nativeState.
func TestOriginalStatelessInitializationCapturesMediaState(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	uninstallInstall(t, a, "stateless-media-capture", builtin.MediaServerRefreshManifest(), builtin.MediaServerRefreshConfiguration{Enabled: true, Delay: json.RawMessage(`0`)}, false)
	in, err := a.pluginStore.Installation(context.Background(), "stateless-media-capture")
	if err != nil {
		t.Fatal(err)
	}
	p := &originalPluginInstance{app: a, installation: in}
	if err = p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, handled, err := originalStatelessState(context.Background(), p); err != nil || !handled || !got {
		t.Fatalf("actual init did not capture enabled member: state=%v handled=%v err=%v", got, handled, err)
	}
}

func TestOriginalStatelessRejectsTypedNilAndLegacySnapshot(t *testing.T) {
	for _, tc := range []struct {
		id     string
		native any
	}{
		{"CategoryEditor", (*builtin.OriginalCategoryEditor)(nil)},
		{"MediaServerRefresh", (*builtin.OriginalMediaServerRefresh)(nil)},
		{"SiteStatistic", (*builtin.OriginalSiteStatistic)(nil)},
		{"MergeSiteSwitch", (*builtin.OriginalMergeSiteSwitch)(nil)},
		{"MediaServerRefresh", builtin.MediaServerRefreshConfiguration{Enabled: true}},
		{"SiteStatistic", builtin.SiteStatisticConfiguration{Enabled: true}},
		{"MergeSiteSwitch", builtin.SiteControls{"search_follow_enable_sites": json.RawMessage(`true`)}},
	} {
		p := &originalPluginInstance{installation: PluginInstallation{PluginID: tc.id}, nativeState: tc.native}
		if _, handled, err := originalStatelessState(context.Background(), p); !handled || err == nil {
			t.Fatalf("%s accepted non-object state", tc.id)
		}
	}
}
