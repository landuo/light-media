package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/sites"
)

func nativeProducerFixture(t *testing.T) (*App, NativeDownloadRequest, *atomic.Int32) {
	t.Helper()
	a := subscriptionTestApp(t)
	source := t.TempDir()
	title := "Jade.Dynasty.S04E01.2160p.WEB-DL.HDR10.MARKER-FixtureTeam"
	torrent, hash := workflowTorrent(title + ".mkv")
	adds := new(atomic.Int32)
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if adds.Load() > 0 {
				task := workflowTask(hash, 0)
				task["name"] = "renamed.by.downloader"
				task["save_path"] = source
				workflowJSON(w, []any{task})
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Error(err)
			}
			if r.FormValue("savepath") != source {
				t.Error("wrong selected directory")
			}
			adds.Add(1)
			io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			io.WriteString(w, workflowHTML(title, "", false))
		case "/download.php":
			w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)
	c := a.Settings()
	c.Downloaders[0].BaseURL = qb.URL
	c.Sites = []SiteConfig{{Config: sites.Config{ID: "producer-site", Kind: "jiayuan", Name: "合成站点", BaseURL: site.URL, Cookie: "synthetic-cookie"}, Enabled: true, DailyBudget: 100}}
	c.AllowedRoots = []string{source}
	c.RSSSites = []string{"producer-site"}
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveResourceMetadataSettings(context.Background(), 0, resourceMetadataConfiguration{SourceMode: "rust", ReleaseGroups: []string{"FixtureTeam"}, Customization: []string{"MARKER"}}); err != nil {
		t.Fatal(err)
	}
	return a, NativeDownloadRequest{SiteID: "producer-site", ResourceID: "101", Query: "Jade Dynasty", DownloaderID: "test-qb", DownloadPath: source, MediaType: "tv", TMDBID: 206484}, adds
}

func executeSubscribeDownloadWorker(t *testing.T, a *App) PluginRun {
	t.Helper()
	run := subscribeGroupClaim(t, a)
	a.executePluginRun(context.Background(), run)
	finished, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || finished.State != "succeeded" {
		t.Fatal("actual Go plugin worker failed", finished.State, finished.ErrorCode, err)
	}
	return finished
}

func TestNativeDownloadProducerThroughActualGoSubscribeWorker(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unidentified", true: "identified"}[known], func(t *testing.T) {
			a, in, adds := nativeProducerFixture(t)
			ctx := context.Background()
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"SubscribeGroup"}`, cookie, "")
			if response.Code != 200 {
				t.Fatal("install failed", response.Code, response.Body.String())
			}
			executeSubscribeDownloadWorker(t, a)
			config, err := a.pluginStore.Config(ctx, "SubscribeGroup")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"revision": config.Revision, "config": subscribeDownloadConfig()})
			response = callTest(t, a.Handler(), "PUT", "/api/plugins/SubscribeGroup/config", string(raw), cookie, "")
			if response.Code != 200 {
				t.Fatal("configuration failed", response.Code, response.Body.String())
			}
			executeSubscribeDownloadWorker(t, a)
			request := subscriptionRequest(t)
			request.DownloadPath = in.DownloadPath
			request.RootID = ""
			sub, err := a.saveSubscription(ctx, "", request)
			if err != nil {
				t.Fatal(err)
			}
			if !known {
				in.TMDBID = 0
				in.MediaType = ""
			}
			native, err := a.addNativeDownload(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			// Changing these inputs after selection cannot re-parse a different
			// source Context, nor can the downloader's deliberately renamed task.
			if _, err = a.saveResourceMetadataSettings(ctx, 1, resourceMetadataConfiguration{SourceMode: "legacy", ReleaseGroups: []string{"OtherTeam"}, Customization: []string{"OTHER"}}); err != nil {
				t.Fatal(err)
			}
			job, err := a.claim(ctx)
			if err != nil || job.Kind != "native_download" {
				t.Fatal("missing core submission", job.Kind, err)
			}
			if err = a.safeRun(ctx, job); err != nil {
				t.Fatal(err)
			}
			if adds.Load() != 1 {
				t.Fatal("not an actual unique downloader submission", adds.Load())
			}
			page, err := a.domainEvents(ctx, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			var accepted DomainEvent
			for _, event := range page.Events {
				if event.Type == "download.accepted" {
					accepted = event
				}
			}
			if accepted.NativeDownloadID != native.ID || accepted.IntentID != "" || accepted.SubjectKind != "native_download" {
				t.Fatal("manual download borrowed intent authority", accepted)
			}
			if known && accepted.MediaID != "tv:206484" || !known && accepted.MediaID != "" {
				t.Fatal("media identity was invented or dropped", accepted.MediaID)
			}
			snapshot, err := a.downloadAddedSnapshot(ctx, accepted)
			if err != nil || snapshot.Metadata == nil || snapshot.Metadata.Team != "FixtureTeam" || snapshot.Metadata.Customization != "MARKER" || snapshot.Seasons != "S04" {
				t.Fatal("source Context was not frozen", snapshot, err)
			}
			if snapshot.Context == nil || snapshot.Context.Metadata == nil || snapshot.Context.Options.SourceMode != "rust" || !reflect.DeepEqual(snapshot.Context.Metadata, snapshot.Metadata) {
				t.Fatal("parsed source Context was not frozen before Add", snapshot.Context)
			}
			for _, change := range []func(*DomainEvent){func(e *DomainEvent) { e.JobID = "job_other" }, func(e *DomainEvent) { e.WorkflowID = "wf_other" }, func(e *DomainEvent) { e.NativeDownloadID = "native_other"; e.SubjectID = "native_other" }} {
				forged := accepted
				change(&forged)
				if _, err := a.downloadAddedSnapshot(ctx, forged); err == nil {
					t.Fatal("forged download authority read Context")
				}
			}
			if err = a.dispatchPluginEvents(ctx, time.Now()); err != nil {
				t.Fatal(err)
			}
			executeSubscribeDownloadWorker(t, a)
			updated := subscribeGroupRead(t, a, sub.ID)
			if known {
				if updated.Policy.FilterParams == nil || *updated.Policy.FilterParams.Include != "MARKER.+FixtureTeam" || *updated.Policy.FilterParams.Resolution != "4K|2160p|x2160" || !reflect.DeepEqual(updated.SiteIDs, []string{"producer-site"}) {
					t.Fatal("actual download did not fill subscription", updated.Policy, updated.SiteIDs)
				}
			} else if !reflect.DeepEqual(updated, sub) {
				t.Fatal("unidentified download claimed an unrelated subscription")
			}
			var sealed []byte
			if err = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='download_added' AND id=?`, accepted.EventID).Scan(&sealed); err != nil || json.Valid(sealed) || bytes.Contains(sealed, []byte("FixtureTeam")) {
				t.Fatal("private Context stored in plaintext", err)
			}
		})
	}
}

