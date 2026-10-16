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
