package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

type completedPreparationFixture struct {
	app            *App
	client         *download.Client
	downloader     DownloaderConfig
	intent         Intent
	task           download.Task
	files          []download.File
	fileStatus     int
	source, target string
}

func completedPreparationTestFixture(t *testing.T, kind string) *completedPreparationFixture {
	t.Helper()
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, kind)
	f := &completedPreparationFixture{app: a, source: source, target: target}
	names := []string{"Test.Film.2024.mkv"}
	if kind == "tv" {
		names = []string{"Test.Series.S01E01.mkv", "Test.Series.S01E02.mkv"}
		importCache(t, a, "season:123:1", catalog.Season{SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{SeasonNumber: 1, EpisodeNumber: 1}, {SeasonNumber: 1, EpisodeNumber: 2}, {SeasonNumber: 1, EpisodeNumber: 3}}})
	}
	_, hash := workflowTorrent(names[0])
	var total int64
	for _, name := range names {
		data := []byte("synthetic completed download " + name)
		importWrite(t, filepath.Join(source, name), data)
		f.files = append(f.files, download.File{Path: name, Size: int64(len(data)), Progress: 1})
		total += int64(len(data))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			task := workflowTask(hash, 1)
			task["save_path"], task["size"], task["completed"] = source, total, total
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/files":
			if f.fileStatus != 0 {
				w.WriteHeader(f.fileStatus)
				return
			}
			rows := []any{}
			for i, file := range f.files {
				rows = append(rows, map[string]any{"index": i, "name": file.Path, "size": file.Size, "progress": file.Progress})
			}
			workflowJSON(w, rows)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	f.downloader = DownloaderConfig{Config: download.Config{ID: "fixture-qb", Kind: "qb", BaseURL: server.URL}, SharedPaths: true}
	a.config.Downloaders = []DownloaderConfig{f.downloader}
	rule := DirectoryRule{ID: "fixture-rule", SourcePath: source, RootID: "root", MediaType: kind, Mode: "copy", MonitorType: "downloader", Enabled: true, Renaming: importFlag(false), Scraping: importFlag(false), LibraryTypeFolder: importFlag(false)}
	a.config.Directories = []DirectoryRule{rule}
	f.intent = Intent{ID: "fixture-intent", WorkflowID: "fixture-workflow", Media: identity, DownloaderID: f.downloader.ID, DownloaderSnapshot: downloaderSnapshot(f.downloader), DownloadPath: source, DirectoryID: rule.ID, DirectorySnapshot: directoryFingerprint(rule), RootID: "root", ImportMode: "copy", Hash: hash, State: "downloaded"}
	if kind == "tv" {
		f.intent.Season, f.intent.Episodes = 1, []int{1, 2, 3}
	}
	if err := a.Store.Put(t.Context(), "intent", f.intent.ID, f.intent); err != nil {
		t.Fatal(err)
	}
	var err error
	f.client, err = download.New(f.downloader.Config)
	if err != nil {
		t.Fatal(err)
	}
	f.task = download.Task{Hash: hash, Progress: 1, TotalSize: total, Completed: total, Status: "seeding", DownloadDir: source}
	return f
}

