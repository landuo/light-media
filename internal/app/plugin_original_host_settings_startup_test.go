package app

import (
	"encoding/json"
	"math/big"
	"os"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalHostSettingsStartupActualSDKSourceSnapshot(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-integration-20261031/source-models/settings-startup-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Commit string `json:"commit"`
		Cases  []struct {
			Name   string                     `json:"name"`
			Values map[string]json.RawMessage `json:"values"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &oracle); err != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 3 {
		t.Fatal("fixed core configuration oracle missing", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			var bigMemory bool
			var locale, source string
			_ = json.Unmarshal(sample.Values["BIG_MEMORY_MODE"], &bigMemory)
			_ = json.Unmarshal(sample.Values["TMDB_LOCALE"], &locale)
			_ = json.Unmarshal(sample.Values["RECOGNIZE_SOURCE"], &source)
			hours, ok := new(big.Int).SetString(string(sample.Values["META_CACHE_EXPIRE"]), 10)
			if !ok {
				t.Fatal("source hour value is not an integer")
			}
			a, err := New(Options{DataDir: t.TempDir(), BigMemoryMode: bigMemory, HostMediaRecognition: &HostMediaRecognitionConfiguration{Locale: locale, Source: source, MetaCacheExpire: hours}})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			_, worker := hostBusinessFixture(t, a)
			for key, want := range sample.Values {
				var result plugins.SettingsHostResultV1
				if err := hostBusinessCall(t, worker, plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: key}, &result); err != nil || string(result.Value) != string(want) || result.Changed != nil {
					t.Fatalf("startup %s got %s expected %s: %v", key, result.Value, want, err)
				}
				if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: key, Value: json.RawMessage(`null`)}, &result); err == nil {
					t.Fatalf("worker mutated trusted startup %s", key)
				}
				if err := hostBusinessCall(t, worker, plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: key}, &result); err != nil || string(result.Value) != string(want) {
					t.Fatalf("rejected write changed %s: %v", key, err)
				}
			}
		})
	}
}
