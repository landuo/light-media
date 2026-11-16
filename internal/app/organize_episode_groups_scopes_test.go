package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Exercise the same completion hook as the worker; a direct jobs-table update
// would leave the native directory's deferred scrape batch unflushed.
func runEpisodeGroupJob(t *testing.T, a *App, job Job) {
	t.Helper()
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.safeRun(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if _, err = a.finishJob(context.Background(), job, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
}

func TestOrganizeEpisodeGroupCrossSeasonFileInventoryAndHistory(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "tv")
	organizeGroupFixture(t, a)
	ctx := context.Background()
	path := filepath.Join(source, "Show.S01E01-E02.mkv")
	importWrite(t, path, []byte("one synthetic physical file covering two canonical seasons"))
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "tv", TMDBID: 123, EpisodeGroup: "merged", RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: path, Season: 1, SeasonExplicit: true, Episodes: []int{1, 2}}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatalf("cross-season preview: %+v %v", plan, err)
	}
	expected := []canonicalEpisodeSlot{{4, 16}, {0, 2}}
	if !sameCanonicalSlots(plan.Files[0].inventorySlots(), expected) || len(plan.Files[0].EpisodeScopes) != 2 {
		t.Fatalf("lost canonical scope: %+v", plan.Files[0])
	}
	if !strings.Contains(plan.Files[0].Target, "S01E01-E02") {
		t.Fatalf("lost group presentation: %s", plan.Files[0].Target)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	runEpisodeGroupJob(t, a, job)
	if err = a.Store.Get(ctx, "import_plan", plan.ID, &plan); err != nil {
		t.Fatal(err)
	}
	file := plan.Files[0]
	var asset Asset
	if err = a.Store.Get(ctx, "asset", file.AssetID, &asset); err != nil || !sameCanonicalSlots(asset.inventorySlots(), expected) {
		t.Fatalf("asset scopes: %+v %v", asset, err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM asset_slots WHERE asset_id=?`, asset.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("slot count=%d %v", count, err)
	}
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM library_asset WHERE 1=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("one physical file became multiple assets: %d %v", count, err)
	}
	for _, slot := range expected {
		var owner string
		if err = a.Store.DB.QueryRow(`SELECT asset_id FROM asset_slots WHERE media_key=? AND season=? AND episode=?`, mediaKey(asset.Media), slot.Season, slot.Episode).Scan(&owner); err != nil || owner != asset.ID {
			t.Fatalf("slot %+v owner=%s %v", slot, owner, err)
		}
		sub := Subscription{ID: "cross-season-sub", Media: asset.Media, Season: slot.Season, StartEpisode: slot.Episode, TotalEpisodes: slot.Episode, State: "active"}
		basis, complete, e := a.ordinaryCompletionBasis(ctx, a.Store.DB, sub, nil)
		if e != nil || !complete || basis == nil || len(basis.Inventory) != 1 || basis.Inventory[0].Asset.ID != asset.ID {
			t.Fatalf("canonical season %d lost real inventory: %+v %v %v", slot.Season, basis, complete, e)
		}
	}
	before := historyParityReadPage(t, a, "")
	if before.Total != 1 || before.Items[0].Season != 1 || !slices.Equal(before.Items[0].Episodes, []int{1, 2}) {
		t.Fatalf("display history: %+v", before)
	}
	// A competing reservation in only the secondary canonical season must be
	// checked too. Existing files at other paths are governed by MP overwrite
	// policy, not a blanket prohibition on multiple versions of an episode.
	if _, err = a.Store.DB.Exec(`INSERT INTO reservations VALUES(?,?,?,?)`, mediaKey(asset.Media), 0, 2, "competing-download"); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(source, "Other.S01E01-E02.mkv")
	importWrite(t, other, []byte("different file for the already covered special"))
	conflict, e := a.previewImport(ctx, ImportRequest{MediaType: "tv", TMDBID: 123, EpisodeGroup: "merged", RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: other, Season: 1, SeasonExplicit: true, Episodes: []int{1, 2}}}})
	if e != nil || len(conflict.Files) != 1 || conflict.Files[0].Status != "blocked" {
		t.Fatalf("secondary-season conflict accepted: %+v %v", conflict, e)
	}
	if _, err = a.Store.DB.Exec(`DELETE FROM reservations WHERE intent_id=?`, "competing-download"); err != nil {
		t.Fatal(err)
	}
	result, e := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{path}, HistoryIDs: []string{before.Items[0].ID}, FromHistory: true, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false)})
	if e != nil || result.Submitted != 1 || result.Failed != 0 {
		t.Fatalf("history reorganization: %+v %v", result, e)
	}
	_, repeat := historyUpsertAttempt(t, a, result.Items[0].JobID)
	runEpisodeGroupJob(t, a, repeat)
	after := historyParityReadPage(t, a, "")
	if after.Total != 1 || after.Items[0].ID == before.Items[0].ID || after.Items[0].Status != "success" || !slices.Equal(after.Items[0].Episodes, []int{1, 2}) {
		t.Fatalf("repeat history changed: %+v", after)
	}
	if _, e = a.deleteTransferHistory(ctx, after.Items[0], historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: after.Items[0].executionFileID()}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("target deletion lost source", e)
	}
	if _, e = os.Lstat(after.Items[0].Target); !os.IsNotExist(e) {
		t.Fatal("target remained", e)
	}
	if e = a.Store.DB.QueryRow(`SELECT count(*) FROM asset_slots`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("orphan canonical slots after history deletion: %d %v", count, e)
	}
}

func TestOrganizeEpisodeGroupBlurayInventory(t *testing.T) {
	for _, name := range []string{"Disc.S01", "Disc.S01E01-E02", "Disc"} {
		explicit := strings.Contains(name, "E01")
		t.Run(name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			importFixture(t, a, "tv")
			organizeGroupFixture(t, a)
			ctx := context.Background()
			disc := filepath.Join(source, name)
			for _, member := range []string{"BDMV/STREAM/00001.m2ts", "BDMV/index.bdmv"} {
				importWrite(t, filepath.Join(disc, member), []byte("owned synthetic disc member"))
			}
			if err := os.Mkdir(filepath.Join(disc, "CERTIFICATE"), 0750); err != nil {
				t.Fatal(err)
			}
			season := 1
			request := manualOrganizeRequest{Paths: []string{disc}, TargetPath: target, Mode: "hardlink", MediaType: "tv", TMDBID: 123, EpisodeGroup: "merged", Season: &season, Scraping: importFlag(true), Preview: true}
			if name == "Disc" {
				request.Season = nil
			}
			preview, err := a.manualOrganize(ctx, request)
			if err != nil || preview.Total != 1 || preview.Failed != 0 || preview.Items[0].Status != "ready" {
				t.Fatalf("disc preview: %+v %v", preview, err)
			}
			var plan ImportPlan
			if err = a.Store.Get(ctx, "import_plan", preview.Items[0].PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			file := plan.Files[0]
			expected := []canonicalEpisodeSlot{{0, 0}}
			if explicit {
				expected = []canonicalEpisodeSlot{{4, 16}, {0, 2}}
			}
			if file.DirectoryDigest == "" || file.GroupNumbering == nil || file.GroupNumbering.Season != 1 || !sameCanonicalSlots(file.inventorySlots(), expected) {
				t.Fatalf("disc scope: %+v", file)
			}
			request.Preview = false
			result, err := a.manualOrganize(ctx, request)
			if err != nil || result.Submitted != 1 || result.Failed != 0 {
				t.Fatalf("disc submission: %+v %v", result, err)
			}
			_, job := historyUpsertAttempt(t, a, result.Items[0].JobID)
			runEpisodeGroupJob(t, a, job)
			if err = a.Store.Get(ctx, "import_plan", result.Items[0].PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			file = plan.Files[0]
			a.wg.Wait()
			runEpisodeGroupJob(t, a, reorgJob(t, a, "scrape", file.AssetID))
			nfoName, expectedTitle, expectedTag := "season.nfo", "第一篇章", "<seasonnumber>1</seasonnumber>"
			if name == "Disc" {
				nfoName, expectedTitle, expectedTag = "tvshow.nfo", "Group Fixture", "<tmdbid>123</tmdbid>"
			}
			nfo, err := os.ReadFile(filepath.Join(file.Target, nfoName))
			if err != nil || !strings.Contains(string(nfo), expectedTag) || !strings.Contains(string(nfo), expectedTitle) {
				t.Fatalf("disc group NFO: %s %v", nfo, err)
			}
			var asset Asset
			if err = a.Store.Get(ctx, "asset", file.AssetID, &asset); err != nil || !sameCanonicalSlots(asset.inventorySlots(), expected) {
				t.Fatalf("disc asset: %+v %v", asset, err)
			}
			for _, member := range []string{"BDMV/STREAM/00001.m2ts", "BDMV/index.bdmv"} {
				src, e := os.Stat(filepath.Join(disc, member))
				if e != nil {
					t.Fatal(e)
				}
				dst, e := os.Stat(filepath.Join(file.Target, member))
				if e != nil || !os.SameFile(src, dst) {
					t.Fatalf("disc member not hardlinked: %s %v", member, e)
				}
			}
			// MP recursively transfers files; an empty child directory does not
			// create a destination by itself (transhandler.py:967-1003).
			if _, e := os.Stat(filepath.Join(file.Target, "CERTIFICATE")); !os.IsNotExist(e) {
				t.Fatal("empty directory diverged from MP file-only recursion", e)
			}
			page := historyParityReadPage(t, a, "")
			if page.Total != 1 || page.Items[0].Status != "success" || page.Items[0].Season != 1 || page.Items[0].EpisodeGroup != "merged" {
				t.Fatalf("disc history: %+v", page)
			}
		})
	}
}

func TestOrganizeEpisodeGroupCanonicalScopeCompatibility(t *testing.T) {
	ordinary := PlanFile{Season: 2, Episodes: []int{1, 2}}
	if !sameCanonicalSlots(ordinary.inventorySlots(), []canonicalEpisodeSlot{{2, 1}, {2, 2}}) || ordinary.EpisodeScopes != nil {
		t.Fatal("ordinary single-season shape changed")
	}
	if !sameCanonicalSlots((PlanFile{}).inventorySlots(), []canonicalEpisodeSlot{{0, 0}}) {
		t.Fatal("unknown/movie slot changed")
	}
}
