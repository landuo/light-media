package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Reuses the existing actual owned-module-override artifact/install fixture.
// Only this helper's listener is explicitly tcp4; the old helper is untouched.
func cloneLiteralOwnedFixture(t *testing.T, declares bool) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	variable := "OWNED_MODULE_OVERRIDE_BINARY"
	if !declares {
		variable = "OWNED_MODULE_NO_DECLARATION_BINARY"
	}
	binary := os.Getenv(variable)
	if binary == "" {
		t.Fatalf("%s must name an actual separately compiled owned-module-override worker", variable)
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(binary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != ownedModuleID || slices.Contains(manifest.Methods, "get_module") != declares {
		t.Fatal("wrong owned module worker", string(raw))
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: "light-media.sdk-base/v1"}}}}}
	server := cloneLiteralTCP4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]string{"plugin_id": ownedModuleID, "repo_url": server.URL + "/index"})
	r := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	value, loaded := a.pluginRunningInstances.Load(ownedModuleID)
	if !loaded || !value.(*originalPluginInstance).external {
		t.Fatal("real worker not loaded")
	}
	return a, cookie, value.(*originalPluginInstance)
}

func cloneLiteralTCP4Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	peer.Start()
	return peer
}

// Expectations below are the approved ordinary-text contract plus the pinned
// source's if-name truthiness, NOT newly sampled Python oracle results.
func TestOriginalCloneNameLiteralActualSDK(t *testing.T) {
	a, cookie, source := cloneLiteralOwnedFixture(t, true)
	ctx := context.Background()
	saveOwnedModule(t, a, cookie, source, `{"enabled":true,"marker":"owned-literal-name","nested":{"zero":0,"literal":"  unchanged  "},"modules":{}}`)
	source = originalBaseSDKLoaded(t, a, ownedModuleID)
	before := ownedModuleRead(t, source)
	sourceConfig, err := a.pluginStore.Config(ctx, ownedModuleID)
	if err != nil {
		t.Fatal(err)
	}
	sourceCode, err := artifacts.ReadInstalled(a.pluginPackageRoot(), ownedModuleID, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	sourceMetadata, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, ownedModuleID)
	if err != nil {
		t.Fatal(err)
	}

	const literalName = `  Owned "literal" \ name  `
	const literalDescription = `  Owned description \t "literal"  `
	cases := []struct{ source, suffix, input, want, description string }{
		{ownedModuleID, "Padded", literalName, literalName, literalDescription},
		{ownedModuleID, "Spaces", "   ", "   ", ""},
		{ownedModuleID, "Empty", "", source.manifest.Name, ""},
		{ownedModuleID + "spaces", "Child", "", "   ", ""},
	}
	names := map[string]string{ownedModuleID: source.manifest.Name}
	metadata := map[string]PluginInstanceMetadata{}
	pids := map[int]bool{before.PID: true}
	for _, row := range cases {
		target := row.source + plugins.OriginalPluginLower(row.suffix)
		request := PluginCloneRequest{Suffix: row.suffix, Name: row.input, Description: row.description}
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		response := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+row.source+"/clone", string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		var reply struct {
			Data struct {
				Created          bool
				InstanceID       string `json:"instance_id"`
				PluginID         string `json:"plugin_id"`
				SourceInstanceID string `json:"source_instance_id"`
			}
		}
		if json.Unmarshal(response.Body.Bytes(), &reply) != nil || !reply.Data.Created || reply.Data.InstanceID != target || reply.Data.PluginID != ownedModuleID || reply.Data.SourceInstanceID != row.source {
			t.Fatalf("actual clone identity reply: %d %s", response.Code, response.Body.String())
		}
		a.wg.Wait()
		object := originalBaseSDKLoaded(t, a, target)
		observation := ownedModuleRead(t, object)
		if !object.external || object == source || object.installation.InstanceID != target || object.manifest.PluginID != source.manifest.PluginID || object.manifest.Version != source.manifest.Version || object.manifest.Name != source.manifest.Name || observation.PID <= 0 || pids[observation.PID] || observation.Calls["init"] != 1 {
			t.Fatalf("clone metadata must not change executable identity/independent init: %s pid=%d init=%d", target, observation.PID, observation.Calls["init"])
		}
		pids[observation.PID] = true
		meta, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, target)
		description := row.description
		if description == "" {
			description = source.manifest.Description
		}
		want := PluginInstanceMetadata{SourceInstanceID: row.source, Name: row.want, Description: description, DisplayVersion: source.manifest.Version, Icon: source.manifest.Icon}
		if err != nil || meta != want {
			t.Fatalf("literal metadata %s: got %+v want %+v error=%v", target, meta, want, err)
		}
		if actualName, err := object.originalDisplayName(ctx); err != nil || actualName != row.want {
			t.Fatalf("actual object display name %s: %q %v", target, actualName, err)
		}
		cloned, err := artifacts.ReadInstalled(a.pluginPackageRoot(), target, validatePluginArtifactContract)
		if err != nil || cloned.SHA256 != sourceCode.SHA256 || cloned.Manifest.PluginID != sourceCode.Manifest.PluginID || cloned.Manifest.Name != sourceCode.Manifest.Name || cloned.Manifest.Version != sourceCode.Manifest.Version {
			t.Fatal("display name changed or replaced actual copied worker", target, err)
		}
		config, err := a.pluginStore.Config(ctx, target)
		if err != nil || !originalBaseJSONEqual(config.Value, json.RawMessage(`{"enabled":false,"enable":false,"marker":"owned-literal-name","nested":{"zero":0,"literal":"  unchanged  "},"modules":{}}`)) {
			t.Fatal("name override changed source-shaped config copy", target, err)
		}
		names[target], metadata[target] = row.want, want
	}

	// This exact route reads local classes only, never market repositories.
	response := callTest(t, a.Handler(), http.MethodGet, "/api/plugins?state=installed", "", cookie, "")
	var cards struct{ Data []pluginCard }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &cards) != nil {
		t.Fatal("actual installed cards", response.Code, response.Body.String())
	}
	seen := map[string]bool{}
	for _, card := range cards.Data {
		want, selected := names[card.InstanceID]
		if !selected {
			continue
		}
		if seen[card.InstanceID] || card.Name != want || card.PluginID != ownedModuleID || card.Version != source.manifest.Version {
			t.Fatalf("literal card identity/name: %s %q", card.InstanceID, card.Name)
		}
		if expected, cloned := metadata[card.InstanceID]; cloned && (card.SourceInstanceID != expected.SourceInstanceID || card.Description != expected.Description || card.DisplayVersion != expected.DisplayVersion || card.Icon != expected.Icon) {
			t.Fatal("name operation changed other metadata", card.InstanceID)
		}
		seen[card.InstanceID] = true
	}
	if len(seen) != len(names) {
		t.Fatal("missing current source/clone cards", len(seen), len(names))
	}
	after := ownedModuleRead(t, source)
	afterConfig, err := a.pluginStore.Config(ctx, ownedModuleID)
	if err != nil || after.PID != before.PID || after.Calls["init"] != before.Calls["init"] || !bytes.Equal(after.Inputs["init"], before.Inputs["init"]) || !bytes.Equal(afterConfig.Value, sourceConfig.Value) || afterConfig.Revision != sourceConfig.Revision {
		t.Fatal("clone name operations mutated source object/config", err)
	}
	afterMetadata, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, ownedModuleID)
	if err != nil || afterMetadata != sourceMetadata {
		t.Fatal("source metadata changed", err)
	}
}
