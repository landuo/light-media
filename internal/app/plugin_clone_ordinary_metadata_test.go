package app

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

//go:embed testdata/clone-metadata-ordinary-source02.json
var cloneOrdinaryMetadataSourceRaw []byte

// One owned App root consumes all four actual source observations. The source
// modifier writes Python text; this test compares its ordinary declaration
// values to the host's existing sealed display carrier, not generated code.
func TestOriginalCloneOrdinaryMetadataSource4ActualSDK(t *testing.T) {
	if got := fmt.Sprintf("%x", sha256.Sum256(cloneOrdinaryMetadataSourceRaw)); got != "c4eae28ce4f68308d87e7f652799e21eb89638b39becf0205f543f7d0d67e7e1" {
		t.Fatal("original raw drift", got)
	}
	var oracle struct {
		CaseCount int `json:"case_count"`
		Cases     []struct {
			ID      string                         `json:"id"`
			Input   struct{ Version, Icon string } `json:"input"`
			Outcome struct {
				Kind  string
				Value []json.RawMessage
			} `json:"outcome"`
			Passive struct {
				Kind      string
				Constants map[string]struct {
					Type  string `json:"python_type"`
					Value json.RawMessage
				} `json:"constant_assignments"`
			} `json:"passive_ast"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(cloneOrdinaryMetadataSourceRaw, &oracle); err != nil || oracle.CaseCount != 4 || len(oracle.Cases) != 4 {
		t.Fatal("original four observations required", err)
	}
	// Existing accepted tcp4 owned worker/artifact/install fixture; no new
	// service, external peer, manifest change or worker metadata protocol.
	a, cookie, original := cloneLiteralOwnedFixture(t, true)
	ctx := context.Background()
	originalBefore := ownedModuleRead(t, original)
	codeBefore, err := artifacts.ReadInstalled(a.pluginPackageRoot(), ownedModuleID, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	create := func(source string, request PluginCloneRequest) *originalPluginInstance {
		t.Helper()
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		reply := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+source+"/clone", string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, reply.Code, reply.Body.Bytes())
		a.wg.Wait()
		target := source + plugins.OriginalPluginLower(request.Suffix)
		var result struct {
			Data struct {
				Created          bool
				InstanceID       string `json:"instance_id"`
				SourceInstanceID string `json:"source_instance_id"`
				PluginID         string `json:"plugin_id"`
			}
		}
		if json.Unmarshal(reply.Body.Bytes(), &result) != nil || !result.Data.Created || result.Data.InstanceID != target || result.Data.SourceInstanceID != source || result.Data.PluginID != ownedModuleID {
			t.Fatal("actual clone identity/result", reply.Body.String())
		}
		return originalBaseSDKLoaded(t, a, target)
	}
	// One setup clone gives the actual source the same ordinary declarations
	// as input-plugin.py.txt, including a nonempty inherited icon. This is not
	// an extra source case and does not modify the immutable executable.
	source := create(ownedModuleID, PluginCloneRequest{Suffix: "MetaSeed", Name: "Owned source", Description: "Owned source description", Version: "1.0", Icon: "source.svg"})
	sourceID := source.installation.InstanceID
	sourceBefore := ownedModuleRead(t, source)
	sourceMeta, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	pids := map[int]bool{originalBefore.PID: true, sourceBefore.PID: true}
	expected := make(map[string]PluginInstanceMetadata)
	ids := []string{"empty_version_icon", "version_outer_spaces", "version_unicode", "icon_all_spaces"}
	for i, row := range oracle.Cases {
		if row.ID != ids[i] || row.Outcome.Kind != "return" || len(row.Outcome.Value) != 2 || string(row.Outcome.Value[0]) != "true" || row.Passive.Kind != "ast_parse_only" {
			t.Fatal("unexpected original observation", row.ID)
		}
		field := func(key string) string {
			t.Helper()
			value, ok := row.Passive.Constants[key]
			var text string
			if !ok || value.Type != "str" || json.Unmarshal(value.Value, &text) != nil {
				t.Fatal("original declaration", key)
			}
			return text
		}
		object := create(sourceID, PluginCloneRequest{Suffix: fmt.Sprintf("Ordinary%d", i), Version: row.Input.Version, Icon: row.Input.Icon})
		observation := ownedModuleRead(t, object)
		if !object.external || object == source || observation.PID <= 0 || pids[observation.PID] || observation.Calls["init"] != 1 {
			t.Fatal("independent actual worker/init", row.ID, observation.PID)
		}
		pids[observation.PID] = true
		id := object.installation.InstanceID
		want := PluginInstanceMetadata{SourceInstanceID: sourceID, Name: field("plugin_name"), Description: field("plugin_desc"), DisplayVersion: field("plugin_version"), Icon: field("plugin_icon")}
		got, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, id)
		if err != nil || got != want {
			t.Fatalf("%s sealed metadata got %+v want %+v err=%v", row.ID, got, want, err)
		}
		installed, err := a.pluginStore.Installation(ctx, id)
		if err != nil || installed.PluginID != ownedModuleID || installed.Version != "1.0" || object.manifest.Version != "1.0" {
			t.Fatal("display value replaced executable/storage identity", row.ID, err)
		}
		cloned, err := artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract)
		if err != nil || cloned.SHA256 != codeBefore.SHA256 || cloned.Manifest.PluginID != codeBefore.Manifest.PluginID || cloned.Manifest.Version != codeBefore.Manifest.Version {
			t.Fatal("display value changed copied executable", row.ID, err)
		}
		expected[id] = want
	}
	response := callTest(t, a.Handler(), http.MethodGet, "/api/plugins?state=installed", "", cookie, "")
	var cards struct{ Data []pluginCard }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &cards) != nil {
		t.Fatal("local cards", response.Body.String())
	}
	seen := make(map[string]bool)
	for _, card := range cards.Data {
		want, ok := expected[card.InstanceID]
		if !ok {
			continue
		}
		if seen[card.InstanceID] || card.Name != want.Name || card.Description != want.Description || card.DisplayVersion != want.DisplayVersion || card.Icon != want.Icon || card.SourceInstanceID != sourceID || card.PluginID != ownedModuleID || card.Version != "1.0" {
			t.Fatal("local display carrier/immutable version", card.InstanceID)
		}
		seen[card.InstanceID] = true
	}
	if len(seen) != 4 {
		t.Fatal("missing original four metadata results", len(seen))
	}
	afterMeta, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, sourceID)
	sourceAfter, originalAfter := ownedModuleRead(t, source), ownedModuleRead(t, original)
	if err != nil || afterMeta != sourceMeta || sourceAfter.PID != sourceBefore.PID || sourceAfter.Calls["init"] != sourceBefore.Calls["init"] || originalAfter.PID != originalBefore.PID || originalAfter.Calls["init"] != originalBefore.Calls["init"] {
		t.Fatal("metadata clone changed either source", err)
	}
	// The four display caps/control/special guards were explicitly retired by
	// the 2026-11-02 field decision. Original four expected values above are unchanged.
	// Executable/storage version grammar remains an independent safety boundary.
	for _, value := range []string{"版本 1", " 9.7 ", "a\"b", `a\b`} {
		if pluginStorageVersion.MatchString(value) {
			t.Fatal("display extension leaked into executable storage grammar", value)
		}
	}
}
