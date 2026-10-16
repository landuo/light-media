package cleaninvalidseed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestFrozenSourceProvenance(t *testing.T) {
	var meta struct {
		Version string
		SHA     string `json:"sha256"`
		AST     string `json:"module_ast_sha256"`
		Matches bool   `json:"local_executable_ast_matches"`
		Methods map[string]string
	}
	raw, e := os.ReadFile(referenceFile("reference/installed-reference.json"))
	if e != nil {
		t.Fatal(e)
	}
	if json.Unmarshal(raw, &meta) != nil || meta.Version != "2.1" || meta.SHA != "fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda" || meta.AST != "0e8b6823865834f207ba1480bf2f73b91c46a2b7f51ac41c934f3cb7333af40a" || !meta.Matches || len(meta.Methods) != 27 {
		t.Fatal("installed executable source evidence changed")
	}
	var c struct{ Files map[string]string }
	raw, e = os.ReadFile(referenceFile("source-checksums.json"))
	if e != nil || json.Unmarshal(raw, &c) != nil {
		t.Fatal("checksum source missing", e)
	}
	for p, want := range c.Files {
		raw, e = os.ReadFile(referenceFile(p))
		if e != nil {
			t.Fatal(p, e)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("frozen original reference changed", p)
		}
	}
}
