package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/sites"
)

func hostBusinessFixture(t *testing.T, a *App) (*http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("OWNED_HOST_BUSINESS_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_HOST_BUSINESS_FIXTURE_BINARY must name the independently built acceptance worker")
	}
	cookie := setupTestAdmin(t, a)
	manifest := installOwnedResourceProcessWorker(t, a, cookie, binary)
	value, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("business acceptance worker not running")
	}
	object := value.(*originalPluginInstance)
	if !object.external || object.hostContract != pluginResourcesSDKContract {
		t.Fatal("not actual standard executable")
	}
	return cookie, object
}
func hostBusinessCall(t *testing.T, o *originalPluginInstance, method string, input, out any) error {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"method": method, "input": input})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := o.callSession(ctx, plugins.Runner{Timeout: 15 * time.Second}, "resource", raw, nil)
	if err != nil {
		return err
	}
	var response struct {
		PID   int             `json:"pid"`
		Value json.RawMessage `json:"value"`
	}
	if err = json.Unmarshal(result.Result, &response); err != nil {
		return err
	}
	if response.PID <= 0 || response.PID == os.Getpid() {
		t.Fatal("resource did not execute in independent SDK worker")
	}
	return json.Unmarshal(response.Value, out)
}
func TestOriginalHostBusinessSubscriptionActualSDKPatchPreservesFields(t *testing.T) {
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	ctx := context.Background()
	config := a.Settings()
	config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-site", Name: "Owned", Kind: "hhanclub", BaseURL: "http://127.0.0.1:9", Cookie: "OWNED_COOKIE"}, Enabled: true}}
	ownedDownloaderSave(t, a, config)
	s := Subscription{ID: "owned-sub", Media: catalog.Media{ID: 42, MediaType: "tv", Title: "Owned", Year: 2024}, Season: 2, State: "paused", SourceState: "S", Revision: 7, Keyword: "keep keyword", TotalEpisodes: 12, StartEpisode: 3, SourceNote: []int{3, 4}, Policy: Policy{Include: "keep include", FilterGroups: []string{"old"}}, Schedule: Schedule{Mode: "manual"}, CreatedAt: now()}
	if err := a.Store.Put(ctx, "subscription", s.ID, s); err != nil {
		t.Fatal(err)
	}
	var before plugins.SubscriptionHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsReadCapability, plugins.SubscriptionHostRequestV1{ID: s.ID}, &before); err != nil || len(before.Items) != 1 {
		t.Fatal("read", err)
	}
	groups := []string{"first", "second"}
	siteIDs := []string{"owned-site"}
	patch := plugins.SubscriptionHostUpdateV1{ID: s.ID, Revision: 7, Patch: plugins.SubscriptionHostPatchV1{Sites: &siteIDs, FilterGroups: &groups}}
	var after plugins.SubscriptionHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, patch, &after); err != nil || len(after.Items) != 1 {
		t.Fatal("patch", err)
	}
	var current Subscription
	if err := a.Store.Get(ctx, "subscription", s.ID, &current); err != nil {
		t.Fatal(err)
	}
	if current.Revision != 8 || current.Keyword != s.Keyword || current.Policy.Include != s.Policy.Include || current.TotalEpisodes != 12 || current.StartEpisode != 3 || current.SourceState != "S" || current.SourceNote[1] != 4 || strings.Join(current.Policy.FilterGroups, ",") != "first,second" || current.SiteIDs[0] != "owned-site" {
		t.Fatalf("patch replaced unrelated data: %+v", current)
	}
	second := s
	second.ID = "owned-sub-second"
	second.SourceState = "R"
	second.Media.ID = 43
	if err := a.Store.Put(ctx, "subscription", second.ID, second); err != nil {
		t.Fatal(err)
	}
	var grouped plugins.SubscriptionHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsReadCapability, plugins.SubscriptionHostRequestV1{State: "R,S"}, &grouped); err != nil || len(grouped.Items) != 2 {
		t.Fatal("source comma states", err)
	}
	var firstRow struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(grouped.Items[0], &firstRow); err != nil || firstRow.ID != s.ID {
		t.Fatal("source list creation order changed", err, string(grouped.Items[0]))
	}
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsReadCapability, plugins.SubscriptionHostRequestV1{State: "unknown"}, &grouped); err != nil || len(grouped.Items) != 0 {
		t.Fatal("source unknown state should select no row", err)
	}
	job, err := a.enqueue(ctx, "subscription", map[string]any{"id": s.ID, "revision": 8}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, noop := range []plugins.SubscriptionHostUpdateV1{{ID: s.ID, Revision: 8}, {ID: s.ID, Revision: 8, Patch: patch.Patch}} {
		if err := hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, noop, &after); err != nil {
			t.Fatal("source no-op patch", err)
		}
		if err := a.Store.Get(ctx, "subscription", s.ID, &current); err != nil || current.Revision != 8 {
			t.Fatal("no-op changed revision", err)
		}
		var state string
		if err := a.Store.DB.QueryRow(`SELECT state FROM jobs WHERE id=?`, job.ID).Scan(&state); err != nil || state != "queued" {
			t.Fatal("no-op cancelled current execution", state, err)
		}
	}
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, patch, &after); err == nil {
		t.Fatal("stale revision accepted")
	}
	missing := plugins.SubscriptionHostUpdateV1{ID: "missing", Patch: plugins.SubscriptionHostPatchV1{FilterGroups: &groups}}
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, missing, &after); err != nil || len(after.Items) != 0 {
		t.Fatal("source missing row must return none", err)
	}
	// The source DB operator does not contact TMDB or emit an update event.
	var events int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='subscribe.modified'`).Scan(&events); err != nil || events != 0 {
		t.Fatal("DB operator unexpectedly emitted UI event", events, err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_host_sub_rollback BEFORE UPDATE ON records WHEN NEW.kind='subscription' BEGIN SELECT RAISE(ABORT,'owned failure'); END`); err != nil {
		t.Fatal(err)
	}
	patch.Revision = 8
	groups = []string{"actual-change-must-rollback"}
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, patch, &after); err == nil {
		t.Fatal("SQL error swallowed")
	}
	if err := a.Store.Get(ctx, "subscription", s.ID, &current); err != nil || current.Revision != 8 {
		t.Fatal("failed patch changed row", err)
	}
}

