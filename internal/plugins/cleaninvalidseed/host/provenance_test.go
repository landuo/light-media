package seedhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if json.Unmarshal(body, &manifest) != nil || manifest.Source != "fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda" || manifest.AST != "0e8b6823865834f207ba1480bf2f73b91c46a2b7f51ac41c934f3cb7333af40a" || manifest.ProductionSQL || len(manifest.Files) != 14 {
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

func TestFormalMutationPhaseInstalledSourceAndContinuationOracleProvenance(t *testing.T) {
	root := filepath.Join("../../../..", "testdata/plugins/clean-invalid-seed-mutation-phase")
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var manifest struct {
		Sources map[string]string `json:"source_sha256"`
		Files   map[string]string `json:"files"`
	}
	if json.Unmarshal(read("source-manifest.json"), &manifest) != nil || len(manifest.Files) != 5 || len(manifest.Sources) != 3 {
		t.Fatal("mutation phase source manifest invalid")
	}
	for name, expected := range map[string]string{
		"installed-plugin.py": "fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda",
		"installed-qb.py":     "114b2f5e78a89bd3384c1bb0fe29a9e6b5144e575ef63fb852bfe148387b29b0",
		"installed-tr.py":     "6fc20e208b86e9e68ada074643f82b88270c2e51749e460ad881b10f744cf676",
	} {
		if manifest.Sources[name] != expected || manifest.Files[name] != expected {
			t.Fatal("installed dependency source identity changed", name)
		}
	}
	for name, want := range manifest.Files {
		if filepath.Base(name) != name {
			t.Fatal("source path escaped fixture")
		}
		sum := sha256.Sum256(read(name))
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("mutation phase source/oracle changed without provenance", name)
		}
	}
	var oracle struct {
		Sources map[string]string `json:"source_sha256"`
		Cases   []struct {
			Kind      string  `json:"kind"`
			Operation string  `json:"operation"`
			Error     *string `json:"error"`
			Attempts  []struct {
				Method string `json:"method"`
				Hash   string `json:"hash"`
			} `json:"attempts"`
			Successes []string `json:"synthetic_successes"`
			CountTwo  bool     `json:"source_count_two"`
		} `json:"cases"`
	}
	if json.Unmarshal(read("source-return-oracle.json"), &oracle) != nil || len(oracle.Cases) != 4 || len(oracle.Sources) != 3 {
		t.Fatal("original continuation oracle invalid")
	}
	for name, sha := range manifest.Sources {
		if oracle.Sources[name] != sha {
			t.Fatal("oracle consumed different source", name)
		}
	}
	seen := map[string]bool{}
	for _, c := range oracle.Cases {
		key := c.Kind + "/" + c.Operation
		method, valid := map[string]string{"qbittorrent/delete": "torrents_delete", "qbittorrent/tags": "torrents_add_tags", "transmission/delete": "remove_torrent", "transmission/tags": "change_torrent"}[key]
		if !valid || seen[key] || c.Error != nil || len(c.Attempts) != 2 || len(c.Successes) != 1 || !c.CountTwo {
			t.Fatal("original failure continuation/count changed", key)
		}
		seen[key] = true
		if c.Attempts[0].Method != method || c.Attempts[1].Method != method || c.Attempts[0].Hash != strings.Repeat("a", 40) || c.Attempts[1].Hash != strings.Repeat("c", 40) || c.Successes[0] != strings.Repeat("c", 40) {
			t.Fatal("source attempt count confused with actual acceptance", key)
		}
	}
}
