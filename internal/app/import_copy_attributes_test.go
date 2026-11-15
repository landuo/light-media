package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestImportCopyAndMoveUseSourceTimesAndDestinationPermissions(t *testing.T) {
	for _, method := range []string{"copy", "move"} {
		t.Run(method, func(t *testing.T) {
			a, source, target := importTestApp(t)
			input, output := filepath.Join(source, "attributes.mkv"), filepath.Join(target, "attributes.mkv")
			data := []byte("copy preserves content and timestamps, not source access")
			importWrite(t, input, data)
			if err := os.Chmod(input, 0700); err != nil {
				t.Fatal(err)
			}
			modified := time.Unix(1700000000, 123456789)
			if err := os.Chtimes(input, modified.Add(-time.Hour), modified); err != nil {
				t.Fatal(err)
			}
			original, err := snapshotPath(input)
			if err != nil {
				t.Fatal(err)
			}
			// Compare ordinary destination creation under the current process umask.
			// Never change the process-global umask just for this test.
			probe, err := os.OpenFile(filepath.Join(target, "permission-probe"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := probe.Stat()
			probe.Close()
			if err != nil {
				t.Fatal(err)
			}
			a.importSourceLink = func(int, string, int, string, int) error { return unix.EXDEV }
			receipt, err := a.publishImport(t.Context(), "copy-attributes", input, output, original, method, t.Context().Err)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := os.Stat(output)
			if err != nil {
				t.Fatal(err)
			}
			if actual.ModTime().UnixNano() != original.ModNS || actual.Mode().Perm() != expected.Mode().Perm() || actual.Mode().Perm()&0111 != 0 {
				t.Fatalf("copy attributes: mtime=%v want=%v mode=%o want=%o", actual.ModTime(), modified, actual.Mode().Perm(), expected.Mode().Perm())
			}
			got, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("copy contents", err)
			}
			if method == "move" {
				if receipt.Move.Publication != "copy" || receipt.Move.State != "completed" {
					t.Fatal(receipt)
				}
				if _, err = os.Lstat(input); !os.IsNotExist(err) {
					t.Fatal("move kept source", err)
				}
			} else {
				if _, err = os.Lstat(input); err != nil {
					t.Fatal("copy removed source", err)
				}
			}
		})
	}
}
