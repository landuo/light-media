package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/catalog"
)

func TestHistoryFromHistoryIdentityPrecedesCurrentForm(t *testing.T) {
	for _, inherit := range []bool{true, false} {
		t.Run(map[bool]string{true: "reuse-history", false: "recognize-again"}[inherit], func(t *testing.T) {
			a, source, target := importTestApp(t)
			ctx := context.Background()
			media := importFixture(t, a, "tv")
			alternative := media
			alternative.ID, alternative.Title = 456, "Corrected Show"
			importCache(t, a, "media:tv:456", alternative)
			importCache(t, a, "season:123:1", catalog.Season{ID: 901, SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 101, SeasonNumber: 1, EpisodeNumber: 1}, {ID: 102, SeasonNumber: 1, EpisodeNumber: 2}}})
			importCache(t, a, "season:456:2", catalog.Season{ID: 902, SeriesID: 456, SeasonNumber: 2, Episodes: []catalog.Episode{{ID: 207, SeasonNumber: 2, EpisodeNumber: 7}}})
			history, _ := historyParityPlan(t, a, source, target, "identity", "Old identity", "failed", "error", "Source.S01E01-E02.mkv")
			history.Media = media
			history.Files[0].Season, history.Files[0].Episodes = 1, []int{1, 2}
			if err := a.Store.Put(ctx, "import_plan", history.ID, history); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.DB.Exec(`DELETE FROM transferhistory WHERE src=?`, history.Files[0].Source); err != nil {
				t.Fatal(err)
			}
			mpTx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = writeTransferHistoryMPTx(ctx, mpTx, transferHistoryMPInput{Plan: history, File: history.Files[0], Job: Job{ID: "identity-history-job"}, Success: false}); err != nil {
				mpTx.Rollback()
				t.Fatal(err)
			}
			if err = mpTx.Commit(); err != nil {
				t.Fatal(err)
			}
			season := 2
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{HistoryIDs: []string{history.Files[0].ID}, FromHistory: inherit, MediaType: "tv", TMDBID: 456, Season: &season, EpisodeDetail: "7", TargetPath: target, Mode: "hardlink", Preview: true, Scraping: importFlag(false)})
			if err != nil || out.Failed != 0 || len(out.Items) != 1 || out.Items[0].Status != "ready" {
				t.Fatalf("preview=%+v err=%v", out, err)
			}
			var plan ImportPlan
			if err := a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			expectedID, expectedSeason, expectedEpisodes := int64(456), 2, []int{7}
			if inherit {
				expectedID, expectedSeason, expectedEpisodes = 123, 1, []int{1, 2}
			}
			if plan.Media.ID != expectedID || plan.Files[0].Season != expectedSeason || !reflect.DeepEqual(plan.Files[0].Episodes, expectedEpisodes) {
				t.Fatalf("history/form priority differs: %+v %+v", plan.Media, plan.Files[0])
			}
			current, err := a.transferHistoryItem(ctx, history.Files[0].ID)
			if err != nil || current.PlanID != history.ID || current.Media.ID != 123 || current.Status != "failed" {
				t.Fatalf("preview changed execution history: %+v %v", current, err)
			}
		})
	}
}

func TestManualOrganizeBlockedPlanRetainsResolvedHistoryMetadata(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	media := importFixture(t, a, "movie")
	path := filepath.Join(source, "Test.2024.mkv")
	importWrite(t, path, []byte("synthetic source video"))
	input := manualOrganizeRequest{Paths: []string{path}, MediaType: "movie", TMDBID: media.ID, Mode: "hardlink", TargetPath: target, Scraping: importFlag(false), LibraryTypeFolder: importFlag(true), Preview: true}
	preview, err := a.manualOrganize(ctx, input)
	if err != nil || preview.Failed != 0 || len(preview.Items) != 1 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	destination := preview.Items[0].Target
	importWrite(t, destination, []byte("unmanaged existing destination"))
	if _, err := a.Store.DB.Exec(`INSERT INTO reservations(media_key,season,episode,intent_id) VALUES('movie:123',0,0,'other-download')`); err != nil {
		t.Fatal(err)
	}
	input.Preview = false
	out, err := a.manualOrganize(ctx, input)
	if err != nil || out.Failed != 1 || len(out.Items) != 1 {
		t.Fatalf("submission=%+v err=%v", out, err)
	}
	page := historyParityReadPage(t, a, "capabilities=deferred")
	if page.Total != 1 {
		t.Fatalf("failed submission not saved: %+v", page)
	}
	history := page.Items[0]
	if history.Status != "failed" || history.Media.ID != media.ID || history.Media.Title != media.Title || history.Mode != "hardlink" || history.Target != destination || history.Category != "华语电影" || history.Error == "" {
		t.Fatalf("resolved failure metadata was discarded: %+v", history)
	}
	if out.Items[0].Media.ID != media.ID || out.Items[0].Target != destination {
		t.Fatalf("failure result discarded known context: %+v", out)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "unmanaged existing destination" {
		t.Fatalf("failed preparation changed destination: %q %v", data, err)
	}
}

func TestHistoryIdentityOnlyOverridesKnownFields(t *testing.T) {
	season := 3
	input := manualOrganizeRequest{MediaType: "tv", TMDBID: 456, Season: &season, EpisodeDetail: "7"}
	input.inheritHistoryIdentity(transferHistoryItem{})
	if input.MediaType != "tv" || input.TMDBID != 456 || *input.Season != 3 || input.EpisodeDetail != "7" {
		t.Fatalf("unknown history erased form: %+v", input)
	}
	input.inheritHistoryIdentity(transferHistoryItem{Media: catalog.Media{ID: 123, MediaType: "tv"}, Season: 0, Episodes: []int{1}})
	if input.TMDBID != 123 || *input.Season != 0 || input.EpisodeDetail != "1" {
		t.Fatalf("special season was lost: %+v", input)
	}
}
