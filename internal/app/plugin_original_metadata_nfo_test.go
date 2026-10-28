package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

func TestOriginalMetadataNFOActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/metadata-module-oracle.json")
	var oracle struct {
		Meta, Information json.RawMessage
		Cases             []struct {
			Name         string
			PluginResult json.RawMessage `json:"plugin_result"`
			Calls        []struct {
				Role   string
				Kwargs json.RawMessage
			}
			Saved     []json.RawMessage
			Exception *string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 8 {
		t.Fatal("fixed metadata wrapper/save consumer oracle", err)
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: "rust"}, StreamingPlatforms: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := parser.ParseTitle("Synthetic Film 2024", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"metadata_nfo": "module_execute"}, "result": sample.PluginResult})
			saveOwnedModule(t, a, cookie, object, string(config))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			core := 0
			content, err := a.originalMetadataNFO(context.Background(), meta, oracle.Information, nil, nil, func() ([]byte, error) { core++; return []byte("owned native core NFO"), nil }, nil)
			if (err != nil) != (sample.Exception != nil) {
				t.Fatalf("fixed _save_file exception changed: %v vs %v", err, sample.Exception)
			}
			wantCore := 0
			var expected json.RawMessage
			for _, call := range sample.Calls {
				if call.Role == "core" {
					wantCore++
				} else {
					expected = call.Kwargs
				}
			}
			if core != wantCore {
				t.Fatal("source list/core continuation", core, wantCore)
			}
			if len(sample.Saved) > 0 {
				want, ok := plugins.DecodeModuleResultBytes(sample.Saved[0])
				if !ok || string(content) != string(want) {
					t.Fatalf("fixed exact saved bytes: %q vs %s", content, sample.Saved[0])
				}
			} else if len(content) != 0 {
				t.Fatalf("source falsey/invalid content was accepted: %q", content)
			}
			after := ownedModuleRead(t, object)
			var actual struct {
				Args   []json.RawMessage
				Kwargs json.RawMessage
			}
			if json.Unmarshal(after.Inputs["module_execute"], &actual) != nil || actual.Args == nil || len(actual.Args) != 0 || !originalBaseJSONEqual(actual.Kwargs, expected) || after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
				t.Fatalf("actual SDK four kwargs changed\n%s\n%s", actual.Kwargs, expected)
			}
		})
	}
}
