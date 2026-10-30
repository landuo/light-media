package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/plugins"
	"os"
	"strings"
	"sync"
	"testing"
)

func systemConfigEvents(t *testing.T, p *originalPluginInstance) []json.RawMessage {
	t.Helper()
	s := sharedSystemConfigWorkerSnapshot(t, p)
	var out []json.RawMessage
	if e := json.Unmarshal(s["events"], &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestSharedSystemConfigUICloneEventsAndConcurrency(t *testing.T) {
	a, cookie, p := sharedSystemConfigFixture(t, sharedSystemConfigAll)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedSystemConfig/clone", `{"suffix":"Peer"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	v, ok := a.pluginRunningInstances.Load("OwnedSystemConfigpeer")
	if !ok {
		t.Fatal("real clone missing")
	}
	peer := v.(*originalPluginInstance)
	if bytes.Equal(sharedSystemConfigWorkerSnapshot(t, p)["pid"], sharedSystemConfigWorkerSnapshot(t, peer)["pid"]) {
		t.Fatal("clone shares process")
	}
	if _, e := systemConfigSet(t, p, `["Raw => Plugin"]`); e != nil {
		t.Fatal(e)
	}
	assertSharedOracleJSON(t, systemConfigGet(t, peer), json.RawMessage(`["Raw => Plugin"]`))
	a.wg.Wait()
	if len(systemConfigEvents(t, p)) != 0 {
		t.Fatal("sync source setter invented event")
	}
	response = callTest(t, a.Handler(), "PUT", "/api/settings/recognition-words", `{"text":"Raw => UI\n\n# keep\n  "}`, cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	a.wg.Wait()
	assertSharedOracleJSON(t, systemConfigGet(t, p), json.RawMessage(`["Raw => UI","# keep","  "]`))
	if got := a.prepareRecognitionTitle("Raw.S01E02"); got != "UI.S01E02" {
		t.Fatal(got)
	}
	for _, obj := range []*originalPluginInstance{p, peer} {
		events := systemConfigEvents(t, obj)
		if len(events) != 1 {
			t.Fatalf("config.updated count=%d", len(events))
		}
		var event plugins.MutableEvent
		if e := json.Unmarshal(events[0], &event); e != nil || event.Type != "config.updated" {
			t.Fatal(string(events[0]), e)
		}
		assertSharedOracleJSON(t, event.Data, json.RawMessage(`{"key":"CustomIdentifiers","value":["Raw => UI","# keep","  "],"change_type":"update"}`))
	}
	response = callTest(t, a.Handler(), "PUT", "/api/settings/recognition-words", `{"text":"Raw => UI\n\n# keep\n  "}`, cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code)
	}
	a.wg.Wait()
	if len(systemConfigEvents(t, p)) != 1 {
		t.Fatal("equal async set emitted event")
	}
	// Independent worker callers share the singleton lock, not per-plugin copies.
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			worker := p
			if i%2 == 1 {
				worker = peer
			}
			raw, _ := json.Marshal([]string{fmt.Sprintf("Raw => Concurrent%d", i)})
			result, e := systemConfigSet(t, worker, string(raw))
			if e != nil {
				failures <- e
			} else if string(result) != "true" {
				failures <- fmt.Errorf("distinct concurrent set=%s", result)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
	current := systemConfigGet(t, p)
	assertSharedOracleJSON(t, systemConfigGet(t, peer), current)
	assertSystemConfigStored(t, a, []json.RawMessage{current})
	_, rules, e := a.recognitionWords(context.Background())
	if e != nil || len(rules) != 1 || !strings.HasPrefix(rules[0], "Raw => Concurrent") {
		t.Fatal(rules, e)
	}
	for _, key := range []string{"", "Notifications", "Downloaders", "UserInstalledPlugins", "plugin.OwnedSystemConfig", "CustomIdentifiers\x00"} {
		for _, method := range sharedSystemConfigAll {
			_, e := sharedSystemConfigCall(t, p, method, map[string]any{"key": key, "value": nil})
			if method == plugins.SharedSystemConfigGet {
				_, e = sharedSystemConfigCall(t, p, method, plugins.SharedSystemConfigGetV1{Key: key})
			}
			if e == nil {
				t.Fatalf("ungranted key %q through %s", key, method)
			}
		}
	}
	assertSharedOracleJSON(t, systemConfigGet(t, p), current)
	if got := callTest(t, a.Handler(), "PUT", "/api/settings/recognition-words", `{"text":"unauthenticated"}`, nil, ""); got.Code != 401 {
		t.Fatal("UI bypassed auth", got.Code)
	}
}
func TestSharedSystemConfigCapabilityAndStale(t *testing.T) {
	a, _, p := sharedSystemConfigFixture(t, []string{plugins.SharedSystemConfigGet})
	if _, e := systemConfigSet(t, p, `["forbidden"]`); e == nil {
		t.Fatal("read grant allowed write")
	}
	assertSystemConfigStored(t, a, nil)
	_ = sharedSystemConfigWorkerSnapshot(t, p)
	snapshot := sharedSystemConfigWorkerSnapshot(t, p)
	if string(snapshot["had_previous"]) != "true" || string(snapshot["stale_denied"]) != "true" {
		t.Fatal(snapshot)
	}
}
func TestSharedSystemConfigInvalidConsumerAndLargeCold(t *testing.T) {
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSystemConfigFixture(t, a, cookie, sharedSystemConfigAll)
	for _, raw := range []string{`true`, `42`, `[true]`, `[{"not":"word"}]`} {
		if _, e = systemConfigSet(t, p, raw); e != nil {
			t.Fatal(e)
		}
		assertSharedOracleJSON(t, systemConfigGet(t, p), json.RawMessage(raw))
		response := callTest(t, a.Handler(), "POST", "/api/search/resources", `{"query":"owned synthetic","site_ids":[]}`, cookie, "")
		if response.Code != 400 || !strings.Contains(response.Body.String(), "SYSTEM_CONFIG_CONSUMER_") {
			t.Fatalf("native error-aware search did not fail before search: %d %s", response.Code, response.Body)
		}
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
		a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
		if e != nil {
			t.Fatal("valid raw config prevented cold startup", e)
		}
		p = a.startOriginalPlugin(context.Background(), "OwnedSystemConfig")
		if p == nil {
			t.Fatal("cold worker")
		}
		assertSharedOracleJSON(t, systemConfigGet(t, p), json.RawMessage(raw))
	}
	rules := []string{}
	for i := 0; i < 24; i++ {
		rules = append(rules, "#"+strings.Repeat("x", 30<<10))
	}
	rules = append(rules, "Raw => Large")
	// Expected native title is captured from two fresh original DAO/WordsMatcher processes.
	sourceRaw, e := os.ReadFile("../../testdata/plugin-shared-host-20261030/system-config/source/large-observation.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Records []struct {
			Method   string          `json:"method"`
			Returned json.RawMessage `json:"returned"`
		} `json:"records"`
	}
	if e = json.Unmarshal(sourceRaw, &source); e != nil {
		t.Fatal(e)
	}
	wantTitle := ""
	for _, row := range source.Records {
		if row.Method == "prepare" {
			var result []json.RawMessage
			if json.Unmarshal(row.Returned, &result) != nil || len(result) != 2 {
				t.Fatal("source large consumer result")
			}
			if e = json.Unmarshal(result[0], &wantTitle); e != nil {
				t.Fatal(e)
			}
		}
	}
	if wantTitle == "" {
		t.Fatal("source large title absent")
	}
	large, _ := json.Marshal(rules)
	if len(large) < 700<<10 {
		t.Fatal("large fixture too small")
	}
	if _, e = systemConfigSet(t, p, string(large)); e != nil {
		t.Fatal(e)
	}
	assertSharedOracleJSON(t, systemConfigGet(t, p), large)
	if got := a.prepareRecognitionTitle("Raw.S01E02"); got != wantTitle {
		t.Fatal(got)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	p = a.startOriginalPlugin(context.Background(), "OwnedSystemConfig")
	assertSharedOracleJSON(t, systemConfigGet(t, p), large)
	if got := a.prepareRecognitionTitle("Raw.S01E02"); got != wantTitle {
		t.Fatal(got)
	}
	t.Logf("actual 700KiB+ rules roundtrip and cold bytes=%d", len(large))
}
