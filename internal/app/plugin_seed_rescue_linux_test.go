//go:build linux

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/sites"
)

func TestSeedRescueInstalledLinuxAMD64ManualDownload(t *testing.T) {
	bundle := os.Getenv("SOURCE_SEED_RESCUE_PACKAGE_DIR")
	iyuuBundle := os.Getenv("SOURCE_SEED_RESCUE_IYUU_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" || iyuuBundle == "" {
		t.Skip("requires indexed Linux amd64 SeedRescue and archived IYUUAutoSeed packages")
	}
	iyuuBundle, err := filepath.Abs(iyuuBundle)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("TZ", "Asia/Shanghai")
	const pluginID, iyuuID = "SeedRescue", "IYUUAutoSeed"
	const targetName, sourceName, downloaderName = "Owned target", "Owned cross-site source", "Owned SeedRescue qB"
	const release = "Owned SeedRescue Fixture 2026 1080p"
	const fixtureToken = "owned-seed-rescue-fixture-token"
	const fixturePasskey = "owned-seed-rescue-fixture-passkey"
	targetTorrent, targetHash := workflowTorrent("target-release.mkv")
	sourceTorrent, sourceHash := workflowTorrent("cross-site-release.mkv")
	// The original private tracker envelope must survive the host's hashed-info
	// projection. Equal info hashes alone would not prove the transmitted bytes.
	trackerEnvelope := workflowBencode(map[string]any{"announce": "https://tracker.example.invalid/" + fixturePasskey + "/announce"})
	sourceTorrent = append(trackerEnvelope[:len(trackerEnvelope)-1], sourceTorrent[1:]...)
	var targetReads, sourceDetails, sourceDownloads, reseedReads, adds atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rss.xml":
			if r.URL.Query().Get("passkey") != fixturePasskey || r.Header.Get("Cookie") != "" {
				t.Error("RSS did not use its bound URL or sent unrelated site credentials")
			}
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Owned RSS</title><link>http://%[1]s</link><description>Owned RSS fixture</description><item><title>%[2]s</title><link>http://%[1]s/details.php?id=101</link><description>Owned release</description><guid>owned-101</guid><pubDate>Sun, 08 Nov 2026 08:00:00 GMT</pubDate><enclosure url="http://%[1]s/download.php?id=101&amp;passkey=%[3]s" length="1024" type="application/x-bittorrent"/></item></channel></rss>`, r.Host, release, fixturePasskey)
		case "/details.php":
			if r.URL.Query().Get("id") != "101" {
				t.Error("target detail ID changed")
			}
			io.WriteString(w, `<h1 id="top">Owned target release <span>[FREE]</span></h1><a href="download.php?id=101">Download</a><div id="peercount">2 / 0</div>`)
		case "/torrents.php":
			if r.URL.Query().Get("search") == "" || r.Header.Get("Cookie") != "session=owned-seed-rescue-target" {
				t.Error("target promotion refresh was not bound to the authenticated title search")
			}
			fmt.Fprintf(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=1">用户</a><table class="torrents"><tr><th>Title</th></tr>
<tr><td>类别</td><td><table class="torrentname"><tr><td class="embedded"><a title="%s" href="details.php?id=101&amp;hit=1">owned</a><br><img class="pro_free2up"></td></tr></table></td><td>1</td><td><span title="2026-11-08 08:00:00">刚刚</span></td><td>1.5<br>GiB</td><td><a href="details.php?id=101#seeders">12</a></td><td>2</td><td>99</td></tr></table>`, release)
		case "/download.php":
			targetReads.Add(1)
			if r.URL.Query().Get("id") != "101" || r.URL.Query().Get("passkey") != fixturePasskey {
				t.Error("RSS enclosure was not resolved to its original bound target URL")
			}
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(targetTorrent)
		default:
			t.Errorf("unexpected target endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "session=owned-seed-rescue-source" || r.URL.Query().Get("id") != "202" {
			t.Error("source site request lost its bound site cookie or torrent identity")
		}
		switch r.URL.Path {
		case "/details.php":
			sourceDetails.Add(1)
			io.WriteString(w, `<h1 id="top">Owned non-free release</h1><a href="download.php?id=202">Download</a><div id="peercount">18 / 3</div>`)
		case "/download.php":
			sourceDownloads.Add(1)
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(sourceTorrent)
		default:
			t.Errorf("unexpected cross-site endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(source.Close)
	// MoviePilot matches IYUU and configured sites by hostname, not port.
	sourceURL := strings.Replace(source.URL, "127.0.0.1", "localhost", 1)
	iyuu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("token") != fixtureToken {
			t.Error("IYUU request was not bound to the SeedRescue token")
		}
		var data any
		switch r.URL.Path {
		case "/reseed/sites/index":
			data = map[string]any{"sites": []map[string]any{{"id": 901, "base_url": sourceURL, "download_page": "download.php?id={id}"}}}
		case "/reseed/sites/reportExisting":
			data = map[string]any{"sid_sha1": "owned-source-list"}
		case "/reseed/index/index":
			reseedReads.Add(1)
			var request struct {
				Hash string `json:"hash"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Hash != `["`+targetHash+`"]` {
				t.Error("IYUU did not receive the exact RSS torrent info hash")
			}
			data = map[string]any{targetHash: map[string]any{"torrent": []map[string]any{{"sid": 901, "torrent_id": 202, "info_hash": sourceHash}}}}
		default:
			t.Errorf("unexpected IYUU endpoint %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		workflowJSON(w, map[string]any{"code": 0, "data": data})
	}))
	t.Cleanup(iyuu.Close)

	a := newTestApp(t)
	a.seedRescueIYUUOrigin = iyuu.URL
	downloadRoot := t.TempDir()
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			io.WriteString(w, "v5.0.5")
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.4")
		case "/api/v2/torrents/info":
			if r.Method != http.MethodPost || r.ParseForm() != nil {
				t.Error("expected native qB torrent POST filter")
			}
			hash, tag, filter := r.Form.Get("hashes"), r.Form.Get("tag"), r.Form.Get("filter")
			if !(hash == targetHash || hash == sourceHash || tag == "seed-rescue" && filter == "downloading") {
				t.Error("qB read escaped the bound hashes and plugin tag")
			}
			workflowJSON(w, []any{})
		case "/api/v2/torrents/add":
			adds.Add(1)
			key := "jobs"
			saved, err := a.pluginStore.originalBaseGetData(r.Context(), pluginID, &key)
			var jobs map[string]struct {
				State      string `json:"state"`
				SourceHash string `json:"source_hash"`
			}
			if err != nil || json.Unmarshal(saved, &jobs) != nil || jobs[targetHash].State != "SOURCE_SUBMITTING" || jobs[targetHash].SourceHash != sourceHash {
				t.Error("download was dispatched before its matching job intent was durably stored")
			}
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				t.Error("invalid native torrent multipart request")
				http.Error(w, "bad multipart", http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			if r.FormValue("paused") != "False" || r.FormValue("stopped") != "False" || r.FormValue("savepath") != downloadRoot ||
				r.FormValue("category") != "seed-rescue" || r.FormValue("tags") != "seed-rescue" {
				t.Error("source add did not use the selected directory, category, tag and active download flags")
			}
			file, _, err := r.FormFile("torrent__0")
			if err != nil {
				t.Error("qB did not receive the source torrent file")
				http.Error(w, "missing torrent", http.StatusBadRequest)
				return
			}
			received, err := io.ReadAll(file)
			file.Close()
			if err != nil || !bytes.Equal(received, sourceTorrent) {
				t.Error("qB did not receive the exact original source torrent including its tracker envelope")
			}
			io.WriteString(w, "Ok.")
		default:
			t.Errorf("unexpected selected qB endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	settings := a.Settings()
	settings.Sites = []SiteConfig{
		{Config: sites.Config{ID: "owned-seed-rescue-target", Name: targetName, Kind: "jiayuan", BaseURL: target.URL, Cookie: "session=owned-seed-rescue-target"}, Enabled: true},
		{Config: sites.Config{ID: "owned-seed-rescue-source", Name: sourceName, Kind: "ubits", BaseURL: sourceURL, Cookie: "session=owned-seed-rescue-source"}, Enabled: true},
	}
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-seed-rescue-qb", Kind: "qbittorrent", BaseURL: qb.URL}, Name: downloaderName, DownloadPath: downloadRoot}}
	settings.AllowedRoots = []string{downloadRoot}
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	siteID := func(id string) int64 {
		t.Helper()
		identity, err := a.Store.nativeSiteByAppID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		value, err := a.Store.nativeSourcePublicSiteID(t.Context(), a.Store.DB, identity)
		if err != nil || value <= 0 {
			t.Fatalf("missing native source identity for %s: %v", id, err)
		}
		return value
	}
	targetSiteID, sourceSiteID := siteID(settings.Sites[0].ID), siteID(settings.Sites[1].ID)
	admin := setupTestAdmin(t, a)
	a.Start(t.Context())
	archivedIYUU := t.TempDir()
	for name, archived := range map[string]string{"index.json": "go-plugins.v1.json", "worker.zip": "IYUUAutoSeed-2.22-linux-amd64.zip"} {
		if err := os.Symlink(filepath.Join(iyuuBundle, archived), filepath.Join(archivedIYUU, name)); err != nil {
			t.Fatal(err)
		}
	}
	installMergeSiteSwitchPackage(t, a, admin, archivedIYUU, iyuuID)
	installMergeSiteSwitchPackage(t, a, admin, bundle, pluginID)
	configure := func(id string, config map[string]any) {
		t.Helper()
		response := callTest(t, a.Handler(), http.MethodPut, "/api/plugins/"+id, string(mustCleanSeedJSON(config)), admin, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	configure(iyuuID, map[string]any{"enabled": true, "token": "owned-iyuu-readiness-token", "downloaders": []string{downloaderName}, "cron": "0 0 31 2 *"})
	config := map[string]any{"enabled": true, "notify": false, "token": fixtureToken,
		"cron": "0 0 31 2 *", "target_site_id": targetSiteID, "source_site_ids": []int64{sourceSiteID},
		"rss_urls": target.URL + "/rss.xml?passkey=" + fixturePasskey, "downloader": downloaderName,
		"save_path": downloadRoot, "confirm_download_risk": true, "selection_mode": "manual", "minimum_seeders": 1,
		"allowed_promotions": []string{"non_free", "free"}}
	configure(pluginID, config)
	for _, suffix := range []string{"", "/form"} {
		got := callTest(t, a.Handler(), http.MethodGet, "/api/plugins/"+pluginID+suffix, "", admin, "")
		body := got.Body.String()
		if got.Code != http.StatusOK || strings.Contains(body, fixtureToken) || strings.Contains(body, fixturePasskey) ||
			!strings.Contains(body, originalSeedRescueSecretPlaceholders["token"]) ||
			!strings.Contains(body, originalSeedRescueSecretPlaceholders["rss_urls"]) {
			t.Fatal("ordinary SeedRescue config reads did not preserve the private-field placeholders")
		}
	}
	for key, placeholder := range originalSeedRescueSecretPlaceholders {
		config[key] = placeholder
	}
	configure(pluginID, config)

	const base = "/api/plugins/SeedRescue/api"
	type response struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	call := func(method, path, body string) response {
		t.Helper()
		got := callTest(t, a.Handler(), method, base+path, body, admin, "")
		var decoded response
		if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &decoded) != nil {
			t.Fatalf("SeedRescue %s %s failed: %d %s", method, path, got.Code, got.Body.String())
		}
		if strings.Contains(got.Body.String(), fixturePasskey) || strings.Contains(got.Body.String(), fixtureToken) {
			t.Fatal("SeedRescue response exposed a site or IYUU credential")
		}
		return decoded
	}
	type candidate struct {
		ID        string `json:"candidate_id"`
		SiteID    int64  `json:"site_id"`
		Promotion string `json:"promotion"`
		Eligible  bool   `json:"eligible"`
	}
	type job struct {
		State      string      `json:"state"`
		TargetHash string      `json:"target_hash"`
		SourceHash string      `json:"source_hash"`
		Selected   string      `json:"selected_candidate_id"`
		Current    bool        `json:"rss_current"`
		Candidates []candidate `json:"candidates"`
	}
	type status struct {
		Enabled bool                            `json:"enabled"`
		Busy    bool                            `json:"busy"`
		Scan    struct{ State, Message string } `json:"scan_status"`
		Jobs    []job                           `json:"jobs"`
	}
	readStatus := func() status {
		t.Helper()
		result := call(http.MethodGet, "/status?view=current", "")
		var value status
		if !result.Success || json.Unmarshal(result.Data, &value) != nil || !value.Enabled {
			t.Fatalf("invalid source status response: %+v", result)
		}
		return value
	}
	if before := readStatus(); before.Busy || len(before.Jobs) != 0 {
		t.Fatalf("unexpected pre-scan status: %+v", before)
	}
	if submitted := call(http.MethodPost, "/scan", ""); !submitted.Success || submitted.Message != "已提交后台扫描" {
		t.Fatalf("manual scan did not admit background work: %+v", submitted)
	}
	deadline := time.Now().Add(10 * time.Second)
	var scanned status
	for time.Now().Before(deadline) {
		scanned = readStatus()
		if !scanned.Busy {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if scanned.Busy || scanned.Scan.State != "success" || len(scanned.Jobs) != 1 {
		t.Fatalf("source RSS scan did not finish with one current job: %+v", scanned)
	}
	waiting := scanned.Jobs[0]
	if waiting.TargetHash != targetHash || waiting.State != "AWAITING_SELECTION" || !waiting.Current || len(waiting.Candidates) != 1 ||
		waiting.Candidates[0].ID == "" || waiting.Candidates[0].SiteID != sourceSiteID || waiting.Candidates[0].Promotion != "non_free" || !waiting.Candidates[0].Eligible ||
		targetReads.Load() != 1 || reseedReads.Load() != 1 || sourceDetails.Load() == 0 || sourceDownloads.Load() != 0 || adds.Load() != 0 {
		t.Fatalf("source cross-site candidate and no-write precondition did not match: %+v", waiting)
	}
	path := "/jobs/" + targetHash
	selected := waiting.Candidates[0].ID
	unconfirmed := call(http.MethodPost, path+"/download", string(mustCleanSeedJSON(map[string]any{"candidate_id": selected})))
	var risk struct {
		Required bool    `json:"confirmation_required"`
		Factor   float64 `json:"download_factor"`
	}
	if unconfirmed.Success || json.Unmarshal(unconfirmed.Data, &risk) != nil || !risk.Required || risk.Factor != 1 || sourceDownloads.Load() != 0 || adds.Load() != 0 {
		t.Fatal("non-free cross-site choice did not require confirmation before downloading")
	}
	confirmed := call(http.MethodPost, path+"/download", string(mustCleanSeedJSON(map[string]any{"candidate_id": selected, "confirmed_download_factor": 1})))
	var active job
	if !confirmed.Success || json.Unmarshal(confirmed.Data, &active) != nil || active.State != "SOURCE_DOWNLOADING" || active.SourceHash != sourceHash || active.Selected != selected ||
		sourceDownloads.Load() != 1 || adds.Load() != 1 {
		t.Fatalf("confirmed source was not added once and actively downloading: %+v (%s)", active, confirmed.Message)
	}
	durable := call(http.MethodGet, path, "")
	if !durable.Success || json.Unmarshal(durable.Data, &active) != nil || active.State != "SOURCE_DOWNLOADING" || active.Selected != selected {
		t.Fatal("actual persisted task does not show the selected downloading source")
	}
	t.Log("archived IYUU and indexed SeedRescue ran through native authenticated RSS matching, non-free confirmation and one exact active qB add")
}
