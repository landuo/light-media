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
	// The original immutable manifest remains unchanged. This independently
	// pinned review records the single deliberate source correction and every
	// newly tested native dependency input, including the exact prior source.
	reviewRaw, err := os.ReadFile(filepath.Join(root, "testdata/plugins/merge-message-native-mako/review.json"))
	if err != nil {
		t.Fatal(err)
	}
	reviewSum := sha256.Sum256(reviewRaw)
	if hex.EncodeToString(reviewSum[:]) != "6ea9497ac6b897f2e34876c0e2f4352a5c2814f8686d7d535e29ba3c21b23ca2" {
		t.Fatal("native template review changed without review")
	}
	var review struct {
		Schema          int
		BaseManifestSHA string `json:"base_manifest_sha256"`
		Changes         map[string]struct {
			BeforeSHA  string `json:"before_sha256"`
			BeforeFile string `json:"before_file"`
			AfterSHA   string `json:"after_sha256"`
		}
		Additions map[string]string
	}
	if json.Unmarshal(reviewRaw, &review) != nil || review.Schema != 1 || review.BaseManifestSHA != hex.EncodeToString(sum[:]) || len(review.Changes) != 1 || len(review.Additions) != 27 {
		t.Fatal("native template review incomplete")
	}
	for name, want := range review.Additions {
		data, e := os.ReadFile(filepath.Join(root, name))
		if e != nil {
			t.Fatal(name, e)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != want {
			t.Fatal("native template input drift", name)
		}
	}
	for _, files := range []map[string]string{manifest.Files, manifest.Inputs} {
		for name, want := range files {
			if change, ok := review.Changes[name]; ok {
				before, e := os.ReadFile(filepath.Join(root, change.BeforeFile))
				if e != nil {
					t.Fatal(e)
				}
				digest := sha256.Sum256(before)
				if change.BeforeSHA != want || hex.EncodeToString(digest[:]) != want {
					t.Fatal("review lost original template source", name)
				}
				want = change.AfterSHA
			}
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
