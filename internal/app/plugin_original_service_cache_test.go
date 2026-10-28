package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestOriginalServiceActualSDKExpiredFinishDoesNotRevive(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	reached, release := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(peer.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	ownedProgressConfig(t, a, cookie, "tuple-fail", peer.URL, 3600000)
	r := callTest(t, a.Handler(), "POST", "/api/plugins/services/run", `{"instance_id":"OwnedInput","service_id":"owned_progress"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("actual SDK method did not hold")
	}
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	progress := state.progress["OwnedInput_owned_progress"]
	if progress == nil {
		state.mu.Unlock()
		t.Fatal("actual method did not update progress")
	}
	progress.updated = time.Now().Add(-24 * time.Hour)
	state.mu.Unlock()
	close(release)
	a.wg.Wait()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.progress["OwnedInput_owned_progress"] != nil {
		t.Fatal("actual SDK completion revived expired source cache")
	}
	if state.jobs[originalServiceKey{"OwnedInput", "owned_progress"}].running {
		t.Fatal("expired progress kept logical service running")
	}
}

func TestOriginalServiceProgressFixedTLRU(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/cache-oracle.json")
	var source struct {
		LRU struct {
			Read     bool `json:"read_key_retained"`
			Least    bool `json:"least_recent_key_retained"`
			Inserted bool `json:"inserted_key_retained"`
		} `json:"lru"`
		Expiry map[string]any `json:"expiry"`
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || len(source.Expiry) != 3 {
		t.Fatal("fixed ProgressHelper/cache source evidence", err)
	}
	a := &App{}
	object := &originalPluginInstance{installation: PluginInstallation{InstanceID: "Cache"}}
	prepare := func(id string) {
		a.prepareOriginalServiceLocked(&originalDirectJob{object: object, definition: pluginScheduleDefinition{ID: id, Name: "Cache"}})
	}
	for i := 0; i < 1024; i++ {
		prepare(strconv.Itoa(i))
	}
	a.originalServiceProgressGetLocked("Cache_0", time.Now())
	prepare("1024")
	for id, expected := range map[string]bool{"0": source.LRU.Read, "1": source.LRU.Least, "1024": source.LRU.Inserted} {
		if got := a.originalServiceProgressGetLocked("Cache_"+id, time.Now()) != nil; got != expected {
			t.Fatalf("source LRU key %s retained=%v, want=%v", id, got, expected)
		}
	}
	prepare("expiry")
	progress := a.pluginOriginalDirectJobs.progress["Cache_expiry"]
	boundary := progress.updated.Add(24 * time.Hour)
	if a.originalServiceProgressGetLocked("Cache_expiry", boundary.Add(-time.Nanosecond)) == nil {
		t.Fatal("read evicted before fixed TTL")
	}
	if got := a.originalServiceProgressGetLocked("Cache_expiry", boundary); got != nil || source.Expiry["at_expiry"] != nil {
		t.Fatal("fixed TTL equality did not expire")
	}
	if _, exists := a.pluginOriginalDirectJobs.progress["Cache_expiry"]; exists {
		t.Fatal("expired value retained")
	}
}
