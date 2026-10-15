package app

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/media"
	"light-media/internal/sites"
)

func workflowBencode(v any) []byte {
	switch x := v.(type) {
	case string:
		return append([]byte(strconv.Itoa(len(x))+":"), []byte(x)...)
	case []byte:
		return append([]byte(strconv.Itoa(len(x))+":"), x...)
	case int:
		return []byte(fmt.Sprintf("i%de", x))
	case []any:
		b := []byte{'l'}
		for _, item := range x {
			b = append(b, workflowBencode(item)...)
		}
		return append(b, 'e')
	case map[string]any:
		keys := []string{}
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b := []byte{'d'}
		for _, key := range keys {
			b = append(b, workflowBencode(key)...)
			b = append(b, workflowBencode(x[key])...)
		}
		return append(b, 'e')
	default:
		panic("unsupported test bencode")
	}
}
func workflowTorrent(name string) ([]byte, string) {
	info := workflowBencode(map[string]any{"name": name, "length": 1024, "piece length": 16384, "pieces": bytes.Repeat([]byte{'p'}, 20)})
	hash := sha1.Sum(info)
	return append(append([]byte("d4:info"), info...), 'e'), hex.EncodeToString(hash[:])
}
func workflowHTML(title, end string, free bool) string {
	promotion := ""
	if free {
		promotion = `<img class="pro_free2up">`
	}
	if end != "" {
		promotion += `<font color="red"><span title="` + end + `">free</span></font>`
	}
	return `<a href="logout.php">退出</a><a href="userdetails.php?id=1">用户</a><table class="torrents"><tr><th>Title</th></tr><tr><td>类别</td><td><table class="torrentname"><tr><td class="embedded"><a title="` + title + `" href="details.php?id=101">资源</a>` + promotion + `</td></tr></table></td><td>1</td><td><span title="2026-10-15 18:30:00">发布</span></td><td>1.5<br>GiB</td><td><a href="details.php?id=101#seeders">12</a></td><td>2</td><td>99</td></tr></table>`
}
func workflowTask(hash string, progress float64) map[string]any {
	return map[string]any{"hash": hash, "name": "test", "state": "stoppedDL", "save_path": "/downloads", "total_size": 1024, "completed": int64(1024 * progress), "progress": progress, "ratio": 0}
}
func workflowJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func workflowConfigure(t *testing.T, a *App, siteURL, downloaderURL string) {
	t.Helper()
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "fixture-site", Name: "fixture", Kind: "ubits", BaseURL: siteURL, Cookie: "fixture-cookie"}, Enabled: true, DailyBudget: 10000}}
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "fixture-qb", Kind: "qb", BaseURL: downloaderURL}, DownloadPath: "/downloads"}}
	a.config.DefaultDownloader = "fixture-qb"
	a.config.DefaultRoot = "root"
}

func TestDownloadFreshPromotionExpiresAfterConfirmation(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	torrent, hash := workflowTorrent("测试影片 (2024) 1080p.mkv")
	var expired atomic.Bool
	var adds atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			workflowJSON(w, []any{})
		case "/api/v2/torrents/add":
			adds.Add(1)
			_, _ = io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			end := "2099-12-31 23:59:59"
			if expired.Load() {
				end = "2000-01-01 00:00:00"
			}
			_, _ = io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", end, true))
		case "/download.php":
			_, _ = w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	ctx := context.Background()
	p, e := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID, Policy: Policy{FreeOnly: true}}, SiteID: "fixture-site", ResourceID: "101"}, false)
	if e != nil {
		t.Fatal(e)
	}
	if p.Hash != hash {
		t.Fatal("preview identity changed")
	}
	intent, e := a.confirmDownload(ctx, p.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	j, e := a.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	expired.Store(true)
	if e = a.runDownload(ctx, j, intent.ID); e == nil {
		t.Fatal("expired free resource submitted after queued confirmation")
	}
	if adds.Load() != 0 {
		t.Fatal("expired free promotion reached downloader Add")
	}
}

func TestDownloadPreviewFreshSearchKeepsSelectedPage(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	torrent, _ := workflowTorrent("测试影片 (2024) 1080p.mkv")
	var pageSeen atomic.Bool
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			if r.URL.Query().Get("page") == "1" {
				pageSeen.Store(true)
				_, _ = io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
			} else {
				_, _ = io.WriteString(w, `<a href="logout.php">退出</a><table class="torrents"><tr><th>Title</th></tr></table>`)
			}
		case "/download.php":
			_, _ = w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, "http://127.0.0.1:1")
	p, e := a.previewDownload(context.Background(), PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID, Page: 2}, SiteID: "fixture-site", ResourceID: "101"}, false)
	if e != nil || p.ID == "" || !pageSeen.Load() {
		t.Fatalf("second-page selection lost during freshness check: %+v %v", p, e)
	}
}

