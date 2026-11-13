package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

func fileBrowserRoot(t *testing.T, a *App, path string) browserRoot {
	t.Helper()
	for _, root := range a.browserRoots() {
		if root.Path == path {
			return root
		}
	}
	t.Fatal("root not listed")
	return browserRoot{}
}
func fileCode(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func TestFileBrowserNavigationContainmentSortAndScan(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	root := fileBrowserRoot(t, a, source)
	importWrite(t, filepath.Join(source, "中文目录", "Demo S01E02.mkv"), []byte("synthetic video"))
	importWrite(t, filepath.Join(source, "z.txt"), []byte("z"))
	importWrite(t, filepath.Join(source, "a.txt"), []byte("aaa"))
	if e := os.Symlink(target, filepath.Join(source, "linked")); e != nil {
		t.Fatal(e)
	}
	out, e := a.browserList(ctx, root.ID, source, "name", "asc")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(out)
	var list struct {
		Items  []browserEntry `json:"items"`
		Parent string         `json:"parent"`
	}
	_ = json.Unmarshal(raw, &list)
	if len(list.Items) != 4 || list.Items[0].Name != "中文目录" || list.Parent != "" {
		t.Fatalf("listing=%s", raw)
	}
	if _, e = a.browserList(ctx, root.ID, filepath.Join(source, "linked"), "name", ""); e == nil {
		t.Fatal("symlink directory followed")
	}
	if _, e = a.browserList(ctx, root.ID, filepath.Join(source, ".."), "name", ""); e == nil {
		t.Fatal("root escape accepted")
	}
	if _, e = a.browserList(ctx, root.ID, source+"/../source", "name", ""); e == nil {
		t.Fatal("noncanonical path accepted")
	}
	result, e := a.browserScan(ctx, []string{filepath.Join(source, "中文目录"), filepath.Join(source, "中文目录", "Demo S01E02.mkv")}, true)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(result)
	if strings.Count(string(encoded), `"path"`) != 1 || !strings.Contains(string(encoded), `"episodes":[2]`) {
		t.Fatalf("scan=%s", encoded)
	}
	if _, e = a.browserScan(ctx, []string{filepath.Join(source, "linked")}, true); e == nil {
		t.Fatal("scan followed symlink")
	}
}

func TestFileBrowserContainerRootIsReadOnly(t *testing.T) {
	a, _, _ := importTestApp(t)
	cookie := setupTestAdmin(t, a)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(outside, "example.torrent")
	importWrite(t, file, []byte("container-only fixture"))
	link := filepath.Join(outside, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	listing := func(path string) string {
		return "/api/files/list?" + url.Values{"root_id": {containerBrowserRootID}, "path": {path}}.Encode()
	}
	h := a.Handler()
	if r := callTest(t, h, "GET", listing(outside), "", nil, ""); r.Code != 401 {
		t.Fatal("unauthenticated container listing", r.Code)
	}
	if r := callTest(t, h, "GET", "/api/files/roots", "", cookie, ""); r.Code != 200 || bytes.Contains(r.Body.Bytes(), []byte(`"kind":"container"`)) {
		t.Fatal("container root leaked into managed path pickers", r.Code)
	}
	if r := callTest(t, h, "GET", "/api/files/roots?scope=container", "", cookie, ""); r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"kind":"container"`)) {
		t.Fatal("missing container root", r.Code)
	}
	if r := callTest(t, h, "GET", listing("/"), "", cookie, ""); r.Code != 200 {
		t.Fatal("container / is not browsable", r.Code)
	}
	if r := callTest(t, h, "GET", listing(outside), "", cookie, ""); r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"name":"example.torrent"`)) || bytes.Contains(r.Body.Bytes(), []byte("container-only fixture")) {
		t.Fatal("container listing did not expose metadata only", r.Code)
	}
	if r := callTest(t, h, "GET", listing(link), "", cookie, ""); r.Code == 200 {
		t.Fatal("container listing followed a symlink")
	}
	content := "/api/files/content?" + url.Values{"root_id": {containerBrowserRootID}, "path": {file}}.Encode()
	if r := callTest(t, h, "GET", content, "", cookie, ""); r.Code == 200 {
		t.Fatal("container listing granted file downloads")
	}
	mutation, _ := json.Marshal(browserOperationRequest{Action: "delete", RootID: containerBrowserRootID, Path: outside, Paths: []string{file}})
	if r := callTest(t, h, "POST", "/api/files/operation-preview", string(mutation), cookie, ""); r.Code == 200 {
		t.Fatal("container listing granted file mutations")
	}
}

