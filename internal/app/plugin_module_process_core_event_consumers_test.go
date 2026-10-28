package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionCoreEventConsumers(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	oraclePath := os.Getenv("PLUGIN_PROCESS_CORE_EVENT_ORACLE")
	if oraclePath == "" {
		oraclePath = "../../testdata/standard-event-target/core-consumer-source-oracle.json"
	}
	raw, err := os.ReadFile(oraclePath)
	var oracle struct {
		Cases []struct {
			Event string
			Data  json.RawMessage
			Calls []struct {
				Arguments string          `json:"data_str"`
				Channel   json.RawMessage `json:"channel"`
				Source    json.RawMessage `json:"source"`
				User      json.RawMessage `json:"userid"`
			}
		}
	}
	var samples struct{ Cases []json.RawMessage }
	if err != nil || json.Unmarshal(raw, &samples) != nil || len(samples.Cases) != 9 {
		t.Fatal("actual concrete event consumer oracle", err)
	}
	selected, _ := json.Marshal(map[string]any{"Cases": []json.RawMessage{samples.Cases[0]}})
	if json.Unmarshal(selected, &oracle) != nil || len(oracle.Cases) != 1 || len(oracle.Cases[0].Calls) != 1 {
		t.Fatal("actual concrete event consumer oracle", err)
	}
	send := func(id, kind string, data any) {
		t.Helper()
		request("POST", "/api/v1/plugin/"+id+"/input", map[string]any{"operation": "event.send", "input": map[string]any{"kind": "broadcast", "type": kind, "data": data}}, 200)
	}
	commandEvents := func(id string) ([]json.RawMessage, []byte) {
		body := request("GET", "/api/v1/plugin/"+id+"/events", nil, 200)
		var response struct{ Events []json.RawMessage }
		if json.Unmarshal(body, &response) != nil {
			t.Fatal("actual command event observations")
		}
		result := []json.RawMessage{}
		for _, raw := range response.Events {
			var event struct {
				Type string
				Data struct {
					Action string
				}
			}
			if json.Unmarshal(raw, &event) == nil && event.Type == "plugin.action" && event.Data.Action == "command" {
				result = append(result, raw)
			}
		}
		return result, body
	}
	ids := []string{"OwnedInput", "OwnedInputservice"}
	before := map[string]int{}
	for _, id := range ids {
		rows, _ := commandEvents(id)
		before[id] = len(rows)
	}
	for i, sender := range ids {
		send(sender, oracle.Cases[0].Event, oracle.Cases[0].Data)
		for _, id := range ids {
			deadline := time.Now().Add(10 * time.Second)
			for {
				rows, body := commandEvents(id)
				if len(rows) == before[id]+i+1 {
					var event struct {
						Data struct {
							Arguments string          `json:"arg_str"`
							Channel   json.RawMessage `json:"channel"`
							Source    json.RawMessage `json:"source"`
							User      json.RawMessage `json:"user"`
						}
					}
					want := oracle.Cases[0].Calls[0]
					if json.Unmarshal(rows[len(rows)-1], &event) != nil || event.Data.Arguments != want.Arguments || !originalBaseJSONEqual(event.Data.Channel, want.Channel) || !originalBaseJSONEqual(event.Data.Source, want.Source) || !originalBaseJSONEqual(event.Data.User, want.User) {
						t.Fatal("actual source Command argument/context consumption changed", string(rows[len(rows)-1]))
					}
					writeOwnedEventsProcessEvidence(t, "events36-core-command-"+id+".json", body)
					break
				}
				if time.Now().After(deadline) || len(rows) > before[id]+i+1 {
					t.Fatal("actual SDK Command consumer missing/duplicated", id, len(rows), before[id]+i+1)
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}
	payload, err := os.ReadFile(os.Getenv("OWNED_CORE_EVENT_OBSERVER_PROCESS_FIXTURE_BINARY"))
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := exec.Command(os.Getenv("OWNED_CORE_EVENT_OBSERVER_PROCESS_FIXTURE_BINARY"), "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(manifestRaw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedCoreEventObserver" {
		t.Fatal("actual core observer artifact", err)
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index" {
			_ = json.NewEncoder(w).Encode(index)
		} else if r.URL.Path == "/worker" {
			_, _ = w.Write(payload)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer repo.Close()
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repo.URL + "/index"})
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "twin", "name": "Owned core observer clone"})
	observerIDs := []string{manifest.PluginID, manifest.PluginID + "twin"}
	trace := filepath.Join(dir, "owned-core-consumer-declarations.jsonl")
	for _, id := range observerIDs {
		defer success("DELETE", "/api/plugins/"+id, nil)
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "mode": "observe", "label": id, "trace_path": trace, "trace_declarations": true})
	}
	readCounts := func() (map[string]int, []byte) {
		body, err := os.ReadFile(trace)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			var row struct {
				Method, Label string
				PID           int
			}
			if json.Unmarshal([]byte(line), &row) != nil || row.PID <= 0 {
				t.Fatal("actual declaration trace", line)
			}
			counts[row.Label+"/"+row.Method]++
		}
		return counts, body
	}
	// Initial command rebuilds are asynchronous in both hosts. Observe a quiet
	// owned declaration trace before measuring the next independent event.
	beforeCounts, _ := readCounts()
	quietUntil, deadline := time.Now().Add(200*time.Millisecond), time.Now().Add(10*time.Second)
	for time.Now().Before(quietUntil) {
		time.Sleep(20 * time.Millisecond)
		current, _ := readCounts()
		x, _ := json.Marshal(beforeCounts)
		y, _ := json.Marshal(current)
		if string(x) != string(y) {
			beforeCounts, quietUntil = current, time.Now().Add(200*time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("initial core observer declarations never settled")
		}
	}
	for _, id := range observerIDs {
		if beforeCounts[id+"/get_command"] == 0 || beforeCounts[id+"/get_service"] == 0 {
			t.Fatal("initial real declaration baseline missing", id, beforeCounts)
		}
	}
	send("OwnedInput", "plugin.reload", map[string]string{"plugin_id": observerIDs[1]})
	deadline = time.Now().Add(10 * time.Second)
	for {
		current, _ := readCounts()
		if current[observerIDs[1]+"/get_service"] == beforeCounts[observerIDs[1]+"/get_service"]+1 {
			if current[observerIDs[0]+"/get_service"] != beforeCounts[observerIDs[0]+"/get_service"] {
				t.Fatal("plugin.reload refreshed a different instance")
			}
			beforeCounts = current
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual Scheduler did not consume plugin.reload")
		}
		time.Sleep(20 * time.Millisecond)
	}
	send("OwnedInputservice", "module.reload", map[string]any{})
	deadline = time.Now().Add(10 * time.Second)
	for {
		current, body := readCounts()
		if current[observerIDs[0]+"/get_command"] == beforeCounts[observerIDs[0]+"/get_command"]+1 && current[observerIDs[1]+"/get_command"] == beforeCounts[observerIDs[1]+"/get_command"]+1 {
			writeOwnedEventsProcessEvidence(t, "events36-core-declaration-trace.jsonl", body)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual Command did not consume module.reload", current, beforeCounts)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Log("actual Linux SDK Event.Send source and clone -> Command command.excute exact arguments/large user -> source+clone callback; plugin.reload -> current clone get_service once; module.reload -> real source+clone get_command once; no new core module/transport business")
	verifyOwnedProductionCoreSiteDeleted(t, dir, request, stop, start)
}
