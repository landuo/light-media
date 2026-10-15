package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reorgRunJob(t *testing.T, a *App, job Job) {
	t.Helper()
	_, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.safeRun(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	_, err = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID)
	if err != nil {
		t.Fatal(err)
	}
}

func reorgJob(t *testing.T, a *App, kind, key string) Job {
	t.Helper()
	job, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind=? AND json_extract(payload,'$.id')=? ORDER BY created_at DESC LIMIT 1`, kind, key))
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func reorgFixture(t *testing.T) (*App, string, Asset) {
	t.Helper()
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	input := filepath.Join(source, "Original.Movie.2024.mkv")
	importWrite(t, input, []byte("reorganize original video"))
	importWrite(t, strings.TrimSuffix(input, ".mkv")+".zh.srt", []byte("owned subtitle"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: identity.ID, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: input}}})
	if err != nil || plan.Files[0].Status != "ready" {
		t.Fatal(plan, err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	var asset Asset
	if err = a.assetAtPath(context.Background(), plan.Files[0].Target, &asset); err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, reorgJob(t, a, "scrape", asset.ID))
	return a, input, asset
}

func TestReorganizeSameMediaMovesOwnedTargetOnlyAfterReplacementAndRetainsSource(t *testing.T) {
	a, source, old := reorgFixture(t)
	ctx := context.Background()
	target := filepath.Join(filepath.Dir(filepath.Dir(old.Path)), "new-library")
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(filepath.Dir(old.Path), "other-user.txt")
	importWrite(t, unmanaged, []byte("unrelated"))
	plan, err := a.previewImport(ctx, ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: "movie", TMDBID: old.Media.ID, TargetPath: target, Mode: "copy", Files: []ImportFileRequest{{Path: source}}})
	if err != nil || plan.Files[0].Status != "ready" {
		t.Fatalf("reorganize preview=%+v %v", plan, err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(old.Path); err != nil {
		t.Fatal("preview/queue removed old video", err)
	}
	reorgRunJob(t, a, job)
	var replacement Asset
	if err = a.assetAtPath(ctx, plan.Files[0].Target, &replacement); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err = a.Store.DB.QueryRow(`SELECT asset_id FROM asset_slots WHERE media_key=?`, mediaKey(old.Media)).Scan(&owner); err != nil || owner != replacement.ID {
		t.Fatal("slot not transferred atomically", owner, err)
	}
	if _, err = os.Stat(old.Path); err != nil {
		t.Fatal("old target removed before cleanup proof", err)
	}
	reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", plan.ID))
	if _, err = os.Stat(old.Path); !os.IsNotExist(err) {
		t.Fatal("old owned video not cleaned", err)
	}
	if _, err = os.Stat(strings.TrimSuffix(old.Path, ".mkv") + ".nfo"); !os.IsNotExist(err) {
		t.Fatal("old owned NFO not cleaned", err)
	}
	if bytes, err := os.ReadFile(source); err != nil || string(bytes) != "reorganize original video" {
		t.Fatal("download source changed", err)
	}
	if bytes, err := os.ReadFile(unmanaged); err != nil || string(bytes) != "unrelated" {
		t.Fatal("unmanaged file changed", err)
	}
	reorgRunJob(t, a, reorgJob(t, a, "scrape", replacement.ID))
	if _, id, err := readAssetNFO(strings.TrimSuffix(replacement.Path, ".mkv") + ".nfo"); err != nil || id != old.Media.ID {
		t.Fatal("replacement did not scrape", id, err)
	}
}

func TestReorganizeCorrectsIdentityInPlaceWithoutRewritingVideoAndRescrapes(t *testing.T) {
	a, source, old := reorgFixture(t)
	ctx := context.Background()
	corrected := old.Media
	corrected.ID = 456 // Same title/year deliberately produces the same target path.
	importCache(t, a, "media:movie:456", corrected)
	before, err := os.Stat(old.Path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := a.previewImport(ctx, ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: "movie", TMDBID: corrected.ID, RootID: old.RootID, Mode: "copy", Files: []ImportFileRequest{{Path: source}}})
	if err != nil || plan.Files[0].Status != "ready" || plan.Files[0].Target != old.Path {
		t.Fatalf("inplace preview=%+v %v", plan, err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	var replacement Asset
	if err = a.assetAtPath(ctx, old.Path, &replacement); err != nil || replacement.Media.ID != corrected.ID || replacement.ID == old.ID {
		t.Fatal("identity not corrected", replacement, err)
	}
	after, err := os.Stat(old.Path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("inplace operation rewrote video", err)
	}
	var oldCount int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM asset_slots WHERE media_key=?`, mediaKey(old.Media)).Scan(&oldCount)
	if oldCount != 0 {
		t.Fatal("old identity slot retained", oldCount)
	}
	reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", plan.ID))
	reorgRunJob(t, a, reorgJob(t, a, "scrape", replacement.ID))
	if _, id, err := readAssetNFO(strings.TrimSuffix(old.Path, ".mkv") + ".nfo"); err != nil || id != corrected.ID {
		t.Fatal("inplace NFO retained wrong identity", id, err)
	}
	if _, err = os.Stat(strings.TrimSuffix(old.Path, ".mkv") + ".zh.srt"); err != nil {
		t.Fatal("inplace subtitle lost", err)
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatal("original download source removed", err)
	}
}