func TestFileOperationsPreviewConfirmAndQuarantinePreserveBytes(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	root := fileBrowserRoot(t, a, source)
	plan, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "mkdir", RootID: root.ID, Path: source, Name: "待整理"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(source, "待整理")); !os.IsNotExist(e) {
		t.Fatal("preview wrote directory")
	}
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); e != nil {
		t.Fatal("confirmation not idempotent", e)
	}
	old := filepath.Join(source, "源文件.mkv")
	importWrite(t, old, []byte("unchanged synthetic fixture"))
	plan, e = a.previewBrowserOperation(ctx, browserOperationRequest{Action: "rename", RootID: root.ID, Path: source, Paths: []string{old}, Name: "新名称.mkv"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); e != nil {
		t.Fatal(e)
	}
	renamed := filepath.Join(source, "新名称.mkv")
	if _, e = os.Stat(old); !os.IsNotExist(e) {
		t.Fatal("old name still present")
	}
	plan, e = a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: source, Paths: []string{renamed}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(plan.Items[0].Target)
	if e != nil || string(b) != "unchanged synthetic fixture" {
		t.Fatal("quarantine lost bytes", e)
	}
	listed, e := a.browserList(ctx, root.ID, source, "name", "")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), ".light-media-trash-") {
		t.Fatal("internal recycle entry exposed as normal selectable file")
	}
	if _, e = a.restoreBrowserOperation(ctx, plan.ID); e != nil {
		t.Fatal("restore failed", e)
	}
	b, e = os.ReadFile(renamed)
	if e != nil || string(b) != "unchanged synthetic fixture" {
		t.Fatal("restore changed bytes", e)
	}
	if _, e = a.restoreBrowserOperation(ctx, plan.ID); e != nil {
		t.Fatal("restore not idempotent", e)
	}
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); fileCode(e) != "FILE_PREVIEW_CONSUMED" {
		t.Fatal("restored delete was reused", e)
	}
}

func TestFileOperationsRejectStaleSymlinksAndProtectedPaths(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	root := fileBrowserRoot(t, a, source)
	file := filepath.Join(source, "test.mkv")
	importWrite(t, file, []byte("initial"))
	plan, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: source, Paths: []string{file}})
	if e != nil {
		t.Fatal(e)
	}
	importWrite(t, file, []byte("changed since preview"))
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); fileCode(e) != "FILE_SNAPSHOT_CHANGED" {
		t.Fatal("stale snapshot accepted", e)
	}
	if _, e = os.Stat(file); e != nil {
		t.Fatal("stale operation changed source", e)
	}
	importWrite(t, filepath.Join(target, "existing.mkv"), []byte("old library data"))
	if e = a.Store.Put(ctx, "asset", "existing", Asset{ID: "existing", Path: filepath.Join(target, "existing.mkv"), State: "present"}); e != nil {
		t.Fatal(e)
	}
	libraryRoot := fileBrowserRoot(t, a, target)
	if _, e = a.previewBrowserOperation(ctx, browserOperationRequest{Action: "rename", RootID: libraryRoot.ID, Path: target, Paths: []string{filepath.Join(target, "existing.mkv")}, Name: "bad.mkv"}); fileCode(e) != "FILE_IN_USE" {
		t.Fatal("library mutation permitted", e)
	}
	if e = os.Link(file, filepath.Join(source, "hardlink.mkv")); e != nil {
		t.Fatal(e)
	}
	if _, e = a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: source, Paths: []string{file}}); fileCode(e) != "FILE_LINK_PROTECTED" {
		t.Fatal("hardlink destructive action accepted", e)
	}
	if e = os.Symlink(target, filepath.Join(source, "directory-link")); e != nil {
		t.Fatal(e)
	}
	if _, e = a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: source, Paths: []string{filepath.Join(source, "directory-link")}}); e == nil {
		t.Fatal("symlink deletion accepted")
	}
	if _, e = a.previewBrowserOperation(ctx, browserOperationRequest{Action: "mkdir", RootID: root.ID, Path: source, Name: "../outside"}); e == nil {
		t.Fatal("mkdir traversal accepted")
	}
}

