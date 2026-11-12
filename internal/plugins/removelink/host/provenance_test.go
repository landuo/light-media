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
		Evidence            map[string]string `json:"evidence_files"`
		FormalEvidence      map[string]string `json:"formal_validation_evidence"`
		TransitionFiles     map[string]string `json:"transition_go_files"`
		TransitionEvidence  map[string]string `json:"transition_evidence_files"`
		AfterActionFiles    map[string]string `json:"after_action_go_files"`
		RootExpansionFiles  map[string]string `json:"root_expansion_go_files"`
		OwnerReferences     map[string]string `json:"owner_reference_files"`
		SourceBarrierFiles  map[string]string `json:"source_barrier_go_files"`
		InitDiagnosticFiles map[string]string `json:"init_diagnostic_go_files"`
		RetiredFiles        map[string]string `json:"retired_init_diagnostic_files"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.InstalledSource != "1f9e719e16f5f72340aa50e942d6f7062390a4bddd82f5f3f47d58ef757dbda4" || len(manifest.Files) != 10 || len(manifest.Evidence) != 3 || len(manifest.FormalEvidence) != 2 || len(manifest.TransitionFiles) != 3 || len(manifest.TransitionEvidence) != 3 || len(manifest.AfterActionFiles) != 1 || len(manifest.RootExpansionFiles) != 2 || len(manifest.OwnerReferences) != 1 || len(manifest.SourceBarrierFiles) != 6 || len(manifest.InitDiagnosticFiles) != 5 || len(manifest.RetiredFiles) != 2 {
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
		// Candidate/formal hashes remain historical evidence. Changed current
		// implementations are explicitly attributed to the source-barrier stage.
		if current, ok := manifest.SourceBarrierFiles["internal/plugins/removelink/host/"+name]; ok {
			expected = current
		}
		if current, ok := manifest.InitDiagnosticFiles["internal/plugins/removelink/host/"+name]; ok {
			expected = current
		}
		verify(name, expected)
	}
	expectedBarrierFiles := []string{
		"internal/plugins/removelink/types.go",
		"internal/plugins/removelink/machine.go",
		"internal/plugins/removelink/storage.go",
		"internal/plugins/removelink/host/types.go",
		"internal/plugins/removelink/host/local.go",
		"internal/plugins/removelink/host/host.go",
	}
	for _, name := range expectedBarrierFiles {
		sum, ok := manifest.SourceBarrierFiles[name]
		if !ok {
			t.Fatalf("missing source-barrier provenance: %s", name)
		}
		if current, ok := manifest.InitDiagnosticFiles[name]; ok {
			sum = current
		}
		verify(filepath.Join("..", "..", "..", "..", name), sum)
	}
	// Only the two explicitly retired App adapters use historical bytes. Keep
	// their original hashes and every active implementation check intact.
	retired := map[string]string{
		"internal/app/plugin_remove_link_owner.go":           "retired/plugin_remove_link_owner.go.txt",
		"internal/app/plugin_remove_link_init_diagnostic.go": "retired/plugin_remove_link_init_diagnostic.go.txt",
	}
	for name, snapshot := range retired {
		if manifest.RetiredFiles[name] != snapshot {
			t.Fatalf("retired source mapping changed: %s", name)
		}
		if _, err := os.Stat(filepath.Join("../../../..", name)); !os.IsNotExist(err) {
			t.Fatalf("retired source restored or inaccessible; review current implementation: %s (%v)", name, err)
		}
	}
	for _, name := range []string{"internal/plugins/removelink/host/types.go", "internal/plugins/removelink/host/host.go", "internal/plugins/removelink/host/watch_linux.go", "internal/app/plugin_remove_link_owner.go", "internal/app/plugin_remove_link_init_diagnostic.go"} {
		sum, ok := manifest.InitDiagnosticFiles[name]
		if !ok {
			t.Fatalf("missing init-diagnostic provenance: %s", name)
		}
		if snapshot, historical := retired[name]; historical {
			verify(filepath.Join(data, snapshot), sum)
		} else {
			verify(filepath.Join("..", "..", "..", "..", name), sum)
		}
	}
	for name, sum := range manifest.Evidence {
		if filepath.Base(name) != name {
			t.Fatal("invalid evidence filename")
		}
		verify(filepath.Join(data, name), sum)
	}
	for name, sum := range manifest.TransitionFiles {
		if filepath.Base(name) != name {
			t.Fatal("invalid transition source filename")
		}
		verify(name, sum)
	}
	for name, sum := range manifest.AfterActionFiles {
		if filepath.Base(name) != name {
			t.Fatal("invalid AfterAction source filename")
		}
		verify(name, sum)
	}
	for name, sum := range manifest.RootExpansionFiles {
		if filepath.Base(name) != name {
			t.Fatal("invalid root expansion filename")
		}
		verify(name, sum)
	}
	for _, evidence := range []map[string]string{manifest.FormalEvidence, manifest.TransitionEvidence, manifest.OwnerReferences} {
		for name, sum := range evidence {
			if filepath.Base(name) != name {
				t.Fatal("invalid formal/transition evidence filename")
			}
			verify(filepath.Join(data, name), sum)
		}
	}
}
