package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/sites"
)

func ownedBrushInventoryChanges(t *testing.T, a *App) int64 {
	t.Helper()
	if a.Store.DB.Stats().MaxOpenConnections != 1 {
		t.Fatal("fixture no longer exercises the actual single-connection store")
	}
	var changes int64
	if err := a.Store.DB.QueryRow(`SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}

func ownedBrushInventoryNoWrites(t *testing.T, a *App, before int64) {
	t.Helper()
	if after := ownedBrushInventoryChanges(t, a); after != before {
		t.Fatalf("read-only inventory wrote the real database: total_changes %d -> %d", before, after)
	}
	var runs int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("read-only inventory used a legacy run: %d %v", runs, err)
	}
}

func ownedBrushInventoryEndpoint(t *testing.T, requests *atomic.Int32) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "owned endpoint must not be contacted by inventory", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func ownedBrushInventorySave(t *testing.T, a *App, config Settings) {
	t.Helper()
	a.settingsMu.Lock()
	err := a.saveSettings(context.Background(), config)
	a.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func ownedBrushInventorySite(t *testing.T, result plugins.BrushInventoryV1, sourceID int64) plugins.BrushSiteReferenceV1 {
	t.Helper()
	for _, site := range result.Sites {
		if site.SourceID == sourceID {
			return site
		}
	}
	t.Fatalf("expected source identity %d missing from inventory", sourceID)
	return plugins.BrushSiteReferenceV1{}
}

func TestOriginalBrushInventoryActualSDKProjectionAndClone(t *testing.T) {
	a, cookie, object := ownedBrushInventorySDKFixture(t, true)
	before := ownedBrushInventoryChanges(t, a)
	empty := ownedBrushInventoryRead(t, object)
	if empty.Inventory.SchemaVersion != 1 || empty.Inventory.Sites == nil || len(empty.Inventory.Sites) != 0 || empty.Inventory.Downloaders == nil || len(empty.Inventory.Downloaders) != 0 {
		t.Fatal("new App without migration bindings did not return real empty arrays")
	}
	ownedBrushInventoryNoWrites(t, a, before)

	var requests atomic.Int32
	siteURL := ownedBrushInventoryEndpoint(t, &requests)
	publicURL := ownedBrushInventoryEndpoint(t, &requests)
	downloaderURL := ownedBrushInventoryEndpoint(t, &requests)
	proxyURL := ownedBrushInventoryEndpoint(t, &requests)
	private := SiteConfig{Config: sites.Config{ID: "owned-private", Kind: "jiayuan", Name: "Owned disabled site", BaseURL: siteURL, Cookie: "OWNED_COOKIE_SECRET", APIKey: "OWNED_SITE_KEY_SECRET", UserAgent: "OWNED_USER_AGENT_SECRET"}, SiteOptions: SiteOptions{Domain: "https://bad.example/?token=OWNED_DOMAIN_SECRET", Token: "OWNED_TOKEN_SECRET", RSS: "https://bad.example/rss?passkey=OWNED_RSS_SECRET", Proxy: true}, Enabled: false}
	public := SiteConfig{Config: sites.Config{ID: "owned-public", Kind: "jiayuan", Name: "Owned public site", BaseURL: publicURL, Cookie: "OWNED_PUBLIC_COOKIE_SECRET"}, SiteOptions: SiteOptions{Public: true}, Enabled: true}
	placeholder := SiteConfig{Config: sites.Config{ID: "owned-placeholder", Kind: "nexusphp", Name: "Owned incomplete reference", BaseURL: "https://user:OWNED_USERINFO_SECRET@bad.example/private/OWNED_PATH_SECRET?token=OWNED_QUERY_SECRET#OWNED_FRAGMENT_SECRET"}, Enabled: false}
	config := a.Settings()
	config.Sites = []SiteConfig{public, placeholder, private}
	no := false
	config.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: "owned-qb-b", Kind: "qbittorrent", BaseURL: downloaderURL, Username: "OWNED_DOWNLOADER_USER_SECRET", Password: "OWNED_DOWNLOADER_PASSWORD_SECRET", APIKey: "OWNED_DOWNLOADER_KEY_SECRET"}, Name: "Owned duplicate name", Enabled: &no, DownloadPath: "/owned/private/directory", Mappings: []PathMapping{{From: "/owned/private/a", To: "/owned/private/b"}}},
		{Config: download.Config{ID: "owned-qb-a", Kind: "qbittorrent", BaseURL: downloaderURL, Username: "OWNED_OTHER_USER_SECRET", Password: "OWNED_OTHER_PASSWORD_SECRET"}, Name: "Owned duplicate name"},
	}
	config.ProxyHost = strings.Replace(proxyURL, "http://", "http://owned:OWNED_PROXY_PASSWORD_SECRET@", 1)
	ownedBrushInventorySave(t, a, config)
	private = a.Settings().Sites[2]
	privateIdentity := identityRequire(t, a, private.ID)
	publicIdentity := identityRequire(t, a, public.ID)
	placeholderIdentity := identityRequire(t, a, placeholder.ID)
	up, ratio := int64(1234567), 3.25
	observation := ownedAccountObservation(t, a, private, "2026-10-24", "08:09:10", "", &up)
	observation.Stats.Ratio = &ratio
	observation.Stats.Username = "OWNED_STATS_USER_SECRET"
	observation.Stats.UserID = "OWNED_STATS_ID_SECRET"
	observation.Stats.Messages = [][3]*string{{}}
	ownedRecordAccount(t, a, private, observation)
	// A failed later day must not manufacture fresh ratio data or erase the
	// existing view's last selected day. No network refresh is used to seed it.
	ownedRecordAccount(t, a, private, ownedAccountObservation(t, a, private, "2026-10-25", "10:11:12", "OWNED_ACCOUNT_ERROR_SECRET", nil))
	before = ownedBrushInventoryChanges(t, a)
	settingsBefore, _ := json.Marshal(a.Settings())
	messagesBefore := append([]byte{}, a.systemMessages.get("system")...)
	snapshot := ownedBrushInventoryRead(t, object)
	if !snapshot.HadPrevious || snapshot.PID != empty.PID {
		t.Fatal("inventory replaced the persistent process or did not test an expired SDK callback")
	}
	if len(snapshot.Inventory.Sites) != 3 || len(snapshot.Inventory.Downloaders) != 2 {
		t.Fatal("disabled references were filtered out")
	}
	for i := 1; i < len(snapshot.Inventory.Sites); i++ {
		if snapshot.Inventory.Sites[i-1].SourceID >= snapshot.Inventory.Sites[i].SourceID {
			t.Fatal("site SourceIDs are not unique and sorted")
		}
	}
	selected := ownedBrushInventorySite(t, snapshot.Inventory, privateIdentity.SourceID)
	if selected.Enabled || selected.Public || selected.URL == nil || *selected.URL != siteURL || selected.Domain == nil || *selected.Domain != "127.0.0.1" {
		t.Fatal("disabled identity or safe canonical origin/domain was changed")
	}
	stats := selected.LocalStatistics
	if stats.Status != "available" || stats.Ratio == nil || *stats.Ratio != ratio || stats.Upload == nil || *stats.Upload != float64(up) || stats.Download != nil || stats.UpdatedDay == nil || *stats.UpdatedDay != "2026-10-24" || stats.UpdatedTime == nil || *stats.UpdatedTime != "08:09:10" {
		t.Fatal("real native local snapshot lost its nullable metrics/date/time")
	}
	selectedPublic := ownedBrushInventorySite(t, snapshot.Inventory, publicIdentity.SourceID)
	if !selectedPublic.Public || !selectedPublic.Enabled || selectedPublic.LocalStatistics.Status != "missing" {
		t.Fatal("public presence and enabled were conflated with task validity or statistics")
	}
	incomplete := ownedBrushInventorySite(t, snapshot.Inventory, placeholderIdentity.SourceID)
	if incomplete.URL != nil || incomplete.Domain != nil || incomplete.Enabled || incomplete.LocalStatistics.Status != "missing" {
		t.Fatal("incomplete disabled reference leaked its original URL or was invented as connected")
	}
	if snapshot.Inventory.Downloaders[0].ID != "owned-qb-a" || !snapshot.Inventory.Downloaders[0].Enabled || snapshot.Inventory.Downloaders[1].ID != "owned-qb-b" || snapshot.Inventory.Downloaders[1].Enabled || snapshot.Inventory.Downloaders[0].Name != snapshot.Inventory.Downloaders[1].Name {
		t.Fatal("downloader ID/order, nil-enabled semantics or duplicate names were altered")
	}
	raw, err := json.Marshal(snapshot.Inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"OWNED_COOKIE_SECRET", "OWNED_SITE_KEY_SECRET", "OWNED_USER_AGENT_SECRET", "OWNED_DOMAIN_SECRET", "OWNED_TOKEN_SECRET", "OWNED_RSS_SECRET", "OWNED_USERINFO_SECRET", "OWNED_PATH_SECRET", "OWNED_QUERY_SECRET", "OWNED_FRAGMENT_SECRET", "OWNED_DOWNLOADER_USER_SECRET", "OWNED_DOWNLOADER_PASSWORD_SECRET", "OWNED_DOWNLOADER_KEY_SECRET", "OWNED_OTHER_PASSWORD_SECRET", "OWNED_PROXY_PASSWORD_SECRET", "OWNED_STATS_USER_SECRET", "OWNED_STATS_ID_SECRET", "OWNED_ACCOUNT_ERROR_SECRET", "/owned/private/"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("private configuration/account field escaped the inventory projection")
		}
	}
	settingsAfter, _ := json.Marshal(a.Settings())
	if !bytes.Equal(settingsBefore, settingsAfter) || !bytes.Equal(messagesBefore, a.systemMessages.get("system")) {
		t.Fatal("inventory changed settings or created a system message")
	}
	ownedBrushInventoryNoWrites(t, a, before)
	if requests.Load() != 0 {
		t.Fatal("inventory contacted a configured owned site/downloader/proxy")
	}

	// A successful observation may contain a NULL ratio. It must remain NULL,
	// not become zero, infinity or unavailable merely because it is nullable.
	observation = ownedAccountObservation(t, a, private, "2026-10-26", "12:13:14", "", &up)
	ownedRecordAccount(t, a, private, observation)
	before = ownedBrushInventoryChanges(t, a)
	nullRatio := ownedBrushInventoryRead(t, object)
	stats = ownedBrushInventorySite(t, nullRatio.Inventory, privateIdentity.SourceID).LocalStatistics
	if stats.Status != "available" || stats.Ratio != nil || stats.Download != nil || stats.Upload == nil {
		t.Fatal("NULL source metrics were normalized into numeric claims")
	}
	ownedBrushInventoryNoWrites(t, a, before)

	// Ordinary core edits preserve the native identity. Changing display name,
	// sort order or enabled is not deletion and does not allocate a new ID.
	config = a.Settings()
	for i := range config.Sites {
		if config.Sites[i].ID == private.ID {
			config.Sites[i].Name, config.Sites[i].Enabled = "Owned renamed site", true
		}
	}
	config.Sites[0], config.Sites[2] = config.Sites[2], config.Sites[0]
	ownedBrushInventorySave(t, a, config)
	before = ownedBrushInventoryChanges(t, a)
	renamed := ownedBrushInventoryRead(t, object)
	selected = ownedBrushInventorySite(t, renamed.Inventory, privateIdentity.SourceID)
	if selected.Name != "Owned renamed site" || !selected.Enabled || identityRequire(t, a, private.ID).SourceID != privateIdentity.SourceID {
		t.Fatal("ordinary edit changed stable identity")
	}
	ownedBrushInventoryNoWrites(t, a, before)

	// Real public Clone copies the acquired executable. Shared references remain
	// shared, but this is a separate process and installation namespace, not a
	// fabricated instance flag. This resource fixture is not a full Brush test.
	cloneResponse := callTest(t, a.Handler(), "POST", "/api/plugins/"+object.installation.PluginID+"/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, cloneResponse.Code, cloneResponse.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load(object.installation.PluginID + "twin")
	if !ok || value == object {
		t.Fatal("actual public clone did not create an independent object")
	}
	clone := value.(*originalPluginInstance)
	before = ownedBrushInventoryChanges(t, a)
	cloned := ownedBrushInventoryRead(t, clone)
	if cloned.PID == renamed.PID || !reflect.DeepEqual(cloned.Inventory, renamed.Inventory) {
		t.Fatal("clone did not read the same core inventory through its own SDK process")
	}
	ownedBrushInventoryNoWrites(t, a, before)
	if requests.Load() != 0 {
		t.Fatal("clone inventory contacted an owned resource endpoint")
	}

	// Corrupt the owned local snapshot, then restore its exact encrypted bytes.
	// The production scope must reject the read rather than return a partial or
	// empty success. The following successful read rules out a dead SDK/session
	// being mistaken for proof of a data-integrity rejection.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	callback, closePhase := object.originalBaseSessionHost(ctx, "snapshot", nil)
	defer closePhase()
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_stats_latest' AND id=?`, private.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='site_stats_latest' AND id=?`, []byte("owned-invalid-snapshot"), private.ID); err != nil {
		t.Fatal(err)
	}
	before = ownedBrushInventoryChanges(t, a)
	if value, err := callback(ctx, identity, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`)); err == nil || len(value) != 0 {
		t.Fatal("corrupt local statistics became a successful/partial inventory")
	}
	ownedBrushInventoryNoWrites(t, a, before)
	if _, err := a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='site_stats_latest' AND id=?`, sealed, private.ID); err != nil {
		t.Fatal(err)
	}
	if value, err := callback(ctx, identity, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`)); err != nil || len(value) == 0 {
		t.Fatal("restored owned snapshot did not become readable", err)
	}
	var proof string
	if err := a.Store.DB.QueryRow(`SELECT proof FROM native_site_identities WHERE site_id=?`, private.ID).Scan(&proof); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE native_site_identities SET proof='owned-invalid-proof' WHERE site_id=?`, private.ID); err != nil {
		t.Fatal(err)
	}
	before = ownedBrushInventoryChanges(t, a)
	if value, err := callback(ctx, identity, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`)); err == nil || len(value) != 0 {
		t.Fatal("corrupt stable identity was accepted or silently omitted")
	}
	ownedBrushInventoryNoWrites(t, a, before)
	if _, err := a.Store.DB.Exec(`UPDATE native_site_identities SET proof=? WHERE site_id=?`, proof, private.ID); err != nil {
		t.Fatal(err)
	}
	if value, err := callback(ctx, identity, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`)); err != nil || len(value) == 0 {
		t.Fatal("restored native identity did not become readable", err)
	}
	if requests.Load() != 0 {
		t.Fatal("local read failure triggered a network fallback")
	}
}

func TestOriginalBrushInventoryActualSDKCapabilityDenied(t *testing.T) {
	a, _, object := ownedBrushInventorySDKFixture(t, false)
	if len(object.manifest.Capabilities) != 0 {
		t.Fatal("denied fixture unexpectedly declares the capability")
	}
	before := ownedBrushInventoryChanges(t, a)
	var result ownedBrushInventorySDKSnapshot
	if err := object.readSession(context.Background(), "snapshot", &result); err == nil {
		t.Fatal("real undeclared SDK resource call was accepted")
	}
	if result.PID != 0 || result.Inventory.Sites != nil {
		t.Fatal("denied SDK invocation returned a fake successful inventory")
	}
	ownedBrushInventoryNoWrites(t, a, before)
}

func TestOriginalBrushInventoryActualScopeIdentityCancellationAndRevocation(t *testing.T) {
	a, _, object := ownedBrushInventorySDKFixture(t, true)
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	before := ownedBrushInventoryChanges(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	callback, closePhase := object.originalBaseSessionHost(ctx, "snapshot", nil)
	defer closePhase()
	// A real Installation query before and after the helper makes this an
	// actual MaxOpenConns(1) regression check, not a validator returning nil.
	if raw, err := callback(ctx, identity, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`)); err != nil || len(raw) == 0 {
		t.Fatal("actual scope or single-connection transaction failed", err)
	}
	for _, raw := range []string{`null`, `[]`, `"text"`, `{"refresh":true}`, `{"plugin_id":"other"}`, `{"url":"https://other.example"}`, `{}{}`, strings.Repeat(" ", (128<<10)+1) + `{}`} {
		if result, err := callback(ctx, identity, plugins.BrushInventoryReadCapability, json.RawMessage(raw)); err == nil || len(result) != 0 {
			t.Fatal("resource accepted non-empty or malformed caller routing/input")
		}
	}
	for _, field := range []string{"plugin", "instance", "version"} {
		wrong := identity
		switch field {
		case "plugin":
			wrong.PluginID += "Other"
		case "instance":
			wrong.InstanceID += "Other"
		case "version":
			wrong.Version = "999"
		}
		if result, err := callback(ctx, wrong, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`)); err == nil || len(result) != 0 {
			t.Fatal("real resource scope accepted the wrong identity field", field)
		}
	}
	closedCtx, stop := context.WithCancel(context.Background())
	stoppedCallback, stopPhase := object.originalBaseSessionHost(closedCtx, "snapshot", nil)
	stop()
	if result, err := stoppedCallback(closedCtx, identity, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) || len(result) != 0 {
		t.Fatal("cancelled real resource scope returned inventory", err)
	}
	stopPhase()

	// Holding the real publication lock makes successful completion impossible
	// before cancellation/revocation. No fake scope validator or timed sleep is
	// used. Either admission observes cancellation early or it rechecks after
	// waiting for the actual lock; both must return no inventory.
	for _, revoke := range []bool{false, true} {
		waitCtx, cancelWait := context.WithCancel(context.Background())
		waiting, closeWaiting := object.originalBaseSessionHost(waitCtx, "snapshot", nil)
		type outcome struct {
			value json.RawMessage
			err   error
		}
		result := make(chan outcome, 1)
		started := make(chan struct{})
		a.pluginMu.Lock()
		go func() {
			close(started)
			value, err := waiting(waitCtx, identity, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`))
			result <- outcome{value, err}
		}()
		<-started
		if revoke {
			closeWaiting()
		} else {
			cancelWait()
		}
		a.pluginMu.Unlock()
		select {
		case got := <-result:
			if got.err == nil || len(got.value) != 0 {
				t.Error("cancelled/revoked scope crossed the real publication lock")
			}
		case <-time.After(5 * time.Second):
			t.Error("cancelled/revoked callback did not return after lock release")
		}
		cancelWait()
		closeWaiting()
	}
	closePhase()
	if result, err := callback(ctx, identity, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`)); err == nil || len(result) != 0 {
		t.Fatal("expired real callback retained inventory authority")
	}
	fresh, closeFresh := object.originalBaseSessionHost(ctx, "snapshot", nil)
	defer closeFresh()
	if result, err := fresh(ctx, identity, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`)); err != nil || len(result) == 0 {
		t.Fatal("new real callback did not receive fresh inventory authority", err)
	}
	ownedBrushInventoryNoWrites(t, a, before)
	for _, contract := range []string{pluginPureSDKContract, pluginBaseSDKContract} {
		if err := validatePluginArtifactContract(ownedBrushInventoryManifest(true), contract); err == nil {
			t.Fatal("non-resource artifact contract accepted inventory capability", contract)
		}
	}
}

