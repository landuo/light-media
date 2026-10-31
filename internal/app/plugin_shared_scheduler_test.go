package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"light-media/internal/plugins/schedule"
)

func managerLifecycleLogin(t *testing.T, a *App) *http.Cookie {
	t.Helper()
	response := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if response.Code != 200 {
		t.Fatalf("owned login %d %s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "lm_session" {
			return cookie
		}
	}
	t.Fatal("owned login returned no session")
	return nil
}
func managerLifecycleHTTP(t *testing.T, a *App, cookie *http.Cookie, id, input string) managerWorkerReply {
	t.Helper()
	response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+id+"/query", input, cookie, "")
	var out managerWorkerReply
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil || out.PID <= 0 {
		t.Fatalf("owned bound HTTP %d %s", response.Code, response.Body.String())
	}
	return out
}
func managerLifecycleSuccess(t *testing.T, reply managerWorkerReply) {
	t.Helper()
	if reply.Error != "" {
		t.Fatal("owned SDK host operation failed", reply.Error)
	}
}
func managerLifecycleServices() []pluginScheduleDefinition {
	trigger := schedule.Spec{Kind: "date", Timezone: "UTC", RunAt: "2099-01-01T00:00:00Z"}
	return []pluginScheduleDefinition{
		{ID: "alpha|one", Name: "first", Handler: "job", Trigger: trigger},
		{ID: "alpha|two", Name: "replacement", Handler: "job", Trigger: trigger},
		{ID: "broken", Name: "invalid max_instances", Handler: "job", Trigger: trigger, SchedulerOptions: json.RawMessage(`{"max_instances":0}`)},
		{ID: "later", Name: "after failure", Handler: "job", Trigger: trigger},
	}
}
func managerLifecycleConfigureServices(t *testing.T, object *originalPluginInstance, defs []pluginScheduleDefinition, stateError, serviceError bool) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"op": "configure", "state": true, "services": defs, "state_error": stateError, "service_error": serviceError})
	managerLifecycleSuccess(t, managerWorkerQuery(t, object, string(raw)))
}
func managerSchedulerSourceProjection(t *testing.T) json.RawMessage {
	t.Helper()
	_ = managerSource(t) // validates the exact independent capture hash
	var raw struct {
		Warm struct {
			Scheduler struct {
				Update struct {
					After struct {
						Registry map[string]struct {
							Name    string `json:"name"`
							PID     string `json:"pid"`
							Running bool   `json:"running"`
						} `json:"registry"`
						Pending []struct {
							ID     string            `json:"id"`
							Name   string            `json:"name"`
							Kwargs map[string]string `json:"kwargs"`
						} `json:"pending_jobs"`
					} `json:"after"`
				} `json:"update_same_prefix_and_add_failure"`
			} `json:"actual_stopped_scheduler"`
		} `json:"warm"`
	}
	if err := json.Unmarshal(managerLifecycleSource, &raw); err != nil {
		t.Fatal(err)
	}
	// The source callable is Python unused_job; the owned Go method is job.
	// Display-name/provider metadata deliberately differs and is not normalized
	// into a fictitious equality claim. Compare the common registry/clock fields.
	registry := map[string]any{}
	for id, row := range raw.Warm.Scheduler.Update.After.Registry {
		registry[id] = map[string]any{"name": row.Name, "pid": row.PID, "running": row.Running}
	}
	pending := raw.Warm.Scheduler.Update.After.Pending
	sort.Slice(pending, func(i, j int) bool { return pending[i].ID < pending[j].ID })
	if len(registry) != 3 || len(pending) != 2 {
		t.Fatal("source scheduler projection absent")
	}
	out, _ := json.Marshal(map[string]any{"registry": registry, "pending_jobs": pending})
	return out
}
func managerSchedulerActualProjection(a *App) json.RawMessage {
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	registry := map[string]any{}
	pending := []map[string]any{}
	for _, job := range state.jobs {
		if job.commonModule {
			continue
		}
		id := originalServiceID(job)
		registry[id] = map[string]any{"name": job.definition.Name, "pid": job.object.installation.InstanceID, "running": job.running}
		if job.clockRegistered {
			pending = append(pending, map[string]any{"id": job.timerID, "name": job.definition.Name, "kwargs": map[string]string{"job_id": id}})
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i]["id"].(string) < pending[j]["id"].(string) })
	out, _ := json.Marshal(map[string]any{"registry": registry, "pending_jobs": pending})
	return out
}

