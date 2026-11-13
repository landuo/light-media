package app

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryDeferredCapabilitiesKeepDurableRowsAndStrictActions(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	plan, _ := historyParityPlan(t, a, source, target, "deferred", "Offline source", "failed", "error", "one.mkv")
	if err := os.Remove(plan.Files[0].Source); err != nil {
		t.Fatal(err)
	}
	// Browsing missing/offline sources must not require revalidation of the
	// files. Both initial page and subsequent polls retain the durable record.
	for _, query := range []string{"capabilities=deferred", "capabilities=deferred&status=failed", "capabilities=deferred&page_size=1000&q=Offline"} {
		page := historyParityReadPage(t, a, query)
		if page.Total != 1 || len(page.Items) != 1 {
			t.Fatalf("lost offline history: %+v", page)
		}
		item := page.Items[0]
		if !item.CapabilitiesDeferred || item.CanRetry || item.CanRecognize || item.Status != "failed" {
			t.Fatalf("unchecked capabilities presented as verified: %+v", item)
		}
	}
	// Existing clients retain the original checked contract unless they opt in.
	legacy := historyUpsertOnly(t, a, "")
	if legacy.CapabilitiesDeferred || legacy.CanRetry || legacy.CanRecognize {
		t.Fatalf("legacy capabilities changed: %+v", legacy)
	}
	// Query flags must never bypass the strict single-item/operation checks.
	request := httptest.NewRequest("POST", "/api/history/transfers/"+legacy.ID+"/retry?capabilities=deferred", strings.NewReader(`{"confirm":true}`)).WithContext(ctx)
	if _, err := a.transferHistoryAPI(request, "transfers/"+legacy.ID+"/retry"); fileCode(err) != "TRANSFER_RETRY_UNAVAILABLE" {
		t.Fatalf("missing source retry accepted: %v", err)
	}
	detail, err := a.transferHistoryItem(ctx, legacy.ID)
	if err != nil || detail.CapabilitiesDeferred || detail.CanRecognize || detail.CanRetry {
		t.Fatalf("single-item lookup bypassed checks: %+v %v", detail, err)
	}
}

func TestHistoryDeferredDoesNotRequireDirectorySnapshot(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	plan, _ := historyParityPlan(t, a, source, target, "deep-directory", "Directory", "succeeded", "imported", "one.mkv")
	root := filepath.Dir(plan.Files[0].Source)
	// Deliberately exceed the recursive snapshot depth. This is valid durable
	// history even when the present-day directory cannot be safely snapshotted.
	if err := os.MkdirAll(filepath.Join(root, strings.Repeat("nested/", 18)), 0700); err != nil {
		t.Fatal(err)
	}
	plan.Files[0].Source = root
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotTransferPath(root); fileCode(err) != "FILE_SELECTION_LIMIT" {
		t.Fatalf("fixture must reject recursive snapshot: %v", err)
	}
	item := historyUpsertOnly(t, a, "capabilities=deferred")
	if !item.CapabilitiesDeferred || item.Source != root || item.Status != "success" {
		t.Fatalf("directory history lost without snapshot: %+v", item)
	}
	// A later explicit detail check is still strict; it cannot report this
	// directory as already validated merely because the list displayed it.
	detail, err := a.transferHistoryItem(ctx, item.ID)
	if err != nil || detail.CapabilitiesDeferred || detail.CanRecognize {
		t.Fatalf("directory capability skipped on detail: %+v %v", detail, err)
	}
}

// Synthetic local-disk comparison, not a NAS benchmark. Both variants read the
// same 50-row page; only the legacy variant visits 3,200 directory members.
func BenchmarkHistoryDirectoryPageCapabilities(b *testing.B) {
	a, err := New(Options{DataDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	defer a.Close()
	source := b.TempDir()
	a.config = Settings{AllowedRoots: []string{source}}
	ctx := context.Background()
	plan := ImportPlan{ID: "directory-benchmark", CreatedAt: now(), State: "complete", Mode: "copy"}
	for i := 0; i < 50; i++ {
		path := filepath.Join(source, fmt.Sprintf("disc-%02d", i))
		if err := os.Mkdir(path, 0700); err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 64; j++ {
			if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("member-%02d.m2ts", j)), []byte("synthetic"), 0600); err != nil {
				b.Fatal(err)
			}
		}
		plan.Files = append(plan.Files, PlanFile{ID: fmt.Sprintf("file-%02d", i), Source: path, Status: "imported"})
	}
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		b.Fatal(err)
	}
	job, err := a.enqueue(ctx, "import", map[string]string{"id": plan.ID}, "")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); err != nil {
		b.Fatal(err)
	}
	// Warm the index before measuring either variant.
	if _, err := a.transferHistory(ctx, httptest.NewRequest("GET", "/api/history/transfers?capabilities=deferred", nil)); err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"checked", "deferred"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result, err := a.transferHistory(ctx, httptest.NewRequest("GET", "/api/history/transfers?capabilities="+mode, nil))
				if err != nil {
					b.Fatal(err)
				}
				items := result.(map[string]any)["items"].([]transferHistoryItem)
				if len(items) != 50 {
					b.Fatalf("page size: %d", len(items))
				}
			}
		})
	}
}
