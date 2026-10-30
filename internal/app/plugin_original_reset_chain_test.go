package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalResetChainActualWorkers(t *testing.T) {
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	mutator := installOrderWorker(t, a, cookie, os.Getenv("OWNED_RESET_MUTATOR"))
	observer := installOrderWorker(t, a, cookie, os.Getenv("OWNED_RESET_OBSERVER"))
	trace := filepath.Join(t.TempDir(), "reset-chain.jsonl")
	configure := func(id, mode, data string) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"enabled": true, "trace_path": trace, "label": id, "mode": mode, "payload": data})
		if err != nil {
			t.Fatal(err)
		}
		response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
	}
	configure(observer.PluginID, "", "null")
	first := `{"plugin_id":"changed-event-only","reset_config":false,"reset_data":false,"large":9007199254740993123456789,"extra":{"nested":[null,false,0,"owned"]}}`
	for _, data := range []string{`null`, `false`, `123456789012345678901234567890`, `"scalar"`, `[null,9007199254740993123456789,false]`, `{"reset_config":"no boolean restriction","reset_data":17,"plugin_id":[false],"extra":{"nested":null}}`} {
		for _, mode := range []string{"return", "none", "ordinary", "error", "panic", "invalid", "transport-error"} {
			t.Run(mode+"/"+data, func(t *testing.T) {
				configure(mutator.PluginID, mode, data)
				if err := os.WriteFile(trace, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if !a.sendOriginalPluginDataReset(context.Background(), mutator.PluginID) {
					t.Fatal("no actual subscribers")
				}
				assertOrderTrace(t, trace, []string{mutator.PluginID + ":reset_first", mutator.PluginID + ":reset_second", observer.PluginID + ":reset_first", observer.PluginID + ":reset_second"})
				rows := readOrderTrace(t, trace)
				if rows[0].PID == rows[2].PID {
					t.Fatal("test did not use independent worker processes")
				}
				var second plugins.MutableEvent
				if err := json.Unmarshal(rows[1].Input, &second); err != nil {
					t.Fatal(err)
				}
				if string(second.Data) != first {
					t.Fatalf("first mutation lost: %s", rows[1].Input)
				}
				want := data
				if mode == "none" || mode == "ordinary" || mode == "invalid" || mode == "transport-error" {
					want = first
				}
				for _, row := range rows[2:] {
					var event plugins.MutableEvent
					if err := json.Unmarshal(row.Input, &event); err != nil {
						t.Fatal(err)
					}
					if event.Type != "plugin.data.reset" || string(event.Data) != want {
						t.Fatalf("arbitrary shared JSON lost: got=%s want=%s", row.Input, want)
					}
				}
			})
		}
	}
	// The endpoint ignores the chain result, including arbitrary final data.
	configure(mutator.PluginID, "error", "null")
	beforeSource := originalBaseSDKLoaded(t, a, mutator.PluginID)
	beforeObserver := originalBaseSDKLoaded(t, a, observer.PluginID)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+mutator.PluginID+"/reset", `{}`, cookie, "")
	if response.Code < 400 || originalBaseSDKLoaded(t, a, mutator.PluginID) != beforeSource {
		t.Fatal("missing source name did not abort HTTP reset", response.Code, response.Body.String())
	}
	configure(mutator.PluginID, "return", "null")
	beforeSource = originalBaseSDKLoaded(t, a, mutator.PluginID)
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+mutator.PluginID+"/reset", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	if originalBaseSDKLoaded(t, a, mutator.PluginID) == beforeSource {
		t.Fatal("arbitrary data skipped source reset")
	}
	if originalBaseSDKLoaded(t, a, observer.PluginID) != beforeObserver {
		t.Fatal("reset affected observer")
	}
	var sourceConfig, observerConfig int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind='config'`, mutator.PluginID).Scan(&sourceConfig); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind='config'`, observer.PluginID).Scan(&observerConfig); err != nil {
		t.Fatal(err)
	}
	if sourceConfig != 0 || observerConfig == 0 {
		t.Fatalf("source caller changed: source=%d observer=%d", sourceConfig, observerConfig)
	}
}
