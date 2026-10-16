package seedhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFormalHostSourceAndInstalledFailurePrefixProvenance(t *testing.T) {
	root := filepath.Clean("../../../..")
	body, err := os.ReadFile(filepath.Join(root, "testdata/plugins/clean-invalid-seed-host/source-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Source        string            `json:"installed_source_sha256"`
		AST           string            `json:"module_ast_sha256"`
		ProductionSQL bool              `json:"production_sql_backend_included"`
		Files         map[string]string `json:"files"`
		Fixtures      map[string]string `json:"fixture_files"`
	}
	if json.Unmarshal(body, &manifest) != nil || manifest.Source != "fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda" || manifest.AST != "0e8b6823865834f207ba1480bf2f73b91c46a2b7f51ac41c934f3cb7333af40a" || manifest.ProductionSQL || len(manifest.Files) != 11 {
		t.Fatal("invalid host provenance")
	}
	for _, files := range []map[string]string{manifest.Files, manifest.Fixtures} {
		for name, want := range files {
			raw, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != want {
				t.Fatal("formal source changed without provenance", name)
			}
		}
	}
}
