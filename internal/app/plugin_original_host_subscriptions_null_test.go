package app

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/sites"
)

func TestOriginalHostBusinessSubscriptionActualSDKExplicitNullAndNativeFormReset(t *testing.T) {
	a := newTestApp(t)
	cookie, worker := hostBusinessFixture(t, a)
	ctx := context.Background()
	config := a.Settings()
	config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-site", Kind: "ubits", Name: "Owned", BaseURL: "http://127.0.0.1:9", Cookie: "owned"}, Enabled: true}}
	ownedDownloaderSave(t, a, config)
	s := Subscription{ID: "owned-null-sub", Media: catalog.Media{ID: 42, MediaType: "movie", Title: "Owned", Year: 2024}, State: "paused", SourceState: "S", Revision: 1, Keyword: "keyword", SiteIDs: []string{"owned-site"}, Policy: Policy{Include: "include", Exclude: "exclude", Resolution: "1080p", Source: "WEB-DL", HDR: "HDR", FilterGroups: []string{"group"}}, Schedule: Schedule{Mode: "manual"}, CreatedAt: now()}
	if err := a.Store.Put(ctx, "subscription", s.ID, s); err != nil {
		t.Fatal(err)
	}
	fields := []string{"sites", "filter_groups", "quality", "resolution", "effect", "include", "exclude", "save_path", "keyword"}
	var result plugins.SubscriptionHostResultV1
	patch := plugins.SubscriptionHostUpdateV1{ID: s.ID, Revision: 1, Patch: plugins.SubscriptionHostPatchV1{NullFields: fields}}
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, patch, &result); err != nil || len(result.Items) != 1 {
		t.Fatal("source null patch", err)
	}
	assertNulls := func(raw json.RawMessage) {
		t.Helper()
		var row map[string]json.RawMessage
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatal(err)
		}
		for _, key := range fields {
			if string(row[key]) != "null" {
				t.Fatalf("source field %s lost explicit null: %s", key, row[key])
			}
		}
	}
	assertNulls(result.Items[0])
	var current Subscription
	if err := a.Store.Get(ctx, "subscription", s.ID, &current); err != nil || current.Revision != 2 || len(current.SourceNullFields) != 9 || current.SiteIDs != nil || current.Policy.FilterGroups != nil {
		t.Fatal("nullable values did not reach host data", err, current)
	}
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsReadCapability, plugins.SubscriptionHostRequestV1{ID: s.ID}, &result); err != nil {
		t.Fatal(err)
	}
	assertNulls(result.Items[0])
	// The same complete native request sent by the editor replaces null rule
	// columns with the user's explicit empty strings/arrays, without retaining
	// the worker-only metadata as hidden state.
	var input SubscriptionRequest
	raw, _ := json.Marshal(current)
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	input.SiteIDs = []string{}
	input.Policy.FilterGroups = []string{}
	input.Keyword = ""
	input.State = "paused"
	input.Revision = 2
	body, _ := json.Marshal(input)
	response := callTest(t, a.Handler(), "PUT", "/api/subscriptions/"+s.ID, string(body), cookie, "")
	if response.Code != 200 {
		t.Fatal("native editor save failed", response.Code, response.Body.String())
	}
	current = Subscription{}
	if err := a.Store.Get(ctx, "subscription", s.ID, &current); err != nil || len(current.SourceNullFields) != 0 || current.Revision != 3 {
		t.Fatal("native editor did not reset nullable metadata", err, current)
	}
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsReadCapability, plugins.SubscriptionHostRequestV1{ID: s.ID}, &result); err != nil {
		t.Fatal(err)
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal(result.Items[0], &row); err != nil {
		t.Fatal(err)
	}
	if string(row["sites"]) != "[]" || string(row["filter_groups"]) != "[]" || string(row["keyword"]) != `""` {
		t.Fatal("native form values were masked by old nulls", string(result.Items[0]))
	}
	// Clearing a subset must not clear another existing nullable column.
	keyword := "restored"
	patch = plugins.SubscriptionHostUpdateV1{ID: s.ID, Revision: 3, Patch: plugins.SubscriptionHostPatchV1{Keyword: &keyword, NullFields: []string{"sites"}}}
	if err := hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, patch, &result); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result.Items[0], &row); err != nil {
		t.Fatal(err)
	}
	if string(row["sites"]) != "null" || string(row["keyword"]) != `"restored"` {
		t.Fatal("mixed ordinary/null update failed", string(result.Items[0]))
	}
}
