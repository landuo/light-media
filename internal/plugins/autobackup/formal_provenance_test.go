package autobackup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func originalFixture(name string) string {
	return filepath.Join("../../../testdata/plugins/auto-backup-2.1.4/input/_auto_backup_port", name)
}

type formalSourceManifest struct {
	Schema          int
	Plugin          string
	OriginalVersion string `json:"original_version"`
	Source          string `json:"installed_source_sha256"`
	Files, Inputs   map[string]string
	Production      map[string]string `json:"candidate_production_files"`
	Tests           map[string]string `json:"candidate_test_files"`
	Embeds          map[string]string `json:"candidate_embed_files"`
	Registered      bool              `json:"app_registered"`
	Migrated        bool              `json:"original_user_migrated"`
}

func formalAutoBackupManifest(t *testing.T) formalSourceManifest {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/plugins/auto-backup-2.1.4/formal-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != "8ec424684d8a8ab74d398628ff099941957d4ea28e287e48b4258daf68480f88" {
		t.Fatal("formal promotion inventory changed without review")
	}
	var m formalSourceManifest
	if json.Unmarshal(b, &m) != nil || m.Schema != 1 || m.Plugin != "AutoBackup" || m.OriginalVersion != "2.1.4" || m.Source != "dedab53b1adf96dc59879ebdd67f77fe67ea773981d4befb04ca000889f607e4" || len(m.Files) != 20 || len(m.Inputs) != 76 || m.Registered || m.Migrated {
		t.Fatal("incomplete formal AutoBackup source identity")
	}
	return m
}

func TestFormalAutoBackupSourceInventory(t *testing.T) {
	m := formalAutoBackupManifest(t)
	for _, files := range []map[string]string{m.Files, m.Inputs} {
		for name, want := range files {
			if filepath.IsAbs(name) || filepath.ToSlash(filepath.Clean(name)) != name || strings.HasPrefix(name, "../") {
				t.Fatal("unsafe source evidence path", name)
			}
			b, err := os.ReadFile(filepath.Join("../../..", name))
			if err != nil {
				t.Fatal(name, err)
			}
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != want {
				t.Fatal("formal source or offline reference changed", name)
			}
		}
	}
}

func TestFormalAutoBackupMatchesFrozenCandidateAndOriginalCorpus(t *testing.T) {
	m := formalAutoBackupManifest(t)
	if len(m.Production) != 12 || len(m.Tests) != 6 || len(m.Embeds) != 2 {
		t.Fatal("incomplete candidate inventory")
	}
	rebase := regexp.MustCompile(`os.ReadFile\("(reference/[^"\n]+)"\)`)
	for kind, files := range map[string]map[string]string{"production": m.Production, "test": m.Tests, "embed": m.Embeds} {
		for name, want := range files {
			original, err := os.ReadFile(originalFixture(name))
			if err != nil {
				t.Fatal(name, err)
			}
			sum := sha256.Sum256(original)
			if hex.EncodeToString(sum[:]) != want {
				t.Fatal("frozen candidate changed", name)
			}
			adapted := original
			if kind == "test" {
				adapted = rebase.ReplaceAll(original, []byte(`os.ReadFile(originalFixture("$1"))`))
				adapted, err = format.Source(adapted)
				if err != nil {
					t.Fatal(err)
				}
			}
			actual, err := os.ReadFile(name)
			if err != nil || !bytes.Equal(actual, adapted) {
				t.Fatal("unlisted formal behavior change", name, err)
			}
		}
	}
	for name, expected := range map[string]int{"business-oracle.json": 82, "webdav-oracle.json": 55, "archive-oracle.json": 9} {
		b, err := os.ReadFile(originalFixture("reference/" + name))
		if err != nil {
			t.Fatal(err)
		}
		var corpus struct {
			Source string `json:"source_sha256"`
			Cases  []json.RawMessage
		}
		wantSource := m.Source
		if name == "webdav-oracle.json" {
			wantSource = "4f436b85d1818651de1480614c9ff74744a8d5ff191bff66af301df0b6aaf95c"
		}
		if json.Unmarshal(b, &corpus) != nil || corpus.Source != wantSource || len(corpus.Cases) != expected {
			t.Fatal("original execution corpus identity changed", name)
		}
	}
	b, err := os.ReadFile(originalFixture("reference/system-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var system struct {
		Source                     string `json:"source_sha256"`
		Container, Versions, Plans []json.RawMessage
	}
	if json.Unmarshal(b, &system) != nil || system.Source != m.Source || len(system.Container) != 10 || len(system.Versions) != 11 || len(system.Plans) != 32 {
		t.Fatal("original system helper corpus identity changed")
	}
}
