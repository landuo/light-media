package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"light-media/internal/download"
)

func TestDirectorySelectionPersistsAndSubmitsActualSavePath(t *testing.T) {
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	torrent, hash := workflowTorrent("测试影片.2024.1080p.mkv")
	var mu sync.Mutex
	var saved string
	var added bool
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if added {
				task := workflowTask(hash, 0)
				task["save_path"] = saved
				workflowJSON(w, []any{task})
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			if e := r.ParseMultipartForm(4 << 20); e != nil {
				t.Error(e)
			}
			saved = r.FormValue("savepath")
			added = true
			io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/torrents.php" {
			io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
		} else if r.URL.Path == "/download.php" {
			w.Write(torrent)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	a.config.Downloaders[0].Mappings = []PathMapping{{From: "/downloads", To: source}}
	rule := DirectoryRule{ID: "movies", Name: "电影下载", DownloaderID: "fixture-qb", DownloadPath: "/downloads/movies", RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "downloader", Enabled: true}
	a.config.Directories = []DirectoryRule{rule}
	ctx := context.Background()
	p, e := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID}, SiteID: "fixture-site", ResourceID: "101", DownloaderID: "fixture-qb", DirectoryID: rule.ID, DownloadPath: "/downloads/movies/custom"}, false)
	if e != nil {
		t.Fatal(e)
	}
	if p.DownloadPath != "/downloads/movies/custom" || p.DirectoryID != rule.ID || p.RootID != "root" {
		t.Fatalf("preview lost destination: %+v", p)
	}
	in, e := a.confirmDownload(ctx, p.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	if in.DownloadPath != p.DownloadPath || in.DirectorySnapshot == "" {
		t.Fatal("intent did not freeze download choice")
	}
	j, e := a.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.runDownload(ctx, j, in.ID); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	actual := saved
	mu.Unlock()
	if actual != "/downloads/movies/custom" {
		t.Fatalf("submitted downloader default instead of user selection: %q", actual)
	}
	s, e := a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: m.MediaType, TMDBID: m.ID, DownloaderID: "fixture-qb", DirectoryID: rule.ID, DownloadPath: "/downloads/movies/custom", State: "paused"})
	if e != nil {
		t.Fatal(e)
	}
	if s.DownloadPath != actual || s.DirectoryID != rule.ID {
		t.Fatal("subscription lost selected directory")
	}
}

func TestDirectoryDownloadWithoutRuleNeverImportsAndRejectsUnsafePaths(t *testing.T) {
	a, source, _ := importTestApp(t)
	d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}, DownloadPath: "/downloads", Mappings: []PathMapping{{From: "/downloads", To: source}}}
	a.config.Downloaders = []DownloaderConfig{d}
	dest, e := a.resolveDownloadDestination(d, "", "/downloads/only", "", "movie")
	if e != nil {
		t.Fatal(e)
	}
	if dest.Root != "" || dest.Rule.ID != "" {
		t.Fatal("unconfigured path acquired library destination")
	}
	in := Intent{ID: "no-rule", DownloaderID: d.ID, DownloadPath: dest.Path}
	if e = a.guardIntentDirectory(context.Background(), in, true); e == nil {
		t.Fatal("unconfigured directory allowed automatic import")
	}
	for _, p := range []string{"/downloads/../outside", "/unmapped", "/", "/etc/shadow"} {
		if _, e = a.resolveDownloadDestination(d, "", p, "", "movie"); e == nil {
			t.Errorf("unsafe destination accepted: %s", p)
		}
	}
}

