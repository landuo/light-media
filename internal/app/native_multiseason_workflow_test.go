package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/metainfo"
)

func TestManualMultiSeasonHTTPWithoutMediaIdentityThenActualDirectoryOrganize(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	m := importFixture(t, a, "tv")
	importCache(t, a, "directory-identify:tv:测试剧集", catalog.Page{Results: []catalog.Media{m}, Page: 1, TotalPages: 1, TotalResults: 1})
	for season := 1; season <= 2; season++ {
		importCache(t, a, "season:123:"+strconv.Itoa(season), catalog.Season{SeriesID: m.ID, SeasonNumber: season, Episodes: []catalog.Episode{{ID: int64(season*10 + 1), SeasonNumber: season, EpisodeNumber: 1, Name: "episode", AirDate: "2024-01-01"}}})
	}
	raw := automaticPackTorrent([]string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S02E01 1080p.mkv"})
	material, e := metainfo.ParseMaterial(raw)
	if e != nil {
		t.Fatal(e)
	}
	var adds atomic.Int64
	var complete atomic.Bool
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if adds.Load() == 0 {
				workflowJSON(w, []any{})
				return
			}
			p := 0.0
			if complete.Load() {
				p = 1
			}
			task := workflowTask(material.Hash, p)
			task["save_path"] = source
			task["total_size"] = material.TotalBytes
			if complete.Load() {
				task["completed"] = material.TotalBytes
			}
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/add":
			if e := r.ParseMultipartForm(4 << 20); e != nil || r.FormValue("savepath") != source {
				t.Error("manual chosen path lost", e, r.Form)
			}
			adds.Add(1)
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/files":
			rows := []any{}
			for i, f := range material.Files {
				p := 0
				if complete.Load() {
					p = 1
				}
				rows = append(rows, map[string]any{"index": i, "name": f.Path, "size": f.Size, "progress": p})
			}
			workflowJSON(w, rows)
		default:
			t.Error("unexpected manual multi-season daemon endpoint", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			io.WriteString(w, workflowHTML("测试剧集 S01-S02 1080p WEB-DL", "", false))
		case "/download.php":
			w.Write(raw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	a.config.Downloaders[0].DownloadPath = ""
	a.config.Downloaders[0].Mappings = nil
	a.config.Downloaders[0].SharedPaths = true
	rule := DirectoryRule{ID: "manual-multi", Enabled: true, SourcePath: source, RootID: "root", MediaType: "tv", Mode: "hardlink", MonitorType: "downloader", IncludeExisting: true}
	a.config.Directories = []DirectoryRule{rule}
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"site_id": "fixture-site", "resource_id": "101", "query": "测试剧集", "downloader_id": "fixture-qb", "download_path": source})
	unauth := callTest(t, a.Handler(), "POST", "/api/downloads/add", string(body), nil, "")
	if unauth.Code != 401 {
		t.Fatal("manual download became unauthenticated", unauth.Code)
	}
	response := callTest(t, a.Handler(), "POST", "/api/downloads/add", string(body), cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var envelope struct {
		Data NativeDownload `json:"data"`
	}
	if json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Data.ID == "" || envelope.Data.TMDBID != 0 || envelope.Data.MediaType != "" {
		t.Fatal("manual HTTP imposed media/season", response.Body.String())
	}
	job, e := a.claim(ctx)
	if e != nil || job.Kind != "native_download" {
		t.Fatal("actual manual job", job.Kind, e)
	}
	if e = a.safeRun(ctx, job); e != nil {
		t.Fatal(e)
	}
	for _, f := range material.Files {
		importWrite(t, filepath.Join(source, f.Path), bytes.Repeat([]byte{'v'}, int(f.Size)))
	}
	directoryPoll(t, a, rule)
	for _, f := range material.Files {
		directoryAge(t, a, rule, filepath.Join(source, f.Path))
	}
	directoryPoll(t, a, rule)
	for _, f := range material.Files {
		if observed := directoryObservation(t, a, rule, filepath.Join(source, f.Path)); observed.State != "downloading" || observed.PlanID != "" {
			t.Fatal("manual monitor organized unfinished torrent", observed)
		}
	}
	complete.Store(true)
	for _, f := range material.Files {
		directoryAge(t, a, rule, filepath.Join(source, f.Path))
	}
	directoryPoll(t, a, rule)
	for index, f := range material.Files {
		observed := directoryRecognize(t, a, rule, filepath.Join(source, f.Path))
		if observed.State != "queued" || observed.PlanID == "" {
			t.Fatal("manual actual completed episode did not reach organizer", observed)
		}
		reorgRunJob(t, a, reorgJob(t, a, "import", observed.PlanID))
		var plan ImportPlan
		if e = a.Store.Get(ctx, "import_plan", observed.PlanID, &plan); e != nil || len(plan.Files) != 1 || plan.Files[0].Season != index+1 {
			t.Fatal("manual multiseason flattened", plan.Files, e)
		}
		var asset Asset
		if e = a.assetAtPath(ctx, plan.Files[0].Target, &asset); e != nil {
			t.Fatal(e)
		}
		reorgRunJob(t, a, reorgJob(t, a, "scrape", asset.ID))
		si, e := os.Stat(plan.Files[0].Source)
		if e != nil {
			t.Fatal(e)
		}
		ti, e := os.Stat(plan.Files[0].Target)
		if e != nil || !os.SameFile(si, ti) {
			t.Fatal("manual organizer lost hardlink/seeding source", e)
		}
	}
	directoryPoll(t, a, rule)
	if adds.Load() != 1 {
		t.Fatal("manual download repeated Add", adds.Load())
	}
	var assets int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND season IN (1,2) AND episode=1`, mediaKey(m)).Scan(&assets); e != nil || assets != 2 {
		t.Fatal("manual per-season library facts missing", assets, e)
	}
}
