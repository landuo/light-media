package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func TestOriginalLocalResourcePublicationKeepsBackendAndClone(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-market/local-reload-actions-oracle.json")
	var oracle struct {
		Cases []struct {
			Kind    string
			Actions []string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("fixed watcher action oracle missing", err)
	}
	expected := map[string][]string{}
	for _, row := range oracle.Cases {
		expected[row.Kind] = row.Actions
	}
	if strings.Join(expected["frontend_ready"], ",") != "sync" || strings.Join(expected["external_python"], ",") != "sync,reload" {
		t.Fatal("fixed watcher action distinction missing")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	root := t.TempDir()
	first, bytes1 := originalFrontendArchive(t, "OWNED_FRONTEND_PROCESS_FIXTURE_BINARY", "local-first")
	second, bytes2 := originalFrontendArchive(t, "OWNED_FRONTEND_PROCESS_FIXTURE_BINARY", "local-second")
	third, bytes3 := originalFrontendArchive(t, "OWNED_FRONTEND_PROCESS_FIXTURE_BINARY", "local-third")
	repository, _ := artifacts.LocalRepositoryURL(originalFrontendFixtureID, root)
	publish := func(index artifacts.Index, payload []byte) {
		t.Helper()
		index.Releases[0].Artifacts[0].URL = "package.zip"
		raw, _ := json.Marshal(index)
		if os.WriteFile(filepath.Join(root, "package.zip"), payload, 0600) != nil || os.WriteFile(filepath.Join(root, artifacts.IndexName), raw, 0600) != nil {
			t.Fatal("owned resource publication failed")
		}
	}
	settings := a.Settings()
	settings.PluginLocalRepoPaths = []string{root}
	settings.PluginAutoReload = true
	settings.PluginMarkets = []string{}
	if err = a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	publish(first, bytes1)
	body, _ := json.Marshal(map[string]any{"plugin_id": originalFrontendFixtureID, "repo_url": repository})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/"+originalFrontendFixtureID, `{"enabled":true,"marker":"local-source"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+originalFrontendFixtureID+"/clone", `{"suffix":"Assets"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	clone := originalFrontendFixtureID + "assets"
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/"+clone, `{"enabled":true,"marker":"local-clone"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	type observation struct {
		Nonce           int
		InitCount       int  `json:"init_count"`
		CwdOK           bool `json:"cwd_ok"`
		Version, Marker string
	}
	read := func(id string) observation {
		t.Helper()
		response := callTest(t, a.Handler(), "GET", "/api/plugins/"+id+"/api/status", "", cookie, "")
		var data observation
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &data) != nil || data.Nonce <= 0 || !data.CwdOK {
			t.Fatal("actual frontend SDK process/cwd unavailable", response.Code)
		}
		return data
	}
	before, cloneBefore := read(originalFrontendFixtureID), read(clone)
	object, _ := a.pluginRunningInstances.Load(originalFrontendFixtureID)
	cloned, _ := a.pluginRunningInstances.Load(clone)
	installation, _ := a.pluginStore.Installation(ctx, originalFrontendFixtureID)
	originalDescriptor := originalFrontendReadDescriptor(t, a, cookie, originalFrontendFixtureID)
	cloneDescriptor := originalFrontendReadDescriptor(t, a, cookie, clone)
	code, err := artifacts.ReadInstalled(a.pluginPackageRoot(), originalFrontendFixtureID, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	cwdBefore, _ := os.Stat(filepath.Dir(code.ExecutablePath))
	entryBefore, _ := os.Stat(code.ExecutablePath)
	state := &originalLocalWatchState{}
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	checkRetained := func() {
		t.Helper()
		if read(originalFrontendFixtureID) != before || read(clone) != cloneBefore {
			t.Fatal("frontend-only sync reset actual process/init/config/cwd or clone")
		}
		current, _ := a.pluginRunningInstances.Load(originalFrontendFixtureID)
		copy, _ := a.pluginRunningInstances.Load(clone)
		row, _ := a.pluginStore.Installation(ctx, originalFrontendFixtureID)
		if current != object || copy != cloned || row.Generation != installation.Generation {
			t.Fatal("resource-only change replaced backend object or authority")
		}
		cwdAfter, _ := os.Stat(filepath.Dir(code.ExecutablePath))
		entryAfter, _ := os.Stat(code.ExecutablePath)
		if !os.SameFile(cwdBefore, cwdAfter) || !os.SameFile(entryBefore, entryAfter) {
			t.Fatal("runtime cwd/executable inode was replaced")
		}
	}
	publish(second, bytes2)
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	checkRetained()
	next := originalFrontendReadDescriptor(t, a, cookie, originalFrontendFixtureID)
	if next.PackageSHA256 == originalDescriptor.PackageSHA256 || originalFrontendReadDescriptor(t, a, cookie, clone).PackageSHA256 != cloneDescriptor.PackageSHA256 {
		t.Fatal("resource identities failed to advance/isolate")
	}
	if response := callTest(t, a.Handler(), "GET", "/api"+originalDescriptor.EntryPath, "", cookie, ""); response.Code != 409 {
		t.Fatal("old source content URL still authorized")
	}
	if response := callTest(t, a.Handler(), "GET", "/api"+next.EntryPath, "", cookie, ""); response.Code != 200 || !strings.Contains(response.Body.String(), "local-second") {
		t.Fatal("new actual resource unavailable")
	}
	if response := callTest(t, a.Handler(), "GET", "/api"+cloneDescriptor.EntryPath, "", cookie, ""); response.Code != 200 || !strings.Contains(response.Body.String(), "local-first") {
		t.Fatal("source resource publish changed clone bytes")
	}
	publish(second, []byte("owned incomplete archive"))
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 1 {
		t.Fatal("incomplete source publication not rejected", failures)
	}
	checkRetained()
	if originalFrontendReadDescriptor(t, a, cookie, originalFrontendFixtureID).PackageSHA256 != next.PackageSHA256 {
		t.Fatal("incomplete publication invalidated prior resources")
	}
	publish(third, bytes3)
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	checkRetained()
	final := originalFrontendReadDescriptor(t, a, cookie, originalFrontendFixtureID)
	if final.PackageSHA256 == next.PackageSHA256 {
		t.Fatal("valid resource recovery was not published")
	}
	// A changed actual worker/class follows the other fixed watcher branch and
	// must replace the source object, while its copied clone remains untouched.
	backend, backendBytes := originalFrontendArchive(t, "OWNED_FRONTEND_PROCESS_FIXTURE_BINARY_V2", "local-backend-two")
	publish(backend, backendBytes)
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	changed := read(originalFrontendFixtureID)
	if changed.Version != "2.0" || changed.Nonce == before.Nonce || changed.InitCount != 1 || changed.Marker != before.Marker || read(clone) != cloneBefore {
		t.Fatal("backend implementation change did not reload source and isolate clone")
	}

}

func TestOriginalLocalMetadataPublicationKeepsActualBaseSession(t *testing.T) {
	a, cookie, object := ownedStandardSDKFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	index, payload := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_BINARY")
	index.Releases[0].Artifacts[0].URL = "worker"
	if os.WriteFile(filepath.Join(root, "worker"), payload, 0600) != nil {
		t.Fatal("owned worker publication failed")
	}
	publish := func() {
		raw, _ := json.Marshal(index)
		if os.WriteFile(filepath.Join(root, artifacts.IndexName), raw, 0600) != nil {
			t.Fatal("owned index publication failed")
		}
	}
	publish()
	settings := a.Settings()
	settings.PluginLocalRepoPaths = []string{root}
	settings.PluginMarkets = []string{}
	settings.PluginAutoReload = true
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, `{"enabled":true,"key":"local-metadata-value"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait() // Await setup command/menu registration before comparing publication effects.
	type baseObservation struct {
		ownedStandardSnapshot
		Value json.RawMessage `json:"value"`
	}
	read := func() baseObservation {
		t.Helper()
		var data baseObservation
		if err := object.readSession(ctx, "snapshot", &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	before := read()
	row, _ := a.pluginStore.Installation(ctx, ownedStandardFixtureID)
	state := &originalLocalWatchState{}
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	index.Label = "metadata changed"
	index.History = map[string]string{"1.0": "updated author notes"}
	publish()
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	after := read()
	current, _ := a.pluginRunningInstances.Load(ownedStandardFixtureID)
	next, _ := a.pluginStore.Installation(ctx, ownedStandardFixtureID)
	for _, method := range []string{"get_state", "get_service", "get_command", "get_api", "init", "close", "stop_service", "stop"} {
		if before.Calls[method] != after.Calls[method] {
			t.Fatal("metadata-only publication repeated a backend getter, job/command registration or lifecycle call", method)
		}
	}
	if current != object || before.Nonce != after.Nonce || before.InitCount != after.InitCount || row.Generation != next.Generation || !originalBaseJSONEqual(before.Config, after.Config) || !originalBaseJSONEqual(before.Value, after.Value) || string(after.Value) != `"local-metadata-value"` {
		t.Fatal("metadata-only publication reset actual Base process/config/KV/authority")
	}
}
