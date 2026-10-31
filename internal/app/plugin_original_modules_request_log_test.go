package app

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

//go:embed testdata/shared-torrents/browse-module-log-source.json
var browseModuleLogSource []byte

type browseModuleLogObservation struct {
	ID    string `json:"id"`
	Input struct {
		Providers []struct {
			ID, Name, Error string
			Result          json.RawMessage
		} `json:"providers"`
	} `json:"input"`
	Outcomes []struct {
		Result    json.RawMessage
		Exception json.RawMessage
	} `json:"outcomes"`
	Trace []struct {
		Kind, Level, Message, ID string
		Kwargs                   json.RawMessage
		Value                    json.RawMessage
	} `json:"trace"`
}

func TestOriginalModulesRequestLogPrecedesTerminalCheck(t *testing.T) {
	var doc struct {
		OriginalSHA string                       `json:"original_stdout_sha256"`
		Cases       []browseModuleLogObservation `json:"cases"`
	}
	if err := json.Unmarshal(browseModuleLogSource, &doc); err != nil || doc.OriginalSHA != "0b0db6fae01aae3ee16ebeacd378a0551a6311d2b90774336077e7a97d035ad4" || len(doc.Cases) != 3 {
		t.Fatal("fixed original three-case projection required", err)
	}
	sum := sha256.Sum256(browseModuleLogSource)
	if hex.EncodeToString(sum[:]) != "3031719c5f5a6af16fca446575850748a50e38420d2aed2135629a73bb1eb129" {
		t.Fatal("selected original fixture bytes changed")
	}
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	manifest := installOrderWorker(t, a, cookie, os.Getenv("OWNED_MODULE_OVERRIDE_BINARY"))
	if manifest.PluginID != ownedModuleID || !slices.Contains(manifest.Methods, "get_module") {
		t.Fatal("actual owned module override artifact required")
	}
	objects := []*originalPluginInstance{originalBaseSDKLoaded(t, a, ownedModuleID)}
	for i := 1; i < 5; i++ {
		objects = append(objects, ownedModuleClone(t, a, cookie, fmt.Sprintf("requestlog%d", i)))
	}
	logs := func() []map[string]any {
		t.Helper()
		page, err := a.queryLogs(context.Background(), map[string]string{"from": "0001-01-01T00:00:00Z", "limit": "500"})
		if err != nil || page.Gap {
			t.Fatal("actual log history unavailable", err, page.Gap)
		}
		return page.Entries
	}
	for _, observed := range doc.Cases {
		t.Run(observed.ID, func(t *testing.T) {
			if len(observed.Outcomes) != 1 || string(observed.Outcomes[0].Exception) != "null" || len(observed.Input.Providers) > len(objects) {
				t.Fatal("unexpected original case shape")
			}
			groups := []originalPluginModuleGroup{}
			before := make([]ownedModuleSnapshot, len(observed.Input.Providers))
			handlers := make([]string, len(observed.Input.Providers))
			for i, provider := range observed.Input.Providers {
				handler := "module_execute"
				if provider.Error != "" {
					handler = "module_fail"
				}
				config, err := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"refresh_torrents": handler}, "result": provider.Result})
				if err != nil {
					t.Fatal(err)
				}
				saveOwnedModule(t, a, cookie, objects[i], string(config))
				bindings, _ := json.Marshal(map[string]string{"refresh_torrents": handler})
				// Only dispatcher behavior is under test. Keep the real loaded
				// object/ID; use the source's owned display label for exact text.
				groups = append(groups, originalPluginModuleGroup{PluginID: objects[i].installation.InstanceID, PluginName: provider.Name, Modules: bindings, object: objects[i]})
				before[i], handlers[i] = ownedModuleRead(t, objects[i]), handler
			}
			wantLogs := []string{}
			wantCalls := map[string]int{}
			wantFailed := 0
			var kwargs json.RawMessage
			var coreTail []json.RawMessage
			for _, event := range observed.Trace {
				if event.Kind == "log" && event.Level == "INFO" && strings.HasPrefix(event.Message, "请求插件 ") {
					wantLogs = append(wantLogs, event.Message)
				}
				if event.Kind == "provider_call" {
					wantCalls[event.ID]++
					if kwargs == nil {
						kwargs = event.Kwargs
					}
				}
				if event.Kind == "log" && event.Level == "ERROR" {
					wantFailed++
				}
				if event.Kind == "core_search_return" {
					if err := json.Unmarshal(event.Value, &coreTail); err != nil {
						t.Fatal(err)
					}
				}
			}
			wantResult := observed.Outcomes[0].Result
			if coreTail != nil {
				// The source collector includes its recording core. This gate
				// is the real plugin half only: remove that observed appended tail,
				// not a newly implemented reference aggregation algorithm.
				var full []json.RawMessage
				if json.Unmarshal(wantResult, &full) != nil || len(full) < len(coreTail) {
					t.Fatal("source core-tail projection")
				}
				for i := range coreTail {
					if !originalBaseJSONEqual(full[len(full)-len(coreTail)+i], coreTail[i]) {
						t.Fatal("source core tail differs")
					}
				}
				wantResult, _ = json.Marshal(full[:len(full)-len(coreTail)])
			}
			seen := map[string]bool{}
			for _, row := range logs() {
				seen[row["id"].(string)] = true
			}
			var attempts originalModuleAttempts
			result, err := a.invokeOriginalPluginModuleGroups(context.Background(), "refresh_torrents", json.RawMessage(`null`), json.RawMessage(`[]`), kwargs, &attempts, groups)
			if err != nil || !originalBaseJSONEqual(result, wantResult) {
				t.Fatalf("actual plugin half %s want %s error %v", result, wantResult, err)
			}
			totalCalls := 0
			for i, provider := range observed.Input.Providers {
				after := ownedModuleRead(t, objects[i])
				count := after.Calls[handlers[i]] - before[i].Calls[handlers[i]]
				if count != wantCalls[provider.ID] || after.PID != before[i].PID || after.Calls["init"] != before[i].Calls["init"] {
					t.Fatalf("actual worker call/reuse %s count=%d want=%d before=%+v after=%+v", provider.ID, count, wantCalls[provider.ID], before[i], after)
				}
				totalCalls += count
			}
			if attempts.Called != totalCalls || attempts.Failed != wantFailed {
				t.Fatalf("attempt counters mixed request logs with RPC/errors: %+v calls=%d failed=%d", attempts, totalCalls, wantFailed)
			}
			gotLogs := []string{}
			gotFailed, gotLimited := 0, 0
			for _, row := range logs() {
				if seen[row["id"].(string)] {
					continue
				}
				switch row["event"] {
				case "module_request":
					if row["level"] != "INFO" {
						t.Fatal("request log level", row)
					}
					gotLogs = append(gotLogs, row["message"].(string))
				case "module_execution_failed":
					gotFailed++
				case "module_rate_limited":
					gotLimited++
				}
			}
			slices.Reverse(gotLogs) // Real queryLogs history is newest first.
			if gotFailed != wantFailed || gotLimited != 0 {
				t.Fatalf("failure/rate-limit events changed: failed=%d want=%d limited=%d", gotFailed, wantFailed, gotLimited)
			}
			if !reflect.DeepEqual(gotLogs, wantLogs) {
				t.Fatalf("request INFO must precede terminal check: got %v want %v; actual RPC=%d", gotLogs, wantLogs, totalCalls)
			}
		})
	}
}
