package app

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"
)

func TestOriginalCommonSchedulerSourceAndActualSDK(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Name   string
			Plugin json.RawMessage
			Fail   bool
			Result json.RawMessage
			Calls  []string
		}
	}
	raw, err := os.ReadFile("../../testdata/standard-module-host/common-scheduler-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 5 {
		t.Fatal("fixed oracle", err)
	}
	a, cookie, source := ownedModuleFixture(t, true)
	clone := ownedModuleClone(t, a, cookie, "common")
	at := time.Now()
	a.initializeOriginalCommonService(at)
	a.pluginOriginalDirectJobs.mu.Lock()
	job := a.pluginOriginalDirectJobs.jobs[originalServiceKey{"", "scheduler_job"}]
	due := job.due
	a.pluginOriginalDirectJobs.mu.Unlock()
	if due.Sub(at) != 10*time.Minute {
		t.Fatal("fixed 10 minute interval", due.Sub(at))
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			method := "module_execute"
			if c.Fail {
				method = "module_fail"
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"scheduler_job": method}, "result": c.Plugin})
			saveOwnedModule(t, a, cookie, source, string(cfg))
			saveOwnedModule(t, a, cookie, clone, `{"enabled":true,"modules":{"scheduler_job":"module_execute"},"result":null}`)
			s0, c0 := ownedModuleRead(t, source), ownedModuleRead(t, clone)
			response := callTest(t, a.Handler(), "GET", "/api/v1/system/runscheduler?jobid=scheduler_job", "", cookie, "")
			if response.Code != 200 {
				t.Fatal(response.Body.String())
			}
			s1, c1 := ownedModuleRead(t, source), ownedModuleRead(t, clone)
			n := 0
			if slices.Contains(c.Calls, "clone") {
				n = 1
			}
			if s1.Calls[method]-s0.Calls[method] != 1 || c1.Calls["module_execute"]-c0.Calls["module_execute"] != n {
				t.Fatal("source dispatcher", s1.Calls, c1.Calls)
			}
			if !originalBaseJSONEqual(s1.Inputs[method], json.RawMessage(`{"args":[],"kwargs":{}}`)) {
				t.Fatal("no timer kwargs")
			}
			progress := a.originalServiceProgressSnapshot("scheduler_job")
			if progress == nil {
				t.Fatal("missing actual progress")
			}
			a.pluginOriginalDirectJobs.mu.Lock()
			if job.running || job.lastError != "" || job.due != due {
				t.Error("void wrapper/manual must not move clock", job.running, job.lastError, job.due)
			}
			a.pluginOriginalDirectJobs.mu.Unlock()
		})
	}
	// Exercise the real existing ticker; backdated initialization is explicitly
	// a synthetic clock test. The production process test waits the full interval.
	saveOwnedModule(t, a, cookie, source, `{"enabled":true,"modules":{"scheduler_job":"module_execute"},"result":null}`)
	before := ownedModuleRead(t, source).Calls["module_execute"]
	a.initializeOriginalCommonService(time.Now().Add(-10*time.Minute + 200*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.runOriginalPluginScheduler(ctx); close(done) }()
	for deadline := time.Now().Add(3 * time.Second); ownedModuleRead(t, source).Calls["module_execute"] == before && time.Now().Before(deadline); {
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	<-done
	a.wg.Wait()
	if ownedModuleRead(t, source).Calls["module_execute"] != before+1 {
		t.Fatal("real ticker missing/duplicate")
	}
	if err = a.stopOriginalPlugin(context.Background(), source.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	if err = a.stopOriginalPlugin(context.Background(), clone.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	if err = a.runOriginalService(context.Background(), "", "scheduler_job", true); err != nil {
		t.Fatal("common service survives plugin stop", err)
	}
}
