package app

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func TestOriginalMarketSelectionFullSourceCases(t *testing.T) {
	for _, fixture := range []string{"source-cases.json", "source-additional-cases.json"} {
		t.Run(fixture, func(t *testing.T) { testOriginalMarketSelectionSourceCases(t, fixture) })
	}
}

func testOriginalMarketSelectionSourceCases(t *testing.T, fixture string) {
	t.Helper()
	var oracle struct {
		Markets []string `json:"markets"`
		Cases   []struct {
			Name                   string
			Markets                []string
			Higher, Base, Selected []struct{ ID, Version, Repo string }
		}
	}
	raw, err := os.ReadFile("../../testdata/market-selection-collision/" + fixture)
	if err != nil || json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("read independently executed source cases", err)
	}
	for _, example := range oracle.Cases {
		t.Run(example.Name, func(t *testing.T) {
			convert := func(rows []struct{ ID, Version, Repo string }) []artifacts.Discovery {
				out := make([]artifacts.Discovery, 0, len(rows))
				for _, row := range rows {
					out = append(out, artifacts.Discovery{PluginID: row.ID, CurrentVersion: row.Version, RepositoryURL: row.Repo})
				}
				return out
			}
			higher, base := convert(example.Higher), convert(example.Base)
			beforeHigher, beforeBase := append([]artifacts.Discovery{}, higher...), append([]artifacts.Discovery{}, base...)
			markets := example.Markets
			if markets == nil {
				markets = oracle.Markets
			}
			got := selectOriginalMarketDeclarations(higher, base, markets)
			if !reflect.DeepEqual(got, convert(example.Selected)) {
				t.Fatalf("source contract mismatch: got %#v, want %#v", got, convert(example.Selected))
			}
			if !reflect.DeepEqual(higher, beforeHigher) || !reflect.DeepEqual(base, beforeBase) {
				t.Fatal("selection mutated caller declaration order")
			}
		})
	}
}

func TestOriginalMarketSourcePackageProvenanceIsNotPublicIndexFormat(t *testing.T) {
	raw, err := json.Marshal(artifacts.Discovery{SourcePackageVersion: "v2"})
	if err != nil || bytes.Contains(raw, []byte("v2")) || bytes.Contains(raw, []byte("SourcePackage")) {
		t.Fatal("host-only provenance leaked into public JSON", string(raw), err)
	}
}

func TestOriginalMarketLocalCandidatesKeepLegalCollisionUntilOuterSelection(t *testing.T) {
	// Source PluginHelper.get_local_plugin_candidates is keyed by ID, not the
	// delimiter-free key used later by PluginManager.process_plugins_list.
	local := []artifacts.Discovery{
		{PluginID: "OwnedA1", CurrentVersion: "2.0", RepositoryURL: "local://OwnedA1"},
		{PluginID: "OwnedA", CurrentVersion: "12.0", RepositoryURL: "local://OwnedA"},
	}
	selected := selectOriginalLocalMarketDeclarations(local)
	if !reflect.DeepEqual(selected, local) {
		t.Fatal("local exact lookup/watch lost a distinct valid ID", selected)
	}
	outer := selectOriginalMarketDeclarations(selected, nil, nil)
	if len(outer) != 1 || outer[0].PluginID != "OwnedA1" {
		t.Fatal("outer source selector did not apply its concatenated key", outer)
	}
}