func TestOriginalBrushInventoryCanonicalDomainProjection(t *testing.T) {
	for _, tc := range []struct{ name, kind, origin, configured, want string }{
		{"registered", "mteam", "https://api.m-team.cc", "m-team.cc", "m-team.cc"},
		{"ordinary-subdomain", "nexusphp", "https://tracker.owned.example.com", "owned.example.com", "owned.example.com"},
		{"u2-exception", "nexusphp", "https://u2.dmhy.org", "u2.dmhy.org", "u2.dmhy.org"},
		{"ecust-exception", "nexusphp", "https://pt.ecust.pp.ua", "pt.ecust.pp.ua", "pt.ecust.pp.ua"},
		{"gtkpw-exception", "nexusphp", "https://pt.gtkpw.xyz", "pt.gtkpw.xyz", "pt.gtkpw.xyz"},
		{"gtkpw-legacy-exception", "nexusphp", "https://pt.gtk.pw", "pt.gtk.pw", "pt.gtk.pw"},
		{"userinfo", "jiayuan", "http://127.0.0.1:12345", "user:OWNED_SECRET@host", "127.0.0.1"},
		{"query", "jiayuan", "http://127.0.0.1:12345", "https://host/?token=OWNED_SECRET", "127.0.0.1"},
		{"path", "jiayuan", "http://127.0.0.1:12345", "host/private/OWNED_SECRET", "127.0.0.1"},
		{"unrelated-host", "mteam", "https://api.m-team.cc", "unrelated.example.com", "m-team.cc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := SiteConfig{Config: sites.Config{Kind: tc.kind}, SiteOptions: SiteOptions{Domain: tc.configured}}
			if got := originalBrushInventoryDomain(site, tc.origin); got != tc.want {
				t.Fatalf("safe domain projection = %q, want %q", got, tc.want)
			}
		})
	}
}

