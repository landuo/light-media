package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
)

func TestPhysicalDirectoryRuleNeedsOnlySourceAndTarget(t *testing.T) {
	a, source, target := importTestApp(t)
	a.config.Roots = nil
	rule := DirectoryRule{Name: "电影", SourcePath: source, LibraryPath: target, MediaType: "movie", Mode: "hardlink", MonitorType: "downloader"}
	if _, err := a.saveManagedDirectory(context.Background(), rule); err != nil {
		t.Fatal(err)
	}
	stored := a.Settings().Directories[0]
	if stored.DownloaderID != "" || stored.DownloadPath != "" || stored.RootID == "" {
		t.Fatalf("physical rule still requires downloader identity: %+v", stored)
	}
	root, err := a.importRoot(stored.RootID)
	if err != nil || root.Path != target || root.FNOSLibraryGUID != "" {
		t.Fatalf("physical target requires FNOS association: %+v %v", root, err)
	}
	for _, key := range []string{"qb", "tr"} {
		d := DownloaderConfig{Config: download.Config{ID: key, Kind: key, BaseURL: "http://127.0.0.1:1"}, SharedPaths: true}
		a.config.Downloaders = append(a.config.Downloaders, d)
		choice, err := a.resolveDownloadDestination(d, "", filepath.Join(source, "chosen"), "", "movie")
		if err != nil || choice.Rule.ID != stored.ID || choice.Path != filepath.Join(source, "chosen") {
			t.Fatalf("shared directory unavailable to %s: %+v %v", key, choice, err)
		}
	}
	public, err := a.managedDirectories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(public)
	for _, hidden := range []string{"downloader_id", "root_id", "download_path", "fnos_library_guid"} {
		if strings.Contains(string(raw), `"`+hidden+`"`) {
			t.Fatalf("public physical directory leaks obsolete field %s", hidden)
		}
	}
}

func TestManualImportAutomaticIdentityAndPhysicalTarget(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	a.config.Roots = nil
	p := filepath.Join(source, "unidentified.mkv")
	importWrite(t, p, []byte("synthetic video"))
	importWrite(t, strings.TrimSuffix(p, ".mkv")+".nfo", []byte(`<movie><title>测试影片</title><uniqueid type="tmdb" default="true">123</uniqueid></movie>`))
	plan, err := a.previewImport(context.Background(), ImportRequest{TargetPath: target, Files: []ImportFileRequest{{Path: p}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatalf("manual automatic preview failed: %+v %v", plan, err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.State, job.Generation = "running", 1
	if err = a.runImport(context.Background(), job, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(p)
	if err != nil {
		t.Fatal("source removed", err)
	}
	targetInfo, err := os.Stat(plan.Files[0].Target)
	if err != nil || !os.SameFile(sourceInfo, targetInfo) {
		t.Fatal("physical import did not preserve hard-linked source", err)
	}
}

func TestManualImportDefaultTargetUsesSourceRuleAndAmbiguityDoesNotWrite(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	if _, err := a.saveManagedDirectory(context.Background(), DirectoryRule{Name: "电影", SourcePath: source, LibraryPath: target, MediaType: "movie", Mode: "copy", MonitorType: "none"}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(source, "unknown.mkv")
	importWrite(t, p, []byte("synthetic"))
	if _, err := a.previewImport(context.Background(), ImportRequest{Files: []ImportFileRequest{{Path: p}}}); err == nil {
		t.Fatal("unknown identity was silently selected")
	}
	if _, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, Files: []ImportFileRequest{{Path: p}}}); pluginErrorCode(err) != "DIRECTORY_TARGET_REQUIRED" {
		t.Fatal("unsorted source rule supplied an original automatic target", err)
	}
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, TargetPath: target, Mode: "copy", Files: []ImportFileRequest{{Path: p}}})
	if err != nil || !within(target, plan.Files[0].Target) || plan.Files[0].Method != "copy" {
		t.Fatalf("automatic physical target failed: %+v %v", plan, err)
	}
	if _, err = os.Stat(plan.Files[0].Target); !os.IsNotExist(err) {
		t.Fatal("preview wrote destination")
	}
	rule := a.Settings().Directories[0]
	rule.Enabled = true
	rule.MonitorType = "monitor"
	if _, err = a.saveManagedDirectory(context.Background(), rule); err != nil {
		t.Fatal(err)
	}
	plan, err = a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, Files: []ImportFileRequest{{Path: p}}})
	if err != nil || !within(target, plan.Files[0].Target) || plan.Files[0].Method != "copy" {
		t.Fatal("actual monitored original source rule did not supply default", plan, err)
	}
}

