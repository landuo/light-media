package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins/autosignin"
)

func siteDeletedTestEvents(t *testing.T, a *App) []DomainEvent {
	t.Helper()
	page, err := a.domainEvents(context.Background(), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var events []DomainEvent
	for _, event := range page.Events {
		if event.Type == "site.deleted" {
			events = append(events, event)
		}
	}
	return events
}

func TestNativeSiteDeletedInstalledProducerSourceIdentity(t *testing.T) {
	root := "../../testdata/plugins/native-site-deleted/reference/"
	raw, err := os.ReadFile(root + "source-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "73ab57c55775b188abd9f16f685ce4173807e45fd133ca0a82b0ad81a0bb7795" {
		t.Fatal("installed producer source inventory changed")
	}
	var m struct {
		Source string `json:"source_sha256"`
		Funcs  map[string]struct {
			SHA string `json:"sha256"`
		} `json:"functions"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Source != "c352a148fd418f7eb6c91f490d8db4319c91c6ac2d68b920b35b525d1d8372a8" || len(m.Funcs) != 2 {
		t.Fatal("wrong actual installed SiteDeleted producers")
	}
	for name, expected := range map[string]string{"delete_site": "4797d1f2ce47bd273b7efd84aece4a7ff7fee4eabc500e9a70f7f81e39585e0f", "reset": "852a833f2163f6381bf6eb6463671467400798a4f9773b234ac72a08b0e740a9"} {
		raw, err := os.ReadFile(root + name + ".py")
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if m.Funcs[name].SHA != expected || hex.EncodeToString(sum[:]) != expected {
			t.Fatal("actual producer source changed", name)
		}
	}
}

func TestNativeSiteDeletedActualHTTPStableNumericEventAndColdSourceRead(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	cookie := setupTestAdmin(t, a)
	site := identityTestSite("owned-private-name", "private-origin.example.com")
	site.Cookie = "owned-private-cookie"
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", site) != 200 {
		t.Fatal("create")
	}
	old := a.Settings().Sites[0]
	identity := identityRequire(t, a, old.ID)
	if identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+old.ID, nil) != 200 {
		t.Fatal("delete")
	}
	events := siteDeletedTestEvents(t, a)
	if len(events) != 1 || events[0].SiteID != old.ID || events[0].SourceSiteID != identity.SourceID || events[0].OccurredAt != identityRequire(t, a, old.ID).DeletedAt {
		t.Fatal("original retired identity not captured", events)
	}
	event := events[0]
	raw, _ := json.Marshal(event)
	for _, secret := range []string{site.Name, site.BaseURL, site.Cookie} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("public event disclosed private source metadata")
		}
	}
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", site) != 200 {
		t.Fatal("readd")
	}
	replacement := identityRequire(t, a, a.Settings().Sites[0].ID)
	if replacement.SourceID <= identity.SourceID || replacement.SiteID == identity.SiteID {
		t.Fatal("readded site reused retired identity")
	}
	data, err := a.Store.siteDeletedSourceData(context.Background(), a.Store.DB, event)
	if err != nil || !reflect.DeepEqual(data, map[string]any{"site_id": identity.publicSourceID()}) {
		t.Fatal("old deletion resolved new site", data, err)
	}
	for _, all := range []bool{false, true} {
		engine := autosignin.New(autosignin.Inventory{}, time.Now)
		selected := []any{identity.publicSourceID(), replacement.publicSourceID()}
		want := []any{replacement.publicSourceID()}
		if all {
			selected, want = []any{"all"}, []any{"all"}
		}
		engine.Enabled = true
		engine.Config = autosignin.Object{"enabled": true, "sign_sites": selected, "login_sites": selected}
		if err := engine.SiteDeleted(data["site_id"]); err != nil || !reflect.DeepEqual(engine.SignSites, want) || !reflect.DeepEqual(engine.LoginSites, want) || engine.Enabled != true {
			t.Fatal("native numeric source data broke original consumer", engine.SignSites, engine.LoginSites, err)
		}
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if after := siteDeletedTestEvents(t, a); !reflect.DeepEqual(after, events) {
		t.Fatal("cold bootstrap republished or rewrote source deletion", after)
	}
	if data, err = a.Store.siteDeletedSourceData(context.Background(), a.Store.DB, event); err != nil || data["site_id"] != identity.publicSourceID() {
		t.Fatal("cold historical fact read failed", data, err)
	}
}

func TestNativeSiteDeletedActualProducersRollbackIdentitySettingsCacheAndEvent(t *testing.T) {
	for _, management := range []bool{true, false} {
		name := "generic-settings"
		if management {
			name = "management-http"
		}
		t.Run(name, func(t *testing.T) {
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", identityTestSite("owned", "rollback.example.com")) != 200 {
				t.Fatal("create")
			}
			site := a.Settings().Sites[0]
			before := identityRequire(t, a, site.ID)
			if _, err := a.Store.DB.Exec(`INSERT INTO records VALUES('site_connection',?,'{}',?)`, site.ID, now()); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_owned_site_delete BEFORE INSERT ON domain_events WHEN json_extract(NEW.payload,'$.event_type')='site.deleted' BEGIN SELECT RAISE(ABORT,'owned delete event failure'); END`); err != nil {
				t.Fatal(err)
			}
			remove := func() int {
				if management {
					return identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+site.ID, nil)
				}
				config := a.Settings()
				config.Sites = nil
				return identityHTTP(t, a, cookie, "PUT", "/api/settings", config)
			}
			if code := remove(); code == 200 {
				t.Fatal("failed event committed settings deletion")
			}
			if len(a.Settings().Sites) != 1 || identityRequire(t, a, site.ID) != before || len(siteDeletedTestEvents(t, a)) != 0 || identityCount(t, a, "native_site_deletions") != 0 {
				t.Fatal("settings/map/event did not roll back together")
			}
			var n int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='site_connection' AND id=?`, site.ID).Scan(&n); err != nil || n != 1 {
				t.Fatal("failed delete removed cache", n, err)
			}
			if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_owned_site_delete`); err != nil {
				t.Fatal(err)
			}
			if remove() != 200 || len(a.Settings().Sites) != 0 || identityRequire(t, a, site.ID).State != "deleted" || len(siteDeletedTestEvents(t, a)) != 1 || identityCount(t, a, "native_site_deletions") != 1 {
				t.Fatal("retry did not commit one complete deletion")
			}
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='site_connection' AND id=?`, site.ID).Scan(&n); err != nil || n != 0 {
				t.Fatal("committed delete kept cache", n, err)
			}
		})
	}
}

func TestNativeSiteDeletedActualEventCollectionAuthenticatesTombstoneBeforeCursor(t *testing.T) {
	for _, corruption := range []string{"none", "proof", "numeric-id", "payload", "missing-fact", "sealed-fact"} {
		t.Run(corruption, func(t *testing.T) {
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", identityTestSite("owned", "event.example.com")) != 200 {
				t.Fatal("create")
			}
			site := a.Settings().Sites[0]
			p := a.pluginStore
			installStoredPlugin(t, p, "consumer")
			if _, _, err := p.SaveConfigAndQueue(context.Background(), "consumer", 0, map[string]any{"enabled": true}); err != nil {
				t.Fatal(err)
			}
			run, err := p.ClaimRun(context.Background())
			if err != nil || run.InstanceID != "consumer" {
				t.Fatal(run.InstanceID, err)
			}
			if err = p.configureEventSubscription(context.Background(), run, "refresh", []string{"site.deleted"}, 0, true); err != nil {
				t.Fatal(err)
			}
			if err = finishClaimedFixtureRun(context.Background(), p, run, "succeeded", "", nil); err != nil {
				t.Fatal(err)
			}
			var before int64
			if err = a.Store.DB.QueryRow(`SELECT cursor FROM plugin_event_subscriptions WHERE instance_id='consumer'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+site.ID, nil) != 200 {
				t.Fatal("delete")
			}
			event := siteDeletedTestEvents(t, a)[0]
			switch corruption {
			case "proof":
				_, err = a.Store.DB.Exec(`UPDATE native_site_identities SET proof='corrupted' WHERE site_id=?`, site.ID)
			case "numeric-id":
				_, err = a.Store.DB.Exec(`UPDATE native_site_identities SET source_id=source_id+100 WHERE site_id=?`, site.ID)
			case "payload":
				event.SourceSiteID++
				event.Sequence = 0
				raw, _ := json.Marshal(event)
				_, err = a.Store.DB.Exec(`UPDATE domain_events SET payload=? WHERE event_id=?`, raw, event.EventID)
			case "missing-fact":
				_, err = a.Store.DB.Exec(`DELETE FROM native_site_deletions WHERE event_id=?`, event.EventID)
			case "sealed-fact":
				_, err = a.Store.DB.Exec(`UPDATE native_site_deletions SET sealed=zeroblob(128) WHERE event_id=?`, event.EventID)
			}
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				err = a.dispatchPluginEvents(context.Background(), time.Now())
				if corruption == "none" && err != nil || corruption != "none" && err == nil {
					t.Fatal("wrong authenticated collection result", corruption, err)
				}
			}
			var after int64
			if err = a.Store.DB.QueryRow(`SELECT cursor FROM plugin_event_subscriptions WHERE instance_id='consumer'`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			want := 0
			if corruption == "none" {
				want = 1
			}
			if pluginEventCount(t, a, "plugin_event_deliveries") != want || pluginEventCount(t, a, "plugin_event_batches") != want || corruption != "none" && before != after {
				t.Fatal("collection omitted authentication or advanced failed cursor", corruption, before, after)
			}
		})
	}
}

