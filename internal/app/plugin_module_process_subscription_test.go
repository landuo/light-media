package app

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"light-media/internal/catalog"
)

// Only the initial paused subscription/media are seeded synthetic test data.
// Every update/cancel/restart and observation then crosses real cmd/server
// HTTP, real owning transactions and independent actual Go SDK processes.
func verifyOwnedSubscriptionProductionEvents(t *testing.T, dir, id, repository string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	ctx := context.Background()
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	const subscriptionID = "sub_owned_plugin_events"
	media := catalog.Media{ID: 900001, MediaType: "tv", Title: "Owned ordinary event fixture", Year: 2026}
	lack := 2
	sub := Subscription{ID: subscriptionID, Media: media, Season: 1, StartEpisode: 1, TargetScope: "season", TotalEpisodes: 2, LackEpisode: &lack, ManualTotalEpisode: true, SourceNote: []int{}, SourceState: "S", State: "paused", Revision: 1, CreatedAt: now(), Schedule: Schedule{Mode: "manual"}}
	if err = seed.Store.Put(ctx, "subscription", sub.ID, sub); err != nil {
		t.Fatal(err)
	}
	if err = seed.Store.Put(ctx, "cache", "media:tv:900001", map[string]any{"at": time.Now(), "data": media}); err != nil {
		t.Fatal(err)
	}
	if err = seed.Store.Put(ctx, "cache", "season:900001:1", map[string]any{"at": time.Now(), "data": catalog.Season{SeriesID: media.ID, SeasonNumber: 1, Episodes: []catalog.Episode{}}}); err != nil {
		t.Fatal(err)
	}
	tx, err := seed.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldWords := "owned old words"
	if err = seed.saveSubscriptionWordsTx(ctx, tx, sub.ID, &oldWords); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": id, "repo_url": repository})
	success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "fail_event": false})
	success("POST", "/api/plugins/"+id+"/clone", map[string]any{"suffix": "Subscription"})
	clone := id + "subscription"
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "fail_event": false})
	type observation struct {
		Events []ownedBusinessEvent `json:"events"`
		PID    int                  `json:"nonce"`
	}
	read := func(instance string) observation {
		t.Helper()
		var got observation
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+instance+"/events", nil, 200), &got) != nil || got.PID <= 0 {
			t.Fatal("actual event subscriber API unavailable")
		}
		return got
	}
	filter := func(got observation, kind string) []ownedBusinessEvent {
		out := []ownedBusinessEvent{}
		for _, event := range got.Events {
			if event.Type == kind {
				out = append(out, event)
			}
		}
		return out
	}
	await := func(instance, kind string, count int) observation {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			got := read(instance)
			n := len(filter(got, kind))
			if n == count {
				return got
			}
			if n > count || time.Now().After(deadline) {
				t.Fatal("actual core event count mismatch", instance, kind, n, count)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	beforeSource, beforeClone := read(id), read(clone)
	if beforeSource.PID == beforeClone.PID {
		t.Fatal("subscriber clone shares process")
	}
	body := map[string]any{"media_type": "tv", "tmdb_id": media.ID, "season": 1, "start_episode": 1, "target_scope": "season", "total_episodes": 3, "state": "paused", "schedule": map[string]any{"mode": "manual"}, "revision": 1, "keyword": "owned new keyword", "custom_words": "owned new words"}
	request("PUT", "/api/subscriptions/"+sub.ID, body, 200)
	source := await(id, "subscribe.modified", 1)
	copied := await(clone, "subscribe.modified", 1)
	if source.PID != beforeSource.PID || copied.PID != beforeClone.PID || !originalBaseJSONEqual(filter(source, "subscribe.modified")[0].Data, filter(copied, "subscribe.modified")[0].Data) {
		t.Fatal("committed event lost independent subscriber identity or payload")
	}
	var modified struct {
		ID      string                     `json:"subscribe_id"`
		Old     map[string]json.RawMessage `json:"old_subscribe_info"`
		Current map[string]json.RawMessage `json:"subscribe_info"`
		Scene   string
		Fields  []string
	}
	if json.Unmarshal(filter(source, "subscribe.modified")[0].Data, &modified) != nil || modified.ID != sub.ID || modified.Scene != "update" || !reflect.DeepEqual(modified.Fields, []string{"custom_words", "keyword", "lack_episode", "total_episode"}) || string(modified.Old["custom_words"]) != `"owned old words"` || string(modified.Current["custom_words"]) != `"owned new words"` || string(modified.Current["lack_episode"]) != "3" {
		t.Fatal("production old/new snapshot or source fields differ", modified.Fields)
	}

	for i, state := range []string{"active", "paused"} {
		request("PUT", "/api/subscriptions/"+sub.ID+"/status", map[string]any{"state": state, "revision": i + 2}, 200)
		statusSource := await(id, "subscribe.modified", i+2)
		statusClone := await(clone, "subscribe.modified", i+2)
		original := filter(statusSource, "subscribe.modified")[i+1].Data
		if !originalBaseJSONEqual(original, filter(statusClone, "subscribe.modified")[i+1].Data) || json.Unmarshal(original, &modified) != nil || modified.Scene != "status" || !reflect.DeepEqual(modified.Fields, []string{"state"}) || string(modified.Current["keyword"]) != `"owned new keyword"` || string(modified.Current["custom_words"]) != `"owned new words"` {
			t.Fatal("production status operation changed unrelated fields or lost source/clone event")
		}
	}
	success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "fail_event": true})
	body["revision"], body["keyword"] = 4, "after callback failure"
	request("PUT", "/api/subscriptions/"+sub.ID, body, 200)
	await(clone, "subscribe.modified", 4)
	request("DELETE", "/api/subscriptions/"+sub.ID, nil, 200)
	deleted := await(clone, "subscribe.deleted", 1)
	var removal struct {
		ID   string                     `json:"subscribe_id"`
		Info map[string]json.RawMessage `json:"subscribe_info"`
	}
	if json.Unmarshal(filter(deleted, "subscribe.deleted")[0].Data, &removal) != nil || removal.ID != sub.ID || string(removal.Info["keyword"]) != `"after callback failure"` || string(removal.Info["state"]) != `"S"` {
		t.Fatal("production delete lost its pre-removal snapshot")
	}
	request("DELETE", "/api/subscriptions/"+sub.ID, nil, 200)
	await(clone, "subscribe.deleted", 1)
	stop()
	start()
	restarted := await(clone, "subscribe.deleted", 1)
	if restarted.PID == deleted.PID || len(filter(restarted, "subscribe.modified")) != 4 {
		t.Fatal("restart lost persisted subscriber data or replayed core mutations")
	}
	request("DELETE", "/api/subscriptions/"+sub.ID, nil, 200)
	await(clone, "subscribe.deleted", 1)

	if os.Getenv("PLUGIN_PROCESS_BROWSER_SUBSCRIPTION") == "true" {
		// A separate owned sample for the browser's actual existing pause/resume
		// controls; seed only while the server is stopped, never a user record.
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "fail_event": false})
		stop()
		browserSeed, err := New(Options{DataDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		sub.ID += "_browser"
		if err = browserSeed.Store.Put(ctx, "subscription", sub.ID, sub); err != nil {
			t.Fatal(err)
		}
		if err = browserSeed.Close(); err != nil {
			t.Fatal(err)
		}
		start()
		t.Log("owned paused browser sample ready; actual subscribers retained")
		return
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+id, nil)
	t.Log("production ordinary subscription events verified: seeded owned paused data; real HTTP update/status pause/resume/cancel commits emit complete original-column snapshots and sorted actual fields; source and clone use independent SDK PIDs; callback failure preserves committed success; repeated cancel and restart do not replay mutations")
}
