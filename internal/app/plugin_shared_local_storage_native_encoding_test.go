package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// Read-only safety boundary: never turn an unrepresentable native filename into
// a different valid U+FFFD path. The worker is not granted delete capability.
func TestSharedLocalStorageNativeFilenameEncodingBoundary(t *testing.T) {
	a, _, worker := sharedLocalStorageFixture(t, []string{plugins.LocalStorageExistsCapability, plugins.LocalStorageListCapability})
	root := sharedLocalStorageRoot(t, a)
	invalid := filepath.Join(root, string([]byte{0xff})+".mkv")
	replacement := filepath.Join(root, "\ufffd.mkv")
	owned := map[string]string{invalid: "owned invalid UTF8 filename content", replacement: "owned genuine U+FFFD filename content"}
	for path, content := range owned {
		if e := sharedLocalStorageFixtureWrite(t, path, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
		stamp := time.Unix(1700000000, 0)
		if e := os.Chtimes(path, stamp, stamp); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := os.ReadFile("../../testdata/plugin-local-storage-20261030/native-encoding-source.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Invalid     string `json:"invalid_name_fs_bytes"`
		Replacement string `json:"replacement_name_fs_bytes"`
		Operations  []struct {
			Case     string          `json:"case"`
			Method   string          `json:"method"`
			Input    json.RawMessage `json:"input"`
			Returned json.RawMessage `json:"returned"`
		} `json:"operations"`
	}
	if e := json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "$ROOT", root)), &source); e != nil {
		t.Fatal(e)
	}
	if len(source.Operations) != 3 || source.Invalid != "ff2e6d6b76" || source.Replacement != "efbfbd2e6d6b76" {
		t.Fatal("source filename identity observation incomplete")
	}
	first := source.Operations[0]
	out, e := sharedLocalStorageCall(t, worker, plugins.LocalStorageListCapability, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, first.Input)})
	if e == nil {
		t.Errorf("native byte filename was not rejected before lossy JSON path conversion: %s", out)
	}
	for _, op := range source.Operations[1:] {
		capability := map[string]string{"exists": plugins.LocalStorageExistsCapability, "list_files": plugins.LocalStorageListCapability}[op.Method]
		out, e := sharedLocalStorageCall(t, worker, capability, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, op.Input)})
		if e != nil {
			t.Fatal("ordinary valid filename recovery", e)
		}
		assertSharedLocalJSON(t, out, op.Returned)
	}
	for path, content := range owned {
		if b, e := os.ReadFile(path); e != nil || string(b) != content {
			t.Fatal("read-only fixture changed a file", e)
		}
	}
	t.Log("only list/exists ran; both distinct native filenames and pristine contents retained")
}