func TestReorganizePublishConflictPreservesOriginalFilesSlotsAndForeignTarget(t *testing.T) {
	a, source, old := reorgFixture(t)
	ctx := context.Background()
	target := filepath.Join(filepath.Dir(filepath.Dir(old.Path)), "conflict-library")
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	plan, err := a.previewImport(ctx, ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: "movie", TMDBID: old.Media.ID, TargetPath: target, Mode: "copy", Files: []ImportFileRequest{{Path: source}}})
	if err != nil || plan.Files[0].Status != "ready" {
		t.Fatal(plan, err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	importWrite(t, plan.Files[0].Target, []byte("someone else's target"))
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
	job.State, job.Generation = "running", 1
	if err = a.runImport(ctx, job, plan.ID, nil); err == nil {
		t.Fatal("foreign target overwritten")
	}
	if bytes, err := os.ReadFile(plan.Files[0].Target); err != nil || string(bytes) != "someone else's target" {
		t.Fatal("foreign target changed", err)
	}
	if _, err = os.Stat(old.Path); err != nil {
		t.Fatal("old target lost after publish failure", err)
	}
	var owner string
	if err = a.Store.DB.QueryRow(`SELECT asset_id FROM asset_slots WHERE media_key=?`, mediaKey(old.Media)).Scan(&owner); err != nil || owner != old.ID {
		t.Fatal("old slot lost after failure", owner, err)
	}
	var current Asset
	if err = a.Store.Get(ctx, "asset", old.ID, &current); err != nil || current.State != "present" {
		t.Fatal("failed publication changed old asset", current, err)
	}
}

func TestReorganizeCanUsePreviousTargetWhenDownloadSourceIsGone(t *testing.T) {
	a, source, old := reorgFixture(t)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(filepath.Dir(old.Path)), "fallback-library")
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	plan, err := a.previewImport(context.Background(), ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: "movie", TMDBID: old.Media.ID, TargetPath: target, Mode: "hardlink", Files: []ImportFileRequest{{Path: old.Path}}})
	if err != nil || plan.Files[0].Status != "ready" {
		t.Fatal(plan, err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", plan.ID))
	if bytes, err := os.ReadFile(plan.Files[0].Target); err != nil || string(bytes) != "reorganize original video" {
		t.Fatal("fallback source did not survive through new target", err)
	}
}

func TestImportRequestOwnerAndManualFieldsCannotBeSuppliedByJSON(t *testing.T) {
	var request ImportRequest
	if err := json.Unmarshal([]byte(`{"manual":true,"reorganize_asset_id":"foreign-owner"}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.Manual || request.ReorganizeAssetID != "" {
		t.Fatal("client JSON supplied internal history authorization")
	}
}

func TestReorganizeCleanupRetainsOldFilesWhenReplacementChangesAndRecoversReceipt(t *testing.T) {
	for _, changeReplacement := range []bool{true, false} {
		t.Run(map[bool]string{true: "replacement-changed", false: "cleanup-replayed"}[changeReplacement], func(t *testing.T) {
			a, source, old := reorgFixture(t)
			ctx := context.Background()
			target := filepath.Join(filepath.Dir(filepath.Dir(old.Path)), "receipt-library")
			if err := os.MkdirAll(target, 0750); err != nil {
				t.Fatal(err)
			}
			plan, err := a.previewImport(ctx, ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: "movie", TMDBID: old.Media.ID, TargetPath: target, Mode: "copy", Files: []ImportFileRequest{{Path: source}}})
			if err != nil || plan.Files[0].Status != "ready" {
				t.Fatal(plan, err)
			}
			job, err := a.executeImport(ctx, plan.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			reorgRunJob(t, a, job)
			cleanupJob := reorgJob(t, a, "reorganize_cleanup", plan.ID)
			if changeReplacement {
				importWrite(t, plan.Files[0].Target, []byte("replacement externally modified"))
				_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, cleanupJob.ID)
				cleanupJob.State, cleanupJob.Generation = "running", 1
				if err = a.runReorganizeCleanup(ctx, cleanupJob, plan.ID); err == nil {
					t.Fatal("changed replacement allowed cleanup")
				}
				if _, err = os.Stat(old.Path); err != nil {
					t.Fatal("old target lost when replacement changed", err)
				}
				return
			}
			reorgRunJob(t, a, cleanupJob)
			var cleanup reorganizeCleanup
			if err = a.Store.Get(ctx, "reorganize_cleanup", plan.ID, &cleanup); err != nil {
				t.Fatal(err)
			}
			// Emulate a crash after durable removal receipt completion but before
			// its separate scrape/checkpoint transaction committed.
			cleanup.State = "pending"
			if err = a.Store.Put(ctx, "reorganize_cleanup", cleanup.ID, cleanup); err != nil {
				t.Fatal(err)
			}
			_, _ = a.Store.DB.Exec(`DELETE FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=?`, cleanup.NewAssetID)
			reorgRunJob(t, a, cleanupJob)
			reorgRunJob(t, a, cleanupJob) // A completed cleanup replay is also inert.
			var count int
			_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=?`, cleanup.NewAssetID).Scan(&count)
			if count != 1 {
				t.Fatal("cleanup recovery duplicated scrape", count)
			}
			if _, err = os.Stat(plan.Files[0].Target); err != nil {
				t.Fatal("cleanup replay removed replacement", err)
			}
		})
	}
}

