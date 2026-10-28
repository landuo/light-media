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
	"sync"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Actual native create -> existing source notice router -> real SDK notification
// modules -> owned HTTP recipients -> Added observers. No sender callback is
// injected into the production process; sharing is explicitly disabled in the
// owned fixture's original settings rather than calling external statistics.
func verifyOwnedProductionSubscriptionCreateNotice(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	oraclePath := os.Getenv("PLUGIN_PROCESS_ASYNC_RECOGNIZE_ORACLE")
	if oraclePath == "" {
		oraclePath = "../../testdata/standard-module-host/async-recognize-source-oracle.json"
	}
	oracleRaw, err := os.ReadFile(oraclePath)
	var oracle struct {
		Caller struct {
			Method string `json:"recognize_method"`
			Kwargs map[string]json.RawMessage
		} `json:"caller_prefix"`
	}
	if err != nil || json.Unmarshal(oracleRaw, &oracle) != nil || oracle.Caller.Method != "async_recognize_media" {
		t.Fatal("fixed async_add caller oracle", err)
	}
	worker := os.Getenv("OWNED_NOTICE_PROCESS_FIXTURE_BINARY")
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedNotice" {
		t.Fatal("actual notice artifact")
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
	var mu sync.Mutex
	deliveries := map[string][]json.RawMessage{}
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("actual core notice HTTP input")
		}
		mu.Lock()
		deliveries[r.URL.Path] = append(deliveries[r.URL.Path], body)
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer sink.Close()
	counts := func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return len(deliveries["/source"]), len(deliveries["/clone"])
	}
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{900004, 900005, 900006} {
		media := catalog.Media{ID: id, MediaType: "tv", Title: "Owned Create Episodes", OriginalTitle: "Owned Create Episodes", Year: 2026}
		if err = seed.Store.Put(context.Background(), "cache", "media:tv:"+jsonNumber(id), map[string]any{"at": time.Now(), "data": media}); err != nil {
			t.Fatal(err)
		}
		items := []catalog.Episode{}
		for n := 1; n <= 5; n++ {
			items = append(items, catalog.Episode{ID: int64(n), EpisodeNumber: n, SeasonNumber: 1, AirDate: "2026-01-01"})
		}
		if err = seed.Store.Put(context.Background(), "cache", "season:"+jsonNumber(id)+":1", map[string]any{"at": time.Now(), "data": catalog.Season{SeriesID: id, SeasonNumber: 1, Episodes: items}}); err != nil {
			t.Fatal(err)
		}
	}
	subscriptionStatsInstallSettings(t, seed, "http://owned-unused.invalid", false)
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	for _, id := range []string{"OwnedInput", "OwnedInputservice"} {
		request("POST", "/api/v1/plugin/"+id+"/input", map[string]any{"operation": "event.manage", "input": map[string]any{"action": "add", "kind": "broadcast", "type": "subscribe.added", "method": "event_dynamic"}}, 200)
	}
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": "OwnedNotice", "repo_url": repo.URL + "/index"})
	defer success("DELETE", "/api/plugins/OwnedNotice", nil)
	success("PUT", "/api/plugins/OwnedNotice", map[string]any{"enabled": true, "endpoint": sink.URL + "/source"})
	success("POST", "/api/plugins/OwnedNotice/clone", map[string]any{"suffix": "Subscription"})
	defer success("DELETE", "/api/plugins/OwnedNoticesubscription", nil)
	success("PUT", "/api/plugins/OwnedNoticesubscription", map[string]any{"enabled": true, "endpoint": sink.URL + "/clone"})
	added := func(id string) []map[string]json.RawMessage {
		t.Helper()
		var out struct {
			Events []struct {
				Type string
				Data map[string]json.RawMessage
			}
		}
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/events", nil, 200), &out) != nil {
			t.Fatal("actual Added observer")
		}
		rows := []map[string]json.RawMessage{}
		for _, e := range out.Events {
			if e.Type == "subscribe.added" {
				rows = append(rows, e.Data)
			}
		}
		return rows
	}
	trace := filepath.Join(dir, "owned-create-recognize.jsonl")
	configure := func(id int64) {
		provider := map[string]any{"id": id, "name": "Owned Create Episodes", "original_name": "Owned Create Episodes", "first_air_date": "2026-01-01", "overview": "owned source metadata", "poster_path": "/owned.jpg", "backdrop_path": "/owned.jpg", "vote_average": 7.3, "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "external_ids": map[string]any{"imdb_id": "tt0900004", "tvdb_id": 41}, "translations": map[string]any{"translations": []any{}}, "alternative_titles": map[string]any{"titles": []any{}, "results": []any{}}, "seasons": []any{map[string]any{"season_number": 1, "episode_count": 5, "air_date": "2026-01-01"}}}
		raw, _ := json.Marshal(provider)
		full, err := sourceTMDBMediaInfo(raw, "tv", "image.tmdb.org", "")
		if err != nil {
			t.Fatal(err)
		}
		success("PUT", "/api/plugins/OwnedEpisodesRefresh", map[string]any{"enabled": true, "mode": "episodes", "episodes_updated": true, "total_episode": 8, "trace_path": trace, "recognize_media": json.RawMessage(full)})
	}
	created := []string{}
	for i, id := range []int64{900004, 900005, 900006} {
		configure(id)
		if i == 1 {
			success("PUT", "/api/plugins/OwnedNotice", map[string]any{"enabled": true, "endpoint": sink.URL + "/source", "fail": true})
		}
		if i == 2 {
			success("PUT", "/api/plugins/OwnedNotice", map[string]any{"enabled": true, "endpoint": sink.URL + "/source", "fail": false})
		}
		var response struct{ Data Subscription }
		body := map[string]any{"media_type": "tv", "tmdb_id": id, "name": "Owned async source", "year": "2026", "season": 1, "total_episodes": 0, "state": "paused", "schedule": map[string]any{"mode": "manual"}}
		if json.Unmarshal(request("POST", "/api/subscriptions", body, 200), &response) != nil || response.Data.ID == "" || response.Data.TotalEpisodes != 8 || response.Data.ManualTotalEpisode || response.Data.LackEpisode == nil || *response.Data.LackEpisode != 8 {
			t.Fatal("actual create result did not consume SDK episodes", response.Data.TotalEpisodes)
		}
		created = append(created, response.Data.ID)
		until := time.Now().Add(5 * time.Second)
		for _, instance := range []string{"OwnedInput", "OwnedInputservice"} {
			for {
				events := added(instance)
				if len(events) == i+1 {
					last := events[len(events)-1]
					if string(last["subscribe_id"]) != `"`+response.Data.ID+`"` || string(last["username"]) != `"service-acceptance"` {
						t.Fatal("actual Added source identity")
					}
					var info map[string]json.RawMessage
					if json.Unmarshal(last["mediainfo"], &info) != nil || len(info["tmdb_info"]) == 0 || len(info["seasons"]) == 0 {
						t.Fatal("actual Added compacted full source metadata")
					}
					break
				}
				if time.Now().After(until) {
					t.Fatal("actual Added callback missing", instance, i)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		wantSource := i + 1
		if i >= 1 {
			wantSource--
		}
		x, y := counts()
		if x != wantSource || y != i+1 {
			t.Fatal("actual notice sender side effects", x, y, wantSource, i+1)
		}
	}
	seen := map[int64]bool{}
	for _, row := range readOrderTrace(t, trace) {
		if row.Method != oracle.Caller.Method {
			continue
		}
		var input struct {
			Args   []json.RawMessage
			Kwargs map[string]json.RawMessage
		}
		if json.Unmarshal(row.Input, &input) != nil || input.Args == nil || len(input.Args) != 0 {
			t.Fatal("actual async_recognize_media argument envelope")
		}
		var id int64
		if json.Unmarshal(input.Kwargs["tmdbid"], &id) != nil {
			t.Fatal("actual recognize_media TMDB id")
		}
		var gotMeta, wantMeta map[string]json.RawMessage
		if json.Unmarshal(input.Kwargs["meta"], &gotMeta) != nil || len(gotMeta) <= 4 || json.Unmarshal(oracle.Caller.Kwargs["meta"], &wantMeta) != nil {
			t.Fatal("actual async_add did not send existing full MetaInfo")
		}
		wantMeta["begin_season"] = json.RawMessage(`1`) // fixture season differs from source oracle's season2
		for key, want := range wantMeta {
			if !originalBaseJSONEqual(gotMeta[key], want) {
				t.Fatal("actual async_add source MetaInfo field", key, string(gotMeta[key]), string(want))
			}
		}
		if string(gotMeta["season_episode"]) != `"S01"` {
			t.Fatal("actual existing MetaInfo derived season projection")
		}
		kwargs := map[string]json.RawMessage{}
		for key, value := range oracle.Caller.Kwargs {
			kwargs[key] = value
		}
		kwargs["meta"] = input.Kwargs["meta"]
		kwargs["tmdbid"] = json.RawMessage(jsonNumber(id))
		kwargs["mediaid"], _ = json.Marshal(jsonNumber(id))
		want, _ := json.Marshal(kwargs)
		got, _ := json.Marshal(input.Kwargs)
		if !originalBaseJSONEqual(got, want) {
			t.Fatal("actual fixed async_recognize_media kwargs", string(got))
		}
		seen[id] = true
	}
	if len(seen) != 3 {
		t.Fatal("actual recognize_media not called for three creates", seen)
	}
	copyOwnedEventsProcessEvidence(t, "events36-create-sdk-trace.jsonl", trace)
	for _, id := range []string{"OwnedInput", "OwnedInputservice"} {
		body, _ := json.Marshal(added(id))
		writeOwnedEventsProcessEvidence(t, "events36-added-"+id+".json", body)
	}
	mu.Lock()
	deliveryEvidence, _ := json.Marshal(deliveries)
	mu.Unlock()
	writeOwnedEventsProcessEvidence(t, "events36-create-notice-deliveries.json", deliveryEvidence)
	m, n := counts()
	stop()
	inspector, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range created {
		var sub Subscription
		if err = inspector.Store.Get(context.Background(), "subscription", key, &sub); err != nil {
			t.Fatal(err)
		}
		origin, err := inspector.sourceSubscriptionOrigin(context.Background(), sub)
		if err != nil {
			t.Fatal(err)
		}
		var full map[string]json.RawMessage
		var provider map[string]json.RawMessage
		if json.Unmarshal(origin.MediaInfo, &full) != nil || json.Unmarshal(origin.ProviderInfo, &provider) != nil || string(full["title"]) != `"Owned Create Episodes"` || string(provider["id"]) != jsonNumber(int64(900004+i)) || !originalBaseJSONEqual(full["tmdb_info"], origin.ProviderInfo) {
			t.Fatal("actual recognized title/ProviderInfo not frozen into source origin")
		}
	}
	if err = inspector.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	time.Sleep(150 * time.Millisecond)
	x, y := counts()
	if x != m || y != n {
		t.Fatal("cold restart repeated consumed core notices")
	}
	t.Logf("actual Linux subscription create/notice/Added: 3 real POST creates consume total5->8; core standard post_message reaches distinct source/clone HTTP recipients counts%d/%d; second source callback fails but clone and Added continue, third recovers; full frozen MediaInfo and user identities observed by source+clone; known share=false avoids external statistics; cold does not resend", x, y)
}
