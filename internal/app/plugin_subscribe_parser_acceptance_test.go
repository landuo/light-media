package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
)

// This is an owned loopback HTTP API and ordinary cmd/server worker, not a
// worker test-binary fixture. No external subscription/site/account is used.
func TestSubscribeGroupUnicodeRulesActualServerHTTP(t *testing.T) {
	transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
	if transferServerBinary.err != nil {
		t.Fatal(transferServerBinary.err)
	}
	a := subscriptionTestApp(t)
	ctx := context.Background()
	a.pluginCommand = func(id string) (plugins.Command, error) {
		if id != "SubscribeGroup" {
			return plugins.Command{}, fmt.Errorf("unexpected plugin %q", id)
		}
		return plugins.Command{Path: transferServerBinary.path, Args: []string{"--plugin-worker", id}}, nil
	}
	settings := a.Settings()
	settings.AllowedRoots = []string{t.TempDir()}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	media := catalog.Media{ID: 206484, MediaType: "tv", Title: "诛仙", OriginalTitle: "Jade Dynasty", Year: 2022, OriginalLanguage: "zh", OriginCountry: []string{"CN"}, GenreIDs: []int64{16}, Genres: []catalog.Genre{{ID: 16, Name: "动画"}}, CategoryMetadataKnown: true}
	if err := a.Store.Put(ctx, "cache", "media:tv:206484", map[string]any{"at": time.Now(), "data": media}); err != nil {
		t.Fatal(err)
	}
	fixture := installNoticeSourceFixtures(t, a, media, 4, 5)
	subscriptionStatsInstallSettings(t, a, "http://127.0.0.1:1", false)
	cookie := setupTestAdmin(t, a)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	request := func(method, path string, input any) json.RawMessage {
		t.Helper()
		var body []byte
		if input != nil {
			var err error
			body, err = json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("owned HTTP %s %s: status=%d body=%s err=%v", method, path, response.StatusCode, raw, err)
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	request("POST", "/api/plugins/install", map[string]any{"plugin_id": "SubscribeGroup"})
	subscribeSDKExecute(t, a, subscribeGroupClaim(t, a))
	rule := "category:国漫#resolution:1080İ#quality:WEB-RİP#effect:DOVı"
	config := subscribegroup.Configuration{Category: true, UpdateConfs: &rule}
	stored, err := a.pluginStore.Config(ctx, "SubscribeGroup")
	if err != nil {
		t.Fatal(err)
	}
	request("PUT", "/api/plugins/SubscribeGroup/config", map[string]any{"revision": stored.Revision, "config": config})
	subscribeSDKExecute(t, a, subscribeGroupClaim(t, a))
	form := request("GET", "/api/plugins/SubscribeGroup/form", nil)
	if !bytes.Contains(form, []byte("WEB-RİP")) {
		t.Fatal("saved original Unicode configuration was not read back")
	}
	subRequest := subscriptionRequest(t)
	subRequest.RootID, subRequest.DownloadPath = "", settings.AllowedRoots[0]
	subRequest.Media = media
	var before Subscription
	if err := json.Unmarshal(request("POST", "/api/subscriptions", subRequest), &before); err != nil || before.ID == "" {
		t.Fatal("subscription HTTP create", err)
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	run := subscribeGroupClaim(t, a)
	if run.Action != "event" {
		t.Fatal("missing actual creation outbox event", run.Action)
	}
	subscribeSDKExecute(t, a, run)
	updated := subscribeGroupRead(t, a, before.ID)
	// Expected fields are read from re-executed pinned original Python methods,
	// not recomputed through the implementation under test.
	var oracle struct {
		Cases []map[string]string `json:"cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugins/subscribe-group-parser-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("pinned parser reference unavailable", err)
	}
	want := map[string]string{}
	for _, c := range oracle.Cases {
		for field, input := range map[string]string{"resolution": "1080İ", "quality": "WEB-RİP", "effect": "DOVı"} {
			if c["input"] == input {
				want[field] = c[field]
			}
		}
	}
	p := updated.Policy.FilterParams
	if len(want) != 3 || p == nil || p.Resolution == nil || p.Quality == nil || p.Effect == nil || *p.Resolution != want["resolution"] || *p.Quality != want["quality"] || *p.Effect != want["effect"] || updated.Revision != before.Revision+1 {
		t.Fatalf("worker result differs from original: policy=%+v want=%v revision=%d", p, want, updated.Revision)
	}
	history, err := a.pluginStore.Data(ctx, "SubscribeGroup", "history")
	var entries []subscribegroup.History
	if err != nil || json.Unmarshal(history.Value, &entries) != nil || len(entries) != 1 {
		t.Fatal("actual transaction history", err)
	}
	var fields map[string]string
	if json.Unmarshal([]byte(entries[0].Content), &fields) != nil || !reflect.DeepEqual(fields, want) {
		t.Fatal("history patch differs from original", fields, want)
	}
	page := request("GET", "/api/plugins/SubscribeGroup/page", nil)
	if !bytes.Contains(page, []byte("二级分类自定义配置 国漫")) {
		t.Fatal("history page did not reflect actual event")
	}
	var list struct {
		Items []Subscription `json:"items"`
	}
	if json.Unmarshal(request("GET", "/api/subscriptions", nil), &list) != nil || len(list.Items) != 1 || list.Items[0].ID != before.ID || !reflect.DeepEqual(list.Items[0].Policy.FilterParams, p) {
		t.Fatal("subscription HTTP readback differs from worker transaction")
	}
	// Core subscription creation sends exactly one notification to our owned
	// loopback fixture; the SubscribeGroup event must not send another.
	if fixture.Sends.Load() != 1 {
		t.Fatal("core synthetic notification missing or repeated", fixture.Sends.Load())
	}
	route := noticeV2OnlyRoute(t, a)
	rendered, err := a.pluginStore.noticeBusinessRender(ctx, route.ID)
	if err != nil || route.BusinessV2 == nil || route.BusinessV2.Binding.Kind != "subscription_add" || route.BusinessV2.Binding.Added == nil || rendered.Snapshot.Fields["title"] != "诛仙 (2022) S04 已添加订阅" {
		t.Fatal("owned notification is not the actual subscription creation", err)
	}
	_, receipt := uiSourceOnlyAdd(t, a)
	if receipt.Saved == nil || receipt.Saved.ID != before.ID || receipt.Identity != *route.BusinessV2.Binding.Added {
		t.Fatal("notification was bound to another subscription")
	}
}
