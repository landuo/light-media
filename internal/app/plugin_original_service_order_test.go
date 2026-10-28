package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginalServiceActualSDKListSourceOrder(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name        string
			Active      bool
			Definitions []struct {
				ID    string
				Delay int64
			}
			Expected []struct {
				ID, Status string
				NextRun    *string `json:"next_run"`
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 2 {
		t.Fatal("fixed list/time source oracle", err)
	}
	a, cookie := installOwnedInputFixture(t)
	reached, release := make(chan struct{}), make(chan struct{})
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(204)
	}))
	defer gate.Close()
	defer close(release)
	definitions := []map[string]any{}
	for _, d := range oracle.Cases[0].Definitions {
		id := strings.TrimPrefix(d.ID, "OwnedInput_")
		input := map[string]any{"mode": "tuple-fail", "progress": 37}
		if id == "running" {
			input["gate"] = gate.URL
		}
		definitions = append(definitions, map[string]any{"id": id, "name": d.ID, "handler": "run_service", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": d.Delay * 1000}, "input": input})
	}
	config, _ := json.Marshal(map[string]any{"enabled": true, "services": definitions})
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(config), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	at := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	a.pluginOriginalDirectJobs.mu.Lock()
	for _, d := range oracle.Cases[0].Definitions {
		a.pluginOriginalDirectJobs.jobs[originalServiceKey{"OwnedInput", strings.TrimPrefix(d.ID, "OwnedInput_")}].due = at.Add(time.Duration(d.Delay) * time.Second)
	}
	a.pluginOriginalDirectJobs.mu.Unlock()
	for _, sample := range oracle.Cases {
		if sample.Active {
			response = callTest(t, a.Handler(), "POST", "/api/plugins/services/run", `{"instance_id":"OwnedInput","service_id":"running"}`, cookie, "")
			assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
			select {
			case <-reached:
			case <-time.After(10 * time.Second):
				t.Fatal("actual SDK did not reach service gate")
			}
		}
		listed := a.originalPublicServices(at)
		if len(listed) != len(sample.Expected) {
			t.Fatal(sample.Name, "incorrect service count", len(listed))
		}
		for i, want := range sample.Expected {
			got := listed[i]
			if got.ID != want.ID || got.Status != want.Status || (got.NextRun == nil) != (want.NextRun == nil) || got.NextRun != nil && *got.NextRun != *want.NextRun {
				t.Fatalf("%s[%d]: got %+v expected %+v", sample.Name, i, got, want)
			}
		}
	}
}

func TestOriginalServiceActualSDKClockOptionsSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/clock-oracle.json")
	var oracle struct {
		Version  string
		Elapsed  float64 `json:"elapsed_seconds"`
		Interval int64   `json:"interval_ms"`
		Cases    []struct {
			Name    string
			Options json.RawMessage
			Calls   int64
			Error   *string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Version != "3.11.3" || len(oracle.Cases) != 8 {
		t.Fatal("actual dependency clock oracle", err)
	}
	a, cookie := installOwnedInputFixture(t)
	var calls atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer peer.Close()
	for _, sample := range oracle.Cases {
		before := calls.Load()
		now := time.Now().UTC()
		anchor := now.Add(-time.Duration(oracle.Elapsed * float64(time.Second)))
		config, _ := json.Marshal(map[string]any{"enabled": true, "services": []map[string]any{{"id": "options", "name": "Owned options", "handler": "run_service", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": oracle.Interval, "start_at": anchor.Format(time.RFC3339Nano)}, "kwargs": sample.Options, "input": map[string]any{"mode": "list-fail", "gate": peer.URL, "progress": 37}}}})
		response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(config), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
		a.pluginOriginalDirectJobs.mu.Lock()
		job := a.pluginOriginalDirectJobs.jobs[originalServiceKey{"OwnedInput", "options"}]
		if sample.Error == nil {
			if !bytes.Contains(sample.Options, []byte(`"next_run_time": null`)) {
				job.due = anchor
			}
		} else if !job.due.IsZero() {
			a.pluginOriginalDirectJobs.mu.Unlock()
			t.Fatal("invalid source options registered a clock", sample.Name)
		}
		a.pluginOriginalDirectJobs.mu.Unlock()
		a.dispatchOriginalPluginJobs(context.Background(), now)
		a.wg.Wait()
		if got := calls.Load() - before; got != sample.Calls {
			t.Fatalf("%s: actual SDK calls %d, fixed dependency %d", sample.Name, got, sample.Calls)
		}
	}
}

func TestOriginalServiceActualSDKPausedSourceFailure(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/source-oracle.json")
	var oracle struct {
		Paused []struct {
			Name     string
			Delays   []*int64
			Error    *string
			Expected []struct {
				ID, Status string
				NextRun    *string `json:"next_run"`
			}
		} `json:"paused_cases"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Paused) != 2 {
		t.Fatal("fixed paused list oracle", err)
	}
	a, cookie := installOwnedInputFixture(t)
	for _, sample := range oracle.Paused {
		defs := []map[string]any{}
		for i, delay := range sample.Delays {
			opts := map[string]any{}
			if delay == nil {
				opts["next_run_time"] = nil
			}
			defs = append(defs, map[string]any{"id": strconv.Itoa(i), "name": strconv.Itoa(i), "handler": "run_service", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000}, "kwargs": opts})
		}
		config, _ := json.Marshal(map[string]any{"enabled": true, "services": defs})
		response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(config), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
		response = callTest(t, a.Handler(), "GET", "/api/v1/dashboard/schedule", "", cookie, "")
		if sample.Error != nil {
			if response.Code != 500 {
				t.Fatal(sample.Name, "source sort failure was hidden", response.Code)
			}
			continue
		}
		var listed struct{ Data []PluginServiceInfo }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &listed) != nil || len(listed.Data) != 1 || listed.Data[0].NextRun == nil || *listed.Data[0].NextRun != *sample.Expected[0].NextRun {
			t.Fatal("single paused timer source projection", response.Code, response.Body.String())
		}
		response = callTest(t, a.Handler(), "GET", "/api/v1/system/runscheduler?jobid=OwnedInput_0", "", cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		if string(a.originalServiceProgressSnapshot("OwnedInput_0")["status"].(json.RawMessage)) != `"success"` {
			t.Fatal("paused clock lost its manually callable method")
		}
	}
}

func TestOriginalServiceNextTimeDependencyConversion(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/clock-oracle.json")
	var oracle struct {
		Cases []struct {
			Input  json.RawMessage
			Output *string
			Error  *string
		} `json:"next_run_cases"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 5 {
		t.Fatal("fixed dependency datetime oracle", err)
	}
	for _, sample := range oracle.Cases {
		definition := pluginScheduleDefinition{SchedulerOptions: json.RawMessage(`{"next_run_time":` + string(sample.Input) + `}`)}
		definition.Trigger.Timezone = "Asia/Shanghai"
		options, err := originalServiceOptions(&definition)
		if sample.Error != nil {
			if err == nil {
				t.Fatal("source invalid time accepted", string(sample.Input))
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if sample.Output == nil {
			if !options.nextRun.IsZero() || !options.hasNextRun {
				t.Fatal("paused source option lost")
			}
			continue
		}
		want, err := time.Parse(time.RFC3339Nano, *sample.Output)
		if err != nil || !want.Equal(options.nextRun) {
			t.Fatal("source datetime conversion differs", string(sample.Input), options.nextRun, sample.Output)
		}
	}
}
