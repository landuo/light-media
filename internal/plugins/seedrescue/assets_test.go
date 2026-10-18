package seedrescue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestOriginalFrontendGraphVerbatim(t *testing.T) {
	b, e := os.ReadFile("reference/frontend-manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	var m struct {
		Files []struct{ Path, SHA256 string }
	}
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	assets := FrontendAssets()
	for _, f := range m.Files {
		data, e := fs.ReadFile(assets, f.Path)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.SHA256 || strings.Contains(string(data), "[REDACTED]") {
			t.Fatal(f.Path)
		}
	}
	if len(m.Files) != 8 {
		t.Fatal("unexpected original graph")
	}
	mode, path := RenderMode()
	if mode != "vue" || path != "dist/assets" {
		t.Fatal(mode, path)
	}
}