func TestSharedSchedulerActualSDKSourceRegistry(t *testing.T) {
	a, object := managerRuntimeWorker(t)
	managerLifecycleConfigureServices(t, object, managerLifecycleServices(), false, false)
	reply := managerWorkerQuery(t, object, `{"op":"scheduler_update","plugin_id":"Owned"}`)
	managerLifecycleSuccess(t, reply)
	managerJSONEqual(t, reply.Value, json.RawMessage(`null`))
	managerJSONEqual(t, managerSchedulerActualProjection(a), managerSchedulerSourceProjection(t))
	// Source registry survives failed clock addition and later declarations still
	// run. Global exact ID ignores the caller-supplied plugin ID.
	managerLifecycleSuccess(t, managerWorkerQuery(t, object, `{"op":"scheduler_remove","plugin_id":"Wrong","job_id":"Owned_alpha"}`))
	a.pluginOriginalDirectJobs.mu.Lock()
	removed := lookupOriginalJobIDLocked(&a.pluginOriginalDirectJobs, "Owned_alpha") == nil
	broken := lookupOriginalJobIDLocked(&a.pluginOriginalDirectJobs, "Owned_broken")
	a.pluginOriginalDirectJobs.mu.Unlock()
	if !removed || broken == nil || broken.clockRegistered {
		t.Fatal("exact ID or failed clock registry differs")
	}
	// Explicit null/empty and omitted job_id all use the plugin namespace.
	for _, suffix := range []string{`,"job_id":null`, `,"job_id":""`, ``} {
		managerLifecycleSuccess(t, managerWorkerQuery(t, object, `{"op":"scheduler_update","plugin_id":"Owned"}`))
		managerLifecycleSuccess(t, managerWorkerQuery(t, object, `{"op":"scheduler_remove","plugin_id":"Owned"`+suffix+`}`))
		managerJSONEqual(t, managerSchedulerActualProjection(a), json.RawMessage(`{"registry":{},"pending_jobs":[]}`))
	}
	// A truthful raw state is not restricted to a boolean. Getter failures are
	// caught only after removing old jobs; a subsequent same-worker update recovers.
	managerLifecycleConfigureServices(t, object, managerLifecycleServices(), false, false)
	managerLifecycleSuccess(t, managerWorkerQuery(t, object, `{"op":"configure","state":{"active":[1,"owned"]}}`))
	managerLifecycleSuccess(t, managerWorkerQuery(t, object, `{"op":"scheduler_update","plugin_id":"Owned"}`))
	managerJSONEqual(t, managerSchedulerActualProjection(a), managerSchedulerSourceProjection(t))
	for _, stateError := range []bool{true, false} {
		managerLifecycleConfigureServices(t, object, managerLifecycleServices(), stateError, !stateError)
		managerLifecycleSuccess(t, managerWorkerQuery(t, object, `{"op":"scheduler_update","plugin_id":"Owned"}`))
		managerJSONEqual(t, managerSchedulerActualProjection(a), json.RawMessage(`{"registry":{},"pending_jobs":[]}`))
	}
	managerLifecycleConfigureServices(t, object, managerLifecycleServices(), false, false)
	managerLifecycleSuccess(t, managerWorkerQuery(t, object, `{"op":"scheduler_update","plugin_id":"Owned"}`))
	managerJSONEqual(t, managerSchedulerActualProjection(a), managerSchedulerSourceProjection(t))
	a.initializeOriginalCommonService(time.Now())
	managerLifecycleSuccess(t, managerWorkerQuery(t, object, `{"op":"scheduler_remove","plugin_id":"Wrong","job_id":"scheduler_job"}`))
	a.pluginOriginalDirectJobs.mu.Lock()
	common := lookupOriginalJobIDLocked(&a.pluginOriginalDirectJobs, "scheduler_job")
	a.pluginOriginalDirectJobs.mu.Unlock()
	if common != nil {
		t.Fatal("global common ID not removed")
	}
	managerLifecycleSuccess(t, managerWorkerQuery(t, object, `{"op":"scheduler_update","plugin_id":""}`))
	managerJSONEqual(t, managerSchedulerActualProjection(a), managerSchedulerSourceProjection(t))
}