func TestFileOperationsProtectActiveImportAndDownloadMappings(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	root := fileBrowserRoot(t, a, source)
	file := filepath.Join(source, "busy.mkv")
	importWrite(t, file, []byte("busy file"))
	if e := a.Store.Put(ctx, "import_plan", "pending", ImportPlan{ID: "pending", State: "queued", Files: []PlanFile{{Source: file}}}); e != nil {
		t.Fatal(e)
	}
	if _, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: source, Paths: []string{file}}); fileCode(e) != "FILE_IN_USE" {
		t.Fatal("queued import source not protected", e)
	}
	a.config.Downloaders = []DownloaderConfig{{DownloadPath: "/remote/download", Mappings: []PathMapping{{From: "/remote", To: source}}}}
	dir := filepath.Join(source, "download")
	if e := os.Mkdir(dir, 0750); e != nil {
		t.Fatal(e)
	}
	if _, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "rename", RootID: root.ID, Path: source, Paths: []string{dir}, Name: "bad"}); fileCode(e) != "FILE_PROTECTED" {
		t.Fatal("mapped downloader path not protected", e)
	}
}

func TestFileDestructiveOperationsProtectOtherExistingDaemonTasks(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	var calls atomic.Int64
	var saveDir atomic.Value
	saveDir.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/info" || r.Method != "GET" {
			t.Errorf("non-readonly downloader request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		dir := saveDir.Load().(string)
		if dir == "" {
			_ = json.NewEncoder(w).Encode([]any{})
			return
		}
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"hash": strings.Repeat("a", 40), "name": "existing-external-task", "save_path": dir, "size": 1, "completed": 1, "progress": 1, "state": "uploading"}})
	}))
	defer server.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "qb", Kind: "qbittorrent", BaseURL: server.URL}, DownloadPath: "/remote/application-download", Mappings: []PathMapping{{From: "/remote", To: source}}}}
	root := fileBrowserRoot(t, a, source)
	directory := filepath.Join(source, "legacy")
	file := filepath.Join(directory, "external-task.mkv")
	importWrite(t, file, []byte("existing daemon source fixture"))
	other := filepath.Join(directory, "other-fixture.mkv")
	importWrite(t, other, []byte("other fixture"))
	plan, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: directory, Paths: []string{file, other}})
	if e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("preview List count", calls.Load())
	}
	saveDir.Store("/remote/legacy")
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); fileCode(e) != "FILE_DOWNLOADER_SOURCE_PROTECTED" {
		t.Fatal("freshly-added existing task source not protected", e)
	}
	if calls.Load() != 2 {
		t.Fatal("confirm List count", calls.Load())
	}
	if _, e = os.Stat(file); e != nil {
		t.Fatal("confirmation changed protected daemon source", e)
	}
	if _, e = a.previewBrowserOperation(ctx, browserOperationRequest{Action: "rename", RootID: root.ID, Path: directory, Paths: []string{file}, Name: "unsafe.mkv"}); fileCode(e) != "FILE_DOWNLOADER_SOURCE_PROTECTED" {
		t.Fatal("preexisting non-application torrent not protected", e)
	}
	if calls.Load() != 3 {
		t.Fatal("rename preview List count", calls.Load())
	}
}