func TestDirectoryCompletedDownloadWaitsForCompletionAndKeepsSource(t *testing.T) {
	a, source, target := importTestApp(t)
	m := importFixture(t, a, "movie")
	filename := "测试影片.2024.1080p.mkv"
	path := filepath.Join(source, "chosen", filename)
	importWrite(t, path, bytes.Repeat([]byte{'x'}, 1024))
	_, hash := workflowTorrent(filename)
	var complete = true
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/info" {
			task := workflowTask(hash, 1)
			task["save_path"] = "/downloads/chosen"
			workflowJSON(w, []any{task})
		} else if r.URL.Path == "/api/v2/torrents/files" {
			progress := 1.0
			if !complete {
				progress = .5
			}
			workflowJSON(w, []any{map[string]any{"index": 0, "name": filename, "size": 1024, "progress": progress}})
		} else {
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: qb.URL}, DownloadPath: "/downloads", Mappings: []PathMapping{{From: "/downloads", To: source}}}
	a.config.Downloaders = []DownloaderConfig{d}
	rule := DirectoryRule{ID: "rule", Name: "测试目录", DownloaderID: d.ID, DownloadPath: "/downloads/chosen", RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "monitor", Enabled: true}
	a.config.Directories = []DirectoryRule{rule}
	in := Intent{ID: "intent-directory", Media: m, DownloaderID: d.ID, DownloadPath: rule.DownloadPath, DirectoryID: rule.ID, DirectorySnapshot: directoryFingerprint(rule), RootID: "root", ImportMode: "hardlink", Hash: hash, State: "downloaded", DownloaderSnapshot: downloaderSnapshot(d)}
	ctx := context.Background()
	if e := a.Store.Put(ctx, "intent", in.ID, in); e != nil {
		t.Fatal(e)
	}
	client, e := download.New(d.Config)
	if e != nil {
		t.Fatal(e)
	}
	task := download.Task{Hash: hash, Progress: 1, TotalSize: 1024, Completed: 1024, Status: "seeding", DownloadDir: rule.DownloadPath}
	complete = false
	if e = a.planCompletedDownload(ctx, client, d, &in, task); e == nil {
		t.Fatal("incomplete file reached import")
	}
	if in.ImportPlanID != "" {
		t.Fatal("partial file created plan")
	}
	complete = true
	wrong := task
	wrong.DownloadDir = "/downloads/other"
	if e = a.planCompletedDownload(ctx, client, d, &in, wrong); e == nil {
		t.Fatal("changed downloader save path reached import")
	}
	if e = a.planCompletedDownload(ctx, client, d, &in, task); e != nil {
		t.Fatal(e)
	}
	job, e := a.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if job.Kind != "import" {
		t.Fatalf("wrong job %s", job.Kind)
	}
	if e = a.runImport(ctx, job, in.ImportPlanID, nil); e != nil {
		t.Fatal(e)
	}
	original, e := os.Stat(path)
	if e != nil {
		t.Fatal("source was removed", e)
	}
	var plan ImportPlan
	if e = a.Store.Get(ctx, "import_plan", in.ImportPlanID, &plan); e != nil {
		t.Fatal(e)
	}
	published, e := os.Stat(plan.Files[0].Target)
	if e != nil {
		t.Fatal(e)
	}
	if !os.SameFile(original, published) || !strings.HasPrefix(plan.Files[0].Target, target) {
		t.Fatal("source was not hardlinked into rule target")
	}
	if e = a.pollDownloads(ctx); e != nil {
		t.Fatal(e)
	} // Imported terminal state is excluded; no downloader List request.
}

func TestDirectoryRuleDisabledAfterPreviewPreventsAutomaticImport(t *testing.T) {
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	path := filepath.Join(source, "film.mkv")
	importWrite(t, path, []byte("complete"))
	d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}, DownloadPath: "/downloads", Mappings: []PathMapping{{From: "/downloads", To: source}}}
	a.config.Downloaders = []DownloaderConfig{d}
	rule := DirectoryRule{ID: "r", Name: "规则", DownloaderID: d.ID, DownloadPath: d.DownloadPath, RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "downloader", Enabled: true}
	a.config.Directories = []DirectoryRule{rule}
	in := Intent{ID: "dl-rule-change", Media: m, DownloaderID: d.ID, DirectoryID: rule.ID, DownloadPath: d.DownloadPath, DirectorySnapshot: directoryFingerprint(rule), DownloaderSnapshot: downloaderSnapshot(d), RootID: "root", State: "downloaded"}
	ctx := context.Background()
	a.Store.Put(ctx, "intent", in.ID, in)
	plan, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: m.ID, RootID: "root", Mode: "hardlink", IntentID: in.ID, Files: []ImportFileRequest{{Path: path}}})
	if e != nil {
		t.Fatal(e)
	}
	a.config.Directories[0].Enabled = false
	if _, e = a.executeImport(ctx, plan.ID, nil); e == nil {
		t.Fatal("disabled rule queued an automatic import")
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal(e)
	}
}