func TestNativeDownloadAcceptanceOutboxFailureReconcilesWithoutReadding(t *testing.T) {
	a, in, adds := nativeProducerFixture(t)
	ctx := context.Background()
	native, err := a.addNativeDownload(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER deny_native_accept BEFORE INSERT ON domain_events WHEN json_extract(NEW.payload,'$.native_download_id') IS NOT NULL BEGIN SELECT RAISE(ABORT,'synthetic late failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = a.runNativeDownload(ctx, job, native.ID); err == nil {
		t.Fatal("outbox failure claimed committed acceptance")
	}
	if adds.Load() != 1 {
		t.Fatal("first actual add missing")
	}
	var snapshots int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='download_added'`).Scan(&snapshots); err != nil || snapshots != 0 {
		t.Fatal("snapshot escaped rollback", snapshots, err)
	}
	page, err := a.domainEvents(ctx, 0, 100)
	if err != nil || len(page.Events) != 0 {
		t.Fatal("acceptance fact escaped rollback", err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER deny_native_accept`); err != nil {
		t.Fatal(err)
	}
	if err = a.runNativeDownload(ctx, job, native.ID); err != nil {
		t.Fatal(err)
	}
	if adds.Load() != 1 {
		t.Fatal("database retry resubmitted torrent", adds.Load())
	}
	page, err = a.domainEvents(ctx, 0, 100)
	if err != nil || len(page.Events) != 1 {
		t.Fatal("confirmed fact missing", err)
	}
	if _, err = a.downloadAddedSnapshot(ctx, page.Events[0]); err != nil {
		t.Fatal("confirmed Context unavailable", err)
	}
}

