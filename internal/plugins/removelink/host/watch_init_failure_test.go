//go:build linux

package cleanuphost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/plugins/removelink"
)

// This is a real native add-watch syscall failure on owned temporary directories,
// not a claim that a machine's inotify resource limit was actually exhausted.
// Unlike the Python source's partial-watch continuation, the native adapter must
// release the entire partially initialized batch and fail closed.
func TestWatchInitFailureNativePartialBatchClosesAllDescriptors(t *testing.T) {
	if (&WatchInitFailure{}).Valid() {
		t.Fatal("zero failure must not carry native provenance")
	}
	for _, mode := range []string{"hardlink", "strm"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			specs := []removelink.Watch{{Path: filepath.Join(root, "first"), Mode: "hardlink"}, {Path: filepath.Join(root, "failed"), Mode: mode}, {Path: filepath.Join(root, "unattempted"), Mode: mode}}
			for _, s := range specs {
				if err := os.Mkdir(s.Path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			count := func() int {
				rows, err := os.ReadDir("/proc/self/fd")
				if err != nil {
					t.Fatal(err)
				}
				n := 0
				for _, row := range rows {
					dest, _ := os.Readlink(filepath.Join("/proc/self/fd", row.Name()))
					if dest == "anon_inode:inotify" {
						n++
					}
				}
				return n
			}
			before := count()
			var opened []string
			w, err := NativeWatch(context.Background(), specs, func(path string) (*os.File, error) {
				opened = append(opened, path)
				f, e := os.Open(path)
				if e == nil && path == specs[1].Path {
					e = f.Close()
				}
				return f, e
			})
			if w != nil {
				_ = w.Close()
				t.Fatal("partial batch returned a live watcher")
			}
			var failure *WatchInitFailure
			if !errors.As(err, &failure) || !failure.Valid() {
				t.Fatalf("native failure lacks provenance: %v", err)
			}
			if failure.Spec() != specs[1] || failure.Ordinal() != 1 {
				t.Fatalf("wrong failed spec: %#v ordinal=%d", failure.Spec(), failure.Ordinal())
			}
			if failure.Stage() == "" || failure.Unwrap() == nil {
				t.Fatal("lost stage/cause")
			}
			if !reflect.DeepEqual(opened, []string{specs[0].Path, specs[1].Path}) {
				t.Fatalf("attempted wrong batch entries: %v", opened)
			}
			if after := count(); after != before {
				t.Fatalf("native inotify descriptor leak: before=%d after=%d", before, after)
			}
			copied := failure.Spec()
			copied.Path = "/forged"
			if failure.Spec() != specs[1] {
				t.Fatal("Spec exposed mutable native attribution")
			}
		})
	}
}