func TestManualDownloadKeepsConfirmedMappingForAutomaticImport(t *testing.T) {
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "tv")
	importCache(t, a, "season:123:4", catalog.Season{SeriesID: 123, SeasonNumber: 4, Episodes: []catalog.Episode{{ID: 105, SeasonNumber: 4, EpisodeNumber: 5, AirDate: "2014-01-01"}}})
	filename := "测试剧集 EP260 1080p.mkv"
	torrent, hash := workflowTorrent(filename)
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			workflowJSON(w, []any{workflowTask(hash, 1)})
		case "/api/v2/torrents/files":
			workflowJSON(w, []any{map[string]any{"name": filename, "size": 1024, "progress": 1}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/torrents.php" {
			_, _ = io.WriteString(w, workflowHTML("测试剧集 EP260 1080p", "", false))
		} else if r.URL.Path == "/download.php" {
			_, _ = w.Write(torrent)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	a.config.Downloaders[0].Mappings = []PathMapping{{From: "/downloads", To: source}}
	offset := -255
	rule := media.MappingRule{ID: "confirmed-offset", Revision: 7, MediaID: mediaKey(m), TargetSeason: 4, From: 260, To: 260, Offset: &offset, Confirmed: true}
	ctx := context.Background()
	p, e := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: "tv", TMDBID: 123, Season: 4, Episodes: []int{5}, Mappings: []media.MappingRule{rule}}, SiteID: "fixture-site", ResourceID: "101"}, false)
	if e != nil {
		t.Fatal(e)
	}
	intent, e := a.confirmDownload(ctx, p.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(intent.Request.Mappings) != 1 || intent.Request.Mappings[0].Revision != 7 {
		t.Fatalf("mapping not persisted with intent: %+v", intent.Request)
	}
	offset = -254 // Later caller edits must not change the persisted mapping snapshot.
	var stored Intent
	if e = a.Store.Get(ctx, "intent", intent.ID, &stored); e != nil {
		t.Fatal(e)
	}
	importWrite(t, filepath.Join(source, filename), bytes.Repeat([]byte{'v'}, 1024))
	client, e := download.New(a.config.Downloaders[0].Config)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.planCompletedDownload(ctx, client, a.config.Downloaders[0], &stored, download.Task{Hash: hash, DownloadDir: "/downloads"}); e != nil {
		t.Fatalf("manual mapping lost before import: %v", e)
	}
	var plan ImportPlan
	if e = a.Store.Get(ctx, "import_plan", stored.ImportPlanID, &plan); e != nil {
		t.Fatal(e)
	}
	if len(plan.Files) != 1 || len(plan.Files[0].Episodes) != 1 || plan.Files[0].Episodes[0] != 5 {
		t.Fatalf("wrong frozen mapping: %+v", plan.Files)
	}
}

