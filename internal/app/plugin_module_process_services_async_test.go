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

func verifyOwnedProductionAsyncServices(t *testing.T,
	request func(string, string, any, int) []byte, success func(string, string, any),
	configure func(string, []map[string]any), pid func(string) int, restore []map[string]any) {
	t.Helper()
	sourceOracle := os.Getenv("PLUGIN_PROCESS_SERVICE_ASYNC_ORACLE")
	if sourceOracle == "" {
		sourceOracle = "../../testdata/standard-services/async-oracle.json"
	}
	raw, err := os.ReadFile(sourceOracle)
	var oracle struct {
		Result struct {
			Early    bool `json:"returned_before_business_release"`
			AfterOld struct {
				Progress struct {
					Value float64
					Text  string
					Data  struct{ Name, Status string }
				}
			} `json:"after_old_completion"`
			AfterNew struct {
				Progress struct {
					Value float64
					Data  struct{ Status string }
				}
			} `json:"after_new_completion"`
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || !oracle.Result.Early {
		t.Fatal("fixed coroutine source oracle", err)
	}
	paths := []string{"/old", "/new", "/reload"}
	release := map[string]chan struct{}{}
	events := map[string]chan int{}
	counts := map[string]int{}
	var mu sync.Mutex
	for _, path := range paths {
		release[path] = make(chan struct{})
		events[path] = make(chan int, 4)
	}
	canceled := make(chan struct{}, 2)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gate, ok := release[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		counts[r.URL.Path]++
		count := counts[r.URL.Path]
		mu.Unlock()
		events[r.URL.Path] <- count
		select {
		case <-gate:
		case <-r.Context().Done():
			if r.URL.Path == "/reload" {
				canceled <- struct{}{}
			}
		}
		w.WriteHeader(204)
	}))
	defer peer.Close()
	defer func() {
		for _, gate := range release {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
	}()
	definition := func(name, mode, path string, value int) map[string]any {
		return map[string]any{"id": "async", "name": name, "handler": "run_service", "async": true, "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000}, "input": map[string]any{"mode": mode, "gate": peer.URL + path, "progress": value}}
	}
	progress := func() map[string]json.RawMessage {
		t.Helper()
		var value struct {
			Success bool
			Data    map[string]json.RawMessage
		}
		if json.Unmarshal(request("GET", "/api/v1/dashboard/schedule/OwnedInput_async/progress", nil, 200), &value) != nil || !value.Success {
			t.Fatal("actual asynchronous progress unavailable")
		}
		return value.Data
	}
	waitStatus := func(status string) map[string]json.RawMessage {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			value := progress()
			if string(value["status"]) == `"`+status+`"` {
				return value
			}
			if time.Now().After(deadline) {
				t.Fatal("actual async state did not settle", status)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	submit := func() {
		t.Helper()
		at := time.Now()
		request("GET", "/api/v1/system/runscheduler?jobid=OwnedInput_async", nil, 200)
		if time.Since(at) > 2*time.Second {
			t.Fatal("async compatibility API waited for business completion")
		}
	}
	waitGate := func(path string, count int) {
		t.Helper()
		select {
		case got := <-events[path]:
			if got != count {
				t.Fatal("actual async side effect count", path, got, count)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("actual async SDK did not reach held side effect", path)
		}
	}
	firstPID := pid("OwnedInput")
	configure("OwnedInput", []map[string]any{definition("Old", "tuple-fail", "/old", 37)})
	submit()
	waitGate("/old", 1)
	if string(progress()["status"]) != `"running"` {
		t.Fatal("async acknowledgement claimed completion")
	}
	configure("OwnedInput", []map[string]any{definition("New", "list-fail", "/new", 51)})
	submit()
	waitGate("/new", 1)
	close(release["/old"])
	oldFinished := waitStatus("failed")
	var value float64
	_ = json.Unmarshal(oldFinished["value"], &value)
	if value != oracle.Result.AfterOld.Progress.Value || string(oldFinished["name"]) != `"`+oracle.Result.AfterOld.Progress.Data.Name+`"` || string(oldFinished["text"]) != `"`+oracle.Result.AfterOld.Progress.Text+`"` {
		t.Fatal("actual old result changed source same-ID completion semantics")
	}
	var listed struct{ Data []PluginServiceInfo }
	if json.Unmarshal(request("GET", "/api/plugins/services", nil, 200), &listed) != nil {
		t.Fatal("actual async list invalid")
	}
	canRun := false
	for _, item := range listed.Data {
		if item.ID == "OwnedInput_async" {
			canRun = item.CanRun
		}
	}
	if !canRun {
		t.Fatal("source old completion must clear current logical running flag")
	}
	close(release["/new"])
	newFinished := waitStatus(oracle.Result.AfterNew.Progress.Data.Status)
	_ = json.Unmarshal(newFinished["value"], &value)
	if value != oracle.Result.AfterNew.Progress.Value || pid("OwnedInput") != firstPID {
		t.Fatal("same-object reinit/result changed actual SDK state")
	}
	// Go reload revokes the old worker's ordinary capabilities. The shared host
	// progress remains logical-ID keyed; no new generation veto is invented.
	configure("OwnedInput", []map[string]any{definition("Before reload", "tuple-fail", "/reload", 37)})
	submit()
	waitGate("/reload", 1)
	success("POST", "/api/plugins/OwnedInput/reload", nil)
	secondPID := pid("OwnedInput")
	if secondPID == firstPID {
		t.Fatal("actual asynchronous reload retained old SDK")
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("old generation did not cancel its actual HTTP side effect")
	}
	oldCanceled := waitStatus("failed")
	if string(oldCanceled["error"]) == "null" || len(oldCanceled["error"]) == 0 {
		t.Fatal("Go worker cancellation had no actual diagnostic")
	}
	configure("OwnedInput", []map[string]any{definition("After reload", "list-fail", "/reload", 51)})
	submit()
	waitGate("/reload", 2)
	close(release["/reload"])
	newFinished = waitStatus("success")
	_ = json.Unmarshal(newFinished["value"], &value)
	if value != 100 || pid("OwnedInput") != secondPID {
		t.Fatal("new asynchronous generation did not complete")
	}
	configure("OwnedInput", restore)
	t.Logf("actual async services: HTTP acknowledgement before held SDK finish; fixed same-ID old failure finishes new logical job/value51 then new success100; reinit PID=%d retained; reload PID=%d, old HTTP canceled and new actual result completed", firstPID, secondPID)
}