func TestFileDownloaderVerificationFailureAndUnrelatedRoot(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "synthetic daemon unavailable", 503)
	}))
	defer server.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "qb", Kind: "qbittorrent", BaseURL: server.URL}, DownloadPath: "/remote/app", Mappings: []PathMapping{{From: "/remote", To: source}}}}
	root := fileBrowserRoot(t, a, source)
	file := filepath.Join(source, "ordinary.mkv")
	importWrite(t, file, []byte("ordinary fixture"))
	if _, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: source, Paths: []string{file}}); fileCode(e) != "FILE_DOWNLOADER_UNVERIFIED" {
		t.Fatal("unavailable downloader treated as no source", e)
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected daemon requests", calls.Load())
	}
	a.config.Downloaders[0].Mappings = []PathMapping{{From: "/remote", To: "/unrelated-media-mapping"}}
	plan, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "rename", RootID: root.ID, Path: source, Paths: []string{file}, Name: "safe-rename.mkv"})
	if e != nil {
		t.Fatal("unrelated local browser root depended on offline downloader", e)
	}
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("unrelated local operation contacted downloader", calls.Load())
	}
}

func TestFileConfirmRechecksWholeTreeAfterDownloaderVerification(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	directory := filepath.Join(source, "directory")
	child := filepath.Join(directory, "child.mkv")
	importWrite(t, child, []byte("before verification"))
	before, e := browserStat(directory)
	if e != nil {
		t.Fatal(e)
	}
	var lists atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/info" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if lists.Add(1) == 2 {
			if e := os.WriteFile(child, []byte("changed child while daemon List was pending"), 0640); e != nil {
				t.Error(e)
			}
		}
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	defer server.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "qb", Kind: "qbittorrent", BaseURL: server.URL}, DownloadPath: "/remote/app", Mappings: []PathMapping{{From: "/remote", To: source}}}}
	root := fileBrowserRoot(t, a, source)
	plan, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: source, Paths: []string{directory}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); fileCode(e) != "FILE_SNAPSHOT_CHANGED" {
		t.Fatal("changed child during verification was moved", e)
	}
	after, e := browserStat(directory)
	if e != nil || after.ModNS != before.ModNS {
		t.Fatal("fixture directory stamp changed or directory was renamed", e)
	}
	if lists.Load() != 2 {
		t.Fatal("must List only once in each stage", lists.Load())
	}
	raw, e := os.ReadFile(child)
	if e != nil || string(raw) != "changed child while daemon List was pending" {
		t.Fatal("confirmation changed concurrent source contents", e)
	}
	if _, e = os.Stat(plan.Items[0].Target); !os.IsNotExist(e) {
		t.Fatal("changed tree entered quarantine", e)
	}
}

func TestFileOperationPauseAndSettingsChanges(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	root := fileBrowserRoot(t, a, source)
	plan, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "mkdir", RootID: root.ID, Path: source, Name: "paused"})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.setControl(ctx, true); e != nil {
		t.Fatal(e)
	}
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); fileCode(e) != "AUTOMATION_PAUSED" {
		t.Fatal("paused filesystem write", e)
	}
	if e = a.setControl(ctx, false); e != nil {
		t.Fatal(e)
	}
	a.config.AllowedRoots = nil
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); fileCode(e) != "FILE_ROOT_CHANGED" {
		t.Fatal("removed root capability still valid", e)
	}
}

func TestFileOperationReconcilesRenameReceiptAfterCrash(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	root := fileBrowserRoot(t, a, source)
	file := filepath.Join(source, "before.mkv")
	importWrite(t, file, []byte("only synthetic fixture"))
	plan, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: source, Paths: []string{file}})
	if e != nil {
		t.Fatal(e)
	}
	plan.State = "executing"
	plan.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if e = a.Store.Put(ctx, "file_operation", plan.ID, plan); e != nil {
		t.Fatal(e)
	}
	parent, e := importDir(source, false)
	if e != nil {
		t.Fatal(e)
	}
	e = importRenameExclusive(int(parent.Fd()), filepath.Base(file), filepath.Base(plan.Items[0].Target))
	_ = unix.Fsync(int(parent.Fd()))
	parent.Close()
	if e != nil {
		t.Fatal(e)
	}
	result, e := a.confirmBrowserOperation(ctx, plan.ID)
	if e != nil || result.State != "completed" || !result.Items[0].Done {
		t.Fatalf("receipt recovery=%+v err=%v", result, e)
	}
	importWrite(t, file, []byte("different concurrent file"))
	if _, e = a.restoreBrowserOperation(ctx, plan.ID); fileCode(e) != "FILE_TARGET_EXISTS" {
		t.Fatal("restore overwrote concurrent file", e)
	}
	b, _ := os.ReadFile(file)
	if string(b) != "different concurrent file" {
		t.Fatal("restore replaced new file")
	}
}

