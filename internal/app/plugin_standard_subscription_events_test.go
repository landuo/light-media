package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"testing"
)

func TestStandardSubscriptionModifiedFieldsPinnedSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-subscription-events/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Before, After json.RawMessage
			Fields        []string
		}
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, test := range oracle.Cases {
		value, err := standardSubscriptionModifiedPayload("owned-sub", test.Before, test.After, "update")
		var payload struct{ Fields []string }
		if err != nil || json.Unmarshal(value, &payload) != nil || !reflect.DeepEqual(payload.Fields, test.Fields) {
			t.Fatal("changed fields differ from actual fixed Python method", string(value), test.Fields, err)
		}
	}
}

func TestStandardSubscriptionMutationActualHTTPAndSDKCommitRollbackIsolation(t *testing.T) {
	a := subscriptionTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedEventFixtureID+"/clone", `{"suffix":"Sub"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	clone := ownedEventFixtureID + "sub"
	setOwnedEventFixtureConfig(t, a, cookie, clone, true, false)
	ctx := context.Background()
	request := subscriptionRequest(t)
	request.TotalEpisodes = 5
	request.CustomWords = subscriptionWordsRaw(t, "owned old words")
	sub, err := a.saveSubscription(ctx, "", request)
	if err != nil {
		t.Fatal(err)
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.modified", 0)
	request.State = "paused"
	request.Revision = sub.Revision
	request.Keyword = "owned new keyword"
	request.TotalEpisodes = 6
	request.CustomWords = subscriptionWordsRaw(t, "owned new words")
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_sub_event_rollback BEFORE UPDATE ON records WHEN NEW.kind='subscription' BEGIN SELECT RAISE(ABORT,'owned rollback'); END`); err != nil {
		t.Fatal(err)
	}
	response = callTest(t, a.Handler(), "PUT", "/api/subscriptions/"+sub.ID, subscriptionWordsRequestBody(t, request), cookie, "")
	if response.Code == 200 {
		t.Fatal("failed owning SQL mutation was accepted")
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.modified", 0)
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_sub_event_rollback`); err != nil {
		t.Fatal(err)
	}
	response = callTest(t, a.Handler(), "PUT", "/api/subscriptions/"+sub.ID, subscriptionWordsRequestBody(t, request), cookie, "")
	if response.Code != 200 {
		t.Fatal("actual subscription editor update failed", response.Body.String())
	}
	sourceEvents := requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.modified", 1)
	cloneEvents := requireBusinessEventCount(t, a, clone, "subscribe.modified", 1)
	if !originalBaseJSONEqual(sourceEvents[0].Data, cloneEvents[0].Data) {
		t.Fatal("source/clone subscribers received different committed snapshots")
	}
	var modified struct {
		ID      string                     `json:"subscribe_id"`
		Old     map[string]json.RawMessage `json:"old_subscribe_info"`
		Current map[string]json.RawMessage `json:"subscribe_info"`
		Scene   string
		Fields  []string
	}
	if json.Unmarshal(sourceEvents[0].Data, &modified) != nil || modified.ID != sub.ID || modified.Scene != "update" {
		t.Fatal("source event envelope incorrect", string(sourceEvents[0].Data))
	}
	oracleRaw, err := os.ReadFile("../../testdata/standard-subscription-events/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var columns struct{ Columns []string }
	if json.Unmarshal(oracleRaw, &columns) != nil {
		t.Fatal("invalid pinned Subscribe model inventory")
	}
	for _, snapshot := range []map[string]json.RawMessage{modified.Old, modified.Current} {
		keys := []string{}
		for key := range snapshot {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		if !reflect.DeepEqual(keys, columns.Columns) {
			t.Fatal("event snapshot added internal fields or omitted original model columns", keys, columns.Columns)
		}
	}
	for key, want := range map[string][2]string{"state": {`"N"`, `"S"`}, "custom_words": {`"owned old words"`, `"owned new words"`}, "total_episode": {`5`, `6`}, "keyword": {`""`, `"owned new keyword"`}} {
		if string(modified.Old[key]) != want[0] || string(modified.Current[key]) != want[1] {
			t.Fatal("event did not freeze old/new original fields in the owning transaction", key, string(modified.Old[key]), string(modified.Current[key]))
		}
	}
	var saved Subscription
	if err = a.Store.Get(ctx, "subscription", sub.ID, &saved); err != nil || saved.State != "paused" || saved.TotalEpisodes != 6 {
		t.Fatal("event did not follow actual committed update", err)
	}
	// A callback failure cannot roll back an already successful core mutation,
	// and another independent subscriber must still receive it.
	setOwnedEventFixtureConfig(t, a, cookie, ownedEventFixtureID, true, true)
	request.Revision = saved.Revision
	request.Keyword = "after callback failure"
	response = callTest(t, a.Handler(), "PUT", "/api/subscriptions/"+sub.ID, subscriptionWordsRequestBody(t, request), cookie, "")
	if response.Code != 200 {
		t.Fatal("plugin callback failure changed committed core response")
	}
	requireBusinessEventCount(t, a, clone, "subscribe.modified", 2)
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_sub_delete_rollback BEFORE UPDATE ON records WHEN NEW.kind='subscription' BEGIN SELECT RAISE(ABORT,'owned rollback'); END`); err != nil {
		t.Fatal(err)
	}
	response = callTest(t, a.Handler(), "DELETE", "/api/subscriptions/"+sub.ID, "", cookie, "")
	if response.Code == 200 {
		t.Fatal("failed logical deletion accepted")
	}
	requireBusinessEventCount(t, a, clone, "subscribe.deleted", 0)
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_sub_delete_rollback`); err != nil {
		t.Fatal(err)
	}
	response = callTest(t, a.Handler(), "DELETE", "/api/subscriptions/"+sub.ID, "", cookie, "")
	if response.Code != 200 {
		t.Fatal("actual logical delete failed", response.Body.String())
	}
	response = callTest(t, a.Handler(), "GET", "/api/subscriptions", "", cookie, "")
	var list struct {
		Data struct {
			Items []json.RawMessage
			Total int
		}
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &list) != nil ||
		list.Data.Items == nil || len(list.Data.Items) != 0 || list.Data.Total != 0 {
		t.Fatal("deleted subscription remained in the public list", response.Body.String())
	}
	if err = a.Store.Get(ctx, "subscription", sub.ID, &saved); err != nil || saved.State != "cancelled" {
		t.Fatal("logical deletion lost the persisted cancellation", saved.State, err)
	}
	deleted := requireBusinessEventCount(t, a, clone, "subscribe.deleted", 1)
	var deletion struct {
		ID   string                     `json:"subscribe_id"`
		Info map[string]json.RawMessage `json:"subscribe_info"`
	}
	if json.Unmarshal(deleted[0].Data, &deletion) != nil || deletion.ID != sub.ID || string(deletion.Info["keyword"]) != `"after callback failure"` || string(deletion.Info["state"]) != `"S"` {
		t.Fatal("delete did not carry the snapshot before logical removal", string(deleted[0].Data))
	}
	response = callTest(t, a.Handler(), "DELETE", "/api/subscriptions/"+sub.ID, "", cookie, "")
	if response.Code != 200 {
		t.Fatal("existing idempotent cancel contract changed")
	}
	requireBusinessEventCount(t, a, clone, "subscribe.deleted", 1)
}

func TestStandardSubscriptionStatusActualHTTPPreservesFieldsAndMissingReferences(t *testing.T) {
	a := subscriptionTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	ctx := context.Background()
	in := subscriptionRequest(t)
	in.Keyword = "owned retained keyword"
	in.CustomWords = subscriptionWordsRaw(t, "owned retained words")
	in.TotalEpisodes = 5
	sub, err := a.saveSubscription(ctx, "", in)
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.subscriptionSourceProjection(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	route := "/api/subscriptions/" + sub.ID + "/status"
	status := func(state string, revision int) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"state": state, "revision": revision})
		return callTest(t, a.Handler(), "PUT", route, string(body), cookie, "")
	}
	for _, state := range []string{"invalid", "cancelled", "P"} {
		if response := status(state, sub.Revision); response.Code == 200 {
			t.Fatal("unsupported native state accepted", state)
		}
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.modified", 0)
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_status_rollback BEFORE UPDATE ON records WHEN NEW.kind='subscription' BEGIN SELECT RAISE(ABORT,'owned rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if response := status("paused", sub.Revision); response.Code == 200 {
		t.Fatal("failed status transaction accepted")
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.modified", 0)
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_status_rollback`); err != nil {
		t.Fatal(err)
	}
	if response := status("paused", sub.Revision); response.Code != 200 {
		t.Fatal("pause failed", response.Body.String())
	}
	events := requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.modified", 1)
	var got struct {
		Before json.RawMessage            `json:"old_subscribe_info"`
		After  map[string]json.RawMessage `json:"subscribe_info"`
		Scene  string
		Fields []string
	}
	if json.Unmarshal(events[0].Data, &got) != nil || got.Scene != "status" || !reflect.DeepEqual(got.Fields, []string{"state"}) || !originalBaseJSONEqual(got.Before, before) || string(got.After["custom_words"]) != `"owned retained words"` || string(got.After["keyword"]) != `"owned retained keyword"` || string(got.After["total_episode"]) != "5" {
		t.Fatal("status changed unrelated source fields", string(events[0].Data))
	}
	if response := status("active", sub.Revision); response.Code == 200 {
		t.Fatal("stale revision accepted")
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.modified", 1)
	var paused Subscription
	if err = a.Store.Get(ctx, "subscription", sub.ID, &paused); err != nil {
		t.Fatal(err)
	}
	if response := status("active", paused.Revision); response.Code != 200 {
		t.Fatal("resume failed", response.Body.String())
	}
	events = requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.modified", 2)
	if json.Unmarshal(events[1].Data, &got) != nil || got.Scene != "status" || !reflect.DeepEqual(got.Fields, []string{"state"}) || string(got.After["state"]) != `"R"` {
		t.Fatal("resume event differs from source", string(events[1].Data))
	}
	var active Subscription
	if err = a.Store.Get(ctx, "subscription", sub.ID, &active); err != nil {
		t.Fatal(err)
	}
	// Only remove this test's synthetic connection. Pausing must remain possible,
	// but acquisition must not gain authority from the event's unknown name.
	a.config.Downloaders = nil
	if _, err = a.subscriptionSourceProjection(ctx, active); err == nil {
		t.Fatal("strict core projection accepted missing downloader")
	}
	if response := status("paused", active.Revision); response.Code != 200 {
		t.Fatal("local pause lost existing stale-reference contract", response.Body.String())
	}
	events = requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.modified", 3)
	if json.Unmarshal(events[2].Data, &got) != nil || got.Scene != "status" || string(got.After["downloader"]) != "null" || !reflect.DeepEqual(got.Fields, []string{"state"}) {
		t.Fatal("missing reference invented a name or lost status event", string(events[2].Data))
	}
	if response := status("active", active.Revision+1); response.Code == 200 {
		t.Fatal("activation accepted missing downloader")
	}
	requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.modified", 3)
}