func TestOriginalHostBusinessSettingsActualSDKLiveParserAndColdPersistence(t *testing.T) {
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	var result plugins.SettingsHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: "CustomIdentifiers", Value: json.RawMessage(`["Owned => Renamed"]`)}, &result); err != nil || result.Changed == nil || !*result.Changed {
		t.Fatal("word update", err)
	}
	if title := a.prepareRecognitionTitle("Owned"); title != "Renamed" {
		t.Fatal("host parser did not use new words", title)
	}
	if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: "CustomIdentifiers", Value: json.RawMessage(`["Owned => Renamed"]`)}, &result); err != nil || result.Changed != nil {
		t.Fatal("source equal set must return None", err)
	}
	for _, key := range []string{"API_TOKEN", "AI_MODELS", "WORKFLOWS"} {
		if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: key, Value: json.RawMessage(`[]`)}, &result); err == nil {
			t.Fatal("non-resource setting accepted", key)
		}
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_host_words_rollback BEFORE UPDATE ON recognition_words BEGIN SELECT RAISE(ABORT,'owned failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: "CustomIdentifiers", Value: json.RawMessage(`["Owned => Changed"]`)}, &result); err == nil {
		t.Fatal("word SQL failure swallowed")
	}
	if title := a.prepareRecognitionTitle("Owned"); title != "Renamed" {
		t.Fatal("failed commit polluted live parser", title)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_host_words_rollback`); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	cold.wg.Wait()
	v, ok := cold.pluginRunningInstances.Load("OwnedHostBusiness")
	if !ok {
		t.Fatal("worker did not cold recover")
	}
	if err = hostBusinessCall(t, v.(*originalPluginInstance), plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: "CustomIdentifiers"}, &result); err != nil || !originalBaseJSONEqual(result.Value, json.RawMessage(`["Owned => Renamed"]`)) {
		t.Fatal("cold settings mismatch", err, string(result.Value))
	}
	if title := cold.prepareRecognitionTitle("Owned"); title != "Renamed" {
		t.Fatal("cold parser mismatch", title)
	}
}

func TestOriginalHostBusinessSubscriptionCanceledScopeDoesNotWrite(t *testing.T) {
	a := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var called bool
	scope := originalBaseCallScope{validate: func(context.Context) error { called = true; return errors.New("revoked") }}
	_, err := a.originalHostUpdateSubscription(ctx, scope, plugins.SubscriptionHostUpdateV1{ID: "x"})
	if err == nil || !called {
		t.Fatal("scope guard did not reject")
	}
}