func TestNativeDownloadProducerRejectsMetadataFailureBeforeAdd(t *testing.T) {
	a, in, adds := nativeProducerFixture(t)
	ctx := context.Background()
	if _, err := a.saveResourceMetadataSettings(ctx, 1, resourceMetadataConfiguration{SourceMode: "legacy", Customization: []string{"["}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.addNativeDownload(ctx, in); pluginErrorCode(err) != "RESOURCE_METADATA_FAILED" {
		t.Fatal("invalid legacy metadata did not fail before downloader submission", err)
	}
	if adds.Load() != 0 {
		t.Fatal("metadata failure submitted to downloader", adds.Load())
	}
	var records int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('native_download','download_context','download_added')`).Scan(&records); err != nil || records != 0 {
		t.Fatal("failed metadata created a planned download", records, err)
	}
	page, err := a.domainEvents(ctx, 0, 100)
	if err != nil || len(page.Events) != 0 {
		t.Fatal("failed metadata emitted accepted fact", err)
	}
}

func TestResourceMetadataSettingsAuthenticationCASAndEncryptedRestart(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if response := callTest(t, a.Handler(), "GET", "/api/settings/resource-metadata", "", nil, ""); response.Code == 200 {
		t.Fatal("anonymous settings read")
	}
	cookie := setupTestAdmin(t, a)
	config := resourceMetadataConfiguration{SourceMode: "legacy", ReleaseGroups: []string{" Group ", "Group", "Group", ""}, Customization: []string{"private-synthetic-pattern-901", "A|B", ""}}
	raw, _ := json.Marshal(map[string]any{"revision": 0, "config": config})
	if response := callTest(t, a.Handler(), "PUT", "/api/settings/resource-metadata", string(raw), cookie, ""); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if response := callTest(t, a.Handler(), "PUT", "/api/settings/resource-metadata", string(raw), cookie, ""); response.Code == 200 || strings.Contains(response.Body.String(), "private-synthetic-pattern-901") {
		t.Fatal("stale save accepted or echoed private pattern")
	}
	// General settings have no ownership of this independent CAS document.
	if err := a.saveSettings(ctx, a.Settings()); err != nil {
		t.Fatal(err)
	}
	revision, stored, err := a.resourceMetadataSettings(ctx)
	if err != nil || revision != 1 || !reflect.DeepEqual(stored, config) {
		t.Fatal("source lines changed or general save overwrote config", revision, stored, err)
	}
	var sealed []byte
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM resource_metadata_settings WHERE id='main'`).Scan(&sealed); err != nil || json.Valid(sealed) || bytes.Contains(sealed, []byte("private-synthetic-pattern-901")) {
		t.Fatal("metadata settings stored in plaintext", err)
	}
	config.Customization = []string{"changed"}
	if revision, err = a.saveResourceMetadataSettings(ctx, 1, config); err != nil || revision != 2 {
		t.Fatal("CAS update failed", revision, err)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	revision, stored, err = reopened.resourceMetadataSettings(ctx)
	if err != nil || revision != 2 || !reflect.DeepEqual(stored, config) {
		t.Fatal("actual reopen lost parser configuration", err)
	}
	// No browser/downloader operation is needed to reject malformed options.
	for _, badConfig := range []resourceMetadataConfiguration{{SourceMode: "python"}, {SourceMode: "rust", ReleaseGroups: []string{strings.Repeat("x", (64<<10)+1)}}} {
		if _, err = reopened.saveResourceMetadataSettings(ctx, revision, badConfig); pluginErrorCode(err) != "INVALID_RESOURCE_METADATA" {
			t.Fatal("invalid parser configuration accepted", err)
		}
	}
	// Escaping is part of the persisted size, even when each raw line and
	// their combined input length fit the configured bounds.
	escaped := resourceMetadataConfiguration{SourceMode: "rust"}
	for i := 0; i < 17; i++ {
		escaped.Customization = append(escaped.Customization, strings.Repeat(`\`, 60<<10))
	}
	if _, err := reopened.saveResourceMetadataSettings(ctx, revision, escaped); pluginErrorCode(err) != "INVALID_RESOURCE_METADATA" {
		t.Fatal("escaped configuration was saved beyond its readable size", err)
	}
	if actualRevision, current, err := reopened.resourceMetadataSettings(ctx); err != nil || actualRevision != revision || !reflect.DeepEqual(current, config) {
		t.Fatal("oversized save damaged the previous configuration", err)
	}
}

func TestDownloadContextRetainsLargeValidRecognitionConfiguration(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	rules := make([]string, 20)
	for i := range rules {
		rules[i] = "#" + strings.Repeat("synthetic", 7000)
	}
	if err := a.saveRecognitionWords(ctx, 0, rules); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 40)
	source, err := a.prepareDownloadContext(ctx, "native_download", "native_large", hash, sites.Resource{Title: "Synthetic.Show.S04E01.1080p.WEB-DL"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(source)
	if len(encoded) <= 1<<20 {
		t.Fatal("fixture does not exercise the former single-value bound")
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = a.saveDownloadContextTx(ctx, tx, source); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal("valid recognition configuration prevented restart", err)
	}
	defer reopened.Close()
	actual, err := reopened.loadDownloadContext(ctx, "native_download", "native_large", hash)
	if err != nil || actual == nil || actual.Metadata == nil || actual.Seasons != "S04" || !reflect.DeepEqual(actual.Options.CustomWords, rules) {
		t.Fatal("large frozen source context did not survive restart", err)
	}
	if _, err = reopened.loadDownloadContext(ctx, "native_download", "native_large", strings.Repeat("b", 40)); err == nil {
		t.Fatal("large context lost its hash binding")
	}
}
