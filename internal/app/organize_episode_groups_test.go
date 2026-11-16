package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func organizeGroupFixture(t *testing.T, a *App) {
	t.Helper()
	fixtures := map[string]string{
		"/3/tv/123":                  `{"id":123,"name":"Group Fixture","original_name":"Group Fixture","first_air_date":"2024-01-01","genres":[],"seasons":[{"season_number":0,"episode_count":2},{"season_number":4,"episode_count":19}],"episode_groups":{"results":[{"id":"merged","name":"合并篇章","group_count":2,"episode_count":4}]} }`,
		"/3/tv/episode_group/merged": `{"id":"merged","groups":[{"order":1,"name":"第一篇章","episodes":[{"id":4016,"order":9,"season_number":4,"episode_number":16,"name":"Canonical sixteen","air_date":"2024-02-01"},{"id":2,"order":0,"season_number":0,"episode_number":2,"name":"Canonical special","air_date":"2024-02-02"}]},{"order":2,"name":"第二篇章","episodes":[{"id":4018,"season_number":4,"episode_number":18,"name":"Canonical eighteen"},{"id":4019,"season_number":4,"episode_number":19,"name":"Canonical nineteen"}]}]}`,
	}
	installMetadataFixture(t, a, "owned-manual-episode-groups", []string{"/3/tv/123", "/3/movie/123", "/3/tv/episode_group/merged", "/3/tv/123/images", "/3/tv/123/season/1"}, func(r *http.Request) (*http.Response, error) {
		body, status := fixtures[r.URL.Path], http.StatusOK
		switch r.URL.Path {
		case "/3/tv/123/images":
			body = `{"posters":[],"backdrops":[]}`
		case "/3/movie/123":
			// The source's TV-file re-recognition passes no explicit mtype.
			status, body = http.StatusNotFound, `{"success":false,"status_code":34,"status_message":"synthetic movie not found"}`
		case "/3/tv/123/season/1":
			// MP's season-poster lookup remains on the ordinary season even
			// when group 1 contains canonical seasons 4 and 0.
			status, body = http.StatusNotFound, `{"status_code":34,"status_message":"synthetic season not found"}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}

func TestOrganizeEpisodeGroupNativeExecutionAndHistory(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	importFixture(t, a, "tv")
	organizeGroupFixture(t, a)
	cookie := setupTestAdmin(t, a)
	response := callTest(t, a.Handler(), http.MethodGet, "/api/catalog/episode-groups?id=123", "", cookie, "")
	var options struct{ Data []organizeEpisodeGroupOption }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &options) != nil || len(options.Data) != 1 || options.Data[0].ID != "merged" || options.Data[0].GroupCount != 2 {
		t.Fatalf("group picker: %d %s", response.Code, response.Body)
	}
	files := []string{filepath.Join(source, "Show.S01E01.mkv"), filepath.Join(source, "Show.S01E02.mkv")}
	for _, path := range files {
		importWrite(t, path, []byte("synthetic episode-group material"))
	}
	season := 1
	in := manualOrganizeRequest{Paths: files, MediaType: "tv", TMDBID: 123, EpisodeGroup: "merged", Season: &season, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false), Preview: true}
	preview, err := a.manualOrganize(ctx, in)
	if err != nil || preview.Failed != 0 || len(preview.Items) != 2 {
		t.Fatalf("preview=%+v %v", preview, err)
	}
	if page := historyParityReadPage(t, a, "capabilities=deferred"); page.Total != 0 {
		t.Fatalf("preview wrote history: %+v", page)
	}
	in.Preview = false
	output, err := a.manualOrganize(ctx, in)
	if err != nil || output.Failed != 0 || output.Submitted != 2 {
		t.Fatalf("submit=%+v %v", output, err)
	}
	ids := []string{}
	for i, result := range output.Items {
		plan, job := historyUpsertAttempt(t, a, result.JobID)
		file := plan.Files[0]
		if len(plan.Media.Seasons) != 2 || plan.Media.Seasons[0].SeasonNumber != 0 || plan.Media.Seasons[1].SeasonNumber != 4 {
			t.Fatalf("compact identity lost canonical seasons: %+v", plan.Media.Seasons)
		}
		wantSeason, wantEpisode := 4, 16
		if i == 1 {
			wantSeason, wantEpisode = 0, 2
		}
		if plan.EpisodeGroup != "merged" || file.Season != wantSeason || !slices.Equal(file.Episodes, []int{wantEpisode}) || file.GroupNumbering == nil || file.GroupNumbering.Season != 1 || !slices.Equal(file.GroupNumbering.Episodes, []int{i + 1}) {
			t.Fatalf("canonical/presentation identity mixed: %+v", plan)
		}
		if !strings.Contains(file.Target, "Season 1") || !strings.Contains(file.Target, []string{"S01E01", "S01E02"}[i]) {
			t.Fatalf("target lost group position: %s", file.Target)
		}
		var meta importMetadata
		if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
			t.Fatal(err)
		}
		projection := meta.OriginalTransfer[file.ID]
		var fields map[string]any
		if json.Unmarshal(projection.Media, &fields) != nil || fields["episode_group"] != "merged" {
			t.Fatalf("missing frozen group media: %s", projection.Media)
		}
		if json.Unmarshal(projection.Meta, &fields) != nil || fields["begin_season"] != float64(1) || fields["begin_episode"] != float64(i+1) {
			t.Fatalf("SDK meta uses canonical instead of group positions: %s", projection.Meta)
		}
		reorgRunJob(t, a, job)
		src, e1 := os.Stat(file.Source)
		dst, e2 := os.Stat(file.Target)
		if e1 != nil || e2 != nil || !os.SameFile(src, dst) {
			t.Fatalf("actual publication: %v %v", e1, e2)
		}
		history, e := a.transferHistoryItem(ctx, file.ID)
		if e != nil || history.Status != "success" || history.EpisodeGroup != "merged" || history.Season != 1 || !slices.Equal(history.Episodes, []int{i + 1}) {
			t.Fatalf("history projection=%+v %v", history, e)
		}
		ids = append(ids, history.ID)
		var count int
		if e = a.Store.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE media_key='tv:123' AND season=? AND episode=?`, wantSeason, wantEpisode).Scan(&count); e != nil || count != 1 {
			t.Fatalf("canonical inventory missing: %d %v", count, e)
		}
	}
	// The shared history path must use visible group positions, not map the
	// canonical stored episode a second time. Batch also retains selected order.
	retry := manualOrganizeRequest{HistoryIDs: []string{ids[1], ids[0]}, FromHistory: true, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false), Preview: true}
	output, err = a.manualOrganize(ctx, retry)
	if err != nil || output.Failed != 0 || len(output.Items) != 2 || output.Items[0].Source != files[1] {
		t.Fatalf("history batch preview=%+v %v", output, err)
	}
	for _, item := range output.Items {
		var plan ImportPlan
		if e := a.Store.Get(ctx, "import_plan", item.PlanID, &plan); e != nil || plan.EpisodeGroup != "merged" || plan.Files[0].GroupNumbering == nil {
			t.Fatalf("history inheritance: %+v %v", plan, e)
		}
	}
	if page := historyParityReadPage(t, a, "capabilities=deferred"); page.Total != 2 {
		t.Fatalf("preview replaced history: %+v", page)
	}
	retry.Preview = false
	output, err = a.manualOrganize(ctx, retry)
	if err != nil || output.Failed != 0 || output.Submitted != 2 {
		t.Fatalf("history execution=%+v %v", output, err)
	}
	for _, item := range output.Items {
		_, job := historyUpsertAttempt(t, a, item.JobID)
		reorgRunJob(t, a, job)
	}
	page := historyParityReadPage(t, a, "capabilities=deferred")
	if page.Total != 2 {
		t.Fatalf("repeat grew history: %+v", page)
	}
	for _, item := range page.Items {
		if item.EpisodeGroup != "merged" || item.Status != "success" || item.Season != 1 {
			t.Fatalf("repeat lost group history: %+v", item)
		}
	}
}