func TestSharedDownloaderUsesContainerPathAndReadsDaemonDefaultOverLegacyPath(t *testing.T) {
	a, source, _ := importTestApp(t)
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v2/app/preferences" {
			t.Errorf("unexpected daemon mutation %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		workflowJSON(w, map[string]any{"save_path": source + "/"})
	}))
	defer qb.Close()
	d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: qb.URL}, DownloadPath: filepath.Join(source, "legacy-hidden-default"), SharedPaths: true, Mappings: []PathMapping{{From: source, To: "/legacy-do-not-use"}}}
	a.config.Downloaders = []DownloaderConfig{d}
	a.config.DefaultRoot = "root"
	choice, err := a.resolveDownloadDestinationContext(context.Background(), d, "", "", "", "movie")
	if err != nil || choice.Path != source || choice.Root != "" {
		t.Fatalf("daemon default was not frozen independently from library: %+v %v", choice, err)
	}
	if got, err := mapDownloaderPath(a.Settings(), d, source); err != nil || got != source {
		t.Fatalf("container path was rewritten by legacy mapping: %s %v", got, err)
	}
	if err := validateSettings(a.Settings()); err != nil {
		t.Fatal("downloader unnecessarily requires configured download path", err)
	}
}

func TestPhysicalDirectoryOverlapRejectedWithoutChangingConfiguration(t *testing.T) {
	a, source, _ := importTestApp(t)
	before := len(a.Settings().Roots)
	_, err := a.saveManagedDirectory(context.Background(), DirectoryRule{Name: "错误目录", SourcePath: source, LibraryPath: filepath.Join(source, "library")})
	if err == nil || len(a.Settings().Directories) != 0 || len(a.Settings().Roots) != before {
		t.Fatal("overlapping directory was accepted or partially persisted")
	}
}

func TestPhysicalDownloadOnlyDirectoryHasNoLibrarySideEffect(t *testing.T) {
	a, source, _ := importTestApp(t)
	a.config.DefaultRoot = "root"
	if _, err := a.saveManagedDirectory(context.Background(), DirectoryRule{Name: "仅下载", SourcePath: source, MonitorType: "none"}); err != nil {
		t.Fatal(err)
	}
	rule := a.Settings().Directories[0]
	d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}, SharedPaths: true}
	a.config.Downloaders = []DownloaderConfig{d}
	choice, err := a.resolveDownloadDestination(d, rule.ID, "", "", "movie")
	if err != nil || choice.Path != source || choice.Root != "" {
		t.Fatalf("download-only directory selected a library: %+v %v", choice, err)
	}
	if err := a.guardIntentDirectory(context.Background(), Intent{DirectoryID: rule.ID, DirectorySnapshot: directoryFingerprint(rule), DownloaderID: d.ID, DownloadPath: source}, true); err == nil {
		t.Fatal("download-only directory authorized an import")
	}
}

func TestUnconfiguredDaemonDefaultStillProtectsExistingSeedingFiles(t *testing.T) {
	a, source, _ := importTestApp(t)
	var calls atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/v2/torrents/info" {
			t.Errorf("unexpected downloader request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		task := workflowTask(strings.Repeat("a", 40), 1)
		task["save_path"] = filepath.Join(source, "external")
		workflowJSON(w, []any{task})
	}))
	defer qb.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: qb.URL}, SharedPaths: true}}
	path := filepath.Join(source, "external", "seeding.mkv")
	importWrite(t, path, []byte("seed data"))
	root := fileBrowserRoot(t, a, source)
	_, err := a.previewBrowserOperation(context.Background(), browserOperationRequest{Action: "delete", RootID: root.ID, Path: filepath.Dir(path), Paths: []string{path}})
	if fileCode(err) != "FILE_DOWNLOADER_SOURCE_PROTECTED" || calls.Load() != 1 {
		t.Fatalf("unconfigured default skipped seeding check: %v calls=%d", err, calls.Load())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("seeding source changed", err)
	}
}

