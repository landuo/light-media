package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func TestExplicitSpecialsPartPublishesOneSourceAndScrapesActualEpisodeID(t *testing.T) {
	for _, mode := range []string{"hardlink", "copy"} {
		t.Run(mode, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			identity := importFixture(t, a, "tv")
			episode := catalog.Episode{ID: 7003, SeasonNumber: 0, EpisodeNumber: 3, Name: "特别篇第三集", AirDate: "2024-01-01"}
			importCache(t, a, "season:123:0", catalog.Season{ID: 900, SeriesID: identity.ID, SeasonNumber: 0, Episodes: []catalog.Episode{episode}})
			input := filepath.Join(source, "Original.Show.S00E03.WEB-DL.mkv")
			importWrite(t, input, []byte("one synthetic special video"))
			importWrite(t, strings.TrimSuffix(input, ".mkv")+".zh.srt", []byte("special subtitle"))
			ctx := context.Background()
			request := ImportRequest{MediaType: "tv", TMDBID: identity.ID, RootID: "root", Mode: mode, Files: []ImportFileRequest{{Path: input, Season: 0, SeasonExplicit: true, Part: "part1"}}}
			plan, err := a.previewImport(ctx, request)
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatalf("specials preview=%+v error=%v", plan, err)
			}
			file := plan.Files[0]
			if file.Season != 0 || len(file.Episodes) != 1 || file.Episodes[0] != 3 || file.Part != "part1" || !strings.HasSuffix(file.Target, "S00E03-part1.mkv") {
				t.Fatalf("specials/part not actually reflected in plan: %+v", file)
			}
			job, err := a.executeImport(ctx, plan.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
			job.State, job.Generation = "running", 1
			if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
				t.Fatal(err)
			}
			src, err := os.Stat(input)
			if err != nil {
				t.Fatal("source was removed", err)
			}
			dst, err := os.Stat(file.Target)
			if err != nil || os.SameFile(src, dst) != (mode == "hardlink") {
				t.Fatal("selected real transfer method not used", err)
			}
			if video, err := os.ReadFile(input); err != nil || string(video) != "one synthetic special video" {
				t.Fatal("source video changed", err)
			}
			if _, err = os.Stat(strings.TrimSuffix(file.Target, ".mkv") + ".zh.srt"); err != nil {
				t.Fatal("subtitle part naming did not follow video", err)
			}
			var asset Asset
			if err = a.assetAtPath(ctx, file.Target, &asset); err != nil || asset.Season != 0 || asset.Part != "part1" {
				t.Fatal("asset lost special or part", err)
			}
			scrapeJob, err := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='scrape' LIMIT 1`))
			if err != nil {
				t.Fatal(err)
			}
			_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, scrapeJob.ID)
			scrapeJob.State, scrapeJob.Generation = "running", 1
			if err = a.runScrape(ctx, scrapeJob, asset.ID); err != nil {
				t.Fatal(err)
			}
			nfo, id, err := readAssetNFO(strings.TrimSuffix(file.Target, ".mkv") + ".nfo")
			if err != nil || id != episode.ID || id == identity.ID || nfo.Season == nil || *nfo.Season != 0 || nfo.Episode == nil || *nfo.Episode != 3 {
				t.Fatalf("NFO did not preserve real special episode identity: %+v id=%d err=%v", nfo, id, err)
			}
			// A second part still claims the same episode slot. It cannot fake a
			// second episode or overwrite the first published asset.
			request.Files[0].Part = "part2"
			second, err := a.previewImport(ctx, request)
			if err != nil || second.Files[0].Status != "blocked" {
				t.Fatalf("same episode part bypassed unique asset slot: %+v %v", second, err)
			}
		})
	}
}

func TestSpecialsRequireExplicitSelectionOrTrustedNFONumberingAndActualMetadata(t *testing.T) {
	for _, invalid := range []string{"implicit-zero", "missing-episode", "wrong-series", "nfo-id-conflict", "valid-nfo"} {
		t.Run(invalid, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			identity := importFixture(t, a, "tv")
			episode := catalog.Episode{ID: 7001, SeasonNumber: 0, EpisodeNumber: 1, Name: "Special"}
			season := catalog.Season{ID: 900, SeriesID: identity.ID, SeasonNumber: 0, Episodes: []catalog.Episode{episode}}
			if invalid == "missing-episode" {
				season.Episodes = nil
			}
			if invalid == "wrong-series" {
				season.SeriesID = 456
			}
			importCache(t, a, "season:123:0", season)
			input := filepath.Join(source, "show", "Specials", "unknown.mkv")
			importWrite(t, input, []byte("special fixture"))
			request := ImportRequest{MediaType: "tv", TMDBID: identity.ID, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: input, Season: 0, SeasonExplicit: invalid != "implicit-zero", Episodes: []int{1}}}}
			if invalid == "nfo-id-conflict" || invalid == "valid-nfo" {
				showNFO, err := assetNFO(identity, nil)
				if err != nil {
					t.Fatal(err)
				}
				if invalid == "nfo-id-conflict" {
					episode.ID = 7999
				}
				episodeNFO, err := assetNFO(identity, &episode)
				if err != nil {
					t.Fatal(err)
				}
				importWrite(t, filepath.Join(source, "show", "tvshow.nfo"), showNFO)
				importWrite(t, strings.TrimSuffix(input, ".mkv")+".nfo", episodeNFO)
				request.MediaType, request.TMDBID = "", 0
				request.Files[0] = ImportFileRequest{Path: input}
			}
			plan, err := a.previewImport(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			want := "blocked"
			if invalid == "valid-nfo" {
				want = "ready"
			}
			if len(plan.Files) != 1 || plan.Files[0].Status != want || plan.Files[0].Season != 0 {
				raw, _ := json.Marshal(plan)
				t.Fatalf("unexpected S0 validation: %s err=%v", raw, err)
			}
		})
	}
}

func TestImportPartRejectsPathFragmentsWithoutPublishing(t *testing.T) {
	a, source, _ := importTestApp(t)
	importFixture(t, a, "movie")
	input := filepath.Join(source, "Movie.2024.mkv")
	importWrite(t, input, []byte("movie fixture"))
	for _, part := range []string{"../../escape", "part1/other", "part\x00two", strings.Repeat("a", 65)} {
		plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: input, Part: part}}})
		if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "blocked" {
			t.Fatalf("unsafe part accepted: %q %+v %v", part, plan, err)
		}
	}
}

func TestExplicitSpecialsCanIdentifyTitleAutomaticallyWithoutChangingRealSeason(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "tv")
	identity.Title, identity.OriginalTitle = "Auto Show", "Auto Show"
	importCache(t, a, "media:tv:123", identity)
	importCache(t, a, "directory-identify:tv:Auto Show", catalog.Page{Results: []catalog.Media{identity}, Page: 1, TotalPages: 1, TotalResults: 1})
	importCache(t, a, "season:123:0", catalog.Season{ID: 900, SeriesID: identity.ID, SeasonNumber: 0, Episodes: []catalog.Episode{{ID: 7003, SeasonNumber: 0, EpisodeNumber: 3, Name: "Special"}}})
	input := filepath.Join(source, "Auto.Show.S00E03.mkv")
	importWrite(t, input, []byte("synthetic special"))
	plan, err := a.previewImport(context.Background(), ImportRequest{RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: input, Season: 0, SeasonExplicit: true}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" || plan.Media.ID != identity.ID || plan.Files[0].Season != 0 || plan.Files[0].Episodes[0] != 3 {
		t.Fatalf("explicit special with automatic media identity failed: %+v %v", plan, err)
	}
}
