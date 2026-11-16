package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryBatchContinuesAfterMissingAndUnavailableRows(t *testing.T) {
	for _, preview := range []bool{true, false} {
		t.Run(map[bool]string{true: "preview", false: "execute"}[preview], func(t *testing.T) {
			a, source, target := importTestApp(t)
			ctx := context.Background()
			media := importFixture(t, a, "movie")
			// A legacy successful history with no remaining asset cannot be reorganized.
			unavailable, _ := historyParityPlan(t, a, source, target, "unavailable", "Old Film", "succeeded", "imported", "Old.mkv")
			path := filepath.Join(source, "Selected.mkv")
			importWrite(t, path, []byte("selected synthetic video bytes"))
			job, err := a.recordManualOrganizeFailure(ctx, path, id("wf_"), bad("MEDIA_NOT_FOUND", "old identification failure"))
			if err != nil {
				t.Fatal(err)
			}
			old, _ := historyUpsertAttempt(t, a, job.ID)
			untouched, _ := historyParityPlan(t, a, source, target, "unselected", "Unselected", "failed", "error", "NotSelected.mkv")
			pageIDs := map[string]string{}
			for _, item := range historyParityReadPage(t, a, "page_size=1000").Items {
				pageIDs[item.FileID] = item.ID
			}
			request := manualOrganizeRequest{HistoryIDs: []string{"missing-row", pageIDs[unavailable.Files[0].ID], pageIDs[old.Files[0].ID], pageIDs[old.Files[0].ID]}, MediaType: "movie", TMDBID: media.ID, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false), Preview: preview}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			value, err := a.fileManagerAPI(httptest.NewRequest("POST", "/api/files/organize", strings.NewReader(string(raw))), "organize")
			if err != nil {
				t.Fatalf("one unavailable selection aborted the batch: %v", err)
			}
			out := value.(manualOrganizeOutput)
			if out.Total != 3 || out.Failed != 2 || len(out.Items) != 3 {
				t.Fatalf("missing independent outcomes or duplicate submitted: %+v", out)
			}
			if out.Items[0].ErrorCode != "HISTORY_NOT_FOUND" || out.Items[2].Source != path {
				t.Fatalf("selection order changed: %+v", out)
			}
			var oldPlan ImportPlan
			if err := a.Store.Get(ctx, "import_plan", untouched.ID, &oldPlan); err != nil || oldPlan.Files[0].Status != "error" {
				t.Fatalf("unselected history was changed: %+v %v", oldPlan, err)
			}
			if preview {
				if out.Submitted != 0 || out.Items[2].Status != "ready" || out.Items[2].JobID != "" {
					t.Fatalf("preview submitted work: %+v", out)
				}
				page := historyParityReadPage(t, a, "capabilities=deferred")
				if page.Total != 3 {
					t.Fatalf("preview changed durable history count: %d", page.Total)
				}
			} else {
				if out.Submitted != 1 || out.Items[2].JobID == "" {
					t.Fatalf("valid peer was not submitted: %+v", out)
				}
				plan, nextJob := historyUpsertAttempt(t, a, out.Items[2].JobID)
				reorgRunJob(t, a, nextJob)
				src, e1 := os.Stat(path)
				dst, e2 := os.Stat(plan.Files[0].Target)
				if e1 != nil || e2 != nil || !os.SameFile(src, dst) {
					t.Fatalf("selected peer did not finish a real hardlink: %v %v", e1, e2)
				}
				var updated transferHistoryItem
				for _, item := range historyParityReadPage(t, a, "page_size=1000").Items {
					if item.Source == path {
						updated = item
					}
				}
				if updated.ID == "" || updated.ID == pageIDs[old.Files[0].ID] || updated.Status != "success" {
					t.Fatalf("MP history did not replace the selected row: %+v", updated)
				}
			}
		})
	}
}
