package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/artifacts"
)

const subscribeGroupFixtureID = "SubscribeGroup"

func TestSubscribeGroupInstalledAMD64Package(t *testing.T) {
	bundle := os.Getenv("SOURCE_SUBSCRIBE_GROUP_PACKAGE_DIR")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the SubscribeGroup candidate ZIP in the owned Linux amd64 fixture")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, downloadRequest, adds := nativeProducerFixture(t)
	ctx := t.Context()
	if err := os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte("tv:\n  OwnedCategory:\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal(err)
	}
	request := subscriptionRequest(t)
	media, err := a.media(ctx, request.MediaType, request.TMDBID)
	if err != nil {
		t.Fatal(err)
	}
	request.Media = media
	request.Policy.Resolution, request.Policy.Exclude = "720p", "OwnedPrevious"
	notices := installNoticeSourceFixtures(t, a, media, request.Season, 5)
	subscriptionStatsInstallSettings(t, a, "", false)

	cookie := setupTestAdmin(t, a)
	installSubscribeGroupCandidate(t, a, cookie, bundle)
	path := "/api/plugins/" + subscribeGroupFixtureID
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
		!bytes.Contains(view.Data.Conf, []byte(`"model":"update_details"`)) ||
		!bytes.Contains(view.Data.Conf, []byte(`"vale":"资源质量"`)) ||
		string(view.Data.Model["update_details"]) != "[]" || string(view.Data.Model["update_confs"]) != `""` {
		t.Fatal("candidate did not expose the source native form")
	}
	for _, flag := range []string{"enabled", "category", "clear", "clear_handle"} {
		if string(view.Data.Model[flag]) != "false" {
			t.Fatal("incorrect installed form default", flag)
		}
	}
	save := func(clear bool) {
		t.Helper()
		body := mustCleanSeedJSON(map[string]any{
			"enabled": true, "category": true, "clear": clear, "clear_handle": clear,
			"update_details": []string{"分辨率", "资源质量", "制作组", "站点"},
			"update_confs":   "category:OwnedCategory#resolution:1080p#exclude:OwnedFromCategory",
		})
		response := callTest(t, a.Handler(), http.MethodPut, path, string(body), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	save(false)

	subscription := callTest(t, a.Handler(), http.MethodPost, "/api/subscriptions", string(mustCleanSeedJSON(request)), cookie, "")
	var added struct{ Data Subscription }
	if subscription.Code != http.StatusOK || json.Unmarshal(subscription.Body.Bytes(), &added) != nil || added.Data.ID == "" {
		t.Fatalf("owned native subscription did not finish: %d %s", subscription.Code, subscription.Body.Bytes())
	}
	_, receipt := uiSourceOnlyAdd(t, a)
	if receipt.Saved == nil || receipt.Saved.ID != added.Data.ID || receipt.Saved.Revision != 1 ||
		receipt.Saved.Policy.Resolution != "720p" || receipt.Saved.Policy.Exclude != "OwnedPrevious" || notices.Sends.Load() != 1 {
		t.Fatal("fixture did not persist the pre-SubscribeAdded values through the actual add/notice chain")
	}
	subscriptionAt := func(revision int) Subscription {
		t.Helper()
		var current Subscription
		privateAwait(t, fmt.Sprintf("source event saved subscription revision %d", revision), func() bool {
			return a.Store.Get(ctx, "subscription", added.Data.ID, &current) == nil && current.Revision >= revision
		})
		if current.Revision != revision {
			t.Fatalf("unexpected repeated subscription mutation: %d, want %d", current.Revision, revision)
		}
		return current
	}
	categorized := subscriptionAt(2)
	if categorized.Policy.Resolution != "1080[pi]|x1080" || categorized.Policy.Exclude != "OwnedFromCategory" ||
		categorized.Policy.Include != "" || categorized.Policy.Source != "" || len(categorized.SiteIDs) != 0 {
		t.Fatal("actual SubscribeAdded did not overwrite exactly the configured category fields")
	}
	t.Log("native SubscribeAdded: existing resolution and exclude replaced by the category rule")

	download := callTest(t, a.Handler(), http.MethodPost, "/api/downloads/add", string(mustCleanSeedJSON(downloadRequest)), cookie, "")
	var queued struct{ Data NativeDownload }
	if download.Code != http.StatusOK || json.Unmarshal(download.Body.Bytes(), &queued) != nil || queued.Data.ID == "" {
		t.Fatalf("owned native download did not queue: %d %s", download.Code, download.Body.Bytes())
	}
	job, err := a.claim(ctx)
	if err != nil || job.Kind != "native_download" {
		t.Fatal("actual native download job could not be claimed", err)
	}
	if err = a.safeRun(ctx, job); err != nil || adds.Load() != 1 {
		t.Fatal("native download did not complete exactly one owned qBittorrent Add", err)
	}
	var accepted NativeDownload
	if err = a.Store.Get(ctx, "native_download", queued.Data.ID, &accepted); err != nil || accepted.State != "accepted" {
		t.Fatal("actual Add did not commit the accepted download fact", err)
	}
	filled := subscriptionAt(3)
	if filled.Policy.Resolution != categorized.Policy.Resolution || filled.Policy.Exclude != categorized.Policy.Exclude ||
		filled.Policy.Source != "WEB-?DL|WEB-?RIP" || filled.Policy.Include != "MARKER.+FixtureTeam" ||
		!slices.Equal(filled.SiteIDs, []string{downloadRequest.SiteID}) {
		t.Fatal("actual DownloadAdded did not fill missing quality, team and RSS site while preserving existing filters")
	}

	readData := func(key string) json.RawMessage {
		t.Helper()
		raw, err := a.pluginStore.originalBaseGetData(ctx, subscribeGroupFixtureID, &key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	site, err := a.Store.nativeSiteByAppID(ctx, downloadRequest.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := a.Store.nativeSourcePublicSiteID(ctx, a.Store.DB, site)
	if err != nil || sourceID < 1 {
		t.Fatal("owned tracker did not have an actual numeric source identity", err)
	}
	var history []struct{ Name, Type, Content, Time string }
	wantContent := []string{
		`{"exclude": "OwnedFromCategory", "resolution": "1080[pi]|x1080"}`,
		fmt.Sprintf(`{"quality": "WEB-?DL|WEB-?RIP", "include": "MARKER.+FixtureTeam", "sites": [%d]}`, sourceID),
	}
	wantTypes := []string{"二级分类自定义配置 OwnedCategory", "种子下载自定义配置"}
	if json.Unmarshal(readData("history"), &history) != nil || len(history) != len(wantContent) {
		t.Fatal("source history must contain both distinct event updates")
	}
	for i, row := range history {
		_, timeErr := time.Parse(time.DateTime, row.Time)
		if row.Name != media.Title || row.Type != wantTypes[i] || row.Content != wantContent[i] || timeErr != nil {
			t.Fatalf("unexpected native history row %d: %+v", i, row)
		}
	}
	var handled []string
	if json.Unmarshal(readData("history_handle"), &handled) != nil || !slices.Equal(handled, []string{"电视剧:206484"}) {
		t.Fatal("download did not persist the source cross-season handled identity")
	}
	readPage := func() json.RawMessage {
		t.Helper()
		response := callTest(t, a.Handler(), http.MethodGet, path+"/page", "", cookie, "")
		var body struct {
			Data struct {
				RenderMode string `json:"render_mode"`
				Page       json.RawMessage
			}
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.RenderMode != "vuetify" {
			t.Fatal("native history page could not be read")
		}
		return body.Data.Page
	}
	page := readPage()
	categoryPos, downloadPos := bytes.Index(page, []byte(wantTypes[0])), bytes.Index(page, []byte(wantTypes[1]))
	if !bytes.Contains(page, []byte(`"component":"VTable"`)) || !bytes.Contains(page, []byte("执行时间")) ||
		categoryPos < 0 || downloadPos < 0 || (categoryPos < downloadPos) != (history[0].Time >= history[1].Time) {
		t.Fatal("native history table did not display the source ordering")
	}
	t.Logf("native DownloadAdded: one accepted Add, stable site %s mapped from source site %d, ordered histories and handled identity persisted", downloadRequest.SiteID, sourceID)

	save(true)
	raw, _, err := a.readOriginalBaseConfigForInit(ctx, subscribeGroupFixtureID)
	var config map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &config) != nil ||
		string(config["clear"]) != "false" || string(config["clear_handle"]) != "false" ||
		string(readData("history")) != "null" || string(readData("history_handle")) != "null" ||
		!bytes.Contains(readPage(), []byte("暂无数据")) {
		t.Fatal("user-driven clear did not remove both data keys and reset the one-shot flags")
	}
	if current := subscriptionAt(3); current.Policy.Include != filled.Policy.Include || adds.Load() != 1 {
		t.Fatal("clearing plugin history affected the core subscription or repeated Add")
	}
	t.Log("user clear: both history keys removed, flags reset, native empty view and existing subscription retained")
}

func installSubscribeGroupCandidate(t *testing.T, a *App, cookie *http.Cookie, bundle string) {
	t.Helper()
	const (
		archiveName = "SubscribeGroup-2.8.7-linux-amd64.zip"
		indexSHA    = "9fa9bd929d65b351a5e0094abd2abadf3b02019473fc91bf476bd39ab47f5775"
		archiveSHA  = "48d0bdfbd46d3acee98be351dfe560da2bf32155786b214049283259291336a0"
		workerSHA   = "609e5b4fd97abfe7922bca8a3a8a860361a863e34d75a58d9a9c646dc2042ff8"
	)
	raw, err := os.ReadFile(filepath.Join(bundle, artifacts.IndexName))
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != indexSHA {
		t.Fatal("expected the exact owned SubscribeGroup index", err)
	}
	var index artifacts.RepositoryIndex
	if json.Unmarshal(raw, &index) != nil || index.SchemaVersion != 1 || len(index.Plugins) != 1 ||
		index.Plugins[0].PluginID != subscribeGroupFixtureID || index.Plugins[0].CurrentVersion != "2.8.7" ||
		len(index.Plugins[0].Releases) != 1 || len(index.Plugins[0].Releases[0].Artifacts) != 1 {
		t.Fatal("unexpected SubscribeGroup candidate index")
	}
	release := &index.Plugins[0].Releases[0]
	artifact := &release.Artifacts[0]
	archive, err := os.ReadFile(filepath.Join(bundle, archiveName))
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if release.Version != "2.8.7" || release.Manifest.PluginID != subscribeGroupFixtureID ||
		!release.Manifest.HasPage || artifact.Format != "zip" || artifact.Platform != "linux-amd64" ||
		artifact.HostContract != pluginResourcesSDKContract || artifact.Size != int64(len(archive)) ||
		artifact.SHA256 != archiveSHA || digest != archiveSHA || artifact.ExecutableSHA256 != workerSHA {
		t.Fatal("expected the indexed linux/amd64 SubscribeGroup ZIP")
	}
	var downloads atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + artifacts.IndexName:
			_ = json.NewEncoder(w).Encode(index)
		case "/" + archiveName:
			downloads.Add(1)
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repository.Close)
	// Only the download location changes for the owned loopback repository.
	artifact.URL = repository.URL + "/" + archiveName
	body := mustCleanSeedJSON(map[string]string{"plugin_id": subscribeGroupFixtureID, "repo_url": repository.URL + "/" + artifacts.IndexName})
	response := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), subscribeGroupFixtureID, validatePluginArtifactContract)
	if err != nil || installed.Manifest.Version != "2.8.7" || installed.SHA256 != artifact.ExecutableSHA256 ||
		len(installed.Files) != len(artifact.Files) || downloads.Load() != 1 {
		t.Fatal("host did not install the indexed SubscribeGroup ZIP", err)
	}
	originalBaseSDKLoaded(t, a, subscribeGroupFixtureID)
	t.Logf("installed candidate: index sha256 %s, ZIP sha256 %s, worker sha256 %s", indexSHA, archiveSHA, workerSHA)
}