func TestFileDirectoryQuarantineRestoresCompleteTree(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	root := fileBrowserRoot(t, a, source)
	directory := filepath.Join(source, "剧集目录")
	importWrite(t, filepath.Join(directory, "Season 1", "Show S01E01.mkv"), []byte("tree video"))
	importWrite(t, filepath.Join(directory, "Season 1", "Show S01E01.zh.srt"), []byte("字幕"))
	importWrite(t, filepath.Join(directory, ".private-note"), []byte("preserved hidden file"))
	plan, e := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: source, Paths: []string{directory}})
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Items[0].Manifest) != 5 {
		t.Fatal("incomplete directory snapshot", len(plan.Items[0].Manifest))
	}
	if _, e = a.confirmBrowserOperation(ctx, plan.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.restoreBrowserOperation(ctx, plan.ID); e != nil {
		t.Fatal(e)
	}
	for path, want := range map[string]string{filepath.Join(directory, "Season 1", "Show S01E01.mkv"): "tree video", filepath.Join(directory, "Season 1", "Show S01E01.zh.srt"): "字幕", filepath.Join(directory, ".private-note"): "preserved hidden file"} {
		got, e := os.ReadFile(path)
		if e != nil || string(got) != want {
			t.Fatal("directory round trip lost file", path, e)
		}
	}
}

func TestFileManagerAuthenticatedHTTPThroughOrganizeHistoryAndScrape(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	ctx := context.Background()
	file := filepath.Join(source, "中文合成影片 (2024).mkv")
	sourceBytes := []byte("HTTP synthetic media fixture bytes")
	importWrite(t, file, sourceBytes)
	cookie := setupTestAdmin(t, a)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	client := server.Client()
	do := func(method, path string, in any, authenticated bool) (int, json.RawMessage) {
		t.Helper()
		var body io.Reader
		if in != nil {
			raw, _ := json.Marshal(in)
			body = bytes.NewReader(raw)
		}
		req, e := http.NewRequest(method, server.URL+"/api"+path, body)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.AddCookie(cookie)
		}
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal(raw, &envelope)
		if res.StatusCode >= 400 {
			return res.StatusCode, raw
		}
		return res.StatusCode, envelope.Data
	}
	if code, _ := do("GET", "/files/roots", nil, false); code != 401 {
		t.Fatal("unauthenticated file browser", code)
	}
	code, data := do("GET", "/files/roots", nil, true)
	if code != 200 {
		t.Fatalf("roots %d %s", code, data)
	}
	var roots []browserRoot
	_ = json.Unmarshal(data, &roots)
	var root browserRoot
	for _, r := range roots {
		if r.Path == source {
			root = r
		}
	}
	if root.ID == "" {
		t.Fatal("source capability missing")
	}
	code, data = do("GET", "/files/list?"+url.Values{"root_id": {root.ID}, "path": {source}}.Encode(), nil, true)
	if code != 200 || !bytes.Contains(data, []byte("中文合成影片")) {
		t.Fatalf("file listing %d %s", code, data)
	}
	code, data = do("POST", "/files/scan", map[string]any{"paths": []string{file}, "recursive": true}, true)
	if code != 200 || !bytes.Contains(data, []byte(`"files":[`)) {
		t.Fatalf("selected-file scan %d %s", code, data)
	}
	code, data = do("POST", "/import/preview", ImportRequest{MediaType: "movie", TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: file}}}, true)
	var plan ImportPlan
	_ = json.Unmarshal(data, &plan)
	if code != 200 || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatalf("preview %d %s", code, data)
	}
	code, data = do("POST", "/import/execute", map[string]any{"plan_id": plan.ID, "file_ids": []string{plan.Files[0].ID}}, true)
	if code != 200 {
		t.Fatalf("confirm %d %s", code, data)
	}
	job, e := a.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.runImport(ctx, job, plan.ID, []string{plan.Files[0].ID}); e != nil {
		t.Fatal(e)
	}
	if _, e = a.finishJob(ctx, job, "succeeded", "", "", 0); e != nil {
		t.Fatal(e)
	}
	code, data = do("GET", "/history/transfers?status=success", nil, true)
	if code != 200 || !bytes.Contains(data, []byte(`"total":1`)) || !bytes.Contains(data, []byte(`"status":"success"`)) {
		t.Fatalf("published history %d %s", code, data)
	}
	code, data = do("GET", "/history/transfers/"+plan.Files[0].ID, nil, true)
	if code != 200 || !bytes.Contains(data, []byte(`"can_retry":false`)) {
		t.Fatalf("history detail %d %s", code, data)
	}
	code, data = do("POST", "/files/scrape", map[string]any{"paths": []string{plan.Files[0].Target}, "confirm": true}, true)
	if code != 200 || !bytes.Contains(data, []byte(`"jobs":[`)) {
		t.Fatalf("file manager scrape %d %s", code, data)
	}
	actual, e := os.ReadFile(file)
	if e != nil || !bytes.Equal(actual, sourceBytes) {
		t.Fatal("source modified in HTTP workflow", e)
	}
	var assets int
	if e = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&assets); e != nil || assets != 1 {
		t.Fatal("asset not durably created", assets, e)
	}
}

