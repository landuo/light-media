package browserrender

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNativeDisplayRetainsPairedReadinessBudgetAndJoins(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the source Docker display is Linux-only")
	}
	oracle := os.Getenv("BROWSER_DISPLAY_SOURCE_ORACLE")
	if oracle == "" {
		oracle = "../../testdata/plugin-platform-final-20261104/browser-ocr/virtual-display-source.json"
	}
	raw, err := os.ReadFile(oracle)
	if err != nil || !strings.Contains(string(raw), "start_time >= 600") {
		t.Fatal("paired 600s displayfd readiness source missing", err)
	}
	real, err := exec.LookPath("Xvfb")
	if err != nil {
		t.Fatal("actual Xvfb required", err)
	}
	directory := t.TempDir()
	pidfile := filepath.Join(directory, "owned-xvfb-pid")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	program := "#!/bin/sh\nprintf '%s\\n' \"$$\" > " + quote(pidfile) + "\nsleep 12\nexec " + quote(real) + " \"$@\"\n"
	if err = os.WriteFile(filepath.Join(directory, "Xvfb"), []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DISPLAY", "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	began := time.Now()
	display, cleanup, err := nativeDisplay(ctx)
	if err != nil {
		t.Fatalf("source-allowed display readiness after12s rejected: %v elapsed=%s", err, time.Since(began))
	}
	defer cleanup()
	if !strings.HasPrefix(display, ":") || time.Since(began) < 12*time.Second {
		t.Fatal("actual delayed display did not become ready", display)
	}
	pidraw, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidraw)))
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("owned Xvfb remained after cleanup/join", err)
	}
}