func (f *completedPreparationFixture) saveCategory(t *testing.T, raw json.RawMessage) {
	t.Helper()
	tx, err := f.app.Store.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshot := downloadContextSnapshot{DownloadKind: "intent", DownloadID: f.intent.ID, Hash: f.intent.Hash, Original: &originalDownloadContext{Media: raw}}
	if err = f.app.saveDownloadContextTx(t.Context(), tx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletedDownloadPreparationFailurePersistsFilesOnce(t *testing.T) {
	for _, failure := range []string{"coverage", "preview"} {
		t.Run(failure, func(t *testing.T) {
			kind, code := "tv", "REVIEW_DOWNLOAD_COVERAGE"
			if failure == "preview" {
				kind, code = "movie", "INVALID_METADATA"
			}
			f := completedPreparationTestFixture(t, kind)
			if failure == "preview" {
				broken := f.intent.Media
				broken.Title = ""
				importCache(t, f.app, "media:movie:123", broken)
			}
			f.saveCategory(t, json.RawMessage(`{"category":"冻结分类","title":"旧标题","tmdb_id":999}`))
			err := f.app.planCompletedDownload(t.Context(), f.client, f.downloader, &f.intent, f.task)
			var api *APIError
			if !errors.As(err, &api) || api.Code != code {
				t.Fatalf("want %s, got %v", code, err)
			}
			var plan ImportPlan
			if err = f.app.Store.Get(t.Context(), "import_plan", f.intent.ImportPlanID, &plan); err != nil {
				t.Fatal(err)
			}
			if len(plan.Files) != len(f.files) || plan.IntentID != f.intent.ID || plan.Mode != "copy" || plan.Media.Title != f.intent.Media.Title || plan.Category != "冻结分类" {
				t.Fatalf("lost known facts: %+v", plan)
			}
			for i, file := range plan.Files {
				history, err := f.app.transferHistoryPlanFile(t.Context(), plan.ID, file.ID)
				if err != nil || history.Status != "failed" || history.JobState != "failed" || history.Media.ID != 123 || history.Mode != "copy" || history.Category != "冻结分类" || history.Target != "" || history.Size != f.files[i].Size {
					t.Fatalf("history=%+v err=%v", history, err)
				}
				if failure == "coverage" && (history.Season != 1 || len(history.Episodes) != 1 || history.Episodes[0] != i+1) {
					t.Fatalf("lost known episode: %+v", history)
				}
				if _, err = os.Stat(file.Source); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := f.app.pollDownloads(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			var jobs, plans int
			if err = f.app.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='import'`).Scan(&jobs); err != nil {
				t.Fatal(err)
			}
			if err = f.app.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='import_plan'`).Scan(&plans); err != nil {
				t.Fatal(err)
			}
			if jobs != len(f.files) || plans != 1 {
				t.Fatalf("poll duplicated failures: jobs=%d plans=%d", jobs, plans)
			}
			entries, err := os.ReadDir(f.target)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failure published files: %v %v", entries, err)
			}
		})
	}
}

func TestCompletedDownloadTransientPreparationHasNoFailureHistory(t *testing.T) {
	for _, transient := range []string{"connection", "metadata-unavailable", "incomplete-later-file", "size-later-file", "missing-later-file"} {
		t.Run(transient, func(t *testing.T) {
			f := completedPreparationTestFixture(t, "tv")
			switch transient {
			case "connection":
				f.fileStatus = http.StatusServiceUnavailable
			case "metadata-unavailable":
				if _, err := f.app.Store.DB.Exec(`DELETE FROM records WHERE kind='cache' AND id='season:123:1'`); err != nil {
					t.Fatal(err)
				}
				installMetadataFixture(t, f.app, "fixture-key", []string{"/3/tv/123/season/1"}, func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":"fixture unavailable"}`))}, nil
				})
			case "incomplete-later-file":
				f.files[1].Progress = .5
			case "size-later-file":
				f.files[1].Size++
			case "missing-later-file":
				if err := os.Remove(filepath.Join(f.source, f.files[1].Path)); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := f.app.planCompletedDownload(t.Context(), f.client, f.downloader, &f.intent, f.task); err == nil {
					t.Fatal("transient unexpectedly planned")
				}
			}
			var jobs, plans int
			if err := f.app.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='import'`).Scan(&jobs); err != nil {
				t.Fatal(err)
			}
			if err := f.app.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='import_plan'`).Scan(&plans); err != nil {
				t.Fatal(err)
			}
			if jobs != 0 || plans != 0 || f.intent.ImportPlanID != "" {
				t.Fatalf("transient created failure: jobs=%d plans=%d intent=%+v", jobs, plans, f.intent)
			}
		})
	}
}

