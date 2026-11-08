package app

import (
	"bytes"
	"context"
	"crypto/sha256"
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

	"light-media/internal/catalog"
	"light-media/internal/plugins/artifacts"
)

const mediaServerRefreshFixtureID = "MediaServerRefresh"

func TestMediaServerRefreshInstalledAMD64Package(t *testing.T) {
	bundle := os.Getenv("SOURCE_MEDIA_SERVER_REFRESH_PACKAGE_DIR")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the exact MediaServerRefresh ZIP in the owned Linux amd64 fixture")
	}
	t.Setenv("MP_SERVER_HOST", "")
	ctx := t.Context()
	var peer *httptest.Server
	t.Cleanup(func() {
		if peer != nil {
			peer.Close()
		}
	})
	a, source, library := importTestApp(t)
	const followerLibraryGUID = "33333333333333333333333333333333"
	followerLibrary := filepath.Join(filepath.Dir(library), "follower-library")
	if err := os.Mkdir(followerLibrary, 0750); err != nil {
		t.Fatal(err)
	}
	primary := importFixture(t, a, "movie")
	follower := primary
	follower.ID, follower.Title, follower.OriginalTitle, follower.Year = 124, "测试跟随影片", "Owned follower", 2025
	importCache(t, a, "media:movie:124", follower)

	var published atomic.Value
	published.Store("")
	var primaryScans, followerScans, coreScans, scanAll, preflights, verifications atomic.Int32
	var firstScanAt, secondScanAt atomic.Int64
	peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v/api/v1/sys/version":
			fmt.Fprint(w, `{"code":0,"data":{"version":"1.0-owned","mediasrvVersion":"1.0-owned"}}`)
		case "/v/api/v1/login":
			fmt.Fprint(w, `{"code":0,"data":{"token":"owned-media-session"}}`)
		case "/v/api/v1/user/info":
			fmt.Fprint(w, `{"code":0,"data":{"is_admin":1}}`)
		case "/v/api/v1/mdb/list":
			fmt.Fprintf(w, `{"code":0,"data":[{"guid":%q,"name":"测试库","category":"Mix","dir_list":[%q]},{"guid":%q,"name":"跟随库","category":"Mix","dir_list":[%q]}]}`,
				ownedCoreFNLibrary, library, followerLibraryGUID, followerLibrary)
		case "/v/api/v1/task/running":
			preflights.Add(1)
			fmt.Fprint(w, `{"code":0,"data":null}`)
		case "/v/api/v1/mdb/scan/" + ownedCoreFNLibrary, "/v/api/v1/mdb/scan/" + followerLibraryGUID:
			body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
			if err != nil || r.Method != http.MethodPost || len(body) != 0 {
				coreScans.Add(1)
				t.Error("the owned library scan must be the plugin's empty-body POST")
				fmt.Fprint(w, `{"code":-1}`)
				return
			}
			if strings.HasSuffix(r.URL.Path, ownedCoreFNLibrary) {
				firstScanAt.Store(time.Now().UnixNano())
				primaryScans.Add(1)
			} else {
				secondScanAt.Store(time.Now().UnixNano())
				followerScans.Add(1)
			}
			fmt.Fprint(w, `{"code":0,"data":true}`)
		case "/v/api/v1/mdb/scanall":
			scanAll.Add(1)
			t.Error("unexpected server-wide scan")
			fmt.Fprint(w, `{"code":-1}`)
		case "/v/api/v1/search/list":
			if r.Method != http.MethodGet || r.URL.Query().Get("q") != primary.Title || primaryScans.Load() != 1 || followerScans.Load() != 1 {
				t.Error("verification ran outside the scanned owned asset")
				fmt.Fprint(w, `{"code":0,"data":[]}`)
				return
			}
			fmt.Fprintf(w, `{"code":0,"data":[{"guid":%q,"type":"Movie","title":%q}]}`, ownedCoreFNMovie, primary.Title)
		case "/v/api/v1/item/" + ownedCoreFNMovie:
			fmt.Fprintf(w, `{"code":0,"data":{"guid":%q,"ancestor_guid":%q,"parent_guid":"","type":"Movie","title":%q}}`, ownedCoreFNMovie, ownedCoreFNLibrary, primary.Title)
		case "/v/api/v1/media/itemfile/" + ownedCoreFNMovie:
			path := published.Load().(string)
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				t.Error("verification did not reach the actual published video")
				fmt.Fprint(w, `{"code":0,"data":[]}`)
				return
			}
			verifications.Add(1)
			fmt.Fprintf(w, `{"code":0,"data":[{"item_guid":%q,"file_path":%q}]}`, ownedCoreFNMovie, path)
		default:
			t.Error("unexpected fnOS request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	settings := a.Settings()
	primaryRoot := settings.Roots[0]
	followerRoot := Root{ID: "follower", Path: followerLibrary, Kind: "mixed"}
	settings.Roots = append(settings.Roots, followerRoot)
	settings.FNOS = FNConfig{Enabled: true, Host: peer.URL + "/v", Username: "owned", Password: "owned-synthetic-password"}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	object := installMediaServerRefreshCandidate(t, a, cookie, bundle)
	path := "/api/plugins/" + mediaServerRefreshFixtureID
	form := callTest(t, a.Handler(), http.MethodGet, path+"/form", "", cookie, "")
	var view struct {
		Data struct {
			RenderMode string `json:"render_mode"`
			Conf       json.RawMessage
			Model      map[string]json.RawMessage
		}
	}
	if form.Code != http.StatusOK || json.Unmarshal(form.Body.Bytes(), &view) != nil || view.Data.RenderMode != "vuetify" ||
		!bytes.Contains(view.Data.Conf, []byte(`"component":"VForm"`)) ||
		!bytes.Contains(view.Data.Conf, []byte(`"model":"mediaservers"`)) ||
		!bytes.Contains(view.Data.Conf, []byte(`"items":[{"title":"fnos","value":"fnos"}]`)) ||
		string(view.Data.Model["enabled"]) != "false" || string(view.Data.Model["delay"]) != "0" {
		t.Fatal("candidate did not expose the original native form and configured fnOS option")
	}
	// The fixed installed stop-batch oracle saves the string "10" from the
	// text field. The selected server is the existing configured fnOS slot.
	save := callTest(t, a.Handler(), http.MethodPut, path,
		`{"enabled":true,"delay":"10","mediaservers":["fnos"]}`, cookie, "")
	assertOriginalRegistrySuccess(t, save.Code, save.Body.Bytes())

	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		a.worker(workerCtx)
	}()
	t.Cleanup(func() { stopWorker(); <-workerDone })

	publish := func(media catalog.Media, root Root, filename string, scrape bool) Asset {
		t.Helper()
		sourcePath := filepath.Join(source, filename)
		importWrite(t, sourcePath, []byte("owned-synthetic-video:"+filename))
		// An installed TransferComplete listener uses the source recognition
		// projection. Supply owned provider data at that established boundary.
		details := json.RawMessage(fmt.Sprintf(`{"id":%d,"title":%q,"original_title":%q,"release_date":"%d-01-01","overview":%q,"original_language":"zh","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`,
			media.ID, media.Title, media.OriginalTitle, media.Year, media.Overview))
		resolved, err := sourceTMDBMediaInfo(details, "movie", "image.tmdb.org", "")
		if err != nil {
			t.Fatal(err)
		}
		plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: media.ID, RootID: root.ID, Mode: "hardlink",
			Scraping: &scrape, Files: []ImportFileRequest{{Path: sourcePath}}, originalResolvedMedia: resolved})
		if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
			t.Fatal("owned transfer preview is not ready", err)
		}
		job, err := a.executeImport(ctx, plan.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		privateAwait(t, "native import succeeded", func() bool {
			var state string
			return a.Store.DB.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id=?`, job.ID).Scan(&state) == nil && state == "succeeded"
		})
		var saved ImportPlan
		if err := a.Store.Get(ctx, "import_plan", plan.ID, &saved); err != nil {
			t.Fatal(err)
		}
		if len(saved.Files) != 1 || saved.Files[0].AssetID == "" {
			t.Fatal("native transfer did not publish its asset")
		}
		var asset Asset
		if err := a.Store.Get(ctx, "asset", saved.Files[0].AssetID, &asset); err != nil || asset.State != "present" ||
			asset.Path != saved.Files[0].Target || !within(root.Path, asset.Path) || asset.FNOSRefreshTransfer != plan.ID+"/"+saved.Files[0].ID {
			t.Fatal("owned transfer asset lacks its native refresh identity", err)
		}
		return asset
	}
	batchReady := func(size int) bool {
		state := &a.originalFNOSRefresh
		state.mu.Lock()
		group := state.groups[object]
		ready := group != nil && group.active == 1 && len(group.events) == size
		state.mu.Unlock()
		if !ready {
			return false
		}
		page, err := a.queryLogs(ctx, map[string]string{"plugin_instance_id": mediaServerRefreshFixtureID, "from": "0001-01-01T00:00:00Z", "limit": "100"})
		count := 0
		for _, entry := range page.Entries {
			if entry["event"] == "plugin_message" && strings.TrimSpace(fmt.Sprint(entry["message"])) == "延迟 10 秒后刷新媒体库..." {
				count++
			}
		}
		return err == nil && count == size
	}

	first := publish(primary, primaryRoot, "Test.2024.mkv", true)
	published.Store(filepath.ToSlash(first.Path))
	privateAwait(t, "first native transfer entered the plugin delay", func() bool { return batchReady(1) })
	var syncJob Job
	privateAwait(t, "actual automatic fnos_sync waits for plugin ownership", func() bool {
		job, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='fnos_sync' AND json_extract(payload,'$.id')=?`, first.ID))
		if err != nil || job.ParentJobID == "" || job.State != "retry_wait" || job.ErrorCode != "PLUGIN_REFRESH_PENDING" {
			return false
		}
		var lineage struct {
			Transfer string `json:"original_fnos_transfer"`
		}
		if json.Unmarshal(job.Payload, &lineage) != nil || lineage.Transfer != first.FNOSRefreshTransfer {
			t.Fatal("the actual automatic sync job lost its native transfer lineage")
		}
		syncJob = job
		return true
	})
	if _, err := os.Stat(strings.TrimSuffix(first.Path, filepath.Ext(first.Path)) + ".nfo"); err != nil {
		t.Fatal("automatic scrape did not publish the owned NFO", err)
	}

	followerSubmitted := time.Now()
	second := publish(follower, followerRoot, "Follower.2025.mkv", false)
	if first.Path == second.Path || first.ID == second.ID || first.FNOSRefreshTransfer == second.FNOSRefreshTransfer {
		t.Fatal("native follower reused the leader's publication")
	}
	// One active owner and two distinct tracked transfers means the follower
	// has returned to the host while the leader still owns both batch items.
	privateAwait(t, "second native transfer joined the waiting refresh batch", func() bool { return batchReady(2) })
	for _, asset := range []Asset{first, second} {
		var callback originalTransferCallback
		if err := a.Store.Get(ctx, "original_transfer_callback", asset.FNOSRefreshTransfer, &callback); err != nil ||
			!callback.FNOSRefreshOwner || callback.File.Target != asset.Path {
			t.Fatal("successful selected fnOS probe did not retain native callback ownership", err)
		}
	}
	stopAt := time.Now()
	deadline := followerSubmitted.Add(10 * time.Second)
	if primaryScans.Load()+followerScans.Load()+coreScans.Load()+scanAll.Load()+preflights.Load() != 0 || !stopAt.Before(deadline) {
		t.Fatal("the delay must still own both transfers before stopping")
	}
	stop := callTest(t, a.Handler(), http.MethodDelete, path, "", cookie, "")
	assertOriginalRegistrySuccess(t, stop.Code, stop.Body.Bytes())

	privateAwait(t, "automatic core retry verified the published asset", func() bool {
		var state string
		return a.Store.DB.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id=?`, syncJob.ID).Scan(&state) == nil && state == "succeeded"
	})
	var saved Asset
	if err := a.Store.Get(ctx, "asset", first.ID, &saved); err != nil || saved.Path != first.Path || saved.ScrapeState != "scraped" ||
		saved.FNOSState != "visible" || saved.FNOSGUID != ownedCoreFNMovie {
		t.Fatal("fnOS Verify did not persist the same published asset", err)
	}
	if primaryScans.Load() != 1 || followerScans.Load() != 1 || coreScans.Load() != 0 || scanAll.Load() != 0 ||
		preflights.Load() != 1 || verifications.Load() != 1 {
		t.Fatal("stop must flush both owned GUID scans in one batch, then Verify without a core scan")
	}
	for _, scanAt := range []int64{firstScanAt.Load(), secondScanAt.Load()} {
		if scanAt < stopAt.UnixNano() || scanAt >= deadline.UnixNano() {
			t.Fatal("the batch did not scan both GUIDs after stop and before the follower deadline")
		}
	}
	if _, loaded := a.pluginRunningInstances.Load(mediaServerRefreshFixtureID); loaded {
		t.Fatal("the actual plugin was not stopped and uninstalled")
	}
	t.Log("exact MediaServerRefresh 1.3.3 ZIP and native form, two committed imports, automatic scrape and pending retry, uninstall flush with one preflight and two owned empty-body GUID scans, and actual asset Verify passed")
}

func installMediaServerRefreshCandidate(t *testing.T, a *App, cookie *http.Cookie, bundle string) *originalPluginInstance {
	t.Helper()
	const archiveName = "MediaServerRefresh-1.3.3-linux-amd64.zip"
	const archiveSHA = "9dc99fcd69f9b8e8a76ed3c4cdb29ea004f940875abec014894f7738b2b9c39d"
	const indexSHA = "239325393e83e46761aa6f2a736cd785394bea1db21643ae4dceb53890b73b05"
	raw, err := os.ReadFile(filepath.Join(bundle, artifacts.IndexName))
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != indexSHA {
		t.Fatal("expected the exact owned MediaServerRefresh package index", err)
	}
	var index artifacts.RepositoryIndex
	if err := json.Unmarshal(raw, &index); err != nil || index.SchemaVersion != 1 || len(index.Plugins) != 1 ||
		index.Plugins[0].PluginID != mediaServerRefreshFixtureID || index.Plugins[0].CurrentVersion != "1.3.3" ||
		len(index.Plugins[0].Releases) != 1 || len(index.Plugins[0].Releases[0].Artifacts) != 1 {
		t.Fatal("unexpected canonical candidate index", err)
	}
	artifact := &index.Plugins[0].Releases[0].Artifacts[0]
	if artifact.Format != "zip" || artifact.Platform != "linux-amd64" || artifact.HostContract != pluginResourcesSDKContract ||
		artifact.SHA256 != archiveSHA {
		t.Fatal("expected the exact canonical linux/amd64 resource ZIP")
	}
	archive := filepath.Join(bundle, archiveName)
	var downloads atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + artifacts.IndexName:
			_ = json.NewEncoder(w).Encode(index)
		case "/" + archiveName:
			downloads.Add(1)
			http.ServeFile(w, r, archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repository.Close)
	// Rewrite only the inert artifact URL for the owned loopback repository.
	artifact.URL = repository.URL + "/" + archiveName
	body, err := json.Marshal(map[string]string{"plugin_id": mediaServerRefreshFixtureID, "repo_url": repository.URL + "/" + artifacts.IndexName})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), mediaServerRefreshFixtureID, validatePluginArtifactContract)
	if err != nil || installed.Manifest.Version != "1.3.3" || installed.SHA256 != artifact.ExecutableSHA256 ||
		len(installed.Files) != len(artifact.Files) || downloads.Load() != 1 {
		t.Fatal("host did not install the exact candidate ZIP", err)
	}
	return originalBaseSDKLoaded(t, a, mediaServerRefreshFixtureID)
}