func TestManualOrganizationAcceptsLibrarySourceAndParentTargetWithoutAdoptingExistingSlots(t *testing.T) {
	a, _, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	input := filepath.Join(target, "Loose.Movie.2024.mkv")
	importWrite(t, input, []byte("manual library source"))
	ctx := context.Background()
	request := ImportRequest{Manual: true, MediaType: "movie", TMDBID: identity.ID, TargetPath: target, Mode: "copy", Files: []ImportFileRequest{{Path: input}}}
	plan, err := a.previewImport(ctx, request)
	if err != nil || plan.Files[0].Status != "ready" || plan.Files[0].Source == plan.Files[0].Target {
		t.Fatal("manual in-library operation rejected by parent directory", plan, err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	if _, err = os.Stat(input); err != nil {
		t.Fatal("manual source removed", err)
	}
	request.Manual = false
	if _, err = a.previewImport(ctx, request); err == nil {
		t.Fatal("automatic path unexpectedly allowed library/source overlap")
	}
	request.Manual = true
	second, err := a.previewImport(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Files[0].Status != "already_imported" && second.Files[0].Status != "blocked" {
		t.Fatal("manual request without historical owner bypassed existing asset slot", second.Files[0])
	}
}

func TestReorganizeCleanupChangedReplacementRootRetainsOldTarget(t *testing.T) {
	a, source, old := reorgFixture(t)
	ctx := context.Background()
	target := filepath.Join(filepath.Dir(filepath.Dir(old.Path)), "changed-root-library")
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	plan, err := a.previewImport(ctx, ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: "movie", TMDBID: old.Media.ID, TargetPath: target, Mode: "copy", Files: []ImportFileRequest{{Path: source}}})
	if err != nil || plan.Files[0].Status != "ready" {
		t.Fatal(plan, err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	var replacement Asset
	if err = a.assetAtPath(ctx, plan.Files[0].Target, &replacement); err != nil {
		t.Fatal(err)
	}
	for index := range a.config.Roots {
		if a.config.Roots[index].ID == replacement.RootID {
			a.config.Roots[index].Path = filepath.Join(filepath.Dir(target), "reconfigured-unrelated-library")
		}
	}
	cleanupJob := reorgJob(t, a, "reorganize_cleanup", plan.ID)
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, cleanupJob.ID)
	cleanupJob.State, cleanupJob.Generation = "running", 1
	if err = a.runReorganizeCleanup(ctx, cleanupJob, plan.ID); err == nil {
		t.Fatal("changed replacement root allowed old target cleanup")
	}
	if _, err = os.Stat(old.Path); err != nil {
		t.Fatal("old target removed after replacement root changed", err)
	}
	if _, err = os.Stat(plan.Files[0].Target); err != nil {
		t.Fatal("replacement removed after configuration change", err)
	}
}
