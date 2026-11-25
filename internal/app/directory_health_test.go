package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryHealthPreservesWindowAndSamples(t *testing.T) {
	a, _, _ := importTestApp(t)
	ctx := t.Context()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 512; i++ {
		key := fmt.Sprintf("observation-%03d", i)
		rec := DirectoryObservedFile{ID: key, DirectoryID: "test-rule", LastSeen: fmt.Sprintf("%03d", i), Path: filepath.Join("/example", key), State: "imported"}
		if i%7 == 0 {
			rec.State = "error"
			rec.Reason = strings.Repeat("集", 81)
		}
		if i == 511 {
			rec.DirectoryID = "other-rule"
		}
		if err := txPut(ctx, tx, "directory_file", key, rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := a.directoryHealth(ctx, "test-rule")
	if err != nil {
		t.Fatal(err)
	}
	counts := got["counts"].(map[string]int)
	// 11..510, multiples of seven 14..504 inclusive.
	if counts["error"] != 71 || counts["imported"] != 429 || len(counts) != 2 {
		t.Fatalf("unexpected counts: %v", counts)
	}
	pending := got["pending"].([]map[string]any)
	if len(pending) != 5 {
		t.Fatalf("expected 5 samples: %v", pending)
	}
	for i, p := range pending {
		if p["file"] != fmt.Sprintf("observation-%03d", 504-7*i) || p["state"] != "error" || p["reason"] != strings.Repeat("集", 80) {
			t.Fatalf("sample %d: %v", i, p)
		}
	}
}

func TestHealthMemoryView(t *testing.T) {
	a := newTestApp(t)
	w := callTest(t, a.Handler(), "GET", "/healthz?view=memory", "", nil, "")
	var response struct {
		Data struct {
			Status  string
			Memory  map[string]uint64
			Plugins *int `json:"plugins_loaded"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.Data.Status != "ok" || response.Data.Plugins != nil {
		t.Fatalf("unexpected memory probe: %v %d %s", err, w.Code, w.Body)
	}
	for _, key := range []string{"heap_live", "heap_alloc", "go_memory_limit", "gogc", "num_gc", "num_forced_gc", "total_alloc"} {
		if _, ok := response.Data.Memory[key]; !ok {
			t.Fatalf("missing %s", key)
		}
	}
}
