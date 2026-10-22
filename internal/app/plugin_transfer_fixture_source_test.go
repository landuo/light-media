package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Source-overlay checks must reject a nested validation checkout when the
// executable was launched from the ordinary tree, before starting any build.
func TestTransferFixtureSourceOverlayIsolation(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "work", "frozen")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, nested} {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module light-media\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	replacement := filepath.Join(root, "owned-replacement.go")
	for _, tc := range []struct {
		name, module, target, replacement string
		valid                             bool
	}{
		{"virtual_new_file", root, filepath.Join(root, "internal", "app", "testdata", "worker", "main.go"), replacement, true},
		{"relative_target", root, "internal/app/fixture.go", replacement, false},
		{"relative_replacement", root, filepath.Join(root, "fixture.go"), "replacement.go", false},
		{"empty_replacement", root, filepath.Join(root, "fixture.go"), "", false},
		{"outside_module", root, filepath.Join(filepath.Dir(root), "outside.go"), replacement, false},
		{"prefix_sibling", root, root + "-other/fixture.go", replacement, false},
		{"module_directory", root, root, replacement, false},
		{"wrong_outer_module", root, filepath.Join(nested, "internal", "app", "fixture.go"), replacement, false},
		{"correct_nested_module", nested, filepath.Join(nested, "internal", "app", "fixture.go"), replacement, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"Replace": map[string]string{tc.target: tc.replacement}})
			if err != nil {
				t.Fatal(err)
			}
			overlay := filepath.Join(t.TempDir(), "overlay.json")
			if err := os.WriteFile(overlay, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateTransferSourceOverlay(tc.module, overlay); (err == nil) != tc.valid {
				t.Fatalf("source boundary acceptance=%t, want=%t", err == nil, tc.valid)
			}
		})
	}
	for _, tc := range []struct{ name, raw string }{{"malformed", "{"}, {"empty", "{}"}, {"empty_replace", `{"Replace":{}}`}} {
		t.Run(tc.name, func(t *testing.T) {
			overlay := filepath.Join(t.TempDir(), "overlay.json")
			if err := os.WriteFile(overlay, []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			if validateTransferSourceOverlay(root, overlay) == nil {
				t.Fatal("invalid source overlay accepted")
			}
		})
	}
}
