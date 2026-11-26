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

func TestDirectoryHeartbeatUpdatesOnlyExistingOlderObservations(t *testing.T) {
	a := newTestApp(t)
	ctx := t.Context()
	const before = "2026-01-01T00:00:00Z"
	const at = "2026-01-01T00:05:00Z"
	const later = "2026-01-01T01:00:00Z"
	var ids []string
	for i := range 66 {
		id := fmt.Sprintf("heartbeat-%d", i)
		ids = append(ids, id)
		if i == 65 {
			continue // removed before the batch was flushed
		}
		f := DirectoryObservedFile{ID: id, State: "imported", PlanID: "completed-plan", LastSeen: before}
		if i == 0 {
			f.LastSeen = later
		}
		if err := a.Store.Put(ctx, "directory_file", id, f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Store.DB.ExecContext(ctx, "UPDATE directory_file SET updated_at=? WHERE state='imported'", later); err != nil {
		t.Fatal(err)
	}
	if err := a.touchDirectoryObservations(ctx, ids, at); err != nil {
		t.Fatal(err)
	}
	var advanced, newer, changed, resurrected int
	err := a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE last_seen=?),
		count(*) FILTER (WHERE last_seen=?),
		count(*) FILTER (WHERE state!='imported' OR json_extract(payload,'$.plan_id')!='completed-plan' OR updated_at<=?),
		count(*) FILTER (WHERE id=?) FROM directory_file`, at, later, at, ids[65]).Scan(&advanced, &newer, &changed, &resurrected)
	if err != nil || advanced != 64 || newer != 1 || changed != 0 || resurrected != 0 {
		t.Fatalf("heartbeat advanced=%d newer=%d changed=%d resurrected=%d: %v", advanced, newer, changed, resurrected, err)
	}
}