func TestNativeSiteDeletedEventCannotBeCreatedFromPublicReferenceOrOtherType(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.saveManagedSite(context.Background(), identityTestSite("active", "active.example.com")); err != nil {
		t.Fatal(err)
	}
	active := identityRequire(t, a, a.Settings().Sites[0].ID)
	fake := active
	fake.State, fake.DeletedAt = "deleted", now()
	event := nativeSiteDeletedEvent(fake)
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = recordDomainEventTx(context.Background(), tx, event)
	_ = tx.Rollback()
	if err == nil || len(siteDeletedTestEvents(t, a)) != 0 {
		t.Fatal("public reference created unauthenticated deletion")
	}
	// A pre-producer tombstone is a real old deletion, but has no new event
	// acceptance receipt. Neither reading it nor a public reference may invent one.
	tx, err = a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Store.writeNativeSiteIdentityTx(context.Background(), tx, fake); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.siteDeletedSourceData(context.Background(), a.Store.DB, event); err == nil {
		t.Fatal("legacy tombstone falsely claimed a producer acceptance")
	}
	tx, err = a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = recordDomainEventTx(context.Background(), tx, event)
	_ = tx.Rollback()
	if err == nil {
		t.Fatal("legacy tombstone allowed fabrication of new event")
	}
	for _, kind := range []string{"site.refreshed", "notice.message", "downloadfile.deleted", "subscription.added"} {
		other := event
		other.Type = kind
		if _, err := validateDomainEvent(other); err == nil {
			t.Fatal("numeric source identity accepted under another event type", kind)
		}
	}
	for _, edit := range []func(*DomainEvent){
		func(e *DomainEvent) { e.SourceSiteID = 0 },
		func(e *DomainEvent) { e.SourceSiteID = -1 },
		func(e *DomainEvent) { e.SiteID = "*" },
		func(e *DomainEvent) { e.MediaID = "movie:1" },
		func(e *DomainEvent) { e.OccurredAt = "" },
		func(e *DomainEvent) { e.NoticeID = "pnm_" + strings.Repeat("a", 64) },
	} {
		invalid := event
		edit(&invalid)
		if _, err := validateDomainEvent(invalid); err == nil {
			t.Fatal("mixed deletion contract accepted", invalid)
		}
	}
}
