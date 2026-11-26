package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Check the kernel subscription directly so the time measurement starts after
// installation, without generating synthetic "ready" events in production code.
func waitForDirectoryWatch(t *testing.T, root string) {
	t.Helper()
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	needle := fmt.Sprintf(" ino:%x ", info.Sys().(*syscall.Stat_t).Ino)
	waitDirectoryCondition(t, "inotify watch registration", func() bool {
		fds, err := os.ReadDir("/proc/self/fdinfo")
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range fds {
			b, _ := os.ReadFile(filepath.Join("/proc/self/fdinfo", fd.Name()))
			if strings.Contains(string(b), "inotify wd:") && strings.Contains(string(b), needle) {
				return true
			}
		}
		return false
	})
}

func waitDirectoryCondition(t *testing.T, message string, ready func() bool) {
	t.Helper()
	end := time.Now().Add(8 * time.Second)
	for time.Now().Before(end) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", message)
}

func TestDirectoryPollWaitsForIdleWriter(t *testing.T) {
	a, rule, source := directoryFileFixture(t, true)
	path := directoryMovieFile(t, source)
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	directoryPoll(t, a, rule)
	directoryAge(t, a, rule, path)
	directoryPoll(t, a, rule)
	if observed := directoryObservation(t, a, rule, path); observed.JobID != "" || observed.State != "observing" {
		t.Fatal("idle writer was admitted by the stability sweep", observed)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	directoryPoll(t, a, rule)
	if observed := directoryObservation(t, a, rule, path); observed.JobID == "" || observed.State != "queued" {
		t.Fatal("sweep did not admit the closed file", observed)
	}
}

func TestDirectoryNativeMoveAndCloseWrite(t *testing.T) {
	for _, tc := range []string{"moved-tree", "rename-open", "write-in-place"} {
		t.Run(tc, func(t *testing.T) {
			a, rule, source := directoryFileFixture(t, false)
			directoryPoll(t, a, rule)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() { defer close(done); a.watchDirectoryRule(ctx, rule, source) }()
			defer func() { cancel(); <-done }()
			waitForDirectoryWatch(t, source)
			dir := filepath.Join(t.TempDir(), "pack")
			if tc == "write-in-place" {
				dir = filepath.Join(source, "pack")
			}
			p := directoryMovieFile(t, dir)
			targetDir := filepath.Join(source, "pack")
			dest := filepath.Join(targetDir, filepath.Base(p))
			var writer *os.File
			var err error
			if tc != "moved-tree" {
				writer, err = os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer writer.Close()
			}
			start := time.Now()
			if tc != "write-in-place" {
				if err := os.Rename(dir, targetDir); err != nil {
					t.Fatal(err)
				}
			}
			if writer != nil {
				waitDirectoryCondition(t, "observation of file held open by writer", func() bool {
					var observed DirectoryObservedFile
					return a.Store.Get(ctx, "directory_file", directoryFileID(rule.ID, dest), &observed) == nil
				})
				if f := directoryObservation(t, a, rule, dest); f.JobID != "" {
					t.Fatalf("queued while writer open: %+v", f)
				}
				if _, err := writer.Write([]byte("completed bytes")); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			waitDirectoryCondition(t, "a real queued job", func() bool {
				var f DirectoryObservedFile
				return a.Store.Get(ctx, "directory_file", directoryFileID(rule.ID, dest), &f) == nil && f.JobID != "" && f.State == "queued"
			})
			t.Logf("native discovery through durable enqueue: %s", time.Since(start))
			f := directoryRecognize(t, a, rule, dest)
			reorgRunJob(t, a, reorgJob(t, a, "import", f.PlanID))
			var plan ImportPlan
			if err := a.Store.Get(ctx, "import_plan", f.PlanID, &plan); err != nil || len(plan.Files) != 1 {
				t.Fatal(plan, err)
			}
			srcInfo, err := os.Stat(dest)
			if err != nil {
				t.Fatal(err)
			}
			dstInfo, err := os.Stat(plan.Files[0].Target)
			if err != nil || !os.SameFile(srcInfo, dstInfo) {
				t.Fatal("target is not a hard link", err)
			}
			if err := a.inspectDirectoryFiles(ctx, rule, []directoryWatchEvent{{path: dest}}); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := a.Store.DB.QueryRow("SELECT count(*) FROM transferhistory WHERE src=?", dest).Scan(&count); err != nil || count != 1 {
				t.Fatal("duplicate histories", count, err)
			}
			var id string
			if err := a.Store.DB.QueryRow("SELECT id FROM transferhistory WHERE src=?", dest).Scan(&id); err != nil {
				t.Fatal(err)
			}
			removeDirectoryHistoryForTest(t, a, id, false)
			if err := a.inspectDirectoryFiles(ctx, rule, []directoryWatchEvent{{path: dest}}); err != nil {
				t.Fatal(err)
			}
			if f := directoryObservation(t, a, rule, dest); f.JobID != "" || f.State != "complete" {
				t.Fatal("deleted history replayed", f)
			}
			if _, err := os.Stat(plan.Files[0].Target); errors.Is(err, os.ErrNotExist) {
				t.Fatal("record-only delete removed the file")
			}
		})
	}
}