func TestDirectoryConfigurationRejectsOverlapAndResumesEnabledRule(t *testing.T) {
	a, source, target := importTestApp(t)
	d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}, DownloadPath: "/downloads", Mappings: []PathMapping{{From: "/downloads", To: source}}}
	a.config.Downloaders = []DownloaderConfig{d}
	rule := DirectoryRule{ID: "r", Name: "目录", DownloaderID: d.ID, DownloadPath: "/downloads", RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "monitor", Enabled: false}
	ctx := context.Background()
	if _, e := a.saveManagedDirectory(ctx, rule); e != nil {
		t.Fatal(e)
	}
	var sealed []byte
	if e := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed); e != nil {
		t.Fatal(e)
	}
	var persisted Settings
	if e := a.Store.Unseal(sealed, &persisted); e != nil {
		t.Fatal(e)
	}
	if len(persisted.Directories) != 1 || persisted.Directories[0].Enabled {
		t.Fatal("rule was not persisted disabled")
	}
	in := Intent{DirectoryID: rule.ID, DirectorySnapshot: directoryFingerprint(rule), DownloaderID: d.ID, DownloadPath: d.DownloadPath, RootID: "root"}
	rule.Enabled = true
	if _, e := a.saveManagedDirectory(ctx, rule); e != nil {
		t.Fatal(e)
	}
	if e := a.guardIntentDirectory(ctx, in, true); e != nil {
		t.Fatal("enabling unchanged rule did not resume downloaded files", e)
	}
	nested := rule
	nested.ID = "nested"
	nested.DownloadPath = "/downloads/movies"
	if _, e := a.saveManagedDirectory(ctx, nested); e == nil {
		t.Fatal("overlapping monitors accepted")
	}
	overlap := rule
	overlap.SourcePath = target
	if _, e := a.saveManagedDirectory(ctx, overlap); e == nil {
		t.Fatal("unmapped library source accepted")
	}
	if _, e := a.deleteManagedDirectory(ctx, rule.ID); e != nil {
		t.Fatal(e)
	}
	if e := a.guardIntentDirectory(ctx, in, true); e == nil {
		t.Fatal("removed rule still authorized import")
	}
	entries, e := os.ReadDir(target)
	if e != nil || len(entries) != 0 {
		t.Fatal("configuration changes scanned or wrote files", e)
	}
}

func TestIndependentMonitorMatchesDownloaderSavePathAndSamePathNeedsNoMapping(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}, DownloadPath: source}
	a.config.Downloaders = []DownloaderConfig{d}
	rule := DirectoryRule{ID: "standalone", Name: "独立文件监听", SourcePath: source, RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "monitor", Enabled: true}
	if _, e := a.saveManagedDirectory(ctx, rule); e != nil {
		t.Fatal(e)
	}
	dest, e := a.resolveDownloadDestination(d, "", filepath.Join(source, "chosen"), "", "movie")
	if e != nil {
		t.Fatal(e)
	}
	if dest.Rule.ID != rule.ID || dest.Root != "root" {
		t.Fatal("download path did not discover independent monitor")
	}
	explicit, e := a.resolveDownloadDestination(d, rule.ID, dest.Path, "", "movie")
	if e != nil || explicit.Rule.ID != rule.ID {
		t.Fatal("independent monitor cannot be selected explicitly", e)
	}
	in := Intent{DownloaderID: d.ID, DownloadPath: dest.Path, DirectoryID: rule.ID, DirectorySnapshot: directoryFingerprint(rule), RootID: dest.Root}
	if e = a.guardIntentDirectory(ctx, in, true); e != nil {
		t.Fatal(e)
	}
	if mapped, e := mapDownloaderPath(a.Settings(), d, filepath.Join(source, "chosen", "file.mkv")); e != nil || mapped != filepath.Join(source, "chosen", "file.mkv") {
		t.Fatal("same path cannot resolve completed file", e)
	}
	if _, e = mapDownloaderPath(a.Settings(), d, "/unapproved/example.mkv"); e == nil {
		t.Fatal("same-path mode escaped allowed roots")
	}
	a.config.Downloaders[0].Mappings = []PathMapping{{From: source, To: filepath.Dir(source)}}
	if e = a.guardIntentDirectory(ctx, in, true); e == nil {
		t.Fatal("changed mapping left independent monitor but remained authorized")
	}
}
