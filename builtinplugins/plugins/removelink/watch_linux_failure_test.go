//go:build linux

package removelink

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Installed watchdog6 Inotify._raise_error maps the two native limit errors;
// installed RemoveLink init_plugin catches start errors per directory. ENOSYS
// occurs after observer selection and must never silently enable polling.
func TestLinuxSourceObserverStartFailure(t *testing.T) {
	for _, sample := range []struct {
		name    string
		errno   syscall.Errno
		message string
	}{
		{"unsupported", syscall.ENOSYS, "function not implemented"},
		{"watch_limit", syscall.ENOSPC, "[Errno 28] inotify watch limit reached"},
		{"instance_limit", syscall.EMFILE, "[Errno 24] inotify instance limit reached"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			calls := 0
			watcher, err := sourceObserverWithInit(Watch{Path: t.TempDir(), Mode: "hardlink"}, func(flags int) (int, error) {
				calls++
				if flags != syscall.IN_NONBLOCK|syscall.IN_CLOEXEC {
					t.Fatal("native descriptor flags", flags)
				}
				return -1, sample.errno
			})
			if watcher != nil {
				watcher.Close()
				t.Fatal("native start failure silently switched observer")
			}
			if calls != 1 || !errors.Is(err, sample.errno) || err.Error() != sample.message {
				t.Fatalf("native start error: calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestLinuxSourceObserverActualNativeDelete(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "owned-native-delete.mkv")
	if err := os.WriteFile(path, []byte("owned inotify fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	watcher, err := sourceObserver(Watch{Path: folder, Mode: "hardlink"})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-watcher.Events():
			if !ok || event.Err != nil {
				t.Fatal("native observer ended before event", event.Err)
			}
			if event.Kind == "deleted" && event.Path == path && !event.Directory {
				return
			}
		case <-deadline.C:
			t.Fatal("real native deletion was not observed")
		}
	}
}
