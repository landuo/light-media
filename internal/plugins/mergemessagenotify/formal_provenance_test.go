package mergemessagenotify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// This inventory proves which Go code and installed-source references were
// tested; it does not prove a production notification was sent or delivered.
func TestFormalMergeAndSMTPSourceInventory(t *testing.T) {
	root := filepath.Clean("../../..")
	raw, err := os.ReadFile(filepath.Join(root, "testdata/plugins/merge-message-notify-1.20.1/formal-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "d33d9a3b5f271c011496b4fcb25a3a86ba29450d9e4290c4d5471f7ecb015c2b" {
		t.Fatal("formal promotion manifest changed without review")
	}
	var manifest struct {
		Version int
		Files   map[string]string
		Inputs  map[string]string
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version != 1 || len(manifest.Files) != 32 || len(manifest.Inputs) != 425 {
		t.Fatal("formal promotion inventory incomplete")
	}
	for _, files := range []map[string]string{manifest.Files, manifest.Inputs} {
		for name, want := range files {
			raw, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(name, err)
			}
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != want {
				t.Fatal("formal source or reference changed", name)
			}
		}
	}
}
