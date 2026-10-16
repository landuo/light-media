package cleanuphost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFrozenCandidateSourceAndEvidence(t *testing.T) {
	data := filepath.Join("..", "..", "..", "..", "testdata", "plugins", "remove-link-host")
	raw, err := os.ReadFile(filepath.Join(data, "source-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		InstalledSource string `json:"installed_source_sha256"`
		Files           map[string]struct {
			Candidate    string `json:"candidate_file"`
			SHA256       string `json:"sha256"`
			FormalSHA256 string `json:"formal_sha256"`
		} `json:"copied_go_files"`
		Evidence map[string]string `json:"evidence_files"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.InstalledSource != "1f9e719e16f5f72340aa50e942d6f7062390a4bddd82f5f3f47d58ef757dbda4" || len(manifest.Files) != 10 || len(manifest.Evidence) != 3 {
		t.Fatal("missing installed source/candidate/evidence provenance")
	}
	verify := func(path, expected string) {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != expected {
			t.Fatalf("frozen source/evidence differs: %s", path)
		}
	}
	for name, source := range manifest.Files {
		if filepath.Base(name) != name || filepath.Base(source.Candidate) != source.Candidate {
			t.Fatal("invalid provenance filename")
		}
		expected := source.SHA256
		if source.FormalSHA256 != "" {
			expected = source.FormalSHA256
		}
		verify(name, expected)
	}
	for name, sum := range manifest.Evidence {
		if filepath.Base(name) != name {
			t.Fatal("invalid evidence filename")
		}
		verify(filepath.Join(data, name), sum)
	}
}
