package removelink

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFrozenSourceFixturesAndOriginalForm(t *testing.T) {
	const dir = "../../../testdata/plugins/remove-link-2.17"
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var manifest struct {
		Plugin        string            `json:"plugin_id"`
		Version       string            `json:"version"`
		Source        string            `json:"installed_source_sha256"`
		WorkflowCases int               `json:"workflow_cases"`
		HelperCases   int               `json:"helper_cases"`
		Files         map[string]string `json:"files"`
	}
	if err := json.Unmarshal(read("fixture-manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Plugin != "RemoveLink" || manifest.Version != "2.17" || manifest.Source != "1f9e719e16f5f72340aa50e942d6f7062390a4bddd82f5f3f47d58ef757dbda4" || manifest.WorkflowCases != 72 || manifest.HelperCases != 42 || len(manifest.Files) != 7 {
		t.Fatal("source fixture identity or coverage changed")
	}
	for name, expected := range manifest.Files {
		sum := sha256.Sum256(read(name))
		if hex.EncodeToString(sum[:]) != expected {
			t.Fatalf("frozen source fixture changed: %s", name)
		}
	}
	if !bytes.Equal(bytes.TrimSpace(Form()), bytes.TrimSpace(read("form.json"))) {
		t.Fatal("embedded form differs from the original AST extraction")
	}
	var installed struct {
		Source  string            `json:"sha256"`
		Version string            `json:"version"`
		Matches bool              `json:"local_sanitized_executable_ast_matches"`
		Methods map[string]string `json:"comparable_method_ast_sha256"`
	}
	if err := json.Unmarshal(read("installed-reference.json"), &installed); err != nil || installed.Source != manifest.Source || installed.Version != manifest.Version || !installed.Matches || len(installed.Methods) != 42 {
		t.Fatal("installed executable AST provenance changed", err)
	}
	for _, fixture := range []struct {
		name  string
		count int
	}{{"oracle.json", manifest.WorkflowCases}, {"helpers-oracle.json", manifest.HelperCases}} {
		var suite struct {
			Cases []json.RawMessage `json:"cases"`
		}
		if err := json.Unmarshal(read(fixture.name), &suite); err != nil || len(suite.Cases) != fixture.count {
			t.Fatalf("source case count changed: %s", fixture.name)
		}
	}
}
