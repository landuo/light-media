package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func TestOrganizeSuccessNoticeFields(t *testing.T) {
	a := newTestApp(t)
	a.config.AppDomain = "https://media.example/"
	a.invalidateSettings()
	plan := ImportPlan{ID: "plan_notice", WorkflowID: "wf_notice", Media: catalog.Media{
		ID: 7, MediaType: "tv", Title: "测试剧集", PosterPath: "https://image.tmdb.org/t/p/original/poster.jpg",
	}}
	file := PlanFile{ID: "file_notice", Season: 2, Episodes: []int{3, 4}, Target: "/library/测试剧集/Season 02/测试剧集 - S02E03-E04.mkv", Size: 3 << 30}
	fields := a.organizeSuccessNoticeFields(plan, []PlanFile{file})
	if fields["mtype"] != "整理入库" || fields["ctype"] != "organizeSuccess" {
		t.Fatalf("source notification type missing: %#v", fields)
	}
	title, _ := fields["title"].(string)
	if !strings.Contains(title, "整理完成") || !strings.Contains(title, "测试剧集 S02E03-E04") {
		t.Fatalf("title does not carry media and episode scope: %q", title)
	}
	text, _ := fields["text"].(string)
	if !strings.Contains(text, file.Target) || !strings.Contains(text, "3.00 GB") {
		t.Fatalf("text does not carry target and size: %q", text)
	}
	if fields["image"] != "https://image.tmdb.org/t/p/w500/poster.jpg" {
		t.Fatalf("poster was not widened to the source size: %v", fields["image"])
	}
	if fields["link"] != "https://media.example/#/history" {
		t.Fatalf("history link missing: %v", fields["link"])
	}
	if fields["save_history"] != true {
		t.Fatal("organize notification must stay in the message history")
	}
}

