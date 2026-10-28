package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStandardSubscriptionEpisodesActualSDKSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-subscription-events/episodes-oracle.json")
	var source struct {
		Cases []struct {
			Name                   string
			Current, Total, Result int
			Updated                bool
			Input                  map[string]json.RawMessage
		}
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || len(source.Cases) != 5 {
		t.Fatal("fixed episodes source oracle", err)
	}
	a := subscriptionTestApp(t)
	cookie := setupTestAdmin(t, a)
	worker := installOrderWorker(t, a, cookie, os.Getenv("OWNED_EPISODES_REFRESH_FIXTURE_BINARY"))
	trace := filepath.Join(t.TempDir(), "episodes.jsonl")
	for _, sample := range source.Cases {
		config, _ := json.Marshal(map[string]any{"enabled": true, "mode": "episodes", "trace_path": trace, "episodes_updated": sample.Updated, "total_episode": sample.Total})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+worker.PluginID, string(config), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		total, err := a.applyStandardSubscriptionEpisodes(context.Background(), sample.Current, 4, sample.Input["mediainfo"], nil, "create")
		if err != nil || total != sample.Result {
			t.Fatal(sample.Name, total, sample.Result, err)
		}
	}
	rows := readOrderTrace(t, trace)
	if len(rows) != 10 {
		t.Fatal("actual declared SDK chain methods", len(rows))
	}
	var first struct{ Data map[string]json.RawMessage }
	if json.Unmarshal(rows[0].Input, &first) != nil {
		t.Fatal("actual SDK input envelope")
	}
	for key, want := range source.Cases[0].Input {
		if !originalBaseJSONEqual(first.Data[key], want) {
			t.Fatal("source input field", key, string(first.Data[key]), string(want))
		}
	}
	config, _ := json.Marshal(map[string]any{"enabled": true, "mode": "episodes_error", "episodes_updated": true, "total_episode": 8})
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+worker.PluginID, string(config), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	request := subscriptionRequest(t)
	request.TotalEpisodes = 0
	sub, err := a.saveSubscription(context.Background(), "", request)
	if err != nil {
		t.Fatal(err)
	}
	installNoticeSourceFixtures(t, a, sub.Media, sub.Season, 5)
	updated, err := a.refreshOrdinaryCompletionScope(context.Background(), sub)
	if err != nil || updated.TotalEpisodes != 8 || updated.LackEpisode == nil || *updated.LackEpisode != 8 || updated.ManualTotalEpisode {
		t.Fatal("actual precheck did not persist callback mutation before completion", updated.TotalEpisodes, err)
	}
}

func TestStandardSubscriptionCreateEpisodesActualCoreAndSDK(t *testing.T) {
	defaultRaw, err := os.ReadFile("../../testdata/standard-subscription-events/add-lack-default-oracle.json")
	var initial struct {
		Result struct {
			Lack int `json:"lack_episode"`
		}
	}
	if err != nil || json.Unmarshal(defaultRaw, &initial) != nil || initial.Result.Lack != 8 {
		t.Fatal("fixed actual async_add initial lack default", err)
	}
	a, input, notice := uiSourceAddFixture(t)
	cookie := installOwnedEventFixture(t, a)
	checker := installOrderWorker(t, a, cookie, os.Getenv("OWNED_EPISODES_REFRESH_FIXTURE_BINARY"))
	config, _ := json.Marshal(map[string]any{"enabled": true, "mode": "episodes_error", "episodes_updated": true, "total_episode": 8})
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+checker.PluginID, string(config), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	subscriptionStatsInstallSettings(t, a, "http://owned-unused.invalid", false)
	input.TotalEpisodes = 0
	saved, err := a.createUISubscription(context.Background(), input, "owned-episodes-user")
	if err != nil || saved.TotalEpisodes != 8 || saved.ManualTotalEpisode || saved.LackEpisode == nil || *saved.LackEpisode != initial.Result.Lack {
		_, receipt := uiSourceOnlyAdd(t, a)
		t.Log("actual create boundary", receipt.Stage, "metadata", notice.Metadata.Load(), "recipient", notice.Sends.Load())
		rows, queryErr := a.Store.DB.Query(`SELECT state,error_code,modules FROM plugin_message_queue`)
		if queryErr == nil {
			defer rows.Close()
			for rows.Next() {
				var state, code string
				var modules int
				if rows.Scan(&state, &code, &modules) == nil {
					t.Log("actual queue boundary", state, code, modules)
				}
			}
		}
		t.Fatal("actual create did not consume automatic episode override", saved.TotalEpisodes, err)
	}
	if notice.Sends.Load() != 1 {
		t.Fatal("actual create did not finish existing owned notification recipient")
	}
	events := requireBusinessEventCount(t, a, ownedEventFixtureID, "subscribe.added", 1)
	var added struct {
		ID        string                     `json:"subscribe_id"`
		User      string                     `json:"username"`
		MediaInfo map[string]json.RawMessage `json:"mediainfo"`
	}
	if json.Unmarshal(events[0].Data, &added) != nil || added.ID != saved.ID || added.User != "owned-episodes-user" || len(added.MediaInfo["tmdb_info"]) == 0 || len(added.MediaInfo["seasons"]) == 0 {
		t.Fatal("actual create event lost frozen full source metadata")
	}
}
