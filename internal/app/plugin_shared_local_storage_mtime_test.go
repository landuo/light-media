package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"light-media/internal/plugins"
)

// Two ordinary, filesystem-supported finite timestamps on the same owned copy;
// no permission, link, file-identity replacement or concurrency manipulation.
func TestSharedLocalStorageOrdinaryMTimeSourceContract(t *testing.T) {
	a, _, worker := sharedLocalStorageFixture(t, sharedLocalStorageAll)
	root := sharedLocalStorageRoot(t, a)
	path := filepath.Join(root, "timestamp.mkv")
	content := []byte("owned ordinary timestamp content")
	if e := sharedLocalStorageFixtureWrite(t, path, content, 0600); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../../testdata/plugin-local-storage-20261030/mtime-source.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Timestamps []struct {
			Label       string `json:"label"`
			Seconds     int64  `json:"seconds"`
			Nanoseconds int64  `json:"nanoseconds"`
		} `json:"timestamps"`
		Operations []struct {
			Case     string          `json:"case"`
			Method   string          `json:"method"`
			Input    json.RawMessage `json:"input"`
			Returned json.RawMessage `json:"returned"`
		} `json:"operations"`
	}
	if e := json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "$ROOT", root)), &source); e != nil {
		t.Fatal(e)
	}
	if len(source.Timestamps) != 2 || len(source.Operations) != 4 {
		t.Fatal("source timestamp matrix incomplete")
	}
	for i, phase := range source.Timestamps {
		stamp := time.Unix(phase.Seconds, phase.Nanoseconds)
		var timeErr error
		if phase.Seconds > 9223372036 {
			// Go 1.26 os.Chtimes itself converts via UnixNano. Use the same
			// timestamp syscall with an explicit seconds/nsec pair for this
			// source-supported date, rather than overflowing the test setup.
			ts := unix.Timespec{Sec: phase.Seconds, Nsec: phase.Nanoseconds}
			timeErr = unix.UtimesNanoAt(unix.AT_FDCWD, path, []unix.Timespec{ts, ts}, 0)
		} else {
			timeErr = os.Chtimes(path, stamp, stamp)
		}
		if e := timeErr; e != nil {
			t.Fatal(e)
		}
		stat, e := os.Stat(path)
		if e != nil || stat.ModTime().Unix() != phase.Seconds || int64(stat.ModTime().Nanosecond()) != phase.Nanoseconds {
			t.Fatal("owned filesystem did not preserve source timestamp", e)
		}
		for _, op := range source.Operations[i*2 : i*2+2] {
			t.Run(op.Case, func(t *testing.T) {
				capability := map[string]string{"exists": plugins.LocalStorageExistsCapability, "list_files": plugins.LocalStorageListCapability}[op.Method]
				out, e := sharedLocalStorageCall(t, worker, capability, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, op.Input)})
				if e != nil {
					t.Fatal(e)
				}
				assertSharedLocalJSON(t, out, op.Returned)
				t.Logf("actual source-bound %s result=%s", op.Case, out)
			})
		}
		if b, e := os.ReadFile(path); e != nil || string(b) != string(content) {
			t.Fatal("ordinary timestamp update changed content", e)
		}
	}
}