func TestCompletedDownloadQualitySelectionIgnoresIncompleteUnselectedFile(t *testing.T) {
	f := completedPreparationTestFixture(t, "tv")
	f.intent.Episodes = []int{1}
	if err := f.app.Store.Put(t.Context(), "intent", f.intent.ID, f.intent); err != nil {
		t.Fatal(err)
	}
	f.files[1].Progress = 0
	if err := os.Remove(filepath.Join(f.source, f.files[1].Path)); err != nil {
		t.Fatal(err)
	}
	tx, err := f.app.Store.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	proof := qualityDownloadSnapshot{Version: 1, Kind: "intent", ID: f.intent.ID, Hash: f.intent.Hash, SubscriptionID: "fixture-subscription", Revision: 1}
	if err = f.app.writeQualitySealedTx(t.Context(), tx, "subscription_quality_download", "intent:"+f.intent.ID, proof); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = f.app.planCompletedDownload(t.Context(), f.client, f.downloader, &f.intent, f.task); err != nil {
		t.Fatal(err)
	}
	var plan ImportPlan
	if err = f.app.Store.Get(t.Context(), "import_plan", f.intent.ImportPlanID, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || plan.Files[0].Source != filepath.Join(f.source, f.files[0].Path) || len(plan.Files[0].Episodes) != 1 || plan.Files[0].Episodes[0] != 1 {
		t.Fatalf("selection included an unselected incomplete file: %+v", plan)
	}
}

func TestCompletedDownloadCategoryRequiresKnownStringAndMatchingContext(t *testing.T) {
	f := completedPreparationTestFixture(t, "movie")
	for _, raw := range []string{`{}`, `{"category":null}`, `{"category":17}`, `{"category":""}`} {
		f.saveCategory(t, json.RawMessage(raw))
		category, err := f.app.completedDownloadCategory(t.Context(), f.intent)
		if err != nil || category != "" {
			t.Fatalf("invented classification for %s: %q %v", raw, category, err)
		}
	}
	f.saveCategory(t, json.RawMessage(`{"category":"known"}`))
	wrong := f.intent
	wrong.Hash = strings.Repeat("f", 40)
	if _, err := f.app.completedDownloadCategory(t.Context(), wrong); err == nil {
		t.Fatal("classification accepted a mismatched download context")
	}
}

func TestCompletedTMDBDownloadKeepsFrozenCategoryNotOldIdentity(t *testing.T) {
	for _, folders := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-folders", true: "folders"}[folders], func(t *testing.T) {
			f := completedPreparationTestFixture(t, "movie")
			f.app.config.Directories[0].LibraryTypeFolder = importFlag(folders)
			f.intent.DirectorySnapshot = directoryFingerprint(f.app.config.Directories[0])
			if err := f.app.Store.Put(t.Context(), "intent", f.intent.ID, f.intent); err != nil {
				t.Fatal(err)
			}
			f.saveCategory(t, json.RawMessage(`{"category":"下载自定义分类","title":"过期标题","tmdb_id":999,"type":"电视剧"}`))
			calls := useCategoryDetailResponse(t, f.app, 500, map[string]any{"error": "unexpected metadata call"})
			if err := f.app.planCompletedDownload(t.Context(), f.client, f.downloader, &f.intent, f.task); err != nil {
				t.Fatal(err)
			}
			var plan ImportPlan
			if err := f.app.Store.Get(t.Context(), "import_plan", f.intent.ImportPlanID, &plan); err != nil {
				t.Fatal(err)
			}
			if plan.Media.ID != 123 || plan.Media.Title != f.intent.Media.Title || plan.Media.MediaType != "movie" || plan.Category != "下载自定义分类" || *calls != 0 {
				t.Fatalf("category replaced current identity or made request: plan=%+v calls=%d", plan, *calls)
			}
			if strings.Contains(plan.Files[0].Target, string(filepath.Separator)+"下载自定义分类"+string(filepath.Separator)) != folders {
				t.Fatalf("category folder switch ignored: %s", plan.Files[0].Target)
			}
			// MP history is written only when execution completes; planning must
			// not create a pending row.
		})
	}
}
