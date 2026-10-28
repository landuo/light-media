package app

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestStandardSubscriptionResetActualHTTPAndSDKSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-subscription-events/reset-oracle.json")
	var source struct {
		After  map[string]json.RawMessage
		Events []struct {
			Data struct {
				Fields []string
				Scene  string
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || len(source.Events) != 1 {
		t.Fatal("fixed reset source oracle", err)
	}
	a := subscriptionTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	ctx := context.Background()
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedEventFixtureID+"/clone", `{"suffix":"Reset"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	clone := ownedEventFixtureID + "reset"
	setOwnedEventFixtureConfig(t, a, cookie, clone, true, false)
	input := subscriptionRequest(t)
	input.TotalEpisodes = 5
	input.State = "paused"
	input.Keyword = "retained"
	sub, err := a.saveSubscription(ctx, "", input)
	if err != nil {
		t.Fatal(err)
	}
	priority, lack := 70, 2
	sub.CurrentPriority = &priority
	sub.EpisodePriority = map[string]int{"1": 70}
	sub.LackEpisode = &lack
	sub.SourceNote = []int{1, 2, 3}
	if err = a.Store.Put(ctx, "subscription", sub.ID, sub); err != nil {
		t.Fatal(err)
	}
	job, err := a.enqueue(ctx, "subscription", map[string]any{"id": sub.ID, "revision": sub.Revision}, "owned-reset")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`INSERT INTO reservations VALUES(?,?,?,?)`, mediaKey(sub.Media), sub.Season, 1, "owned-reset-inflight"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_reset_failure BEFORE UPDATE ON records WHEN NEW.kind='subscription' BEGIN SELECT RAISE(ABORT,'owned reset rollback'); END`); err != nil {
		t.Fatal(err)
	}
	path := "/api/subscriptions/" + sub.ID + "/reset"
	if response := callTest(t, a.Handler(), "POST", path, "{}", cookie, ""); response.Code == 200 {
		t.Fatal("reset SQL failure accepted")
	}
	requireBusinessEventCount(t, a, clone, "subscribe.modified", 0)
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_reset_failure`); err != nil {
		t.Fatal(err)
	}
	r = callTest(t, a.Handler(), "POST", path, "{}", cookie, "")
	if r.Code != 200 {
		t.Fatal("reset failed", r.Code, r.Body.String())
	}
	for _, id := range []string{ownedEventFixtureID, clone} {
		events := requireBusinessEventCount(t, a, id, "subscribe.modified", 1)
		var got struct {
			Scene   string
			Fields  []string
			Current map[string]json.RawMessage `json:"subscribe_info"`
		}
		if json.Unmarshal(events[0].Data, &got) != nil || got.Scene != source.Events[0].Data.Scene || !reflect.DeepEqual(got.Fields, source.Events[0].Data.Fields) {
			t.Fatal("actual reset source scene/fields", got.Scene, got.Fields)
		}
		for _, key := range []string{"note", "lack_episode", "current_priority", "episode_priority", "manual_total_episode", "state", "keyword"} {
			if !originalBaseJSONEqual(got.Current[key], source.After[key]) {
				t.Fatal("actual reset source field", key, string(got.Current[key]), string(source.After[key]))
			}
		}
	}
	var saved Subscription
	if err = a.Store.Get(ctx, "subscription", sub.ID, &saved); err != nil || saved.Revision != sub.Revision+1 || saved.State != "active" {
		t.Fatal("actual reset native revision/state", err)
	}
	var state string
	var generation, count int
	if err = a.Store.DB.QueryRow(`SELECT state,generation FROM jobs WHERE id=?`, job.ID).Scan(&state, &generation); err != nil || state != "cancelled" || generation != 1 {
		t.Fatal("reset left old native execution valid", state, generation, err)
	}
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM reservations WHERE intent_id='owned-reset-inflight'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("reset changed accepted/inflight scope", count, err)
	}
}
