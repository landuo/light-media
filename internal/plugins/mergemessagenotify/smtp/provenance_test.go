package mergemessagesmtp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedSourceAndFrozenMergeDependency(t *testing.T) {
	raw, e := os.ReadFile(originalFixture("source-checksums.json"))
	if e != nil {
		t.Fatal(e)
	}
	type entry struct {
		Bytes int
		SHA   string `json:"sha256"`
	}
	var manifest struct {
		Files    map[string]entry
		External map[string]entry `json:"frozen_external"`
	}
	if e = json.Unmarshal(raw, &manifest); e != nil {
		t.Fatal(e)
	}
	for prefix, items := range map[string]map[string]entry{".": manifest.Files, "..": manifest.External} {
		for name, want := range items {
			if filepath.IsAbs(name) || strings.Contains(name, "..") {
				t.Fatal("invalid inventory path")
			}
			b, e := os.ReadFile(originalFixture(filepath.Join(prefix, name)))
			if e != nil {
				t.Fatal(e)
			}
			sum := sha256.Sum256(b)
			if len(b) != want.Bytes || hex.EncodeToString(sum[:]) != want.SHA {
				t.Fatalf("pinned source/evidence changed: %s", name)
			}
		}
	}
	if len(manifest.Files) < 40 || len(manifest.External) < 10 {
		t.Fatal("incomplete evidence inventory")
	}
}
