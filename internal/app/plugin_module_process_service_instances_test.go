package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func verifyOwnedProductionServiceInstances(t *testing.T,
	request func(string, string, any, int) []byte, success func(string, string, any),
	configure func(string, []map[string]any), restore []map[string]any) {
	t.Helper()
	path := os.Getenv("PLUGIN_PROCESS_SERVICE_INSTANCES_ORACLE")
	if path == "" {
		path = "../../testdata/standard-services/instances-oracle.json"
	}
	raw, err := os.ReadFile(path)
	var oracle struct {
		Version string
		Cases   []struct {
			Maximum  int
			Observed struct {
				New    bool
				Active int
			} `json:"observed_while_old_held"`
			Calls map[string]int
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Version != "3.11.3" || len(oracle.Cases) != 2 {
		t.Fatal("actual scheduler executor source oracle", err)
	}
	for _, sample := range oracle.Cases {
		oldReached, newReached, oldRelease, newRelease := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		var mu sync.Mutex
		counts := map[string]int{}
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			counts[r.URL.Path]++
			mu.Unlock()
			gate := oldRelease
			if r.URL.Path == "/old" {
				close(oldReached)
			} else {
				gate = newRelease
				close(newReached)
			}
			select {
			case <-gate:
			case <-r.Context().Done():
			}
			w.WriteHeader(204)
		}))
		func() {
			defer peer.Close()
			defer func() {
				select {
				case <-oldRelease:
				default:
					close(oldRelease)
				}
				select {
				case <-newRelease:
				default:
					close(newRelease)
				}
			}()
			definition := func(name, path string, maximum int) map[string]any {
				return map[string]any{"id": "limit", "name": name, "handler": "run_service", "trigger": map[string]any{"kind": "date", "timezone": "UTC", "run_at": time.Now().Add(300 * time.Millisecond).Format(time.RFC3339Nano)}, "kwargs": map[string]any{"max_instances": maximum, "misfire_grace_time": nil}, "input": map[string]any{"mode": "list-fail", "gate": peer.URL + path, "progress": 37, "text": "business-only"}}
			}
			old := definition("Old", "/old", 1)
			configure("OwnedInput", []map[string]any{old})
			select {
			case <-oldReached:
			case <-time.After(5 * time.Second):
				t.Fatal("actual old date method not held")
			}
			newDefinition := definition("New", "/new", sample.Maximum)
			configure("OwnedInput", []map[string]any{newDefinition})
			if sample.Observed.New {
				select {
				case <-newReached:
				case <-time.After(5 * time.Second):
					t.Fatal("actual max_instances2 did not start new held date method")
				}
			} else {
				deadline := time.Now().Add(5 * time.Second)
				for {
					var listed struct{ Data []PluginServiceInfo }
					if json.Unmarshal(request("GET", "/api/plugins/services", nil, 200), &listed) != nil {
						t.Fatal("actual executor list invalid")
					}
					present := false
					for _, item := range listed.Data {
						if item.ID == "OwnedInput_limit" {
							present = true
						}
					}
					if !present {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("actual max_instances1 did not consume/skip replacement date")
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			// The worker records the actual received JSON in its genuine progress
			// callback, proving clock kwargs were not substituted for func_kwargs.
			var state struct {
				Success bool
				Data    struct{ Data map[string]json.RawMessage }
			}
			if json.Unmarshal(request("GET", "/api/v1/dashboard/schedule/OwnedInput_limit/progress", nil, 200), &state) != nil || !state.Success {
				t.Fatal("actual method argument readback unavailable")
			}
			expected := old["input"]
			if sample.Observed.New {
				expected = newDefinition["input"]
			}
			expectedRaw, _ := json.Marshal(expected)
			if !originalBaseJSONEqual(state.Data.Data["actual_input"], expectedRaw) {
				t.Fatal("clock kwargs changed actual method input")
			}
			close(oldRelease)
			close(newRelease)
			deadline := time.Now().Add(5 * time.Second)
			for {
				mu.Lock()
				oldCount, newCount := counts["/old"], counts["/new"]
				mu.Unlock()
				if oldCount == sample.Calls["old"] && newCount == sample.Calls["new"] {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("actual executor callbacks differ from fixed source", oldCount, newCount, sample.Calls)
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Logf("actual production max_instances=%d: old held; new admitted=%v; actual business calls old=%d new=%d; clock kwargs and input separated", sample.Maximum, sample.Observed.New, sample.Calls["old"], sample.Calls["new"])
			success("PUT", "/api/plugins/OwnedInput", map[string]any{"enabled": false})
		}()
	}
	configure("OwnedInput", restore)
}