func TestTransferHistoryProjectsDurablePerFileProgressAndPagination(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	importFixture(t, a, "movie")
	file := filepath.Join(source, "History (2024).mkv")
	importWrite(t, file, []byte("history synthetic"))
	plan, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: file}}})
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("GET", "/history/transfers?page_size=1", nil)
	out, e := a.transferHistory(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"total":0`) {
		t.Fatal("preview counted as executed history", string(b))
	}
	job, e := a.executeImport(ctx, plan.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	out, e = a.transferHistory(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	b, _ = json.Marshal(out)
	if !strings.Contains(string(b), `"status":"pending"`) || !strings.Contains(string(b), `"total":1`) {
		t.Fatalf("pending history=%s", b)
	}
	_, e = a.Store.DB.Exec(`UPDATE jobs SET state='failed',error_message='synthetic disk error' WHERE id=?`, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	item, e := a.transferHistoryItem(ctx, plan.Files[0].ID)
	if e != nil || !item.CanRetry || item.Status != "failed" || item.Error != "synthetic disk error" {
		t.Fatalf("item=%+v err=%v", item, e)
	}
	plan.Files[0].Status = "imported"
	plan.State = "complete"
	if e = a.Store.Put(ctx, "import_plan", plan.ID, plan); e != nil {
		t.Fatal(e)
	}
	item, e = a.transferHistoryItem(ctx, plan.Files[0].ID)
	if e != nil || item.CanRetry || item.Status != "success" {
		t.Fatalf("success item=%+v err=%v", item, e)
	}
	// More than Store.List's former 500-row limit: history uses SQL pagination.
	plan2 := ImportPlan{ID: "many", WorkflowID: "wf_many", Media: catalog.Media{Title: "分页剧集", MediaType: "tv", ID: 124}, State: "complete", Mode: "copy", CreatedAt: now()}
	for i := 0; i < 505; i++ {
		plan2.Files = append(plan2.Files, PlanFile{ID: "many_" + strconv.Itoa(i), Source: filepath.Join(source, strconv.Itoa(i)+".mkv"), Target: "/unread/target.mkv", Status: "imported", Episodes: []int{i + 1}, Season: 1})
	}
	if e = a.Store.Put(ctx, "import_plan", plan2.ID, plan2); e != nil {
		t.Fatal(e)
	}
	if _, e = a.enqueue(ctx, "import", map[string]string{"id": plan2.ID}, plan2.WorkflowID); e != nil {
		t.Fatal(e)
	}
	out, e = a.transferHistory(ctx, httptest.NewRequest("GET", "/history/transfers?q=%E5%88%86%E9%A1%B5&page=11&page_size=50&status=success", nil))
	if e != nil {
		t.Fatal(e)
	}
	b, _ = json.Marshal(out)
	var page struct {
		Total int                   `json:"total"`
		Items []transferHistoryItem `json:"items"`
	}
	_ = json.Unmarshal(b, &page)
	if page.Total != 505 || len(page.Items) != 5 {
		t.Fatalf("pagination total=%d items=%d", page.Total, len(page.Items))
	}
}

func TestTransferHistoryOnlyContainsExecutedSelections(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	plan := ImportPlan{ID: "selection", State: "queued", CreatedAt: now(), Media: catalog.Media{ID: 1, MediaType: "tv", Title: "选择范围"}, Files: []PlanFile{{ID: "one", Source: filepath.Join(source, "one.mkv"), Status: "ready"}, {ID: "two", Source: filepath.Join(source, "two.mkv"), Status: "ready"}, {ID: "unselected", Source: filepath.Join(source, "unselected.mkv"), Status: "ready"}}}
	if e := a.Store.Put(ctx, "import_plan", plan.ID, plan); e != nil {
		t.Fatal(e)
	}
	first, e := a.enqueue(ctx, "import", map[string]any{"id": plan.ID, "file_ids": []string{"one"}}, "wf_selection")
	if e != nil {
		t.Fatal(e)
	}
	out, e := a.transferHistory(ctx, httptest.NewRequest("GET", "/history/transfers", nil))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(out)
	var result struct {
		Items []transferHistoryItem `json:"items"`
		Total int                   `json:"total"`
	}
	_ = json.Unmarshal(raw, &result)
	if result.Total != 1 || result.Items[0].ID != "one" || result.Items[0].Status != "pending" {
		t.Fatalf("unselected files leaked into history=%s", raw)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='failed' WHERE id=?`, first.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.enqueue(ctx, "import", map[string]any{"id": plan.ID, "file_ids": []string{"two"}}, "wf_selection"); e != nil {
		t.Fatal(e)
	}
	one, e := a.transferHistoryItem(ctx, "one")
	if e != nil || one.Status != "failed" || one.JobID != first.ID {
		t.Fatalf("unrelated file retry changed history: %+v err=%v", one, e)
	}
	two, e := a.transferHistoryItem(ctx, "two")
	if e != nil || two.Status != "pending" {
		t.Fatalf("selected second file not pending: %+v err=%v", two, e)
	}
}

