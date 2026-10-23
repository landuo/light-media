package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"light-media/internal/plugins/builtin"
)

// Original MoviePilot 8a83efe: app/api/endpoints/plugin.py:992-1030.
// Ordinary uninstall retains configuration/data. Clone uninstall deletes its
// own configuration/data and permits recreating that same clone identifier.
// Executable original reference: plugin-module-parity-review-20261023/oracle-v2.
func TestPluginOriginalOrdinaryUninstallRetainsConfigAndData(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	p := a.pluginStore
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	before, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enabled": false, "marker": "source-config", "zero": 0})
	if err != nil {
		t.Fatal(err)
	}
	data, err := p.SaveData(ctx, m.PluginID, "history", 0, []string{"source-only"})
	if err != nil {
		t.Fatal(err)
	}
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+m.PluginID+"/uninstall", `{}`, cookie, "")
	if r.Code != 200 {
		t.Fatalf("uninstall: %d %s", r.Code, r.Body.String())
	}
	var body struct {
		Data struct {
			Uninstalled bool  `json:"uninstalled"`
			Retained    *bool `json:"data_retained"`
		} `json:"data"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &body); err != nil || !body.Data.Uninstalled || body.Data.Retained == nil || !*body.Data.Retained {
		t.Fatalf("ordinary retention response: %s (%v)", r.Body.String(), err)
	}
	after, err := p.Config(ctx, m.PluginID)
	if err != nil || !bytes.Equal(before.Value, after.Value) || before.Revision != after.Revision {
		t.Fatalf("ordinary config not retained: %v", err)
	}
	afterData, err := p.Data(ctx, m.PluginID, "history")
	if err != nil || !bytes.Equal(data.Value, afterData.Value) || data.Revision != afterData.Revision {
		t.Fatalf("ordinary data not retained: %v", err)
	}
	in, err := p.Installation(ctx, m.PluginID)
	if err != nil || in.Installed {
		t.Fatalf("ordinary still installed: %+v %v", in, err)
	}
	if _, err = p.InstallAndQueue(ctx, in, map[string]any{"enabled": false}); err != nil {
		t.Fatal("reinstall", err)
	}
	again, err := p.Config(ctx, m.PluginID)
	if err != nil || !bytes.Equal(before.Value, again.Value) {
		t.Fatalf("reinstall replaced retained config: %v", err)
	}
	againData, err := p.Data(ctx, m.PluginID, "history")
	if err != nil || !bytes.Equal(data.Value, againData.Value) {
		t.Fatalf("reinstall lost retained data: %v", err)
	}
}

func TestPluginOriginalCloneUninstallClearsOnlyCloneAndAllowsSameID(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	p := a.pluginStore
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	source, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enabled": false, "marker": "source-config"})
	if err != nil {
		t.Fatal(err)
	}
	sourceData, err := p.SaveData(ctx, m.PluginID, "history", 0, []string{"source-only"})
	if err != nil {
		t.Fatal(err)
	}
	request := PluginCloneRequest{Suffix: "ParityOwned"}
	clone, err := p.Clone(ctx, m.PluginID, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveData(ctx, clone.InstanceID, "history", 0, []string{"old-clone-only"}); err != nil {
		t.Fatal(err)
	}
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/uninstall", `{}`, cookie, "")
	if r.Code != 200 {
		t.Fatalf("clone uninstall: %d %s", r.Code, r.Body.String())
	}
	var body struct {
		Data struct {
			Uninstalled bool  `json:"uninstalled"`
			Retained    *bool `json:"data_retained"`
		} `json:"data"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &body); err != nil || !body.Data.Uninstalled {
		t.Fatalf("clone uninstall response: %s (%v)", r.Body.String(), err)
	}
	// Record all independent semantic mismatches, rather than stop at response.
	if body.Data.Retained == nil || *body.Data.Retained {
		t.Errorf("clone API must truthfully report data_retained=false: %s", r.Body.String())
	}
	if _, err = p.Config(ctx, clone.InstanceID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("clone configuration must be removed, got %v", err)
	}
	if _, err = p.Data(ctx, clone.InstanceID, "history"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("clone history must be removed, got %v", err)
	}
	current, err := p.Config(ctx, m.PluginID)
	if err != nil || !bytes.Equal(source.Value, current.Value) || source.Revision != current.Revision {
		t.Errorf("source config changed: %v", err)
	}
	currentData, err := p.Data(ctx, m.PluginID, "history")
	if err != nil || !bytes.Equal(sourceData.Value, currentData.Value) || sourceData.Revision != currentData.Revision {
		t.Errorf("source history changed: %v", err)
	}
	recreated, err := p.Clone(ctx, m.PluginID, request)
	if err != nil {
		t.Fatalf("original clone uninstall permits clean same-ID recreation: %v", err)
	}
	if recreated.InstanceID != clone.InstanceID {
		t.Fatalf("recreated another ID: %s", recreated.InstanceID)
	}
	if _, err = p.Data(ctx, recreated.InstanceID, "history"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("recreated clone resurrected old history: %v", err)
	}
	fresh, err := p.Config(ctx, recreated.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var conf map[string]any
	if err = json.Unmarshal(fresh.Value, &conf); err != nil || conf["marker"] != "source-config" || conf["enabled"] != false {
		t.Errorf("recreated clone config: %s (%v)", fresh.Value, err)
	}
}

// Original reset deletes only this instance's configuration/data before reload.
// Active cancellation/join ordering and the separate HTTP read-after-reset root
// remain in the common owner's existing tests; this test does not fake events.
func TestPluginOriginalResetClearsOnlyTargetConfigAndData(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	p := a.pluginStore
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	source, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enabled": false, "marker": "source-config"})
	if err != nil {
		t.Fatal(err)
	}
	sourceData, err := p.SaveData(ctx, m.PluginID, "history", 0, []string{"source-only"})
	if err != nil {
		t.Fatal(err)
	}
	clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "ResetParity"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveData(ctx, clone.InstanceID, "history", 0, []string{"clone-only"}); err != nil {
		t.Fatal(err)
	}
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/reset", `{}`, cookie, "")
	if r.Code != 200 {
		t.Fatalf("reset: %d %s", r.Code, r.Body.String())
	}
	var body struct {
		Data struct {
			Reset bool `json:"reset"`
		} `json:"data"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &body); err != nil || !body.Data.Reset {
		t.Fatalf("reset response: %s (%v)", r.Body.String(), err)
	}
	if _, err = p.Config(ctx, clone.InstanceID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("reset retains old target configuration: %v", err)
	}
	if _, err = p.Data(ctx, clone.InstanceID, "history"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("reset retains old target history: %v", err)
	}
	current, err := p.Config(ctx, m.PluginID)
	if err != nil || !bytes.Equal(source.Value, current.Value) || source.Revision != current.Revision {
		t.Errorf("reset changed source config: %v", err)
	}
	currentData, err := p.Data(ctx, m.PluginID, "history")
	if err != nil || !bytes.Equal(sourceData.Value, currentData.Value) || sourceData.Revision != currentData.Revision {
		t.Errorf("reset changed source data: %v", err)
	}
	in, err := p.Installation(ctx, clone.InstanceID)
	if err != nil || !in.Installed {
		t.Errorf("reset must retain installed clone: %+v %v", in, err)
	}
}
