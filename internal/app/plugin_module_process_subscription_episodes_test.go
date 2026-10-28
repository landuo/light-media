package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Synthetic initial rows are seeded while the server is stopped. Reset and
// check then use real native HTTP/jobs, actual SDK chains and real persisted
// subscriber observations. No notice or metadata transport is mocked here.
func verifyOwnedProductionSubscriptionEpisodesReset(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	workerPath := os.Getenv("OWNED_EPISODES_PROCESS_FIXTURE_BINARY")
	payload, err := os.ReadFile(workerPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(workerPath, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedEpisodesRefresh" {
		t.Fatal("actual episodes artifact")
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repo.Close()
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	media := catalog.Media{ID: 900002, MediaType: "tv", Title: "Owned Episodes", OriginalTitle: "Owned Episodes", Year: 2026}
	lack, priority, short := 5, 70, 2
	auto := Subscription{ID: "sub_owned_episodes", Media: media, Season: 1, StartEpisode: 1, TargetScope: "season", TotalEpisodes: 5, LackEpisode: &lack, SourceState: "R", SourceNote: []int{}, State: "active", Revision: 1, CreatedAt: now(), Schedule: Schedule{Mode: "manual"}}
	reset := auto
	reset.ID = "sub_owned_reset"
	reset.Media.ID = 900003
	reset.State = "paused"
	reset.SourceState = "S"
	reset.ManualTotalEpisode = true
	reset.LackEpisode = &short
	reset.CurrentPriority = &priority
	reset.EpisodePriority = map[string]int{"1": 70}
	reset.SourceNote = []int{1, 2, 3}
	for _, sub := range []Subscription{auto, reset} {
		if err = seed.Store.Put(context.Background(), "subscription", sub.ID, sub); err != nil {
			t.Fatal(err)
		}
		if err = seed.Store.Put(context.Background(), "cache", "media:tv:"+jsonNumber(sub.Media.ID), map[string]any{"at": time.Now(), "data": sub.Media}); err != nil {
			t.Fatal(err)
		}
	}
	episodes := []catalog.Episode{}
	for i := 1; i <= 5; i++ {
		episodes = append(episodes, catalog.Episode{ID: int64(i), SeasonNumber: 1, EpisodeNumber: i, AirDate: "2026-01-01"})
	}
	if err = seed.Store.Put(context.Background(), "cache", "season:900002:1", map[string]any{"at": time.Now(), "data": catalog.Season{SeriesID: 900002, SeasonNumber: 1, Episodes: episodes}}); err != nil {
		t.Fatal(err)
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	controls := []map[string]any{{"action": "add", "kind": "broadcast", "type": "subscribe.modified", "method": "event_dynamic"}}
	for _, id := range []string{"OwnedInput", "OwnedInputservice"} {
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "event_controls": controls})
	}
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repo.URL + "/index"})
	defer success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	provider := map[string]any{"id": 900002, "name": "Owned Episodes", "original_name": "Owned Episodes", "first_air_date": "2026-01-01", "overview": "owned source metadata", "poster_path": "/owned.jpg", "backdrop_path": "/owned.jpg", "vote_average": 7.3, "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "external_ids": map[string]any{"imdb_id": "tt0900002", "tvdb_id": 41}, "translations": map[string]any{"translations": []any{}}, "alternative_titles": map[string]any{"titles": []any{}, "results": []any{}}, "seasons": []any{map[string]any{"season_number": 1, "episode_count": 5, "air_date": "2026-01-01"}}}
	providerRaw, _ := json.Marshal(provider)
	full, err := sourceTMDBMediaInfo(providerRaw, "tv", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(dir, "owned-episodes.jsonl")
	success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "mode": "episodes_error", "episodes_updated": true, "total_episode": 8, "trace_path": trace, "recognize_media": json.RawMessage(full)})
	events := func(id string) []map[string]json.RawMessage {
		t.Helper()
		var out struct {
			Events []struct {
				Type string
				Data map[string]json.RawMessage
			}
		}
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/events", nil, 200), &out) != nil {
			t.Fatal("actual subscription observers")
		}
		rows := []map[string]json.RawMessage{}
		for _, event := range out.Events {
			if event.Type == "subscribe.modified" {
				rows = append(rows, event.Data)
			}
		}
		return rows
	}
	base := map[string]int{}
	for _, id := range []string{"OwnedInput", "OwnedInputservice"} {
		base[id] = len(events(id))
	}
	request("POST", "/api/subscriptions/"+reset.ID+"/reset", map[string]any{}, 200)
	until := time.Now().Add(5 * time.Second)
	for _, id := range []string{"OwnedInput", "OwnedInputservice"} {
		for {
			rows := events(id)
			if len(rows) == base[id]+1 {
				var got struct {
					Scene  string
					Fields []string
					Info   map[string]json.RawMessage `json:"subscribe_info"`
				}
				raw, _ := json.Marshal(rows[len(rows)-1])
				if json.Unmarshal(raw, &got) != nil || got.Scene != "reset" || len(got.Fields) != 6 || string(got.Info["lack_episode"]) != "5" || string(got.Info["state"]) != `"R"` {
					t.Fatal("actual source reset callback fields", string(raw))
				}
				break
			}
			if time.Now().After(until) {
				t.Fatal("actual reset callback missing", id)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	list := func() map[string]Subscription {
		t.Helper()
		var out struct {
			Data struct{ Items []Subscription }
		}
		if json.Unmarshal(request("GET", "/api/subscriptions", nil, 200), &out) != nil {
			t.Fatal("actual subscriptions response")
		}
		rows := map[string]Subscription{}
		for _, sub := range out.Data.Items {
			rows[sub.ID] = sub
		}
		return rows
	}
	request("POST", "/api/subscriptions/"+auto.ID+"/check", map[string]any{}, 200)
	until = time.Now().Add(8 * time.Second)
	for {
		got := list()[auto.ID]
		if got.TotalEpisodes == 8 && got.LackEpisode != nil && *got.LackEpisode == 8 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("actual check job did not consume episodes SDK mutation", got.TotalEpisodes)
		}
		time.Sleep(20 * time.Millisecond)
	}
	rows := readOrderTrace(t, trace)
	requireOwnedSubscriptionRecognizeSource(t, rows, "Owned Episodes", "2026", "电视剧", 900002, 1)
	seen, workerPID := false, 0
	for _, row := range rows {
		if row.Method != "chain_first" {
			continue
		}
		var input struct{ Data map[string]json.RawMessage }
		if json.Unmarshal(row.Input, &input) == nil && string(input.Data["scene"]) == `"precheck"` {
			if string(input.Data["current_total_episode"]) != "5" || string(input.Data["subscribe_id"]) != `"`+auto.ID+`"` || !json.Valid(input.Data["mediainfo"]) {
				t.Fatal("actual full source precheck payload")
			}
			seen, workerPID = true, row.PID
		}
	}
	if !seen || workerPID <= 0 {
		t.Fatal("actual SDK precheck side effect not observed")
	}
	copyOwnedEventsProcessEvidence(t, "events36-precheck-sdk-trace.jsonl", trace)
	for _, id := range []string{"OwnedInput", "OwnedInputservice"} {
		body, _ := json.Marshal(events(id))
		writeOwnedEventsProcessEvidence(t, "events36-reset-"+id+".json", body)
	}
	stop()
	start()
	cold := list()
	if cold[auto.ID].TotalEpisodes != 8 || cold[reset.ID].State != "active" || cold[reset.ID].ManualTotalEpisode {
		t.Fatal("cold native state lost callback consumption/reset")
	}
	for _, id := range []string{"OwnedInput", "OwnedInputservice"} {
		if len(events(id)) != 0 {
			t.Fatal("cold restart replayed non-durable plugin mutation")
		}
	}
	t.Logf("actual Linux subscription reset+episodes: seeded owned initial rows only; POST reset commits source six fields and scene reset to source+clone; real /check job sends full precheck payload to SDK PID%d, exception-before-mutation return still persists total/lack8; actual restart preserves native state and does not replay modified; no notice/metadata transport mock", workerPID)
	if os.Getenv("PLUGIN_PROCESS_SUBSCRIPTION_DECREASE_FIXTURE") == "true" {
		verifyOwnedProductionSubscriptionDecrease(t, dir, auto.ID, trace, request, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_SUBSCRIPTION_NOTICE_FIXTURE") == "true" {
		verifyOwnedProductionSubscriptionCreateNotice(t, dir, request, success, stop, start)
	}
}
