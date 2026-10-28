package app

import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
)

// The stopped-process cache holds only owned metadata input. Actual native HTTP
// creates the subscription, its job/worker dispatches the plugin and the real
// store supplies completion/readback; no business record or call count is seeded.
func verifyOwnedResourceSubscriptionScope(t *testing.T, dir string, second SiteConfig, request func(string, string, any) []byte, success func(string, string, any), stop, start func(), read func() ownedModuleSnapshot, native *atomic.Int32) {
	t.Helper()
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	media := catalog.Media{ID: 900044, MediaType: "tv", Title: "Owned scope", OriginalTitle: "Owned scope", Year: 2020}
	episodes := []catalog.Episode{}
	for n := 1; n <= 3; n++ {
		episodes = append(episodes, catalog.Episode{ID: int64(n), SeasonNumber: 1, EpisodeNumber: n, AirDate: "2020-01-01"})
	}
	for key, data := range map[string]any{"media:tv:900044": media, "season:900044:1": catalog.Season{SeriesID: media.ID, SeasonNumber: 1, Episodes: episodes}} {
		if err := seed.Store.Put(context.Background(), "cache", key, map[string]any{"at": time.Now(), "data": data}); err != nil {
			t.Fatal(err)
		}
	}
	subscriptionStatsInstallSettings(t, seed, "http://owned-unused.invalid", false)
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	second.Enabled = true
	request("POST", "/api/sites/manage", second)
	defer func() { second.Enabled = false; request("POST", "/api/sites/manage", second) }()
	provider := json.RawMessage(`{"id":900044,"name":"Owned scope","original_name":"Owned scope","first_air_date":"2020-01-01","seasons":[{"season_number":1,"episode_count":3}],"alternative_titles":{"results":[{"title":"Owned scope"}]},"genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`)
	full, err := sourceTMDBMediaInfo(provider, "tv", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	success("PUT", "/api/plugins/"+ownedModuleID, map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_page_size", "recognize_media": "module_page_size", "batch_search_torrents": "module_execute", "async_batch_search_torrents": "module_fail"}, "page_size": full, "result": map[string]any{"1": []any{}, "2": []any{}}})
	var created struct{ Data Subscription }
	body := map[string]any{"media_type": "tv", "tmdb_id": 900044, "name": "Owned scope", "year": "2020", "season": 1, "total_episodes": 3, "target_scope": "episodes", "episodes": []int{1, 2}, "site_ids": []string{"owned-resource-site", "owned-resource-zsecond"}, "downloader_id": "owned-resource-qb", "state": "active", "schedule": map[string]any{"mode": "manual"}, "keyword": "Owned scope"}
	if json.Unmarshal(request("POST", "/api/subscriptions", body), &created) != nil || created.Data.ID == "" {
		t.Fatal("actual source create missing")
	}
	defer request("DELETE", "/api/subscriptions/"+created.Data.ID, nil)
	before, coreBefore := read(), native.Load()
	var queued struct{ Data Job }
	if json.Unmarshal(request("POST", "/api/subscriptions/"+created.Data.ID+"/check", map[string]any{}), &queued) != nil || queued.Data.ID == "" {
		t.Fatal("actual check job was not queued")
	}
	until := time.Now().Add(8 * time.Second)
	for {
		var current struct{ Data struct{ Job Job } }
		if json.Unmarshal(request("GET", "/api/jobs/"+queued.Data.ID, nil), &current) != nil || current.Data.Job.ID != queued.Data.ID {
			t.Fatal("actual job readback missing")
		}
		if current.Data.Job.State == "succeeded" {
			break
		}
		if current.Data.Job.State == "failed" || time.Now().After(until) {
			t.Fatal("actual native shared search job failed", current.Data.Job.State, current.Data.Job.ErrorCode)
		}
		time.Sleep(20 * time.Millisecond)
	}
	after := read()
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 || after.Calls["module_page_size"]-before.Calls["module_page_size"] != 1 || after.Calls["module_fail"] != before.Calls["module_fail"] || native.Load() != coreBefore {
		t.Fatal("actual process repeated episode/site search, changed sync method, or ignored terminal batch", after.Calls, before.Calls, native.Load()-coreBefore)
	}
	var call struct {
		Kwargs struct {
			Sites   []struct{ ID int }
			Mtype   string
			Keyword string
			Page    int
		}
	}
	if json.Unmarshal(after.Inputs["module_execute"], &call) != nil || len(call.Kwargs.Sites) != 2 || call.Kwargs.Sites[0].ID != 1 || call.Kwargs.Sites[1].ID != 2 || call.Kwargs.Mtype != "电视剧" || call.Kwargs.Keyword != "Owned scope" || call.Kwargs.Page != 0 {
		t.Fatal("actual shared subscription module scope differs")
	}
	var listed struct {
		Data struct{ Items []Subscription }
	}
	if json.Unmarshal(request("GET", "/api/subscriptions", nil), &listed) != nil {
		t.Fatal("actual subscription list")
	}
	found := false
	for _, sub := range listed.Data.Items {
		if sub.ID == created.Data.ID {
			found = true
			if sub.LastCheck == "" || sub.State != "active" || !reflect.DeepEqual(sub.Episodes, []int{1, 2}) {
				t.Fatal("actual shared check changed missing target", sub.LastCheck, sub.State, sub.Episodes)
			}
		}
	}
	if !found {
		t.Fatal("actual checked subscription disappeared")
	}
	t.Logf("actual main subscription SDK%d: source-created two-episode/two-site check completed with sync batch1/recognize1/nativeSearchHTTP0 and retained target/readback", after.PID)
}
