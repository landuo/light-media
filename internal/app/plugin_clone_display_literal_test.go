package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Expectations are the explicit 2026-11-02 ordinary-display-text decision, not
// generated Python execution or a newly sampled original-source oracle. This
// root uses the already accepted CLI metadata-aware owned-module fixture byte
// source with -X main.fixtureProcessAPI=1. Its fixed HTTP snapshot calls
// Base.GetInstanceMetadata and GetName through the real worker HostCaller.
// Do not substitute the old snapshot-only fixture.
func TestCloneDisplayLiteralApprovalActualSDK(t *testing.T) {
	a, cookie, source := cloneLiteralOwnedFixture(t, true)
	ctx := context.Background()
	read := func(object *originalPluginInstance) plugins.InstanceMetadata {
		t.Helper()
		var result struct {
			Metadata plugins.InstanceMetadata `json:"metadata"`
			Name     string                   `json:"name"`
			PID      int                      `json:"pid"`
		}
		response := callTest(t, a.Handler(), http.MethodGet, "/api/v1/plugin/"+object.installation.InstanceID+"/snapshot", "", cookie, "")
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatal("actual registered SDK snapshot route", response.Code)
		}
		if result.PID <= 0 || result.Metadata.InstanceID != object.installation.InstanceID || result.Name != result.Metadata.Name || result.Metadata.PluginID != ownedModuleID {
			t.Fatal("metadata-aware actual SDK fixture required", object.installation.InstanceID)
		}
		return result.Metadata
	}
	sourceBefore := read(source)
	sourceCode, err := artifacts.ReadInstalled(a.pluginPackageRoot(), ownedModuleID, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	// One value per field simultaneously exceeds its former byte cap and
	// includes literal quotes, backslashes and C0/C1 controls. No string is
	// interpreted as Python, a command, a filesystem path or a resource URL.
	literal := " owned \"literal\" \\ <owned-tag>\x00\x01\t\n\r\x1f\x7f\u0085\u009f end "
	request := PluginCloneRequest{
		Suffix:      "Literal",
		Name:        strings.Repeat("n", 257) + literal,
		Description: strings.Repeat("d", 4097) + literal,
		Version:     strings.Repeat("v", 65) + literal,
		Icon:        strings.Repeat("i", 2049) + literal,
	}
	wantFields := PluginInstanceMetadata{SourceInstanceID: ownedModuleID, Name: request.Name, Description: request.Description, DisplayVersion: request.Version, Icon: request.Icon}
	expected := make(map[string]PluginInstanceMetadata)
	parent := source
	for step := 0; step < 2; step++ {
		if step == 1 {
			request = PluginCloneRequest{Suffix: "Inherited"}
			wantFields.SourceInstanceID = parent.installation.InstanceID
		}
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		sourceID := parent.installation.InstanceID
		reply := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+sourceID+"/clone", string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, reply.Code, reply.Body.Bytes())
		a.wg.Wait()
		id := sourceID + plugins.OriginalPluginLower(request.Suffix)
		clone := originalBaseSDKLoaded(t, a, id)
		parentPID := ownedModuleRead(t, parent).PID
		cloneSnapshot := ownedModuleRead(t, clone)
		if clone == parent || !clone.external || cloneSnapshot.PID <= 0 || cloneSnapshot.PID == parentPID || cloneSnapshot.Calls["init"] != 1 {
			t.Fatal("independent actual clone worker", id)
		}
		got, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, id)
		if err != nil || got != wantFields {
			t.Fatal("literal sealed metadata or parent inheritance changed", id, err)
		}
		wantSDK := plugins.InstanceMetadata{
			InstanceID: id, SourceInstanceID: sourceID, PluginID: ownedModuleID,
			Version: sourceBefore.Version, Name: wantFields.Name, Description: wantFields.Description,
			DisplayVersion: wantFields.DisplayVersion, Icon: wantFields.Icon,
		}
		if gotSDK := read(clone); gotSDK != wantSDK {
			t.Fatal("actual SDK did not preserve four literal strings", id)
		}
		installed, err := a.pluginStore.Installation(ctx, id)
		if err != nil || installed.PluginID != ownedModuleID || installed.Version != sourceBefore.Version || clone.manifest.Version != sourceBefore.Version {
			t.Fatal("display fields reached executable identity", id, err)
		}
		var manifest plugins.Manifest
		if json.Unmarshal(installed.Manifest, &manifest) != nil || manifest.PluginID != sourceCode.Manifest.PluginID || manifest.Version != sourceCode.Manifest.Version || manifest.Name != sourceCode.Manifest.Name || manifest.Icon != sourceCode.Manifest.Icon {
			t.Fatal("literal display data rewrote authenticated manifest", id)
		}
		cloned, err := artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract)
		if err != nil || cloned.SHA256 != sourceCode.SHA256 || cloned.Manifest.PluginID != sourceCode.Manifest.PluginID || cloned.Manifest.Version != sourceCode.Manifest.Version {
			t.Fatal("literal data changed executable bytes or artifact selection", id, err)
		}
		expected[id] = wantFields
		parent = clone
	}
	response := callTest(t, a.Handler(), http.MethodGet, "/api/plugins?state=installed", "", cookie, "")
	var cards struct{ Data []pluginCard }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &cards) != nil {
		t.Fatal("local card read")
	}
	seen := make(map[string]bool)
	for _, card := range cards.Data {
		want, ok := expected[card.InstanceID]
		if !ok {
			continue
		}
		if seen[card.InstanceID] || card.Name != want.Name || card.Description != want.Description || card.DisplayVersion != want.DisplayVersion || card.Icon != want.Icon || card.SourceInstanceID != want.SourceInstanceID || card.Version != sourceBefore.Version || card.PluginID != ownedModuleID {
			t.Fatal("JSON card did not preserve literal metadata separately from identity", card.InstanceID)
		}
		seen[card.InstanceID] = true
	}
	if len(seen) != 2 || read(source) != sourceBefore {
		t.Fatal("missing clone or source metadata changed")
	}
	for _, suffix := range []string{"", "../outside", "bad/path", `bad\path`, "bad\x00", "bad\n"} {
		if validatePluginCloneRequest(PluginCloneRequest{Suffix: suffix, Name: wantFields.Name, Version: wantFields.DisplayVersion}) == nil {
			t.Fatal("literal display approval changed executable identity guard")
		}
	}
	for _, version := range []string{wantFields.DisplayVersion, "a\"b", `a\b`, "a\n", "版本"} {
		if pluginStorageVersion.MatchString(version) {
			t.Fatal("literal display approval changed storage version grammar")
		}
	}
}
