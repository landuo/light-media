package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalTransferHistoryCommitPrecedesBroadcastAndCleanupRetry(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	input := filepath.Join(source, "Owned.2024.mkv")
	importWrite(t, input, []byte("owned committed video"))
	full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	id := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"mediainfo": full})
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: identity.MediaType, TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatal("actual preview", err)
	}
	_, err = a.Store.DB.Exec(`CREATE TRIGGER owned_transfer_after_commit BEFORE UPDATE ON records WHEN NEW.kind='directory_overwrite' AND EXISTS(SELECT 1 FROM records WHERE kind='asset') BEGIN SELECT RAISE(ABORT,'owned post-commit cleanup failure');END`)
	if err != nil {
		t.Fatal(err)
	}
	job := overwriteStart(t, a, plan)
	if err := a.runImport(context.Background(), job, plan.ID, nil); err == nil {
		t.Fatal("actual post-commit SQL failure was not reached")
	}
	a.wg.Wait()
	var actual ImportPlan
	if err := a.Store.Get(context.Background(), "import_plan", plan.ID, &actual); err != nil || actual.Files[0].Status != "imported" {
		t.Fatal("committed transfer incorrectly became a failed transfer", actual, err)
	}
	if bytes, err := os.ReadFile(actual.Files[0].Target); err != nil || string(bytes) != "owned committed video" {
		t.Fatal("actual committed target")
	}
	count := func() (int, int) {
		var rows []struct{ Type string }
		response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
		if json.Unmarshal(response.Body.Bytes(), &rows) != nil {
			t.Fatal("real transfer trace")
		}
		complete, failed := 0, 0
		for _, row := range rows {
			if row.Type == "transfer.complete" {
				complete++
			}
			if row.Type == "transfer.failed" {
				failed++
			}
		}
		return complete, failed
	}
	if complete, failed := count(); complete != 1 || failed != 0 {
		t.Fatal("history success broadcast order", complete, failed)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_transfer_after_commit`); err != nil {
		t.Fatal(err)
	}
	if err := a.runImport(context.Background(), job, plan.ID, nil); err != nil {
		t.Fatal("actual cleanup recovery", err)
	}
	a.wg.Wait()
	if complete, failed := count(); complete != 1 || failed != 0 {
		t.Fatal("cleanup retry replayed successful transfer", complete, failed)
	}
}
