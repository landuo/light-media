package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

func importFlag(value bool) *bool { return &value }

func TestImportRenameScrapeAndTypeFolderOptionsPublishRealFiles(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		for _, rename := range []bool{false, true} {
			for _, scrape := range []bool{false, true} {
				for _, typeFolder := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/rename-%t/scrape-%t/type-%t", kind, rename, scrape, typeFolder), func(t *testing.T) {
						a, source, target := importTestApp(t)
						ctx := context.Background()
						identity := importFixture(t, a, kind)
						name := "Original.Movie.2024.WEB-DL.MKV"
						entry := ImportFileRequest{}
						if kind == "tv" {
							name = "Original.Show.S02E03.WEB-DL.MKV"
							entry.Season, entry.Episodes = 2, []int{3}
							importCache(t, a, "season:123:2", catalog.Season{ID: 20, SeriesID: 123, SeasonNumber: 2, Episodes: []catalog.Episode{{ID: 203, SeasonNumber: 2, EpisodeNumber: 3, Name: "第三集"}}})
						}
						entry.Path = filepath.Join(source, name)
						importWrite(t, entry.Path, []byte("option synthetic video"))
						importWrite(t, strings.TrimSuffix(entry.Path, ".MKV")+".zh.srt", []byte("synthetic subtitle"))
						plan, e := a.previewImport(ctx, ImportRequest{MediaType: kind, TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{entry}, Renaming: importFlag(rename), Scraping: importFlag(scrape), LibraryTypeFolder: importFlag(typeFolder)})
						if e != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
							t.Fatalf("preview=%+v error=%v", plan, e)
						}
						base := target
						if typeFolder {
							if kind == "movie" {
								base = filepath.Join(base, "华语电影")
							} else {
								base = filepath.Join(base, "国产剧")
							}
						}
						want := filepath.Join(base, name)
						if rename {
							folder := fmt.Sprintf("%s (%d)", identity.Title, identity.Year)
							want = filepath.Join(base, folder, folder+".mkv")
							if kind == "tv" {
								want = filepath.Join(base, folder, "Season 2", identity.Title+" - S02E03 - 第 3 集.MKV")
							}
						}
						if plan.Files[0].Target != want {
							t.Fatalf("target=%s want=%s", plan.Files[0].Target, want)
						}
						job, e := a.executeImport(ctx, plan.ID, nil)
						if e != nil {
							t.Fatal(e)
						}
						_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
						job.State, job.Generation = "running", 1
						if e = a.runImport(ctx, job, plan.ID, nil); e != nil {
							t.Fatal(e)
						}
						if got, e := os.ReadFile(want); e != nil || string(got) != "option synthetic video" {
							t.Fatal("published video", e)
						}
						if got, e := os.ReadFile(entry.Path); e != nil || string(got) != "option synthetic video" {
							t.Fatal("source changed", e)
						}
						subtitle := strings.TrimSuffix(want, filepath.Ext(want)) + ".zh.srt"
						if _, e := os.Stat(subtitle); e != nil {
							t.Fatal("subtitle name", e)
						}
						var count int
						_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='scrape'`).Scan(&count)
						if count != map[bool]int{false: 0, true: 1}[scrape] {
							t.Fatal("unexpected scrape job", count)
						}
						var asset Asset
						if e = a.assetAtPath(ctx, want, &asset); e != nil {
							t.Fatal(e)
						}
						if !scrape {
							if asset.ScrapeState != "skipped" {
								t.Fatal("disabled scrape not recorded", asset.ScrapeState)
							}
							if _, e = os.Stat(strings.TrimSuffix(want, filepath.Ext(want)) + ".nfo"); !os.IsNotExist(e) {
								t.Fatal("disabled scrape wrote NFO", e)
							}
							return
						}
						scrapeJob, e := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='scrape' LIMIT 1`))
						if e != nil {
							t.Fatal(e)
						}
						_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, scrapeJob.ID)
						scrapeJob.State, scrapeJob.Generation = "running", 1
						if e = a.runScrape(ctx, scrapeJob, asset.ID); e != nil {
							t.Fatal(e)
						}
						if _, e = os.Stat(strings.TrimSuffix(want, filepath.Ext(want)) + ".nfo"); e != nil {
							t.Fatal("enabled scrape missing NFO", e)
						}
						if kind == "tv" {
							showDir := filepath.Dir(want)
							if rename {
								showDir = filepath.Dir(showDir)
							}
							if _, e = os.Stat(filepath.Join(showDir, "tvshow.nfo")); e != nil {
								t.Fatal("TV metadata wrong directory", e)
							}
							if !rename {
								if _, e = os.Stat(filepath.Join(filepath.Dir(showDir), "tvshow.nfo")); !os.IsNotExist(e) {
									t.Fatal("flat TV metadata escaped destination", e)
								}
							}
						}
					})
				}
			}
		}
	}
}

