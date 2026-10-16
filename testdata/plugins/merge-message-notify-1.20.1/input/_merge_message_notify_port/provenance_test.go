package mergemessagenotify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceAndReferenceChecksums(t *testing.T) {
	raw, e := os.ReadFile("source-checksums.json")
	if e != nil {
		t.Fatal(e)
	}
	var manifest struct {
		Files map[string]struct {
			SHA   string `json:"sha256"`
			Bytes int    `json:"bytes"`
		}
	}
	if e = json.Unmarshal(raw, &manifest); e != nil {
		t.Fatal(e)
	}
	if len(manifest.Files) < 300 {
		t.Fatal("incomplete installed source inventory")
	}
	for p, want := range manifest.Files {
		if filepath.IsAbs(p) || strings.Contains(p, "..") {
			t.Fatal("invalid evidence path")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(b)
		if len(b) != want.Bytes || hex.EncodeToString(sum[:]) != want.SHA {
			t.Fatalf("source/reference changed: %s", p)
		}
	}
}