func TestOrganizeSuccessNoticePostsToHistoryAndNoticeChannel(t *testing.T) {
	a := newTestApp(t)
	a.config.AppDomain = "https://media.example"
	a.invalidateSettings()
	plan := ImportPlan{ID: "plan_notice", WorkflowID: "wf_notice", Media: catalog.Media{ID: 11, MediaType: "movie", Title: "测试影片"}}
	file := PlanFile{ID: "file_notice", Target: "/library/测试影片/测试影片.mkv", Size: 1 << 30}
	a.postOriginalOrganizeSuccessNotice(t.Context(), plan, []PlanFile{file})

	raw, err := a.notifications(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	page := raw.(map[string]any)
	items := page["items"].([]json.RawMessage)
	if len(items) != 1 {
		t.Fatalf("organize notice must be recorded once: %d", len(items))
	}
	var n Notification
	if err = json.Unmarshal(items[0], &n); err != nil {
		t.Fatal(err)
	}
	if n.MType != "整理入库" || !strings.Contains(n.Title, "整理完成") || !strings.Contains(n.Message, file.Target) {
		t.Fatalf("recorded notification differs from the source fields: %+v", n)
	}
	if n.Link != "https://media.example/#/history" || n.Image != "" {
		t.Fatalf("recorded notification lost link or invented an image: %+v", n)
	}
}

func TestOrganizeSuccessNoticeScope(t *testing.T) {
	if !organizeSuccessNoticeApplies(PlanFile{}, importReceipt{}, false) {
		t.Fatal("ordinary fresh publication must notify")
	}
	cases := []struct {
		name    string
		file    PlanFile
		receipt importReceipt
		sourced bool
	}{
		{"material", PlanFile{MaterialKind: "subtitle"}, importReceipt{}, false},
		{"reused hardlink", PlanFile{}, importReceipt{ReusedHardlink: true}, false},
		{"plugin owned", PlanFile{}, importReceipt{}, true},
	}
	for _, c := range cases {
		if organizeSuccessNoticeApplies(c.file, c.receipt, c.sourced) {
			t.Fatalf("%s must not post an organize notification", c.name)
		}
	}
}

func TestOrganizeSuccessNoticeFailureKeepsImportOutcome(t *testing.T) {
	a := newTestApp(t)
	// A closed app cannot admit background work; the import must still succeed.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	plan := ImportPlan{ID: "plan_notice", Media: catalog.Media{Title: "测试影片"}}
	a.postOriginalOrganizeSuccessNotice(context.Background(), plan, []PlanFile{{Target: "/library/a.mkv"}})
}

func organizeNoticeApp(t *testing.T) (*App, string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, target := filepath.Join(base, "source"), filepath.Join(base, "library")
	for _, dir := range []string{source, target} {
		if err = os.MkdirAll(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(Options{DataDir: filepath.Join(base, "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.cfgMu.Lock()
	a.config = Settings{AllowedRoots: []string{source}, Roots: []Root{{ID: "root", Path: target, Kind: "mixed"}}}
	a.cfgMu.Unlock()
	a.invalidateSettings()
	return a, source, target
}

func TestOrganizeSuccessNoticeFiresOnceForCommittedImport(t *testing.T) {
	a, source, target := organizeNoticeApp(t)
	importFixture(t, a, "movie")
	installMetadataFixture(t, a, "notice-move", []string{"/3/movie/123"}, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":123,"title":"测试影片","original_title":"Test Film","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`))}, nil
	})
	path := filepath.Join(source, "Notice.2024.mkv")
	importWrite(t, path, []byte("synthetic notice source"))
	request := manualOrganizeRequest{Paths: []string{path}, MediaType: "movie", TMDBID: 123, TargetPath: target, Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false)}
	out, err := a.manualOrganize(t.Context(), request)
	if err != nil || out.Failed != 0 || out.Submitted == 0 {
		t.Fatalf("organize submission %+v %v", out, err)
	}
	for _, item := range out.Items {
		if item.JobID != "" {
			_, job := historyUpsertAttempt(t, a, item.JobID)
			runEpisodeGroupJob(t, a, job)
		}
	}
	notices := organizeNotices(t, a)
	if len(notices) != 1 || notices[0].MType != "整理入库" || !strings.Contains(notices[0].Title, "测试影片") || !strings.Contains(notices[0].Message, target) {
		t.Fatalf("committed import did not post one organize notice: %+v", notices)
	}

	// A second submission is already organized and must not replay the notice.
	if _, err = a.manualOrganize(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if replay := organizeNotices(t, a); len(replay) != 1 {
		t.Fatalf("already imported files replayed the notice: %+v", replay)
	}
}

func organizeNotices(t *testing.T, a *App) []Notification {
	t.Helper()
	raw, err := a.notifications(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	items := raw.(map[string]any)["items"].([]json.RawMessage)
	out := []Notification{}
	for _, item := range items {
		var n Notification
		if err = json.Unmarshal(item, &n); err != nil {
			t.Fatal(err)
		}
		if n.MType == "整理入库" {
			out = append(out, n)
		}
	}
	return out
}

func TestOrganizeSuccessNoticeAggregatesOneImportRun(t *testing.T) {
	a := newTestApp(t)
	a.config.AppDomain = "https://media.example"
	a.invalidateSettings()
	plan := ImportPlan{ID: "plan_batch", Media: catalog.Media{ID: 9, MediaType: "tv", Title: "测试剧集"}}
	files := []PlanFile{
		{ID: "f1", Season: 1, Episodes: []int{1}, Target: "/library/测试剧集/S01E01.mkv", Size: 2 << 30},
		{ID: "f2", Season: 1, Episodes: []int{2}, Target: "/library/测试剧集/S01E02.mkv", Size: 1 << 30},
	}
	fields := a.organizeSuccessNoticeFields(plan, files)
	title, _ := fields["title"].(string)
	if !strings.Contains(title, "S01E01") || !strings.Contains(title, "S01E02") {
		t.Fatalf("episode scope missing from the aggregated title: %q", title)
	}
	text, _ := fields["text"].(string)
	for _, file := range files {
		if !strings.Contains(text, file.Target) {
			t.Fatalf("aggregated text lost %s: %q", file.Target, text)
		}
	}
	if !strings.Contains(text, "3.00 GB") {
		t.Fatalf("aggregated text lost the total size: %q", text)
	}
	a.postOriginalOrganizeSuccessNotice(t.Context(), plan, files)
	if notices := organizeNotices(t, a); len(notices) != 1 {
		t.Fatalf("one import run must post one notice: %+v", notices)
	}
}