func TestDirectoryImportOptionsPropagateAndKeepLegacyFingerprint(t *testing.T) {
	a, rule, source := directoryFileFixture(t, true)
	legacy := directoryFingerprint(rule)
	rule.Renaming, rule.Scraping, rule.LibraryTypeFolder = importFlag(true), importFlag(true), importFlag(false)
	if directoryFingerprint(rule) != legacy {
		t.Fatal("explicit legacy defaults invalidated unchanged queued rules")
	}
	rule.Renaming, rule.Scraping, rule.LibraryTypeFolder = importFlag(false), importFlag(false), importFlag(true)
	a.config.Directories = []DirectoryRule{rule}
	path := directoryMovieFile(t, source)
	directoryPoll(t, a, rule)
	directoryAge(t, a, rule, path)
	directoryPoll(t, a, rule)
	observation := directoryRecognize(t, a, rule, path)
	var plan ImportPlan
	if e := a.Store.Get(context.Background(), "import_plan", observation.PlanID, &plan); e != nil {
		t.Fatal(e)
	}
	if importBool(plan.Renaming, true) || importBool(plan.Scraping, true) || !importBool(plan.LibraryTypeFolder, false) {
		t.Fatal("monitor lost configured options", plan)
	}
	root, e := a.importRoot(rule.RootID)
	if e != nil {
		t.Fatal(e)
	}
	if plan.Files[0].Target != filepath.Join(root.Path, "华语电影", filepath.Base(path)) {
		t.Fatal("monitor target", plan.Files[0].Target)
	}
	if directoryFingerprint(rule) == legacy {
		t.Fatal("changed output settings did not invalidate old rule")
	}
	public := publicDirectory(a.Settings(), rule)
	if public["renaming"] != false || public["scraping"] != false || public["library_type_folder"] != true {
		t.Fatal("public settings lost false options", public)
	}
}

func TestManualImportInheritsDirectoryOptionsAndExplicitOverrides(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	path := filepath.Join(source, "Original.Movie.2024.mkv")
	importWrite(t, path, []byte("manual option fixture"))
	a.config.Directories = []DirectoryRule{{ID: "options", Enabled: true, MonitorType: "monitor", SourcePath: source, RootID: "root", MediaType: "movie", Mode: "copy", Renaming: importFlag(false), Scraping: importFlag(false), LibraryTypeFolder: importFlag(true)}}
	request := ImportRequest{MediaType: "movie", TMDBID: 123, Files: []ImportFileRequest{{Path: path}}}
	plan, e := a.previewImport(context.Background(), request)
	if e != nil {
		t.Fatal(e)
	}
	if plan.Mode != "copy" || importBool(plan.Renaming, true) || importBool(plan.Scraping, true) || plan.Files[0].Target != filepath.Join(target, "华语电影", filepath.Base(path)) {
		t.Fatalf("manual did not inherit %+v", plan)
	}
	request.Renaming, request.Scraping, request.LibraryTypeFolder = importFlag(true), importFlag(true), importFlag(false)
	plan, e = a.previewImport(context.Background(), request)
	if e != nil {
		t.Fatal(e)
	}
	if !importBool(plan.Renaming, false) || !importBool(plan.Scraping, false) || importBool(plan.LibraryTypeFolder, true) || plan.Files[0].Target != filepath.Join(target, "测试影片 (2024)", "测试影片 (2024).mkv") {
		t.Fatalf("manual override ignored %+v", plan)
	}
}

func TestCompletedDownloadPassesDirectoryImportOptions(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	identity := importFixture(t, a, "movie")
	directory := filepath.Join(source, "chosen")
	name := "Original.Movie.2024.WEB-DL.mkv"
	path := filepath.Join(directory, name)
	data := []byte("completed download synthetic")
	importWrite(t, path, data)
	_, hash := workflowTorrent(name)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/info" {
			task := workflowTask(hash, 1)
			task["save_path"], task["size"], task["completed"] = directory, len(data), len(data)
			workflowJSON(w, []any{task})
			return
		}
		if r.URL.Path == "/api/v2/torrents/files" {
			workflowJSON(w, []any{map[string]any{"index": 0, "name": name, "size": len(data), "progress": 1.0}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: server.URL}, SharedPaths: true}
	a.config.Downloaders = []DownloaderConfig{d}
	rule := DirectoryRule{ID: "directory", Name: "options", SourcePath: directory, RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "downloader", Enabled: true, Renaming: importFlag(false), Scraping: importFlag(false), LibraryTypeFolder: importFlag(true)}
	a.config.Directories = []DirectoryRule{rule}
	intent := Intent{ID: "options-intent", Media: identity, DownloaderID: d.ID, DownloadPath: directory, DirectoryID: rule.ID, DirectorySnapshot: directoryFingerprint(rule), RootID: "root", ImportMode: "hardlink", Hash: hash, State: "downloaded", DownloaderSnapshot: downloaderSnapshot(d)}
	if e := a.Store.Put(ctx, "intent", intent.ID, intent); e != nil {
		t.Fatal(e)
	}
	client, e := download.New(d.Config)
	if e != nil {
		t.Fatal(e)
	}
	task := download.Task{Hash: hash, Progress: 1, TotalSize: int64(len(data)), Completed: int64(len(data)), Status: "seeding", DownloadDir: directory}
	if e = a.planCompletedDownload(ctx, client, d, &intent, task); e != nil {
		t.Fatal(e)
	}
	var plan ImportPlan
	if e = a.Store.Get(ctx, "import_plan", intent.ImportPlanID, &plan); e != nil {
		t.Fatal(e)
	}
	if importBool(plan.Renaming, true) || importBool(plan.Scraping, true) || plan.Files[0].Target != filepath.Join(target, "华语电影", name) {
		t.Fatalf("completion dropped directory options %+v", plan)
	}
}