// This is a core read-model selection check, not evidence of a completed source
// migration. The authenticated archive path is separately covered by existing
// TestSourceAccountLatestMatchesOriginalSQLiteWithErrorsAndDuplicates.
func TestOriginalBrushInventoryViewKeepsLatestDayErrorRow(t *testing.T) {
	a := newTestApp(t)
	site := SiteConfig{Config: sites.Config{ID: "owned-mixed", Kind: "nexusphp", Name: "Owned mixed", BaseURL: "https://owned.example.com"}}
	up := int64(7)
	good := ownedAccountObservation(t, a, site, "2026-10-24", "08:00:00", "", &up)
	failed := ownedAccountObservation(t, a, site, "2026-10-24", "09:00:00", "owned same-day failure", nil)
	nextFailed := ownedAccountObservation(t, a, site, "2026-10-25", "10:00:00", "owned later all-failed day", nil)
	view := siteAccountView{Sites: map[string]SiteConfig{site.ID: site}, Rank: map[int64]int{}, Rows: []accountObservation{
		{SiteID: site.ID, Header: sourceAccountHeader{ID: 1, Day: "2026-10-24", Time: "08:00:00", Good: true}, Native: &good},
		{SiteID: site.ID, Header: sourceAccountHeader{ID: 2, Day: "2026-10-24", Time: "09:00:00", Good: false}, Native: &failed},
		{SiteID: site.ID, Header: sourceAccountHeader{ID: 3, Day: "2026-10-25", Time: "10:00:00", Good: false}, Native: &nextFailed},
	}}
	latest := view.latest(site.ID)
	if len(latest) != 2 || latest[0].Header.ID != 2 || latest[0].Header.Good {
		t.Fatal("core selection lost the later error row on the most recent good day")
	}
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	data, err := a.siteAccountStatisticTx(context.Background(), tx, view, latest[0])
	if err != nil || data.Ratio != nil || data.Upload != nil || data.Download != nil || data.UpdatedDay != "2026-10-24" || data.UpdatedTime != "09:00:00" || data.Error == "" {
		t.Fatal("core first-row projection was silently replaced by older successful numbers", err)
	}
}