func TestOrganizeEpisodeGroupMappingAndMembership(t *testing.T) {
	a, source, _ := importTestApp(t)
	importFixture(t, a, "tv")
	organizeGroupFixture(t, a)
	ctx := context.Background()
	group, err := a.organizeEpisodeGroup(ctx, 123, "merged")
	if err != nil {
		t.Fatal(err)
	}
	file := PlanFile{Season: 2, Episodes: []int{1, 2}}
	if err = applyImportEpisodeGroup(&file, group); err != nil || file.Season != 4 || !slices.Equal(file.Episodes, []int{18, 19}) {
		t.Fatalf("two-episode mapping: %+v %v", file, err)
	}
	if display := file.presentationFile(); display.Season != 2 || !slices.Equal(display.Episodes, []int{1, 2}) {
		t.Fatalf("presentation lost: %+v", display)
	}
	if _, err = a.organizeEpisodeGroup(ctx, 123, "another-series-group"); err == nil {
		t.Fatal("accepted foreign group")
	}
	invalid := PlanFile{Season: 1, Episodes: []int{9}}
	if err = applyImportEpisodeGroup(&invalid, group); err == nil || invalid.GroupNumbering != nil {
		t.Fatalf("invented missing episode: %+v %v", invalid, err)
	}
	path := filepath.Join(source, "Show.S02E01.mkv")
	importWrite(t, path, []byte("synthetic episode ID evidence"))
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "tv", TMDBID: 123, EpisodeGroup: "merged", RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: path, Season: 2, SeasonExplicit: true, Episodes: []int{1}, SourceEpisodeID: 4018}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" || plan.Files[0].Season != 4 || !slices.Equal(plan.Files[0].Episodes, []int{18}) {
		t.Fatalf("canonical episode-ID evidence: %+v %v", plan, err)
	}
	// Existing native numbering remains unchanged without a selected group.
	importCache(t, a, "season:123:2", catalog.Season{SeriesID: 123, SeasonNumber: 2, Episodes: []catalog.Episode{{ID: 201, SeasonNumber: 2, EpisodeNumber: 1}}})
	plain, err := a.previewImport(ctx, ImportRequest{MediaType: "tv", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: path, Season: 2, SeasonExplicit: true, Episodes: []int{1}}}})
	if err != nil || plain.EpisodeGroup != "" || plain.Files[0].GroupNumbering != nil || plain.Files[0].Season != 2 {
		t.Fatalf("default numbering changed: %+v %v", plain, err)
	}
}