func TestFileManagerIndividualMovieHTTPPreviewsPublishSeparateIdentities(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	first := importFixture(t, a, "movie")
	second := catalog.Media{ID: 124, MediaType: "movie", Title: "第二部合成影片", OriginalTitle: "Second Fixture", Year: 2025}
	importCache(t, a, "media:movie:124", second)
	cookie := setupTestAdmin(t, a)
	files := []string{filepath.Join(source, "First Fixture (2024).mkv"), filepath.Join(source, "Second Fixture (2025).mkv")}
	identities := []catalog.Media{first, second}
	jobs := []Job{}
	for i, file := range files {
		importWrite(t, file, []byte("individual movie "+strconv.Itoa(i)))
		body, _ := json.Marshal(ImportRequest{MediaType: "movie", TMDBID: identities[i].ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: file}}})
		w := callTest(t, a.Handler(), "POST", "/api/import/preview", string(body), cookie, "")
		var envelope struct {
			Data ImportPlan `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &envelope)
		plan := envelope.Data
		if w.Code != 200 || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
			t.Fatalf("individual preview %d %s", w.Code, w.Body)
		}
		var count int
		_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='import'`).Scan(&count)
		if count != i {
			t.Fatal("preview submitted a job", count, i)
		}
		confirm, _ := json.Marshal(map[string]any{"plan_id": plan.ID, "file_ids": []string{plan.Files[0].ID}})
		w = callTest(t, a.Handler(), "POST", "/api/import/execute", string(confirm), cookie, "")
		var jobEnvelope struct {
			Data Job `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &jobEnvelope)
		if w.Code != 200 || jobEnvelope.Data.ID == "" {
			t.Fatalf("individual confirmation %d %s", w.Code, w.Body)
		}
		jobs = append(jobs, jobEnvelope.Data)
	}
	for _, job := range jobs {
		if _, e := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID); e != nil {
			t.Fatal(e)
		}
		job.State = "running"
		job.Generation = 1
		var payload struct {
			ID      string   `json:"id"`
			FileIDs []string `json:"file_ids"`
		}
		_ = json.Unmarshal(job.Payload, &payload)
		if e := a.runImport(ctx, job, payload.ID, payload.FileIDs); e != nil {
			t.Fatal(e)
		}
		if _, e := a.finishJob(ctx, job, "succeeded", "", "", 0); e != nil {
			t.Fatal(e)
		}
	}
	w := callTest(t, a.Handler(), "GET", "/api/history/transfers?status=success", "", cookie, "")
	var history struct {
		Data struct {
			Items []transferHistoryItem `json:"items"`
			Total int                   `json:"total"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &history)
	if w.Code != 200 || history.Data.Total != 2 || len(history.Data.Items) != 2 {
		t.Fatalf("individual histories %d %s", w.Code, w.Body)
	}
	seen := map[int64]bool{}
	for _, item := range history.Data.Items {
		seen[item.Media.ID] = true
		if item.Source == item.Target {
			t.Fatal("source treated as published target")
		}
	}
	if !seen[first.ID] || !seen[second.ID] {
		t.Fatal("individual identities collapsed", seen)
	}
	for i, file := range files {
		got, e := os.ReadFile(file)
		if e != nil || string(got) != "individual movie "+strconv.Itoa(i) {
			t.Fatal("individual source changed", e)
		}
	}
}