func TestPollCannotResurrectCancelledDownload(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	_, hash := workflowTorrent("测试影片 (2024).mkv")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var lists atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/info" {
			http.NotFound(w, r)
			return
		}
		if lists.Add(1) == 1 {
			close(entered)
			<-release
			workflowJSON(w, []any{workflowTask(hash, .5)})
		} else {
			workflowJSON(w, []any{})
		}
	}))
	defer qb.Close()
	defer once.Do(func() { close(release) })
	workflowConfigure(t, a, "http://127.0.0.1:1", qb.URL)
	ctx := context.Background()
	intent := Intent{ID: "dl-poll-race", Media: m, Hash: hash, DownloaderID: "fixture-qb", RootID: "root", State: "accepted", CreatedAt: now()}
	if e := a.Store.Put(ctx, "intent", intent.ID, intent); e != nil {
		t.Fatal(e)
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Reserve(ctx, tx, mediaKey(m), 0, nil, intent.ID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	pollDone := make(chan error, 1)
	go func() { pollDone <- a.pollDownloads(ctx) }()
	<-entered
	actionDone := make(chan error, 1)
	started := make(chan struct{})
	go func() { close(started); actionDone <- a.downloadAction(ctx, intent.ID, "remove_keep_data") }()
	<-started
	select {
	case e := <-actionDone:
		once.Do(func() { close(release) })
		<-pollDone
		t.Fatalf("cancel raced ahead of active poll (old snapshot can resurrect intent): %v", e)
	case <-time.After(80 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if e = <-pollDone; e != nil {
		t.Fatal(e)
	}
	if e = <-actionDone; e != nil {
		t.Fatal(e)
	}
	if e = a.pollDownloads(ctx); e != nil {
		t.Fatal(e)
	}
	var current Intent
	if e = a.Store.Get(ctx, "intent", intent.ID, &current); e != nil {
		t.Fatal(e)
	}
	if current.State != "cancelled" {
		t.Fatalf("cancelled intent resurrected: %+v", current)
	}
	var count int
	_ = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reservations WHERE intent_id=?`, intent.ID).Scan(&count)
	if count != 0 {
		t.Fatal("explicit cancellation reservation remained")
	}
}

func TestPausedReadyImportPreviewResumesExactlyOnce(t *testing.T) {
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	filename := filepath.Join(source, "测试影片 (2024).mkv")
	importWrite(t, filename, bytes.Repeat([]byte{'v'}, 1024))
	ctx := context.Background()
	plan, e := a.previewImport(ctx, ImportRequest{MediaType: m.MediaType, TMDBID: m.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: filename}}})
	if e != nil {
		t.Fatal(e)
	}
	in := Intent{ID: "dl-ready-paused", Media: m, State: "downloaded", ImportPlanID: plan.ID}
	if e = a.setControl(ctx, true); e != nil {
		t.Fatal(e)
	}
	if e = a.resumeCompletedPlan(ctx, &in); e == nil {
		t.Fatal("paused preview started import")
	}
	if e = a.setControl(ctx, false); e != nil {
		t.Fatal(e)
	}
	if e = a.resumeCompletedPlan(ctx, &in); e != nil {
		t.Fatal(e)
	}
	if e = a.resumeCompletedPlan(ctx, &in); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=?`, plan.ID).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 1 {
		t.Fatalf("resumed import jobs=%d", count)
	}
}

func TestSiteLockWaitRechecksSubscriptionBeforeBudget(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	s, e := a.saveSubscription(ctx, "", request)
	if e != nil {
		t.Fatal(e)
	}
	site := SiteConfig{Config: sites.Config{ID: "locked-site", Kind: "ubits", BaseURL: "http://127.0.0.1:1", Cookie: "fixture"}, Enabled: true, DailyBudget: 1000}
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	a.siteLocks.Store(site.ID, lock)
	done := make(chan error, 1)
	var called atomic.Bool
	go func() {
		done <- a.withSite(subscriptionContext(ctx, s.ID, s.Revision), site, true, func(*sites.Client) error { called.Store(true); return nil })
	}()
	time.Sleep(60 * time.Millisecond)
	request.State = "paused"
	request.Revision = s.Revision
	if _, e = a.saveSubscription(ctx, s.ID, request); e != nil {
		<-lock
		t.Fatal(e)
	}
	<-lock
	if e = <-done; e == nil {
		t.Fatal("paused subscription passed site lock")
	}
	var used int
	if e = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_usage WHERE site_id=?`, site.ID).Scan(&used); e != nil {
		t.Fatal(e)
	}
	if used != 0 || called.Load() {
		t.Fatalf("paused waiter consumed budget=%d or made request=%v", used, called.Load())
	}
}

func TestNewConfirmationReasonIsNotCoveredByOldExtraFileWarning(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	name1, name2 := "测试影片 (2024) CD1.mkv", "测试影片 (2024) CD2.mkv"
	info := workflowBencode(map[string]any{"name": "movie", "files": []any{map[string]any{"length": 1024, "path": []any{name1}}, map[string]any{"length": 1024, "path": []any{name2}}}, "piece length": 16384, "pieces": bytes.Repeat([]byte{'p'}, 20)})
	torrent := append(append([]byte("d4:info"), info...), 'e')
	var changed atomic.Bool
	var adds atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/info" {
			workflowJSON(w, []any{})
		} else if r.URL.Path == "/api/v2/torrents/add" {
			adds.Add(1)
			_, _ = io.WriteString(w, "Ok.")
		} else {
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/torrents.php" {
			year := "2024"
			if changed.Load() {
				year = "2023"
			}
			_, _ = io.WriteString(w, workflowHTML("测试影片."+year+".1080p.WEB-DL.x264-TEST", "", false))
		} else if r.URL.Path == "/download.php" {
			_, _ = w.Write(torrent)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	ctx := context.Background()
	p, e := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID}, SiteID: "fixture-site", ResourceID: "101"}, false)
	if e != nil {
		t.Fatal(e)
	}
	if !p.NeedsConfirmation {
		t.Fatal("expected multi-video warning")
	}
	intent, e := a.confirmDownload(ctx, p.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	j, e := a.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	changed.Store(true)
	if e = a.runDownload(ctx, j, intent.ID); e == nil {
		t.Fatal("new year conflict silently covered by old extra-file confirmation")
	}
	if adds.Load() != 0 {
		t.Fatal("new confirmation reason reached Add")
	}
	if !strings.Contains(e.Error(), "预览") && !strings.Contains(e.Error(), "变化") {
		t.Fatalf("unclear review failure: %v", e)
	}
}

func TestPollDoesNotOverwriteImportedTerminalState(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	_, hash := workflowTorrent("测试影片 (2024).mkv")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		workflowJSON(w, []any{workflowTask(hash, 1)})
	}))
	defer qb.Close()
	defer once.Do(func() { close(release) })
	workflowConfigure(t, a, "http://127.0.0.1:1", qb.URL)
	ctx := context.Background()
	intent := Intent{ID: "dl-import-race", Media: m, Hash: hash, DownloaderID: "fixture-qb", RootID: "root", State: "downloading", CreatedAt: now()}
	if e := a.Store.Put(ctx, "intent", intent.ID, intent); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- a.pollDownloads(ctx) }()
	<-entered
	intent.State = "imported"
	if e := a.Store.Put(ctx, "intent", intent.ID, intent); e != nil {
		t.Fatal(e)
	}
	once.Do(func() { close(release) })
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	var current Intent
	if e := a.Store.Get(ctx, "intent", intent.ID, &current); e != nil {
		t.Fatal(e)
	}
	if current.State != "imported" {
		t.Fatalf("poll overwrote import worker completion: %+v", current)
	}
}

func TestActualTorrentFilenameConflictRequiresReview(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	torrent, _ := workflowTorrent("Another Movie (2020) 1080p.mkv")
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/torrents.php" {
			_, _ = io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
		} else if r.URL.Path == "/download.php" {
			_, _ = w.Write(torrent)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, "http://127.0.0.1:1")
	request := PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID}, SiteID: "fixture-site", ResourceID: "101"}
	if _, e := a.previewDownload(context.Background(), request, true); e == nil {
		t.Fatal("listing title hid a conflicting actual torrent filename from automation")
	}
	p, e := a.previewDownload(context.Background(), request, false)
	if e != nil {
		t.Fatal(e)
	}
	if !p.NeedsConfirmation || len(p.Warnings) == 0 {
		t.Fatalf("manual filename conflict not visible: %+v", p)
	}
}

func TestImportedMovieSubscriptionCompletesWithoutFurtherSiteChecks(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	workflowConfigure(t, a, "http://127.0.0.1:1", "http://127.0.0.1:1")
	ctx := context.Background()
	s, e := a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: "movie", TMDBID: m.ID, Schedule: fridaySchedule(t), DownloaderID: "fixture-qb", RootID: "root"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.ExecContext(ctx, `INSERT INTO asset_slots VALUES(?,?,?,?)`, mediaKey(m), 0, 0, "movie-asset"); e != nil {
		t.Fatal(e)
	}
	if _, e = a.enqueue(ctx, "subscription", map[string]any{"id": s.ID, "revision": s.Revision}, "wf-movie-complete"); e != nil {
		t.Fatal(e)
	}
	j, e := a.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.checkSubscription(ctx, j, s.ID, s.Revision); e != nil {
		t.Fatal(e)
	}
	var current Subscription
	if e = a.Store.Get(ctx, "subscription", s.ID, &current); e != nil {
		t.Fatal(e)
	}
	if current.State != "completed" || current.NextCheck != "" {
		t.Fatalf("movie remained subscribed after import: %+v", current)
	}
	var usage int
	_ = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_usage`).Scan(&usage)
	if usage != 0 {
		t.Fatal("completed movie consumed another site check")
	}
}

func TestActualMetainfoBytesCannotBypassSizePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, reported string
		policy         Policy
	}{{"minimum", "1.5<br>GiB", Policy{MinBytes: 2048}}, {"maximum", "512<br>B", Policy{MaxBytes: 768}}} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _ := importTestApp(t)
			m := importFixture(t, a, "movie")
			torrent, _ := workflowTorrent("测试影片 (2024) 1080p.mkv")
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/torrents.php" {
					html := workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false)
					_, _ = io.WriteString(w, strings.Replace(html, "1.5<br>GiB", tc.reported, 1))
				} else if r.URL.Path == "/download.php" {
					_, _ = w.Write(torrent)
				} else {
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, "http://127.0.0.1:1")
			if _, e := a.previewDownload(context.Background(), PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID, Policy: tc.policy}, SiteID: "fixture-site", ResourceID: "101"}, false); e == nil {
				t.Fatal("site-advertised size bypassed actual metainfo byte limits")
			}
		})
	}
}