func TestDisabledDownloaderIsNotPolledAndDoesNotLoseExistingIntent(t *testing.T) {
	a, source, _ := importTestApp(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "must not poll disabled daemon", 500)
	}))
	defer server.Close()
	flag := false
	d := DownloaderConfig{Config: download.Config{ID: "off", Kind: "qb", BaseURL: server.URL}, Enabled: &flag, SharedPaths: true}
	a.config.Downloaders = []DownloaderConfig{d}
	in := Intent{ID: "existing", DownloaderID: d.ID, DownloadPath: source, State: "accepted", Hash: strings.Repeat("a", 40)}
	if err := a.Store.Put(context.Background(), "intent", in.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, err := a.downloader(d.ID); fileCode(err) != "DOWNLOADER_DISABLED" {
		t.Fatal("disabled instance accepted explicit submission", err)
	}
	if _, err := a.downloadList(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.pollDownloads(context.Background()); err != nil {
		t.Fatal(err)
	}
	var after Intent
	if err := a.Store.Get(context.Background(), "intent", in.ID, &after); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || after.State != in.State {
		t.Fatalf("disabled downloader was polled or existing task changed: calls=%d state=%s", calls.Load(), after.State)
	}
	legacy := DownloaderConfig{Config: download.Config{ID: "legacy", Kind: "qb", BaseURL: server.URL}}
	if !downloaderEnabled(legacy) {
		t.Fatal("old configuration unexpectedly disabled")
	}
}

func TestPhysicalDirectoryBulkSaveIsAtomicAndMissingListDoesNotDeleteRules(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	if _, err := a.saveManagedDirectory(ctx, DirectoryRule{Name: "原规则", SourcePath: source, LibraryPath: target}); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(a.Settings())
	for _, body := range []string{`{}`, `{"directories":[{"name":"新规则","source_path":"` + source + `","library_path":"` + target + `"},{"name":"错误规则","source_path":"` + source + `","library_path":"` + source + `/nested"}]}`} {
		request := httptest.NewRequest("PUT", "/api/directories/manage", strings.NewReader(body))
		if _, err := a.directoryAPI(request, "manage"); err == nil {
			t.Fatal("invalid bulk replacement accepted")
		}
		after, _ := json.Marshal(a.Settings())
		if string(before) != string(after) {
			t.Fatal("failed bulk save partially changed configuration")
		}
	}
	request := httptest.NewRequest("PUT", "/api/directories/manage", strings.NewReader(`{"directories":[]}`))
	if _, err := a.directoryAPI(request, "manage"); err != nil || len(a.Settings().Directories) != 0 {
		t.Fatal("explicit empty list did not delete rules", err)
	}
}

func TestSharedDirectoryMonitorIgnoresUnrelatedTasksAndUsesActualSaveDirectories(t *testing.T) {
	a, source, _ := importTestApp(t)
	const relevantHash = "cccccccccccccccccccccccccccccccccccccccc"
	var fileCalls atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			tasks := []any{}
			for i, path := range []string{"/9kg", "/brushflow", source + "/"} {
				task := workflowTask(strings.Repeat(string(rune('a'+i)), 40), .5)
				if selected := r.URL.Query().Get("hashes"); selected != "" && task["hash"] != selected {
					continue
				}
				task["save_path"] = path
				tasks = append(tasks, task)
			}
			workflowJSON(w, tasks)
		case "/api/v2/torrents/files":
			fileCalls.Add(1)
			if r.URL.Query().Get("hash") != relevantHash {
				t.Errorf("unrelated daemon files read: %s", r.URL.Query().Get("hash"))
			}
			workflowJSON(w, []any{map[string]any{"index": 0, "name": "partial.mkv", "size": 20, "progress": .5}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: qb.URL}, DownloadPath: "/old-default", Mappings: []PathMapping{{From: "/legacy", To: "/old-visible"}}, SharedPaths: true}}
	files, err := a.directoryDownloadFiles(context.Background(), DirectoryRule{SourcePath: source}, source)
	entry, ok := files[filepath.Join(source, "partial.mkv")]
	if err != nil || !ok || entry.Complete || fileCalls.Load() != 1 {
		t.Fatalf("actual shared save directory not checked correctly: files=%+v error=%v calls=%d", files, err, fileCalls.Load())
	}
}

func TestOrderedPhysicalRulesProcessOnlyFirstMatchAndBlockStalePriorityPlan(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	target2 := filepath.Join(filepath.Dir(target), "second-library")
	if err := os.Mkdir(target2, 0750); err != nil {
		t.Fatal(err)
	}
	for i, dest := range []string{target, target2} {
		if _, err := a.saveManagedDirectory(context.Background(), DirectoryRule{Name: []string{"优先", "后备"}[i], SourcePath: source, LibraryPath: dest, MediaType: "movie", MonitorType: "monitor", Enabled: true, IncludeExisting: true}); err != nil {
			t.Fatal(err)
		}
	}
	first, second := a.Settings().Directories[0], a.Settings().Directories[1]
	p := directoryMovieFile(t, source)
	directoryPoll(t, a, first)
	directoryPoll(t, a, second)
	directoryAge(t, a, first, p)
	directoryPoll(t, a, first)
	directoryPoll(t, a, second)
	owned := directoryObservation(t, a, first, p)
	skipped := directoryObservation(t, a, second, p)
	if owned.PlanID == "" || skipped.PlanID != "" || skipped.State != "baseline" {
		t.Fatalf("overlapping watches both organized: first=%+v second=%+v", owned, skipped)
	}
	var plans int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='import_plan'`).Scan(&plans); err != nil || plans != 1 {
		t.Fatal("overlapping rules created duplicate plans", plans, err)
	}
	a.config.Directories = []DirectoryRule{second, first}
	if err := a.guardDirectoryFilePlan(context.Background(), owned.PlanID); fileCode(err) != "DIRECTORY_RULE_CHANGED" {
		t.Fatal("priority change left previous target authorized", err)
	}
}