func TestFileManagerTVBatchNumberingPreviewPreservesExplicitEpisodesWithOptionalMetadata(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "tv")
	importCache(t, a, "season:123:2", catalog.Season{SeriesID: 123, SeasonNumber: 2, Episodes: []catalog.Episode{{ID: 12302, SeasonNumber: 2, EpisodeNumber: 2, Name: "官方第二集"}}})
	files := []ImportFileRequest{}
	for _, ep := range []int{102, 103} {
		file := filepath.Join(source, "Show S01E"+strconv.Itoa(ep)+".mkv")
		importWrite(t, file, []byte("batch-numbering synthetic"))
		files = append(files, ImportFileRequest{Path: file, Season: 2, Episodes: []int{ep - 100}})
	}
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(ImportRequest{MediaType: "tv", TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Files: files})
	w := callTest(t, a.Handler(), "POST", "/api/import/preview", string(body), cookie, "")
	var envelope struct {
		Data ImportPlan `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	if w.Code != 200 || len(envelope.Data.Files) != 2 {
		t.Fatalf("TV batch preview %d %s", w.Code, w.Body)
	}
	for i, file := range envelope.Data.Files {
		if file.Status != "ready" || file.Season != 2 || len(file.Episodes) != 1 || file.Episodes[0] != i+2 || file.Source != files[i].Path || file.Target == file.Source {
			t.Fatalf("explicit numbering lost with optional metadata: %+v", file)
		}
		if !strings.Contains(file.Target, "S02E0"+strconv.Itoa(i+2)) {
			t.Fatalf("target ignored explicit numbering: %+v", file)
		}
		if _, err := os.Stat(file.Target); !os.IsNotExist(err) {
			t.Fatal("preview published a file", err)
		}
		if raw, err := os.ReadFile(file.Source); err != nil || string(raw) != "batch-numbering synthetic" {
			t.Fatal("preview changed source", err)
		}
	}
	var imports int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='import'`).Scan(&imports)
	if imports != 0 {
		t.Fatal("batch numbering preview automatically executed", imports)
	}
}
