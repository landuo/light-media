package app

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Adapted from the independent reviewer's exact six-case red driver; only the
// fixture source path/normalization and permanent root name differ. The source
// expected floats are preserved, not recalculated by the Go implementation.
// Read-only projection after timestamping only a new owned regular fixture.
func TestSharedLocalStorageOrdinaryMTimePrecisionSourceContract(t *testing.T) {
	a, _, worker := sharedLocalStorageFixture(t, []string{plugins.LocalStorageExistsCapability, plugins.LocalStorageListCapability})
	root := sharedLocalStorageRoot(t, a)
	path := filepath.Join(root, "regular.mkv")
	content := []byte("independent synthetic regular file preserved")
	if e := sharedLocalStorageFixtureWrite(t, path, content, 0600); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../../testdata/plugin-local-storage-20261030/mtime-precision-source.json")
	if e != nil {
		t.Fatal(e)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "$ROOT", root))
	var source struct {
		Operations []struct {
			Case        string          `json:"case"`
			Seconds     int64           `json:"seconds"`
			Nanoseconds int64           `json:"nanoseconds"`
			Returned    json.RawMessage `json:"returned"`
			Item        json.RawMessage `json:"get_file_item"`
		} `json:"operations"`
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	if len(source.Operations) != 6 {
		t.Fatal("independent source precision matrix incomplete")
	}
	for _, op := range source.Operations {
		t.Run(op.Case, func(t *testing.T) {
			stamp := unix.Timespec{Sec: op.Seconds, Nsec: op.Nanoseconds}
			if e := unix.UtimesNanoAt(unix.AT_FDCWD, path, []unix.Timespec{stamp, stamp}, 0); e != nil {
				t.Fatal(e)
			}
			st, e := os.Stat(path)
			if e != nil || st.ModTime().Unix() != op.Seconds || int64(st.ModTime().Nanosecond()) != op.Nanoseconds {
				t.Fatalf("timestamp setup failed %v", e)
			}
			got, e := sharedLocalStorageCall(t, worker, plugins.LocalStorageListCapability, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, map[string]any{"path": path})})
			if e != nil {
				t.Fatal(e)
			}
			var actual, want any
			if json.Unmarshal(got, &actual) != nil || json.Unmarshal(op.Returned, &want) != nil {
				t.Fatal("json")
			}
			if !reflect.DeepEqual(actual, want) {
				t.Errorf("source native list differs actual=%s source=%s", got, op.Returned)
			}
			item, e := a.originalMetadataFileItemResult(context.Background(), "local", path)
			if e != nil {
				t.Fatal(e)
			}
			if json.Unmarshal(item, &actual) != nil || json.Unmarshal(op.Item, &want) != nil {
				t.Fatal("metadata json")
			}
			if !reflect.DeepEqual(actual, want) {
				t.Errorf("shared metadata projection differs actual=%s source=%s", item, op.Item)
			}
			t.Logf("timestamp %s native=%s metadata=%s", op.Case, got, item)
		})
	}
	if actual, e := os.ReadFile(path); e != nil || string(actual) != string(content) {
		t.Fatal("content changed", e)
	}
}
