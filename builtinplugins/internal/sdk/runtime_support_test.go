package plugins

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeSupportManifestCompatibility(t *testing.T) {
	m := fixtureManifest()
	raw, _ := json.Marshal(m)
	if strings.Contains(string(raw), "runtime_support") || m.Validate() != nil {
		t.Fatal("legacy manifest changed")
	}
	for _, support := range []*RuntimeSupport{nil, {}, {Platforms: []string{"linux"}}, {StorageTypes: []string{"local"}}, {Platforms: []string{"linux", "future_os"}, StorageTypes: []string{"local", "future-storage"}}} {
		m.RuntimeSupport = support
		if err := m.Validate(); err != nil {
			t.Fatalf("valid declaration: %v", err)
		}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var restored Manifest
		if err := json.Unmarshal(raw, &restored); err != nil || restored.Validate() != nil {
			t.Fatal("roundtrip failed", err)
		}
	}
}

func TestRuntimeSupportManifestRejectsInvalidIdentifiers(t *testing.T) {
	for _, values := range [][]string{{""}, {"Linux"}, {"local", "local"}, {"../local"}, {"a b"}, {"<script>"}, {"linux\n"}, {strings.Repeat("a", 33)}, make([]string, 17)} {
		for _, field := range []string{"platforms", "storage_types"} {
			t.Run(field+"/"+strings.Join(values, ","), func(t *testing.T) {
				m := fixtureManifest()
				m.RuntimeSupport = &RuntimeSupport{}
				if field == "platforms" {
					m.RuntimeSupport.Platforms = values
				} else {
					m.RuntimeSupport.StorageTypes = values
				}
				if codeOf(m.Validate()) != "invalid_manifest" {
					t.Fatal("unsafe metadata accepted")
				}
			})
		}
	}
}
