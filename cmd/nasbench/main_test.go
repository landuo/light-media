package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const passedJSON = `{"status":"passed","evidence":"synthetic_local_filesystem","driver":"modernc","sqlite_version":"3.53.4"}`

func TestBenchmarkSummaryValidation(t *testing.T) {
	for _, b := range []string{"not-json", passedJSON + "{}", strings.Replace(passedJSON, "passed", "failed", 1), strings.Replace(passedJSON, "modernc", "mattn", 1)} {
		if _, err := parseBenchmark([]byte(b)); err == nil {
			t.Errorf("invalid summary accepted: %s", b)
		}
	}
	if _, err := parseBenchmark([]byte(passedJSON + "\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := parseBenchmark([]byte(strings.Repeat("x", jsonLimit+1))); err == nil {
		t.Fatal("oversized JSON accepted")
	}
}

func TestOutputCaptureIsBounded(t *testing.T) {
	b := boundedBuffer{limit: 4}
	if _, err := b.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Write([]byte("de")); err == nil || n != 1 {
		t.Fatalf("want one-byte partial write and limit error: %d %v", n, err)
	}
	if b.buf.String() != "abcd" {
		t.Fatal("capture grew beyond limit")
	}
}

func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bench")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestChildUsesOnlyFixedSyntheticArguments(t *testing.T) {
	path := script(t, "[ \"$#\" -eq 6 ] || exit 11\n[ \"$1\" = '--driver' ] && [ \"$2\" = 'modernc' ] && [ \"$3\" = '--temp-parent' ] && [ \"$4\" = '/data' ] && [ \"$5\" = '--timeout' ] && [ \"$6\" = '90s' ] || exit 12\nprintf '%s\\n' '"+passedJSON+"'")
	r, err := run(context.Background(), path)
	if err != nil || r.Status != "passed" || r.Runtime != "single_synthetic_run" {
		t.Fatalf("%+v %v", r, err)
	}
	if string(r.Benchmark) != passedJSON {
		t.Fatal("benchmark JSON changed")
	}
	if r.WrapperSelfStatus["scope"].Value != "wrapper_only_not_sqlitebench_or_process_tree" {
		t.Fatal("wrapper RSS scope missing")
	}
}

func TestChildFailureDoesNotBecomePassedBenchmark(t *testing.T) {
	path := script(t, "printf '%s\\n' '"+passedJSON+"'\nexit 7")
	r, err := run(context.Background(), path)
	if err == nil || r.Status != "failed" || len(r.Benchmark) != 0 || !strings.Contains(r.Error, "exit code 7") {
		t.Fatalf("failure not preserved: %+v %v", r, err)
	}
	r, err = run(context.Background(), script(t, "printf '%s\\n' 'not-json'"))
	if err == nil || r.Status != "failed" {
		t.Fatal("invalid child JSON accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r, err = run(ctx, script(t, "exec sleep 5"))
	if err == nil || r.Status != "failed" {
		t.Fatal("timed-out child accepted")
	}
}
