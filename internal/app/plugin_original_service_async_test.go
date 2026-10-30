package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestOriginalServiceActualSDKAsyncReplacementSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/async-oracle.json")
	var oracle struct {
		Result struct {
			Early    bool `json:"returned_before_business_release"`
			AfterOld struct {
				CurrentRunning, OldRunning bool
				Progress                   struct {
					Value float64
					Text  string
					Data  struct {
						Name, Status, Error string
						Success             bool
					}
				}
			} `json:"after_old_completion"`
			AfterNew struct {
				Progress struct {
					Value float64
					Data  struct {
						Status  string
						Success bool
					}
				}
			} `json:"after_new_completion"`
		}
	}
	// Source snake-case fields are intentionally explicit rather than a freshly
	// invented invariant that an old completion must not affect a new job.
	var source struct {
		Result struct {
			AfterOld struct {
				CurrentRunning bool `json:"current_running"`
				OldRunning     bool `json:"old_running"`
			} `json:"after_old_completion"`
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || json.Unmarshal(raw, &source) != nil || !oracle.Result.Early {
		t.Fatal("fixed coroutine source replay", err)
	}
	a, cookie := installOwnedInputFixture(t)
	oldReached, newReached, oldRelease, newRelease := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var oldOnce, newOnce sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gate := oldRelease
		if r.URL.Path == "/new" {
			newOnce.Do(func() { close(newReached) })
			gate = newRelease
		} else {
			oldOnce.Do(func() { close(oldReached) })
		}
		select {
		case <-gate:
		case <-r.Context().Done():
		}
		w.WriteHeader(204)
	}))
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
	configure := func(name, mode, path string, value int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"enabled": true, "services": []map[string]any{{"id": "shared", "name": name, "handler": "run_service", "async": true, "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000}, "input": map[string]any{"mode": mode, "gate": peer.URL + path, "progress": value}}}})
		response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	submit := func() {
		t.Helper()
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- callTest(t, a.Handler(), "GET", "/api/v1/system/runscheduler?jobid=OwnedInput_shared", "", cookie, "")
		}()
		select {
		case response := <-done:
			assertOriginalServiceWireSuccess(t, response.Code, response.Body.Bytes())
		case <-time.After(2 * time.Second):
			t.Fatal("coroutine-mapped method did not acknowledge before real SDK completion")
		}
	}
	wait := func(signal <-chan struct{}) {
		t.Helper()
		select {
		case <-signal:
		case <-time.After(5 * time.Second):
			t.Fatal("actual coroutine-mapped worker did not reach held side effect")
		}
	}
	configure("Old", "tuple-fail", "/old", 37)
	submit()
	wait(oldReached)
	a.pluginOriginalDirectJobs.mu.Lock()
	oldJob := a.pluginOriginalDirectJobs.jobs[originalServiceKey{"OwnedInput", "shared"}]
	a.pluginOriginalDirectJobs.mu.Unlock()
	configure("New", "list-fail", "/new", 51)
	submit()
	wait(newReached)
	close(oldRelease)
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot := a.originalServiceProgressSnapshot("OwnedInput_shared")
		if snapshot != nil && string(snapshot["status"].(json.RawMessage)) == `"failed"` {
			value := snapshot["value"].(float64)
			if value != oracle.Result.AfterOld.Progress.Value || string(snapshot["name"].(json.RawMessage)) != `"`+oracle.Result.AfterOld.Progress.Data.Name+`"` || snapshot["text"] != oracle.Result.AfterOld.Progress.Text {
				t.Fatal("old completion did not preserve source logical-ID behavior", snapshot)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old actual SDK result did not finish shared progress")
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.pluginOriginalDirectJobs.mu.Lock()
	current := a.pluginOriginalDirectJobs.jobs[originalServiceKey{"OwnedInput", "shared"}]
	currentRunning, oldRunning := current.running, oldJob.running
	a.pluginOriginalDirectJobs.mu.Unlock()
	if currentRunning != source.Result.AfterOld.CurrentRunning || oldRunning != source.Result.AfterOld.OldRunning {
		t.Fatal("source job dictionary replacement was silently changed", currentRunning, oldRunning)
	}
	listed := a.originalPublicServices(time.Now())
	if len(listed) != 1 || !listed[0].CanRun {
		t.Fatal("source old-completion running flag not reflected in list")
	}
	close(newRelease)
	a.wg.Wait()
	snapshot := a.originalServiceProgressSnapshot("OwnedInput_shared")
	if string(snapshot["status"].(json.RawMessage)) != `"`+oracle.Result.AfterNew.Progress.Data.Status+`"` || snapshot["value"].(float64) != oracle.Result.AfterNew.Progress.Value {
		t.Fatal("new actual completion did not replace old shared progress", snapshot)
	}
}

func TestOriginalServiceActualSDKExecutorInstancesReplacement(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/instances-oracle.json")
	var oracle struct {
		Version string
		Cases   []struct {
			Maximum  int
			Observed struct {
				Old, New bool
				Active   int
			} `json:"observed_while_old_held"`
			Calls map[string]int
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Version != "3.11.3" || len(oracle.Cases) != 2 {
		t.Fatal("actual executor admission oracle", err)
	}
	a, cookie := installOwnedInputFixture(t)
	for _, sample := range oracle.Cases {
		t.Run(strconv.Itoa(sample.Maximum), func(t *testing.T) {
			oldReached, newReached, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var mu sync.Mutex
			calls := map[string]int{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls[r.URL.Path]++
				mu.Unlock()
				if r.URL.Path == "/old" {
					close(oldReached)
				} else {
					close(newReached)
				}
				select {
				case <-release:
				case <-r.Context().Done():
				}
				w.WriteHeader(204)
			}))
			defer peer.Close()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			configure := func(name, path string) {
				body, _ := json.Marshal(map[string]any{"enabled": true, "services": []map[string]any{{"id": "limit", "name": name, "handler": "run_service", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000}, "kwargs": map[string]any{"max_instances": sample.Maximum}, "input": map[string]any{"mode": "list-fail", "gate": peer.URL + path, "progress": 37}}}})
				response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(body), cookie, "")
				assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
			}
			dispatch := func() {
				at := time.Now()
				a.pluginOriginalDirectJobs.mu.Lock()
				a.pluginOriginalDirectJobs.jobs[originalServiceKey{"OwnedInput", "limit"}].due = at
				a.pluginOriginalDirectJobs.mu.Unlock()
				a.dispatchOriginalPluginJobs(context.Background(), at)
			}
			configure("Old", "/old")
			dispatch()
			select {
			case <-oldReached:
			case <-time.After(5 * time.Second):
				t.Fatal("old actual executor method not held")
			}
			configure("New", "/new")
			dispatch()
			if sample.Observed.New {
				select {
				case <-newReached:
				case <-time.After(5 * time.Second):
					t.Fatal("max_instances did not admit the real new method")
				}
			}
			a.pluginOriginalDirectJobs.mu.Lock()
			active := a.pluginOriginalDirectJobs.clockInstances["OwnedInput_limit"]
			a.pluginOriginalDirectJobs.mu.Unlock()
			if active != sample.Observed.Active {
				t.Fatal("actual clock executor limit was only parsed", active, sample.Observed.Active)
			}
			close(release)
			a.wg.Wait()
			mu.Lock()
			oldCount, newCount := calls["/old"], calls["/new"]
			mu.Unlock()
			if oldCount != sample.Calls["old"] || newCount != sample.Calls["new"] {
				t.Fatal("actual SDK execution differs from fixed executor", oldCount, newCount, sample.Calls)
			}
		})
	}
}

func TestOriginalServiceArgumentsFixedStartSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/async-oracle.json")
	var oracle struct {
		Cases []struct {
			Input  json.RawMessage
			Calls  []json.RawMessage
			Errors []string
		} `json:"argument_cases"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 7 {
		t.Fatal("fixed start kwargs conversion oracle", err)
	}
	for _, sample := range oracle.Cases {
		got, err := originalServiceArguments(sample.Input)
		if len(sample.Errors) > 0 {
			if err == nil {
				t.Fatal("source rejected argument was accepted", string(sample.Input))
			}
			continue
		}
		if err != nil || len(sample.Calls) != 1 || !originalBaseJSONEqual(got, sample.Calls[0]) {
			t.Fatal("ordinary method kwargs conversion differs", string(sample.Input), string(got), err)
		}
	}
}

func TestOriginalServiceProgressFixedModelProjection(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/progress-oracle.json")
	var oracle struct {
		Cases []struct {
			Name   string
			Input  map[string]any
			Output json.RawMessage
			Error  *string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 5 {
		t.Fatal("fixed ScheduleProgress model oracle", err)
	}
	for _, sample := range oracle.Cases {
		err := normalizeOriginalServiceProgressProjection(sample.Input)
		if sample.Error != nil {
			if err == nil {
				t.Fatal("source-invalid progress was emitted", sample.Name)
			}
			continue
		}
		got, _ := json.Marshal(sample.Input)
		if err != nil || !originalBaseJSONEqual(got, sample.Output) {
			t.Fatal("fixed progress model differs", sample.Name, string(got), string(sample.Output), err)
		}
	}
}