func TestSharedSchedulerActualRetainedSelfRemove(t *testing.T) {
	a, object := managerRuntimeWorker(t)
	cookie := managerLifecycleLogin(t, a)
	defs := managerLifecycleServices()[:1]
	managerLifecycleConfigureServices(t, object, defs, false, false)
	managerLifecycleSuccess(t, managerWorkerQuery(t, object, `{"op":"scheduler_update","plugin_id":"Owned"}`))
	before := managerLifecycleHTTP(t, a, cookie, "Owned", `{"op":"inspect"}`)
	managerLifecycleSuccess(t, before)
	client := ownedSubmittedClient(object)
	life := object.originalRetainedState()
	managerMutationSeed(t, a, "Owned")
	for _, op := range []string{"delete_config", "delete_data"} {
		raw, _ := json.Marshal(map[string]any{"op": op, "plugin_id": "Owned", "force": false})
		reply := managerWorkerQuery(t, object, string(raw))
		managerLifecycleSuccess(t, reply)
		managerJSONEqual(t, reply.Value, json.RawMessage(`true`))
	}
	managerMutationSeed(t, a, "Owned")
	removed := managerLifecycleHTTP(t, a, cookie, "Owned", `{"op":"remove","plugin_id":"Owned"}`)
	managerLifecycleSuccess(t, removed)
	if removed.PID != before.PID {
		t.Fatal("self remove changed worker")
	}
	if _, ok := a.pluginRunningInstances.Load("Owned"); ok {
		t.Fatal("self remove kept running entry")
	}
	if _, ok := a.pluginOriginalClasses.Load("Owned"); ok {
		t.Fatal("self remove kept class")
	}
	managerMutationConfig(t, a, "Owned", `{"persisted":"before-delete"}`)
	managerMutationData(t, a, "Owned", `{"data":"retained"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.runOriginalService(ctx, "Owned", "alpha", true); err != nil {
		t.Fatal("retained bound job refused", err)
	}
	after := managerLifecycleHTTP(t, a, cookie, "Owned", `{"op":"inspect"}`)
	managerLifecycleSuccess(t, after)
	var snapshot struct {
		Counter int               `json:"counter"`
		Trace   []json.RawMessage `json:"trace"`
	}
	if json.Unmarshal(after.Value, &snapshot) != nil || snapshot.Counter != 101 || after.PID != before.PID {
		t.Fatal("bound job lost exact post-close object", string(after.Value))
	}
	expected := []json.RawMessage{json.RawMessage(`["get_state"]`), json.RawMessage(`["get_service"]`), json.RawMessage(`["close"]`), json.RawMessage(`["stop_service"]`), json.RawMessage(`["job"]`)}
	actual, _ := json.Marshal(snapshot.Trace)
	want, _ := json.Marshal(expected)
	managerJSONEqual(t, actual, want)
	managerLifecycleSuccess(t, managerLifecycleHTTP(t, a, cookie, "Owned", `{"op":"scheduler_remove","plugin_id":"Wrong","job_id":"Owned_alpha"}`))
	if err := a.runOriginalService(ctx, "Owned", "alpha", true); err == nil {
		t.Fatal("removed bound job still callable")
	}
	select {
	case <-life.retired():
		t.Fatal("API owner lost after job removal")
	default:
	}
	a.unregisterOriginalPluginAPIs("Owned")
	retainedRuntimeAwait(t, life.retired(), "actual last manager job/API owner reap")
	if err := client.Stop(context.Background()); err != nil {
		t.Fatal("actual owned worker terminal", err)
	}
	gone := callTest(t, a.Handler(), "POST", "/api/v1/plugin/Owned/query", `{"op":"inspect"}`, cookie, "")
	if gone.Code != 404 {
		t.Fatal("removed API still exposed", gone.Code)
	}
}
