package app

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This owned peer implements only the pinned client's HTTP contract. Its
// aggregation is test data, not a reconstruction of the opaque official center.
type ownedPluginCenter struct {
	server                                    *httptest.Server
	fail                                      atomic.Bool
	mu                                        sync.Mutex
	uid                                       string
	userAgent                                 string
	requests, singles, bulks, successfulBulks int
	counts                                    map[string]int
	ratings                                   map[string]float64
}

func newOwnedPluginCenter(t *testing.T) *ownedPluginCenter {
	t.Helper()
	peer := &ownedPluginCenter{counts: map[string]int{}, ratings: map[string]float64{}}
	peer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer.mu.Lock()
		defer peer.mu.Unlock()
		if r.URL.Path == "/owned/compare-uid" {
			macUID := originalPluginUIDFromHost(func() (string, error) { return "", errors.New("owned forced root failure") }, originalPluginMACNode)
			_ = json.NewEncoder(w).Encode(map[string]any{"observed": peer.uid != "", "matches_fixed_native_source": peer.uid != "" && peer.uid == r.Header.Get("X-Owned-Expected-UID"), "matches_fixed_native_user_agent": peer.userAgent != "" && peer.userAgent == r.Header.Get("X-Owned-Expected-UA"), "matches_fixed_native_mac_fallback": macUID == r.Header.Get("X-Owned-Expected-MAC-UID"), "mac_identity_available": macUID != ""})
			return
		}
		if r.URL.Path == "/owned/observations" {
			_ = json.NewEncoder(w).Encode(map[string]any{"requests": peer.requests, "single_reports": peer.singles, "bulk_attempts": peer.bulks, "bulk_successes": peer.successfulBulks, "counts": peer.counts, "uid_observed": peer.uid != "", "failed": peer.fail.Load()})
			return
		}
		peer.requests++
		uid := r.Header.Get("X-MoviePilot-User-Uid")
		decoded, err := hex.DecodeString(uid)
		if err != nil || len(decoded) != 32 || peer.uid != "" && peer.uid != uid {
			http.Error(w, "owned UID mismatch", 400)
			return
		}
		peer.uid = uid
		peer.userAgent = r.Header.Get("User-Agent")
		position := strings.Index(r.URL.Path, "/plugin/")
		if position < 0 {
			http.NotFound(w, r)
			return
		}
		path := r.URL.Path[position:]
		if r.Method == "POST" && path == "/plugin/install" {
			peer.bulks++
		}
		if peer.fail.Load() {
			http.Error(w, "owned center unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		rating := func(id string) map[string]any {
			value, exists := peer.ratings[id]
			result := map[string]any{"plugin_id": id, "average_rating": 0, "rating_count": 0, "user_rating": nil}
			if exists {
				result["average_rating"], result["rating_count"], result["user_rating"] = value, 1, value
			}
			return result
		}
		switch {
		case r.Method == "GET" && path == "/plugin/statistic":
			_ = json.NewEncoder(w).Encode(peer.counts)
		case r.Method == "POST" && path == "/plugin/install":
			var body struct {
				Plugins []struct {
					ID         string  `json:"plugin_id"`
					Repository *string `json:"repo_url"`
				} `json:"plugins"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Plugins) == 0 {
				http.Error(w, "owned report invalid", 400)
				return
			}
			for _, plugin := range body.Plugins {
				if plugin.ID == "" || plugin.Repository != nil {
					http.Error(w, "owned initial report shape invalid", 400)
					return
				}
			}
			peer.successfulBulks++
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		case r.Method == "POST" && strings.HasPrefix(path, "/plugin/install/"):
			var body struct {
				ID         string  `json:"plugin_id"`
				Repository *string `json:"repo_url"`
			}
			id := strings.TrimPrefix(path, "/plugin/install/")
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != id || body.Repository == nil || strings.Contains(*body.Repository, "?") {
				http.Error(w, "owned install shape invalid", 400)
				return
			}
			peer.singles++
			peer.counts[id]++
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		case path == "/plugin/rating" && r.Method == "GET":
			values := map[string]any{}
			for _, id := range strings.Split(r.URL.Query().Get("plugin_ids"), ",") {
				values[id] = rating(id)
			}
			_ = json.NewEncoder(w).Encode(values)
		case strings.HasPrefix(path, "/plugin/rating/"):
			id := strings.TrimPrefix(path, "/plugin/rating/")
			if r.Method == "POST" {
				var body struct {
					Rating float64 `json:"rating"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Rating != 4.5 && body.Rating != 1 {
					http.Error(w, "owned rating fixture expects fixed source4.5/true inputs", 400)
					return
				}
				peer.ratings[id] = body.Rating
			}
			if id == "Defaults" {
				_ = json.NewEncoder(w).Encode(map[string]any{"plugin_id": id})
				return
			}
			_ = json.NewEncoder(w).Encode(rating(id))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.server.Close)
	return peer
}

func verifyOwnedProductionPluginCenter(t *testing.T, peer *ownedPluginCenter, repository string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	const id = ownedStandardFixtureID
	configure := func(shared bool, prefix string) {
		success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{}, "plugin_server_url": peer.server.URL + prefix, "plugin_statistic_share": shared})
	}
	configure(false, "/prefix")
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": id, "repo_url": repository + "/version-one/index", "force": true})
	success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "key": "center-source"})
	peer.mu.Lock()
	requests := peer.requests
	peer.mu.Unlock()
	if requests != 0 {
		t.Fatal("disabled statistics sharing sent an installation report")
	}
	configure(true, "/prefix")
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": id, "repo_url": repository + "/version-one/index", "force": true})
	peer.mu.Lock()
	singles := peer.singles
	peer.mu.Unlock()
	if singles != 1 {
		t.Fatal("actual installation did not report exactly once")
	}
	var statistics struct{ Data map[string]int }
	if json.Unmarshal(request("GET", "/api/plugins/statistic", nil, 200), &statistics) != nil || statistics.Data[id] != 1 {
		t.Fatal("actual installation count was not delivered to host consumer")
	}
	read := func() map[string]json.RawMessage {
		t.Helper()
		var value map[string]json.RawMessage
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/echo", nil, 200), &value) != nil || !bytes.Equal(value["value"], []byte(`"center-source"`)) {
			t.Fatal("actual center test SDK/KV unavailable")
		}
		return value
	}
	before := read()
	for _, input := range []any{"4.5", true} {
		success("POST", "/api/plugins/rating/"+id, map[string]any{"rating": input, "source_ignored_extra": true})
	}
	var rating struct{ Data originalPluginRating }
	if json.Unmarshal(request("GET", "/api/plugins/rating/"+id, nil, 200), &rating) != nil || rating.Data.UserRating == nil || *rating.Data.UserRating != 1 || rating.Data.RatingCount != "1" {
		t.Fatal("actual rating write/read or numeric coercion failed")
	}
	request("GET", "/api/plugins/rating?plugin_ids="+id+",Defaults", nil, 200)
	if json.Unmarshal(request("GET", "/api/plugins/rating/Owned%20caf%C3%A9", nil, 200), &rating) != nil || rating.Data.PluginID != "Owned café" {
		t.Fatal("rating URL path was double escaped")
	}
	if json.Unmarshal(request("GET", "/api/plugins/rating/Defaults", nil, 200), &rating) != nil || rating.Data.AverageRating != 0 || rating.Data.RatingCount != "0" || rating.Data.UserRating != nil {
		t.Fatal("actual schema defaults differ")
	}
	request("POST", "/api/plugins/rating/NotInstalled", map[string]any{"rating": 4.5}, 400)
	peer.fail.Store(true)
	for _, path := range []string{"/api/plugins/statistic", "/api/plugins/rating?plugin_ids=" + id} {
		var empty struct{ Data map[string]json.RawMessage }
		if json.Unmarshal(request("GET", path, nil, 200), &empty) != nil || empty.Data == nil || len(empty.Data) != 0 {
			t.Fatal("source unavailable list fallback differs", path)
		}
	}
	if json.Unmarshal(request("GET", "/api/plugins/rating/"+id, nil, 200), &rating) != nil || rating.Data.PluginID != id || rating.Data.RatingCount != "0" || rating.Data.UserRating != nil {
		t.Fatal("source unavailable single fallback differs")
	}
	var rejected struct {
		Data struct {
			Success bool
			Message string
		}
	}
	if json.Unmarshal(request("POST", "/api/plugins/rating/"+id, map[string]any{"rating": 3}, 200), &rejected) != nil || rejected.Data.Success || rejected.Data.Message == "" {
		t.Fatal("unavailable rating write claimed success")
	}
	after := read()
	for _, key := range []string{"nonce", "init_count", "config", "value", "version"} {
		if !bytes.Equal(before[key], after[key]) {
			t.Fatal("center reads/failure changed actual SDK", key)
		}
	}
	waitBulk := func(attempts, successful int) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for {
			peer.mu.Lock()
			a, s := peer.bulks, peer.successfulBulks
			peer.mu.Unlock()
			if a >= attempts && s >= successful {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("actual startup report did not reach owned peer")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	stop()
	start()
	waitBulk(1, 0)
	peer.fail.Store(false)
	stop()
	start()
	waitBulk(2, 1)
	read()
	// The source receipt is global, not keyed by server URL. Changing the
	// configured prefix must not report the same initial installation again.
	configure(true, "/other-prefix")
	stop()
	start()
	read()
	time.Sleep(250 * time.Millisecond)
	peer.mu.Lock()
	attempts, successful := peer.bulks, peer.successfulBulks
	peer.mu.Unlock()
	if attempts != 2 || successful != 1 {
		t.Fatal("initial report receipt was lost or keyed by endpoint")
	}
	configure(false, "/other-prefix")
	peer.mu.Lock()
	requests = peer.requests
	peer.mu.Unlock()
	request("GET", "/api/plugins/statistic", nil, 200)
	peer.mu.Lock()
	afterRequests := peer.requests
	peer.mu.Unlock()
	if afterRequests != requests {
		t.Fatal("disabled statistics still queried center")
	}
	request("GET", "/api/plugins/rating/"+id, nil, 200)
	success("DELETE", "/api/plugins/"+id, nil)
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_server_url": "", "plugin_statistic_share": true})
	t.Log("actual production center client: install/share gate/count, rating read/write/defaults/coercion/fallbacks, no SDK mutation, startup retry/global receipt/stable UID across3 restarts; owned protocol peer only")
}
