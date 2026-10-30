package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func disableResetFixtureStatistics(t *testing.T, a *App) {
	t.Helper()
	settings := a.Settings()
	noShare := false
	noServer := ""
	settings.PluginStatisticShare = &noShare
	settings.PluginServerURL = &noServer
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
}

type resetSourceCase struct {
	Case        string `json:"case"`
	Observation struct {
		Success       *bool             `json:"success"`
		Error         json.RawMessage   `json:"error"`
		Trace         []json.RawMessage `json:"trace"`
		EventData     json.RawMessage   `json:"event_data_after"`
		Notifications []struct {
			Title string `json:"title"`
		} `json:"notifications"`
	} `json:"observation"`
}

func TestOriginalResetSourceNameActualWorkers(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-reset-name-20261030/source-observations.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []resetSourceCase
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, source := range cases {
		t.Run(source.Case, func(t *testing.T) {
			a := newTestApp(t)
			disableResetFixtureStatistics(t, a)
			t.Cleanup(a.closeOriginalSessions)
			cookie := setupTestAdmin(t, a)
			mutator := installOrderWorker(t, a, cookie, os.Getenv("OWNED_RESET_MUTATOR"))
			observer := installOrderWorker(t, a, cookie, os.Getenv("OWNED_RESET_OBSERVER"))
			trace := filepath.Join(t.TempDir(), "trace.jsonl")
			configure := func(id string, config map[string]any) {
				config["enabled"] = true
				config["trace_path"] = trace
				config["label"] = id
				raw, _ := json.Marshal(config)
				r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
				assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
				a.wg.Wait()
			}
			configure(observer.PluginID, map[string]any{"name_case": "observer"})
			mode := "error"
			if source.Case == "success-with-getter-error" {
				mode = "return"
			}
			var value any
			switch source.Case {
			case "string":
				value = "ordinary source name"
			case "empty":
				value = ""
			}
			configure(mutator.PluginID, map[string]any{"mode": mode, "payload": "null", "name_case": source.Case, "name_value": value})
			before := originalBaseSDKLoaded(t, a, mutator.PluginID)
			other := originalBaseSDKLoaded(t, a, observer.PluginID)
			if err := a.pluginStore.originalBaseSaveData(context.Background(), mutator.PluginID, "marker", json.RawMessage(`"retained-data"`)); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(trace, nil, 0600); err != nil {
				t.Fatal(err)
			}
			var finalData json.RawMessage
			a.pluginOriginalEventsMu.Lock()
			for i, handler := range a.pluginOriginalChainHandlers["plugin.data.reset"] {
				old := handler.Invoke
				a.pluginOriginalChainHandlers["plugin.data.reset"][i].Invoke = func(ctx context.Context, event *plugins.MutableEvent) error {
					err := old(ctx, event)
					finalData = append(json.RawMessage(nil), event.Data...)
					return err
				}
			}
			a.pluginOriginalEventsMu.Unlock()
			response := callTest(t, a.Handler(), "POST", "/api/plugins/"+mutator.PluginID+"/reset", `{}`, cookie, "")
			a.wg.Wait()
			wantSuccess := source.Observation.Success != nil && *source.Observation.Success
			if (response.Code < 400) != wantSuccess {
				t.Fatal("source success mismatch", response.Code, response.Body.String())
			}
			if string(finalData) != string(source.Observation.EventData) {
				t.Fatal("mutation before failure lost", string(finalData))
			}
			rows := readOrderTrace(t, trace)
			got := []string{}
			for _, row := range rows {
				if row.Method == "reset_first" || row.Method == "reset_second" || row.Method == "name_access" {
					got = append(got, row.Method+":"+row.Label)
				}
			}
			want := []string{}
			for _, row := range source.Observation.Trace {
				var fields []json.RawMessage
				_ = json.Unmarshal(row, &fields)
				var method, id string
				_ = json.Unmarshal(fields[0], &method)
				_ = json.Unmarshal(fields[1], &id)
				if method == "reset_first" || method == "reset_second" || method == "name_access" {
					want = append(want, method+":"+id)
				}
			}
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatal("source callback/getter order", got, want)
			}
			if len(rows) < 2 || rows[0].PID <= 0 {
				t.Fatal("no actual process")
			}
			if originalBaseSDKLoaded(t, a, observer.PluginID) != other {
				t.Fatal("observer identity changed")
			}
			if (originalBaseSDKLoaded(t, a, mutator.PluginID) != before) != wantSuccess {
				t.Fatal("reset stop/reload boundary mismatch")
			}
			var count int
			if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_values WHERE instance_id=? AND kind IN ('config','data')`, mutator.PluginID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (wantSuccess && count != 0) || (!wantSuccess && count != 2) {
				t.Fatal("reset config/data mutation", count)
			}
			errorsSeen := 0
			for _, row := range rows {
				if row.Method == "system_error" {
					errorsSeen++
					if strings.Contains(string(row.Input), "owned failure") || !strings.Contains(string(row.Input), "go-sdk-error-code/v1") {
						t.Fatal("unsafe or missing error diagnostic", string(row.Input))
					}
				}
			}
			if errorsSeen != len(source.Observation.Notifications) {
				t.Fatal("actual SystemError count", errorsSeen)
			}
			notice := a.systemMessages.get("system")
			if (len(notice) > 0) != (len(source.Observation.Notifications) > 0) {
				t.Fatal("source notice boundary", string(notice))
			}
			if len(notice) > 0 {
				var n struct {
					Title string `json:"title"`
				}
				if err := json.Unmarshal(notice, &n); err != nil {
					t.Fatal(err)
				}
				wantTitle := strings.ReplaceAll(source.Observation.Notifications[0].Title, "ChainEventType.PluginDataReset", "plugin.data.reset")
				if n.Title != wantTitle {
					t.Fatal("source name value", n.Title, wantTitle)
				}
			}
		})
	}
}

// Invalid/transport failures have no source callback snapshot. They abort the
// checked reset explicitly as a Go boundary, not a fabricated AttributeError.
func TestOriginalResetCheckedBoundaryAndCancellation(t *testing.T) {
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	mutator := installOrderWorker(t, a, cookie, os.Getenv("OWNED_RESET_MUTATOR"))
	observer := installOrderWorker(t, a, cookie, os.Getenv("OWNED_RESET_OBSERVER"))
	trace := filepath.Join(t.TempDir(), "boundary.jsonl")
	configure := func(id, mode, name string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"enabled": true, "mode": mode, "payload": "null", "name_case": name, "name_value": "ordinary", "trace_path": trace, "label": id})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	configure(observer.PluginID, "", "observer")
	for _, mode := range []string{"invalid", "transport-error"} {
		configure(mutator.PluginID, mode, "string")
		before := originalBaseSDKLoaded(t, a, mutator.PluginID)
		if err := os.WriteFile(trace, nil, 0600); err != nil {
			t.Fatal(err)
		}
		r := callTest(t, a.Handler(), "POST", "/api/plugins/"+mutator.PluginID+"/reset", `{}`, cookie, "")
		if r.Code != 400 || !strings.Contains(r.Body.String(), "PLUGIN_EVENT_CALLBACK_BOUNDARY") {
			t.Fatal(mode, r.Code, r.Body.String())
		}
		assertOrderTrace(t, trace, []string{mutator.PluginID + ":reset_first", mutator.PluginID + ":reset_second"})
		if originalBaseSDKLoaded(t, a, mutator.PluginID) != before || len(a.systemMessages.get("system")) != 0 {
			t.Fatal("boundary continued reset/notification")
		}
	}
	configure(mutator.PluginID, "return", "success-with-getter-error")
	before := originalBaseSDKLoaded(t, a, mutator.PluginID)
	if err := os.WriteFile(trace, nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.resetOriginalPluginData(ctx, mutator.PluginID); err == nil {
		t.Fatal("cancelled reset succeeded")
	}
	if originalBaseSDKLoaded(t, a, mutator.PluginID) != before || len(readOrderTrace(t, trace)) != 0 {
		t.Fatal("cancelled callback executed or reset")
	}
}

func TestOriginalResetTypedSendEventChecked(t *testing.T) {
	t.Setenv("OWNED_INPUT_FIXTURE_BINARY", os.Getenv("OWNED_RESET_SENDER"))
	a, cookie := installOwnedInputFixture(t)
	mutator := installOrderWorker(t, a, cookie, os.Getenv("OWNED_RESET_MUTATOR"))
	configure := func(name string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"enabled": true, "mode": "error", "payload": "null", "name_case": name, "name_value": "ordinary"})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+mutator.PluginID, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	configure("missing")
	before := originalBaseSDKLoaded(t, a, mutator.PluginID)
	payload := `{"kind":"chain","type":"plugin.data.reset","data":{"plugin_id":"OwnedResetMutator","reset_config":true,"reset_data":true}}`
	r := ownedInputRequest(t, a, cookie, "OwnedInput", "event.send", payload)
	if r.Code != 403 {
		t.Fatal("typed SendEvent lost secondary exception", r.Code, r.Body.String())
	}
	configure("string")
	r = ownedInputRequest(t, a, cookie, "OwnedInput", "event.send", payload)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var event plugins.MutableEvent
	if err := json.Unmarshal(r.Body.Bytes(), &event); err != nil || string(event.Data) != "null" {
		t.Fatal("typed SendEvent mutation", r.Body.String(), err)
	}
	if originalBaseSDKLoaded(t, a, mutator.PluginID) != before {
		t.Fatal("sending event alone reset object")
	}
	if _, err := a.pluginStore.Config(context.Background(), mutator.PluginID); err != nil {
		t.Fatal("sending event alone deleted config", err)
	}
}
