package autosignin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// These are immutable source/fixture checks, not evidence of a live site login
// or a production PluginStore/scheduler/notification integration.
func TestFormalSourceAndOriginalReferenceIntegrity(t *testing.T) {
	raw, err := readAutoSignInFixture("formal-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "4943e98b86155e8162102fb8bd74b3490559b9aa3c4889cb93e6c6f87a2b074e" {
		t.Fatal("formal input manifest changed without review")
	}
	var manifest struct {
		Version  int
		Files    map[string]string
		Fixtures map[string]string
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version != 1 || len(manifest.Files) != 65 || len(manifest.Fixtures) != 35 {
		t.Fatal("formal input inventory incomplete")
	}
	// The original promotion remains immutable; reviewed host/source-boundary
	// changes are explicit amendments, never replacements for installed fixtures.
	amendmentRaw, err := readAutoSignInFixture("host-source-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	amendmentSum := sha256.Sum256(amendmentRaw)
	if hex.EncodeToString(amendmentSum[:]) != "3d7ee2ea024298cf94caf1367f7a3b3bbb4fd3a83c472ea0ac6f3d44c8292989" {
		t.Fatal("host source amendment changed without review")
	}
	var amendment struct {
		Version            int
		BaseManifestSHA256 string `json:"base_manifest_sha256"`
		Files              map[string]string
	}
	if json.Unmarshal(amendmentRaw, &amendment) != nil || amendment.Version != 1 || amendment.BaseManifestSHA256 != hex.EncodeToString(sum[:]) || len(amendment.Files) == 0 {
		t.Fatal("host source amendment incomplete")
	}
	for name, digest := range amendment.Files {
		if _, fixture := manifest.Fixtures[name]; fixture {
			t.Fatal("host amendment cannot replace installed fixture", name)
		}
		manifest.Files[name] = digest
	}
	for _, files := range []map[string]string{manifest.Files, manifest.Fixtures} {
		for name, want := range files {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", name))
			if err != nil {
				t.Fatal(name, err)
			}
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != want {
				t.Fatal("formal source/reference changed without review", name)
			}
		}
	}
}
