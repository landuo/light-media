package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeasonOnlyDiscSetPublishesIndependentHardlinks(t *testing.T) {
	a, source, library := importTestApp(t)
	ctx := t.Context()
	tv := importFixture(t, a, "tv")
	organizeGroupFixture(t, a)
	season := 1
	sub := Subscription{ID: "disc-series", Media: tv, Season: season, StartEpisode: 1, TotalEpisodes: 8, State: "active"}
	if err := a.Store.Put(ctx, "subscription", sub.ID, sub); err != nil {
		t.Fatal(err)
	}

	// Production file names, disposable content. No episode list identifies
	// which of the eight episodes any one of these three physical discs holds.
	parent := filepath.Join(source, "Fallout.S01.2160p.UHD.Blu-ray.HEVC.Atmos.TrueHD7.1-DiY@HDHome")
	members := []string{"BDMV/index.bdmv", "BDMV/STREAM/00001.m2ts"}
	for _, disc := range []string{"Disc 1", "Disc 2", "Disc 3"} {
		for _, member := range members {
			importWrite(t, filepath.Join(parent, disc, member), []byte(disc+" "+member))
		}
	}
	output, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{parent}, MediaType: "tv", TMDBID: tv.ID, Season: &season, Mode: "hardlink", TargetPath: library, Scraping: importFlag(false)})
	if err != nil || output.Failed != 0 || output.Submitted != 3 {
		t.Fatalf("multi-disc submission: %+v %v", output, err)
	}
	for _, item := range output.Items {
		_, job := historyUpsertAttempt(t, a, item.JobID)
		runEpisodeGroupJob(t, a, job)
	}
	history := historyParityReadPage(t, a, "")
	if history.Total != 3 {
		t.Fatalf("expected three physical disc records: %+v", history)
	}
	var targetDir string
	for _, item := range history.Items {
		if item.Status != "success" || item.Season != season || len(item.Episodes) != 0 || filepath.Base(item.Source) != filepath.Base(item.Target) {
			t.Fatalf("wrong disc history: %+v", item)
		}
		dir := filepath.Dir(item.Target)
		if filepath.Base(dir) != "Season 1" || targetDir != "" && dir != targetDir {
			t.Fatalf("unexpected season path %q, first %q", dir, targetDir)
		}
		targetDir = dir
		for _, member := range members {
			src, err := os.Stat(filepath.Join(item.Source, member))
			if err != nil {
				t.Fatal(err)
			}
			dst, err := os.Stat(filepath.Join(item.Target, member))
			if err != nil || !os.SameFile(src, dst) {
				t.Fatalf("wrong hardlink %s/%s: %v", item.Target, member, err)
			}
		}
	}
	var count int
	if err := a.Store.DB.QueryRow("SELECT count(*) FROM asset_slots WHERE media_key=?", mediaKey(tv)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invented disc episode slots: %d %v", count, err)
	}
	if err := a.Store.DB.QueryRow("SELECT count(*) FROM reservations WHERE media_key=?", mediaKey(tv)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invented disc episode reservations: %d %v", count, err)
	}

	// Reopen/backfill an older database which assigned a season's first disc
	// an imaginary E0; the other episode and whole-movie rows still belong.
	var assetID string
	if err := a.Store.DB.QueryRow("SELECT id FROM library_asset WHERE record_kind='asset' LIMIT 1").Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec("INSERT INTO asset_slots VALUES(?,?,0,?),('movie:9',0,0,'movie'),(?,1,5,'known')", mediaKey(tv), season, assetID, mediaKey(tv)); err != nil {
		t.Fatal(err)
	}
	if err := backfillAssetSlots(ctx, a.Store.DB); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DB.QueryRow("SELECT count(*) FROM asset_slots WHERE asset_id=?", assetID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale E0 after backfill: %d %v", count, err)
	}
	if err := a.Store.DB.QueryRow("SELECT count(*) FROM asset_slots WHERE asset_id IN ('movie','known')").Scan(&count); err != nil || count != 2 {
		t.Fatalf("lost established inventory: %d %v", count, err)
	}
	if err := a.Store.DB.QueryRow("SELECT lack_episode FROM subscribe WHERE uid=?", sub.ID).Scan(&count); err != nil || count != 7 {
		t.Fatalf("subscription should credit only episode five: %d %v", count, err)
	}
}
