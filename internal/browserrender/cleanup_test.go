package browserrender

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestProfileCleanupRetriesActualOwnedDirectoryAfterLateWrite(t *testing.T) {
	// This is the actual paired dependency's removeFolders, captured from a
	// disposable read-only dependency image, rather than the new Go algorithm.
	oracle := os.Getenv("BROWSER_CLEANUP_SOURCE_ORACLE")
	if oracle == "" {
		oracle = "../../testdata/plugin-platform-final-20261104/browser-ocr/playwright-cleanup-source.json"
	}
	raw, err := os.ReadFile(oracle)
	if err != nil {
		t.Fatal(err)
	}
	var source []struct {
		Body string `json:"body"`
	}
	if json.Unmarshal(raw, &source) != nil || len(source) != 1 || !strings.Contains(source[0].Body, "maxRetries: 10") {
		t.Fatal("paired Playwright cleanup source missing")
	}
	profile := filepath.Join(t.TempDir(), "owned-browser-profile")
	if err = os.MkdirAll(filepath.Join(profile, "Default"), 0700); err != nil {
		t.Fatal(err)
	}
	late := filepath.Join(profile, "Default", ".org.chromium.Chromium.owned")
	attempts := 0
	err = removeBrowserProfile(profile, func(path string) error {
		if path != profile {
			t.Fatal("cleanup escaped the owned profile")
		}
		attempts++
		if attempts == 1 {
			// Simulate the observed Chromium final atomic-write race at the
			// filesystem boundary, leaving a real file for the retry to remove.
			if err := os.WriteFile(late, []byte("owned preferences"), 0600); err != nil {
				t.Fatal(err)
			}
			return &os.PathError{Op: "remove", Path: path, Err: syscall.ENOTEMPTY}
		}
		return os.RemoveAll(path)
	})
	if err != nil || attempts != 2 {
		t.Fatalf("late-write cleanup was not retried: calls=%d err=%v", attempts, err)
	}
	if _, err = os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned profile remains", err)
	}
}

func TestProfileCleanupPropagatesPermanentFailure(t *testing.T) {
	calls := 0
	err := removeBrowserProfile("owned-only", func(string) error { calls++; return &os.PathError{Op: "remove", Path: "owned-only", Err: syscall.EIO} })
	if calls != 1 || !errors.Is(err, syscall.EIO) {
		t.Fatal("permanent cleanup failure was hidden", calls, err)
	}
}

func TestProfileCleanupRetainsRevocationAndFirstFailure(t *testing.T) {
	owned := errors.New("owned scope revoked")
	primary := &GuardError{Cause: owned}
	cleanup := removeBrowserProfile("owned-only", func(string) error { return syscall.EIO })
	combined := browserProfileCleanupOutcome(primary, cleanup)
	var revoked *GuardError
	var diagnostic *Error
	if !errors.As(combined, &revoked) || !errors.Is(combined, owned) || !errors.As(combined, &diagnostic) || diagnostic.Code != "browser_profile_cleanup_failed" {
		t.Fatal("cleanup replaced the host's scope rejection", combined)
	}
	if browserProfileCleanupOutcome(primary, nil) != primary {
		t.Fatal("successful cleanup changed the first failure")
	}
	if browserProfileCleanupOutcome(nil, nil) != nil {
		t.Fatal("successful cleanup manufactured a failure")
	}
}
