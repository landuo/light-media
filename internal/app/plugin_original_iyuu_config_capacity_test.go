package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalIYUUFullConfigCapacity(t *testing.T) {
	a := newTestApp(t)
	ctx := t.Context()
	p := a.pluginStore
	install := func(instance, pluginID, version string) PluginInstallation {
		t.Helper()
		manifest := plugins.Manifest{
			SchemaVersion: 1, PluginID: pluginID, Version: version, Name: "Configuration capacity fixture",
			Runtime: "go-executable", Entry: "owned-worker", HostAPIMin: plugins.HostAPIVersion,
			HostAPIMax: plugins.HostAPIVersion, Methods: []string{"init_plugin", "stop"},
		}
		if err := manifest.Validate(); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Install(ctx, PluginInstallation{InstanceID: instance, PluginID: pluginID, Version: version, ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
			t.Fatal(err)
		}
		in, err := p.Installation(ctx, instance)
		if err != nil {
			t.Fatal(err)
		}
		a.pluginOriginalClasses.Store(instance, originalPluginClass{installation: in})
		return in
	}
	encode := func(value any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	update := func(instance string, raw json.RawMessage) error {
		a.pluginOriginalBase.mu.Lock()
		defer a.pluginOriginalBase.mu.Unlock()
		_, err := a.originalBaseUpdateConfigLocked(ctx, instance, raw)
		return err
	}
	assertStored := func(instance string, want json.RawMessage) {
		t.Helper()
		stored, err := p.Config(ctx, instance)
		if err != nil || !bytes.Equal(stored.Value, want) {
			t.Fatalf("stored config for %s: size %d, expected %d, error %v", instance, len(stored.Value), len(want), err)
		}
		a.invalidateOriginalBaseConfig(instance)
		loaded, present, err := a.readOriginalBaseConfigForInit(ctx, instance)
		if err != nil || !present || !bytes.Equal(loaded, want) {
			t.Fatalf("cold initialization read for %s: size %d, expected %d, error %v", instance, len(loaded), len(want), err)
		}
	}
	configure := func(method, instance string, body json.RawMessage) any {
		t.Helper()
		request := httptest.NewRequest(method, "/api/plugins/"+instance, bytes.NewReader(body))
		result, err := a.originalPluginConfigurationHTTP(httptest.NewRecorder(), request, instance)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	const instance = "IYUUAutoSeed"
	install(instance, instance, "2.22")
	// The pinned 2.22 source keeps 10,000 unique entries in each of these
	// three arrays. Use distinct, synthetic 40-character info hashes.
	config := map[string]any{"enabled": true, "enable": true}
	for group, key := range []string{"success_caches", "error_caches", "permanent_error_caches"} {
		hashes := make([]string, 10000)
		for i := range hashes {
			hashes[i] = fmt.Sprintf("%040x", group*len(hashes)+i+1)
		}
		config[key] = hashes
	}
	raw := encode(config)
	if len(raw) <= 1<<20 || len(raw) >= 2<<20 {
		t.Fatalf("fixture did not cross the previous 1 MiB limit: %d", len(raw))
	}
	if result, ok := configure(http.MethodPut, instance, raw).(map[string]any); !ok || result["success"] != true {
		t.Fatal("manager configuration save failed")
	}
	assertStored(instance, raw)

	config["marker"] = "worker"
	raw = encode(config)
	if err := update(instance, raw); err != nil {
		t.Fatal(err)
	}
	assertStored(instance, raw)
	if result, ok := configure(http.MethodGet, instance, nil).(json.RawMessage); !ok || !bytes.Equal(result, raw) {
		t.Fatal("manager read failed to return complete worker config")
	}

	// Updates and rollback change version metadata. Ordinary uninstall retains
	// the base installation identity and its encrypted configuration row.
	for _, version := range []string{"2.23", "2.19", "2.22"} {
		install(instance, instance, version)
		assertStored(instance, raw)
	}
	if err := a.setOriginalPluginInstalled(ctx, instance, false); err != nil {
		t.Fatal(err)
	}
	assertStored(instance, raw)
	clone, err := p.cloneOriginalClass(ctx, instance, PluginCloneRequest{Suffix: "capacity"})
	if err != nil {
		t.Fatal(err)
	}
	config["enabled"], config["enable"] = false, false
	assertStored(clone.InstanceID, encode(config))
	assertStored(instance, raw)

	ordinary := install(instance+"impostor", "OrdinaryCapacityFixture", "2.22")
	if err = update(ordinary.InstanceID, raw); pluginErrorCode(err) != "INVALID_PLUGIN_VALUE" {
		t.Fatalf("ordinary Base config lost its 1 MiB cap: %v", err)
	}
	if _, err = p.saveOriginalPluginConfiguration(ctx, ordinary, raw); pluginErrorCode(err) != "INVALID_PLUGIN_VALUE" {
		t.Fatalf("ordinary manager config lost its 1 MiB cap: %v", err)
	}

	padding := func(size int) json.RawMessage {
		return json.RawMessage(`{"value":"` + strings.Repeat("x", size-len(`{"value":""}`)) + `"}`)
	}
	max := padding(2 << 20)
	if err = update(clone.InstanceID, max); err != nil {
		t.Fatal(err)
	}
	if err = update(clone.InstanceID, padding((2<<20)+1)); pluginErrorCode(err) != "INVALID_PLUGIN_VALUE" {
		t.Fatalf("oversized IYUU Base config was accepted: %v", err)
	}
	assertStored(clone.InstanceID, max)
}

func TestOriginalIYUUExactIDCloneUpdateKeepsConfigCapacity(t *testing.T) {
	a := newTestApp(t)
	ctx := t.Context()
	p := a.pluginStore
	installation := func(id, version string) PluginInstallation {
		t.Helper()
		manifest := plugins.Manifest{
			SchemaVersion: 1, PluginID: id, Version: version, Name: "Exact ID capacity fixture",
			Runtime: "go-executable", Entry: "owned-worker", HostAPIMin: plugins.HostAPIVersion,
			HostAPIMax: plugins.HostAPIVersion, Methods: []string{"init_plugin", "stop"},
		}
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		return PluginInstallation{InstanceID: id, PluginID: id, Version: version, ManifestSchemaVersion: 1, Manifest: raw}
	}
	config := func(marker string) json.RawMessage {
		return json.RawMessage(`{"enable":false,"enabled":false,"marker":"` + marker + `","padding":"` + strings.Repeat("x", 1<<20) + `"}`)
	}
	const source = "IYUUAutoSeed"
	base := installation(source, "2.22")
	if err := p.Install(ctx, base); err != nil {
		t.Fatal(err)
	}
	before := config("before")
	if _, err := p.saveOriginalPluginConfiguration(ctx, base, before); err != nil {
		t.Fatal(err)
	}
	clone, err := p.cloneOriginalClass(ctx, source, PluginCloneRequest{Suffix: "exact"})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := p.instanceMetadata(ctx, p.store.DB, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.installOriginalCloneArtifact(ctx, installation(clone.InstanceID, "2.23"), clone, meta); err != nil {
		t.Fatal(err)
	}
	exact, err := p.Installation(ctx, clone.InstanceID)
	if err != nil || exact.PluginID != clone.InstanceID {
		t.Fatalf("clone did not transition to its exact implementation ID: %v", err)
	}
	stored, err := p.Config(ctx, clone.InstanceID)
	if err != nil || !bytes.Equal(stored.Value, before) {
		t.Fatalf("exact-ID update did not retain its full snapshot: %v", err)
	}

	after := config("after")
	a.pluginOriginalBase.mu.Lock()
	_, err = a.originalBaseUpdateConfigLocked(ctx, clone.InstanceID, after)
	a.pluginOriginalBase.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	a.invalidateOriginalBaseConfig(clone.InstanceID)
	loaded, present, err := a.readOriginalBaseConfigForInit(ctx, clone.InstanceID)
	if err != nil || !present || !bytes.Equal(loaded, after) {
		t.Fatalf("exact-ID clone failed to cold-read its updated snapshot: %v", err)
	}

	nested, err := p.cloneOriginalClass(ctx, exact.InstanceID, PluginCloneRequest{Suffix: "nested"})
	if err != nil || nested.PluginID != exact.InstanceID {
		t.Fatalf("nested clone did not copy the exact-ID parent's implementation and configuration: %v", err)
	}
	stored, err = p.Config(ctx, nested.InstanceID)
	if err != nil || !bytes.Equal(stored.Value, after) {
		t.Fatalf("nested copied implementation lost its inherited configuration: %v", err)
	}
	meta, err = p.instanceMetadata(ctx, p.store.DB, nested.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.installOriginalCloneArtifact(ctx, installation(nested.InstanceID, "2.24"), nested, meta); err != nil {
		t.Fatal(err)
	}
	loaded, present, err = a.readOriginalBaseConfigForInit(ctx, nested.InstanceID)
	if err != nil || !present || !bytes.Equal(loaded, after) {
		t.Fatalf("nested exact-ID update lost its inherited configuration: %v", err)
	}
}
