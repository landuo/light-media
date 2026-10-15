package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/download"
)

func TestNativeDownloadWithoutMediaUsesSelectedDirectoryAndIsIdempotent(t *testing.T) {
	a, source, _ := importTestApp(t)
	torrent, hash := workflowTorrent("a title with no recognized identity.mkv")
	var added bool
	var saves []string
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/preferences":
			workflowJSON(w, map[string]any{"save_path": source})
		case "/api/v2/torrents/info":
			if added {
				task := workflowTask(hash, 0)
				task["save_path"] = saves[0]
				workflowJSON(w, []any{task})
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			if e := r.ParseMultipartForm(4 << 20); e != nil {
				t.Error(e)
			}
			saves = append(saves, r.FormValue("savepath"))
			added = true
			io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/torrents.php" {
			io.WriteString(w, workflowHTML("Fixture 2024 1080p WEB-DL x264-TEST", "", false))
		} else if r.URL.Path == "/download.php" {
			w.Write(torrent)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	a.config.Downloaders[0].Mappings = nil
	a.config.Downloaders[0].DownloadPath = ""
	ctx := context.Background()
	path := filepath.Join(source, "selected")
	in := NativeDownloadRequest{SiteID: "fixture-site", ResourceID: "101", Query: "Fixture", DownloaderID: "fixture-qb", DownloadPath: path}
	record, e := a.addNativeDownload(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if record.TMDBID != 0 || record.DownloadPath != path {
		t.Fatalf("unexpected media requirement/path: %+v", record)
	}
	job, e := a.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.safeRun(ctx, job); e != nil {
		t.Fatal(e)
	}
	if len(saves) != 1 || saves[0] != path {
		t.Fatalf("actual paths=%v", saves)
	}
	second, e := a.addNativeDownload(ctx, in)
	if e != nil || second.ID != record.ID {
		t.Fatalf("duplicate=%+v err=%v", second, e)
	}
	second.State, second.DownloaderSnapshot = "failed", "obsolete-config"
	if e = a.Store.Put(ctx, "native_download", second.ID, second); e != nil {
		t.Fatal(e)
	}
	retried, e := a.addNativeDownload(ctx, in)
	if e != nil || retried.ID == record.ID || retried.State != "planned" || retried.DownloaderSnapshot != downloaderSnapshot(a.config.Downloaders[0]) {
		t.Fatalf("explicit retry did not create a usable attempt: %+v %v", retried, e)
	}
	if e = a.Store.Get(ctx, "native_download", second.ID, &second); e != nil || second.State != "cancelled" {
		t.Fatalf("old attempt still active: %+v %v", second, e)
	}
	if e = a.runNativeDownload(ctx, job, second.ID); e != nil || len(saves) != 1 {
		t.Fatalf("superseded attempt resubmitted: %v %v", saves, e)
	}
	a.config.Roots = append(a.config.Roots, Root{ID: "changed-library", Path: source, Kind: "mixed"})
	if e = a.runNativeDownload(ctx, job, retried.ID); e == nil || len(saves) != 1 {
		t.Fatalf("queued download wrote into a newly configured media library: %v %v", saves, e)
	}
	var reservations, assets int
	_ = a.Store.DB.QueryRow(`SELECT count(*) FROM reservations`).Scan(&reservations)
	_ = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&assets)
	if reservations != 0 || assets != 0 {
		t.Fatal("unidentified download claimed media/library")
	}
	if _, e = a.addNativeDownload(ctx, NativeDownloadRequest{SiteID: in.SiteID, ResourceID: in.ResourceID, Query: in.Query, DownloadPath: "/etc"}); e == nil {
		t.Fatal("unmounted save path accepted")
	}
}

func TestNativeDownloadRecoversFromUncertainAddWithoutAddingTwice(t *testing.T) {
	a, source, _ := importTestApp(t)
	torrent, hash := workflowTorrent("File.2024.mkv")
	var added bool
	adds := 0
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if added {
				task := workflowTask(hash, 0)
				task["save_path"] = source
				workflowJSON(w, []any{task})
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			added = true
			adds++
			h := w.(http.Hijacker)
			conn, _, _ := h.Hijack()
			conn.Close()
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: qb.URL}}
	a.config.Downloaders = []DownloaderConfig{d}
	ref, e := a.persistMaterial(context.Background(), torrent, hash)
	if e != nil {
		t.Fatal(e)
	}
	record := NativeDownload{ID: "raw", Hash: hash, MaterialRef: ref, DownloaderID: d.ID, DownloadPath: source, DownloaderSnapshot: downloaderSnapshot(d), State: "planned"}
	if e = a.Store.Put(context.Background(), "native_download", record.ID, record); e != nil {
		t.Fatal(e)
	}
	job := importClaim(t, a, "native_download", record.ID)
	if e = a.runNativeDownload(context.Background(), job, record.ID); e == nil {
		t.Fatal("disconnected add claimed success")
	}
	if e = a.runNativeDownload(context.Background(), job, record.ID); e != nil {
		t.Fatal(e)
	}
	if adds != 1 {
		t.Fatalf("duplicate submission count=%d", adds)
	}
}

func TestDownloaderTaskActionsApplyToExistingExternalTasksAndKeepFiles(t *testing.T) {
	a, _, _ := importTestApp(t)
	hash := strings.Repeat("a", 40)
	actions := []string{}
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/info" {
			workflowJSON(w, []any{workflowTask(hash, 1)})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v2/torrents/") {
			r.ParseForm()
			if r.FormValue("hashes") != hash {
				t.Error("wrong task")
			}
			if strings.HasSuffix(r.URL.Path, "/delete") && r.FormValue("deleteFiles") != "false" {
				t.Error("deleted downloaded data")
			}
			actions = append(actions, r.URL.Path)
			return
		}
		http.NotFound(w, r)
	}))
	defer qb.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "external", Kind: "qb", BaseURL: qb.URL}}}
	for _, action := range []string{"pause", "start", "verify", "remove_keep_data"} {
		if e := a.downloaderTaskAction(context.Background(), "external", hash, action); e != nil {
			t.Fatal(e)
		}
	}
	if len(actions) != 4 {
		t.Fatalf("external task controls not forwarded: %v", actions)
	}
	if e := a.downloaderTaskAction(context.Background(), "external", strings.Repeat("b", 40), "pause"); e == nil {
		t.Fatal("unverified external task modified")
	}
}
