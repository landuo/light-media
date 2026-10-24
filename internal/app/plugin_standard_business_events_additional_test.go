package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"light-media/internal/sites"
)

func TestStandardBusinessEventsActualSiteCreateUpdatePayloadAndRollback(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	site := identityTestSite("Owned source name", "owned-updated.example.com")
	site.Cookie = "owned-private-cookie"
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", site) != 200 {
		t.Fatal("actual create")
	}
	created := requireBusinessEventCount(t, a, ownedEventFixtureID, "site.updated", 1)
	if string(created[0].Data) != `{"domain":"example.com"}` {
		t.Fatal("source create payload must not become edit payload", string(created[0].Data))
	}
	saved := a.Settings().Sites[0]
	identity := identityRequire(t, a, saved.ID)
	saved.Name = "Owned updated name"
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_standard_site_update BEFORE UPDATE ON records WHEN NEW.kind='config' BEGIN SELECT RAISE(ABORT,'owned rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", saved) == 200 {
		t.Fatal("rollback accepted")
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "site.updated", 1)
	if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_standard_site_update`); err != nil {
		t.Fatal(err)
	}
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", saved) != 200 {
		t.Fatal("actual update")
	}
	updated := requireBusinessEventCount(t, a, ownedEventFixtureID, "site.updated", 2)
	var data map[string]json.RawMessage
	if json.Unmarshal(updated[1].Data, &data) != nil || len(data) != 4 || string(data["site_id"]) != fmt.Sprint(identity.SourceID) || string(data["domain"]) != `"example.com"` || string(data["name"]) != `"Owned updated name"` || string(data["site_url"]) != `"https://owned-updated.example.com/"` {
		t.Fatal("source edit fields/normalization changed", string(updated[1].Data))
	}
	if a.Settings().Sites[0].Name != saved.Name {
		t.Fatal("plugin event preceded committed settings")
	}
}

func TestStandardBusinessEventsActualBatchRefreshErrorDTOAndRetry(t *testing.T) {
	a := newTestApp(t)
	installOwnedEventFixture(t, a)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "owned source error", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	site := SiteConfig{Config: sites.Config{ID: "standard-error-site", Kind: "jiayuan", Name: "Owned", BaseURL: server.URL, Cookie: "owned-cookie"}, Enabled: true}
	if _, err := a.saveManagedSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	if _, err := a.enqueue(context.Background(), "site_refresh", map[string]string{"id": "site_refresh"}, ""); err != nil {
		t.Fatal(err)
	}
	job, err := a.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.safeRun(context.Background(), job); pluginErrorCode(err) != "SITE_STATISTICS_REFRESH_FAILED" {
		t.Fatal("error DTO changed source aggregate failure", err)
	}
	events := requireBusinessEventCount(t, a, ownedEventFixtureID, "site.refreshed", 2)
	identity := identityRequire(t, a, site.ID)
	want := map[string]bool{fmt.Sprintf(`{"site_id":%d}`, identity.SourceID): true, `{"site_id":"*"}`: true}
	for _, event := range events {
		if !want[string(event.Data)] {
			t.Fatal("unexpected source refresh payload", string(event.Data))
		}
		delete(want, string(event.Data))
	}
	if len(want) != 0 {
		t.Fatal("numeric or batch source event missing")
	}
	_ = a.safeRun(context.Background(), job)
	requireBusinessEventCount(t, a, ownedEventFixtureID, "site.refreshed", 2)
}

func TestStandardBusinessEventsActualSourceSubscriptionFactRollbackNoEarlyDispatch(t *testing.T) {
	a, in, fixture := uiSourceAddFixture(t)
	installOwnedEventFixture(t, a)
	source, err := a.buildUISubscriptionAdd(context.Background(), in, "owned-source-user")
	if err != nil {
		t.Fatal(err)
	}
	identity, job, err := a.admitUISubscriptionAdd(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER deny_standard_subscribe BEFORE INSERT ON domain_events WHEN json_extract(NEW.payload,'$.event_type')='subscription.added' BEGIN SELECT RAISE(ABORT,'owned rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if err = a.runUISubscriptionAdd(context.Background(), job, identity.ID); err == nil {
		t.Fatal("fact rollback accepted")
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.added", 0)
	if fixture.Sends.Load() != 1 {
		t.Fatal("source notice stage was bypassed")
	}
	_, receipt := uiSourceOnlyAdd(t, a)
	if receipt.Stage != "saved" {
		t.Fatal("uncommitted fact changed source stage", receipt.Stage)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER deny_standard_subscribe`); err != nil {
		t.Fatal(err)
	}
	if err = a.runUISubscriptionAdd(context.Background(), job, identity.ID); pluginErrorCodeInternal(err) != "SUBSCRIPTION_ASYNC_STATS_DEPENDENCY" {
		t.Fatal(err)
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.added", 1)
	if fixture.Sends.Load() != 1 {
		t.Fatal("fact retry resent already-completed original notice")
	}
}

func TestStandardBusinessEventsActualAppReopenDoesNotReplayCommittedFacts(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", identityTestSite("Owned", "owned-cold-events.example.com")) != 200 {
		t.Fatal("create")
	}
	site := a.Settings().Sites[0]
	if identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+site.ID, nil) != 200 {
		t.Fatal("delete")
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "site.deleted", 1)
	dir := a.Dir
	a.closeOriginalSessions()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	defer cold.closeOriginalSessions()
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	if value, ok := cold.pluginRunningInstances.Load(ownedEventFixtureID); !ok || !value.(*originalPluginInstance).external {
		t.Fatal("actual worker did not reload after app reopen")
	}
	requireBusinessEventCount(t, cold, ownedEventFixtureID, "site.deleted", 1)
}

func TestStandardBusinessEventsActualSiteUpdatedOriginalDomainRules(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	cases := []struct{ Host, ConfiguredDomain, Want string }{
		{"one.two.example.net", "example.net", "one.two.example.net"},
		{"owned.example.co.uk", "example.co.uk", "owned.example.co.uk"},
		{"example.co.uk", "example.co.uk", "co.uk"},
		{"u2.dmhy.org", "dmhy.org", "u2.dmhy.org"},
	}
	for index, tc := range cases {
		site := identityTestSite("Owned domain rule", tc.Host)
		site.Domain = tc.ConfiguredDomain
		if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", site) != 200 {
			t.Fatal("actual create", tc.Host)
		}
		events := requireBusinessEventCount(t, a, ownedEventFixtureID, "site.updated", index+1)
		var data map[string]json.RawMessage
		want, _ := json.Marshal(tc.Want)
		if json.Unmarshal(events[index].Data, &data) != nil || len(data) != 1 || string(data["domain"]) != string(want) {
			t.Fatal("configured/publicsuffix domain replaced original URL rule", tc.Host, string(events[index].Data), tc.Want)
		}
	}
}
