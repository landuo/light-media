package removelink

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestManifestHasOriginalSurfacesWithoutCleanupDelegate(t *testing.T) {
	m := Manifest()
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	if len(m.APIs) != 0 || len(m.BroadcastHandlers) != 0 || m.HasPage || m.Frontend == nil || m.Version != "2.18" {
		t.Fatal("invented source surface", m)
	}
	for _, c := range m.Capabilities {
		if strings.HasPrefix(c, "cleanup.") {
			t.Fatal("host business delegation", c)
		}
	}
	if m.Frontend.Root != "frontend" || m.Frontend.Exposes[0] != "Config" {
		t.Fatal("wrong frontend ABI")
	}
	var f []any
	var d map[string]any
	if json.Unmarshal(form, &f) != nil || json.Unmarshal(defaults, &d) != nil || len(d) != 13 {
		t.Fatal("native source form/defaults incomplete")
	}
}
func TestInstalledVueAssetsBoundWithScopedAdaptations(t *testing.T) {
	var manifest struct {
		Assets []struct{ Path, SHA256 string }
	}
	raw, e := os.ReadFile("reference/frontend-files.json")
	if e != nil {
		t.Fatal(e)
	}
	if json.Unmarshal(raw, &manifest) != nil || len(manifest.Assets) != 3 {
		t.Fatal("invalid asset provenance")
	}
	for _, a := range manifest.Assets {
		raw, e := os.ReadFile(a.Path)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != a.SHA256 {
			t.Fatal("source Vue asset changed", a.Path)
		}
	}
}
