package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// Actual App lifecycle tests. No App.Start, scheduler pump, business event,
// network, substituted HostHandler, fabricated receipt or configuration cipher.
func originalBuiltinIntegrationObject(t *testing.T, a *App, instance string, manifest plugins.Manifest) *originalPluginInstance {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: instance, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw, DesiredEnabled: true}); err != nil {
		t.Fatal(err)
	}
	in, err := a.pluginStore.Installation(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	return &originalPluginInstance{app: a, installation: in}
}

func originalBuiltinIntegrationSave(t *testing.T, p *originalPluginInstance, config any) {
	t.Helper()
	stored, err := p.app.pluginStore.Config(context.Background(), p.installation.InstanceID)
	revision := int64(0)
	if err == nil {
		revision = stored.Revision
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err = p.app.pluginStore.SaveConfig(context.Background(), p.installation.InstanceID, revision, config); err != nil {
		t.Fatal(err)
	}
}

func originalBuiltinIntegrationState(t *testing.T, p *originalPluginInstance, want bool) {
	t.Helper()
	got, err := p.getState(context.Background())
	if err != nil || got != want {
		t.Fatalf("actual get_state=%v err=%v want=%v", got, err, want)
	}
}

func TestOriginalBuiltinAppNoneAndEmptyInitRetainActualObjects(t *testing.T) {
	for _, manifest := range []plugins.Manifest{builtin.CategoryEditorManifest(), builtin.MediaServerRefreshManifest(), builtin.SiteStatisticManifest(), builtin.MergeSiteSwitchManifest()} {
		t.Run(manifest.PluginID, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			p := originalBuiltinIntegrationObject(t, a, "native-none-"+manifest.PluginID, manifest)
			// Missing config takes the existing original queue's null/revision-zero
			// route. No synthetic null is written to the object-only config API.
			if err := p.init(context.Background()); err != nil {
				t.Fatal("initial None", err)
			}
			originalBuiltinIntegrationState(t, p, false)
			before := p.nativeState
			if before == nil {
				t.Fatal("actual native object missing")
			}
			if err := p.init(context.Background()); err != nil {
				t.Fatal("repeated None", err)
			}
			if p.nativeState != before {
				t.Fatal("None re-init replaced actual object")
			}
			originalBuiltinIntegrationSave(t, p, map[string]any{})
			if err := p.init(context.Background()); err != nil {
				t.Fatal("empty dict", err)
			}
			if p.nativeState != before {
				t.Fatal("empty init replaced actual object")
			}
			originalBuiltinIntegrationState(t, p, false)
			var count int
			if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=? AND action='init'`, p.installation.InstanceID).Scan(&count); err != nil || count != 3 {
				t.Fatalf("init runs=%d err=%v", count, err)
			}
		})
	}
}

func TestOriginalBuiltinAppMediaEmptyInitKeepsMembersAndIsolation(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	p := originalBuiltinIntegrationObject(t, a, "native-media-retained", builtin.MediaServerRefreshManifest())
	originalBuiltinIntegrationSave(t, p, builtin.MediaServerRefreshConfiguration{Enabled: true, Delay: json.RawMessage(`7`), MediaServers: []string{}})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	native, ok := p.nativeState.(*builtin.OriginalMediaServerRefresh)
	if !ok {
		t.Fatalf("wrong actual object %T", p.nativeState)
	}
	originalBuiltinIntegrationSave(t, p, map[string]any{})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.nativeState != native || !native.State() || string(native.Configuration().Delay) != "7" {
		t.Fatal("empty config erased initialized members")
	}
	originalBuiltinIntegrationState(t, p, true)
	sibling := originalBuiltinIntegrationObject(t, a, "native-media-sibling", builtin.MediaServerRefreshManifest())
	if err := sibling.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	originalBuiltinIntegrationState(t, sibling, false)
	if sibling.nativeState == native {
		t.Fatal("source and sibling share object")
	}
	// State reads never enqueue or replay events.configure.
	var before, after int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, p.installation.InstanceID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		originalBuiltinIntegrationState(t, p, true)
	}
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, p.installation.InstanceID).Scan(&after); err != nil || after != before {
		t.Fatal("getter replayed initialization", err)
	}
}

func TestOriginalBuiltinAppCategoryHostFailureRetainsMember(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	p := originalBuiltinIntegrationObject(t, a, "native-category-error", builtin.CategoryEditorManifest())
	originalBuiltinIntegrationSave(t, p, map[string]any{"enabled": false})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := p.nativeState
	// Use the existing real store failure boundary, not a fake HostHandler.
	// Malformed YAML is NOT a reliable failure injection: original reload can
	// retain/fallback maps. Refuse the owned notification insert instead.
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER original_builtin_owned_notice_failure BEFORE INSERT ON records WHEN NEW.kind='notification' BEGIN SELECT RAISE(ABORT,'owned actual builtin notification failure'); END`); err != nil {
		t.Fatal(err)
	}
	content := defaultMediaCategoryPolicy
	originalBuiltinIntegrationSave(t, p, map[string]any{"enabled": true, "content": content})
	if err := p.init(context.Background()); err == nil {
		t.Fatal("owned notification failure unexpectedly accepted")
	}
	written, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
	if err != nil || string(written) != content {
		t.Fatalf("real category write did not precede notification failure: %v", err)
	}
	if p.nativeState != before {
		t.Fatal("host failure replaced actual object")
	}
	originalBuiltinIntegrationState(t, p, true)
	stored, err := a.pluginStore.Config(context.Background(), p.installation.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Enabled bool `json:"enabled"`
	}
	if err = json.Unmarshal(stored.Value, &saved); err != nil || !saved.Enabled {
		t.Fatal("failed notification must precede enabled reset/persistence", err)
	}
	originalBuiltinIntegrationSave(t, p, map[string]any{})
	if err := p.init(context.Background()); err != nil {
		t.Fatal("empty config replayed failed notification", err)
	}
	if p.nativeState != before {
		t.Fatal("empty config replaced failed object")
	}
	originalBuiltinIntegrationState(t, p, true)
}

func TestOriginalBuiltinAppSiteOnceMemberDiffersFromPersistedConfig(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	p := originalBuiltinIntegrationObject(t, a, "native-statistic-once", builtin.SiteStatisticManifest())
	originalBuiltinIntegrationSave(t, p, builtin.SiteStatisticConfiguration{Enabled: true, OnlyOnce: true, DashboardType: "today"})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	native, ok := p.nativeState.(*builtin.OriginalSiteStatistic)
	if !ok || !native.Configuration().OnlyOnce {
		t.Fatalf("actual once member missing: %T", p.nativeState)
	}
	stored, err := a.pluginStore.Config(context.Background(), p.installation.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var saved builtin.SiteStatisticConfiguration
	if err = json.Unmarshal(stored.Value, &saved); err != nil || saved.OnlyOnce {
		t.Fatal("persisted onlyonce not reset", err)
	}
	originalBuiltinIntegrationSave(t, p, map[string]any{})
	if err := p.init(context.Background()); err != nil {
		t.Fatal("empty dict original once reuse", err)
	}
	if p.nativeState != native || !native.Configuration().OnlyOnce {
		t.Fatal("persisted reset overwrote member")
	}
	originalBuiltinIntegrationState(t, p, true)
	var private, public int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_schedules WHERE instance_id=? AND group_key='once' AND active=1`, p.installation.InstanceID).Scan(&private); err != nil || private != 1 {
		t.Fatalf("private schedules=%d err=%v", private, err)
	}
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_schedules WHERE instance_id=? AND group_key='original_services' AND active=1`, p.installation.InstanceID).Scan(&public); err != nil || public != 0 {
		t.Fatalf("private once leaked public=%d err=%v", public, err)
	}
	services, handled, err := originalStatelessServices(context.Background(), p)
	if err != nil || !handled || services != nil {
		t.Fatal("original public service is not nil")
	}
	// Source stop_service is pass. No cleanup expectation or scheduler pump.
}

func TestOriginalBuiltinAppMediaRealHostErrorRetainsMembers(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	p := originalBuiltinIntegrationObject(t, a, "native-media-host-error", builtin.MediaServerRefreshManifest())
	originalBuiltinIntegrationSave(t, p, builtin.MediaServerRefreshConfiguration{Delay: json.RawMessage(`0`)})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := p.nativeState
	// This reference is syntactically legal configuration but the existing real
	// media host rejects its unmapped identity before any network operation.
	originalBuiltinIntegrationSave(t, p, builtin.MediaServerRefreshConfiguration{Enabled: true, Delay: json.RawMessage(`0`), MediaServers: []string{"synthetic-unmapped"}})
	if err := p.init(context.Background()); err == nil {
		t.Fatal("unmapped media host unexpectedly accepted")
	}
	if p.nativeState != before {
		t.Fatal("host error replaced actual media object")
	}
	originalBuiltinIntegrationState(t, p, true)
	native, ok := p.nativeState.(*builtin.OriginalMediaServerRefresh)
	if !ok || len(native.Configuration().MediaServers) != 1 || native.Configuration().MediaServers[0] != "synthetic-unmapped" {
		t.Fatal("partial host failure lost assigned member")
	}
}
