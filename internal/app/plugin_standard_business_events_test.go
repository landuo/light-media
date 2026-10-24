package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"light-media/internal/sites"
)

const ownedEventFixtureID = "OwnedEventFixture"

type ownedBusinessEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func installOwnedEventFixture(t *testing.T, a *App) *http.Cookie {
	t.Helper()
	path := os.Getenv("OWNED_EVENT_FIXTURE_BINARY")
	if path == "" {
		t.Fatal("OWNED_EVENT_FIXTURE_BINARY must name the actual compiled standard Base SDK worker")
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	raw, err := exec.CommandContext(ctx, path, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var m plugins.Manifest
	if json.Unmarshal(raw, &m) != nil || m.PluginID != ownedEventFixtureID || m.Validate() != nil {
		t.Fatal("wrong actual SDK artifact")
	}
	digest := sha256.Sum256(binary)
	hash := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: m.PluginID, CurrentVersion: m.Version, Releases: []artifacts.Release{{Version: m.Version, Manifest: m, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: hash, ExecutableSHA256: hash, Size: int64(len(binary)), HostContract: "light-media.sdk-base/v1"}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(binary)
		default:
			t.Error("unexpected artifact request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"plugin_id": m.PluginID, "repo_url": server.URL + "/index"})
	installed := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, installed.Code, installed.Body.Bytes())
	a.wg.Wait()
	loaded, ok := a.pluginRunningInstances.Load(ownedEventFixtureID)
	if !ok || !loaded.(*originalPluginInstance).external {
		t.Fatal("fixture was not installed as a real external SDK object")
	}
	t.Cleanup(a.closeOriginalSessions)
	setOwnedEventFixtureConfig(t, a, cookie, ownedEventFixtureID, true, false)
	return cookie
}

func setOwnedEventFixtureConfig(t *testing.T, a *App, cookie *http.Cookie, instance string, enabled, fail bool) {
	t.Helper()
	raw, _ := json.Marshal(map[string]bool{"enabled": enabled, "fail_event": fail})
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+instance, string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
}

func ownedBusinessEvents(t *testing.T, a *App, instance string) []ownedBusinessEvent {
	t.Helper()
	a.wg.Wait()
	value, err := a.pluginStore.Data(context.Background(), instance, "events")
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var events []ownedBusinessEvent
	if err = json.Unmarshal(value.Value, &events); err != nil {
		t.Fatal(err)
	}
	return events
}

func requireBusinessEventCount(t *testing.T, a *App, instance, kind string, want int) []ownedBusinessEvent {
	t.Helper()
	out := []ownedBusinessEvent{}
	for _, e := range ownedBusinessEvents(t, a, instance) {
		if e.Type == kind {
			out = append(out, e)
		}
	}
	if len(out) != want {
		t.Fatalf("actual %s subscriber %s attempts=%d want=%d", instance, kind, len(out), want)
	}
	var runs int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("business events revived durable plugin invocation workflow", runs, err)
	}
	return out
}

func TestStandardBusinessEventsActualSiteDeleteCommitRollbackCloneAndFailure(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	cloneResponse := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedEventFixtureID+"/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, cloneResponse.Code, cloneResponse.Body.Bytes())
	clone := ownedEventFixtureID + "twin"
	a.wg.Wait()
	setOwnedEventFixtureConfig(t, a, cookie, clone, false, false)
	site := identityTestSite("owned-private-name", "owned-event.example.com")
	site.Cookie = "owned-event-private-cookie"
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", site) != 200 {
		t.Fatal("create")
	}
	stored := a.Settings().Sites[0]
	identity := identityRequire(t, a, stored.ID)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_standard_site_delete BEFORE INSERT ON domain_events WHEN json_extract(NEW.payload,'$.event_type')='site.deleted' BEGIN SELECT RAISE(ABORT,'owned rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+stored.ID, nil) == 200 {
		t.Fatal("failed transaction reported success")
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "site.deleted", 0)
	if identityRequire(t, a, stored.ID).State != "active" {
		t.Fatal("rollback changed identity")
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_standard_site_delete`); err != nil {
		t.Fatal(err)
	}
	setOwnedEventFixtureConfig(t, a, cookie, ownedEventFixtureID, true, true)
	if identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+stored.ID, nil) != 200 {
		t.Fatal("subscriber error rolled back successful core delete")
	}
	events := requireBusinessEventCount(t, a, ownedEventFixtureID, "site.deleted", 1)
	requireBusinessEventCount(t, a, clone, "site.deleted", 0)
	var data map[string]json.RawMessage
	if json.Unmarshal(events[0].Data, &data) != nil || len(data) != 1 || string(data["site_id"]) != fmt.Sprint(identity.SourceID) {
		t.Fatal("wrong source numeric site payload", string(events[0].Data))
	}
	if strings.Contains(string(events[0].Data), site.Cookie) || strings.Contains(string(events[0].Data), site.BaseURL) {
		t.Fatal("event leaked site credentials")
	}
	if identityRequire(t, a, stored.ID).State != "deleted" {
		t.Fatal("subscriber failure undid committed tombstone")
	}
	if err := a.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "site.deleted", 1)
	setOwnedEventFixtureConfig(t, a, cookie, clone, true, false)
	if err := a.stopOriginalPlugin(context.Background(), ownedEventFixtureID); err != nil {
		t.Fatal(err)
	}
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", site) != 200 {
		t.Fatal("re-add")
	}
	replacement := a.Settings().Sites[0]
	if identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+replacement.ID, nil) != 200 {
		t.Fatal("replacement delete")
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "site.deleted", 1)
	cloneEvents := requireBusinessEventCount(t, a, clone, "site.deleted", 1)
	if string(cloneEvents[0].Data) == string(events[0].Data) {
		t.Fatal("new site reused deleted numeric identity")
	}
}

func TestStandardBusinessEventsActualSiteRefreshCommitAndIdempotentFact(t *testing.T) {
	a := newTestApp(t)
	installOwnedEventFixture(t, a)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=1">synthetic</a><div>上传：1 GB 下载：0 B 总做种数：0 总做种体积：0 B</div>`)
	}))
	defer server.Close()
	site := SiteConfig{Config: sites.Config{ID: "standard-refresh", Kind: "jiayuan", Name: "Owned", BaseURL: server.URL, Cookie: "owned-cookie"}, Enabled: true}
	if _, err := a.saveManagedSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_standard_refresh BEFORE INSERT ON domain_events BEGIN SELECT RAISE(ABORT,'owned rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.collectManagedSite(context.Background(), site.ID, "owned-fixed-refresh", nil); err == nil {
		t.Fatal("rollback accepted")
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "site.refreshed", 0)
	if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_standard_refresh`); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if _, err := a.collectManagedSite(context.Background(), site.ID, "owned-fixed-refresh", nil); err != nil {
			t.Fatal(err)
		}
	}
	events := requireBusinessEventCount(t, a, ownedEventFixtureID, "site.refreshed", 1)
	identity := identityRequire(t, a, site.ID)
	want := fmt.Sprintf(`{"site_id":%d}`, identity.SourceID)
	if string(events[0].Data) != want {
		t.Fatal("wrong source site refresh payload", string(events[0].Data), want)
	}
	var snapshot siteSnapshot
	if err := a.Store.Get(context.Background(), "site_stats_latest", site.ID, &snapshot); err != nil {
		t.Fatal("event visible without committed observation", err)
	}
}

func TestStandardBusinessEventsActualHistoryAndDownloaderSourceResults(t *testing.T) {
	for _, mode := range []string{"success", "reject", "lost", "keep"} {
		t.Run(mode, func(t *testing.T) {
			a, item := historyRemovalFixture(t)
			installOwnedEventFixture(t, a)
			daemon := &coreDeleteDaemon{kind: "qb", name: filepath.Base(item.Source), path: filepath.Dir(item.Source), mode: mode, tag: "light-media", present: true}
			server := httptest.NewServer(daemon.handle(t))
			defer server.Close()
			d := DownloaderConfig{Config: download.Config{ID: "owned", Kind: "qb", BaseURL: server.URL}, Name: "Owned daemon"}
			a.config.Downloaders = []DownloaderConfig{d}
			a.config.DefaultDownloader = d.ID
			coreHistoryAccepted(t, a, item, d)
			if err := a.saveSettings(context.Background(), a.Settings()); err != nil {
				t.Fatal(err)
			}
			coreDeleteHistoryHTTP(t, a, item)
			files := requireBusinessEventCount(t, a, ownedEventFixtureID, "downloadfile.deleted", 1)
			var fileData map[string]any
			if json.Unmarshal(files[0].Data, &fileData) != nil || fileData["src"] != item.Source || fileData["hash"] != coreFileHash || len(fileData) != 2 {
				t.Fatal("wrong original history payload", string(files[0].Data))
			}
			if _, err := os.Stat(item.Source); !os.IsNotExist(err) {
				t.Fatal("event preceded physical source removal", err)
			}
			if _, err := os.Stat(item.Target); err != nil {
				t.Fatal("library link removed", err)
			}
			job := coreDeleteJob(t, a)
			err := a.safeRun(context.Background(), job)
			if (mode == "reject" || mode == "lost") != (err != nil) {
				t.Fatal("source outcome changed", err)
			}
			events := requireBusinessEventCount(t, a, ownedEventFixtureID, "download.deleted", 1)
			var deleted map[string]any
			if json.Unmarshal(events[0].Data, &deleted) != nil || deleted["hash"] != coreFileHash || len(deleted["torrents"].([]any)) != 1 || len(deleted) != 2 {
				t.Fatal("wrong downloader source payload", string(events[0].Data))
			}
			_ = a.safeRun(context.Background(), job)
			requireBusinessEventCount(t, a, ownedEventFixtureID, "download.deleted", 1)
			requireBusinessEventCount(t, a, ownedEventFixtureID, "downloadfile.deleted", 1)
		})
	}
}

func TestStandardBusinessEventsActualSourceSubscriptionAfterNoticeBeforeStats(t *testing.T) {
	a, in, fixture := uiSourceAddFixture(t)
	installOwnedEventFixture(t, a)
	_, err := a.createUISubscription(context.Background(), in, "owned-event-admin")
	if pluginErrorCodeInternal(err) != "SUBSCRIPTION_ASYNC_STATS_DEPENDENCY" {
		t.Fatal("unexpected existing source statistics boundary", err)
	}
	if fixture.Sends.Load() != 1 {
		t.Fatal("source added event preceded actual original notice stage")
	}
	job, receipt := uiSourceOnlyAdd(t, a)
	events := requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.added", 1)
	var data struct {
		SubscribeID string          `json:"subscribe_id"`
		Username    string          `json:"username"`
		MediaInfo   json.RawMessage `json:"mediainfo"`
	}
	if json.Unmarshal(events[0].Data, &data) != nil || data.SubscribeID != receipt.Saved.ID || data.Username != "owned-event-admin" {
		t.Fatal("wrong source subscribe payload", string(events[0].Data))
	}
	var actual, expected any
	if json.Unmarshal(data.MediaInfo, &actual) != nil || json.Unmarshal(receipt.Input.Origin.MediaInfo, &expected) != nil {
		t.Fatal("invalid full source MediaInfo")
	}
	left, _ := json.Marshal(actual)
	right, _ := json.Marshal(expected)
	if string(left) != string(right) {
		t.Fatal("source MediaInfo changed or compact catalog substituted")
	}
	job = qualityRunningJob(t, a, job)
	_ = a.safeRun(context.Background(), job)
	requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.added", 1)
}
