package app

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"slices"
	"testing"

	"light-media/internal/plugins"
)

//go:embed testdata/shared-plugin-manager/remove-source.json
var managerRemoveAllSource []byte

func managerRemoveSourceHookTrace(t *testing.T, sourceID string) json.RawMessage {
	t.Helper()
	sum := sha256.Sum256(managerRemoveAllSource)
	if hex.EncodeToString(sum[:]) != "74876e4ed8cbc2142131d098774fce4fbf109ccf661e29c9d4fcdaefe4c0e989" {
		t.Fatal("source empty-all capture bytes changed")
	}
	var source struct {
		Trace [][]json.RawMessage                 `json:"trace"`
		After struct{ Classes, Running []string } `json:"after"`
	}
	if err := json.Unmarshal(managerRemoveAllSource, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.After.Classes) != 0 || len(source.After.Running) != 0 {
		t.Fatal("source terminal registry not empty")
	}
	trace := [][]string{}
	for _, entry := range source.Trace {
		if len(entry) != 2 {
			continue
		}
		var method, id string
		_ = json.Unmarshal(entry[0], &method)
		_ = json.Unmarshal(entry[1], &id)
		if id == sourceID && (method == "close" || method == "stop_service") {
			trace = append(trace, []string{method})
		}
	}
	if len(trace) == 0 {
		t.Fatal("source callback trace absent")
	}
	out, _ := json.Marshal(trace)
	return out
}

// Two fresh owned instances use the already delivered clone operation only
// for fixture construction. This is not clone/UI migration acceptance.
func TestSharedManagerRemoveActualEmptyAllSource(t *testing.T) {
	a, first := managerRuntimeWorker(t)
	cookie := managerLifecycleLogin(t, a)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/Owned/clone", `{"suffix":"Second"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load("Ownedsecond")
	if !ok {
		t.Fatal("owned second instance missing")
	}
	second := value.(*originalPluginInstance)
	objects := []*originalPluginInstance{first, second}
	ids := []string{"Owned", "Ownedsecond"}
	sourceIDs := []string{"OwnedFirst", "OwnedSecond"}
	clients := []*plugins.Client{ownedSubmittedClient(first), ownedSubmittedClient(second)}
	for index, object := range objects {
		if clients[index] == nil || !clients[index].SupportsConcurrent() {
			t.Fatal("actual concurrent worker required")
		}
		raw, _ := json.Marshal(map[string]any{"op": "configure", "close_error": index == 1})
		managerLifecycleSuccess(t, managerWorkerQuery(t, object, string(raw)))
	}
	before := managerLifecycleHTTP(t, a, cookie, ids[0], `{"op":"inspect"}`)
	managerLifecycleSuccess(t, before)
	removed := managerLifecycleHTTP(t, a, cookie, ids[0], `{"op":"remove","plugin_id":""}`)
	managerLifecycleSuccess(t, removed)
	if removed.PID != before.PID {
		t.Fatal("empty remove terminated its own caller")
	}
	for index, object := range objects {
		if _, ok := a.pluginRunningInstances.Load(ids[index]); ok {
			t.Fatal("empty remove kept running instance", ids[index])
		}
		if _, ok := a.pluginOriginalClasses.Load(ids[index]); ok {
			t.Fatal("empty remove kept class", ids[index])
		}
		object.configMu.Lock()
		enabled := object.eventsEnabled
		object.configMu.Unlock()
		if enabled {
			t.Fatal("removed object events remained enabled")
		}
		reply := managerLifecycleHTTP(t, a, cookie, ids[index], `{"op":"inspect"}`)
		managerLifecycleSuccess(t, reply)
		var snapshot struct {
			Counter int             `json:"counter"`
			Trace   json.RawMessage `json:"trace"`
		}
		if json.Unmarshal(reply.Value, &snapshot) != nil || snapshot.Counter != 100 {
			t.Fatal("cleanup count", string(reply.Value))
		}
		managerJSONEqual(t, snapshot.Trace, managerRemoveSourceHookTrace(t, sourceIDs[index]))
		a.unregisterOriginalPluginAPIs(ids[index])
		retainedRuntimeAwait(t, object.originalRetainedState().retired(), "empty-remove owned worker reap")
		if err := clients[index].Stop(context.Background()); err != nil {
			t.Fatal("owned terminal", err)
		}
	}
}

// Component gate only while resource dispatch is intentionally closed. Real
// self-remove/current-stack/job/API gates follow the coupled complete slice.
func TestSharedManagerRemoveClassOnlySourceScope(t *testing.T) {
	a, scope, _, active := managerMutationFixture(t)
	for _, id := range []string{"Owned", "Other"} {
		managerMutationSeed(t, a, id)
		a.storeOriginalPluginClass(id, originalPluginClass{installation: PluginInstallation{PluginID: id, InstanceID: id, Version: "1"}})
	}
	result, err := a.originalSharedManagerRemove(context.Background(), scope, json.RawMessage(`{"plugin_id":"Owned"}`))
	if err != nil {
		t.Fatal(err)
	}
	managerJSONEqual(t, result, managerSourceObservation(t, managerSource(t), "remove_class_only").Result.Returned)
	if _, ok := a.pluginOriginalClasses.Load("Owned"); ok {
		t.Fatal("class-only entry retained")
	}
	if _, ok := a.pluginOriginalClasses.Load("Other"); !ok {
		t.Fatal("other class removed")
	}
	for _, id := range []string{"Owned", "Other"} {
		managerMutationConfig(t, a, id, `{"persisted":"before-delete"}`)
		managerMutationData(t, a, id, `{"data":"retained"}`)
	}
	if result, err = a.originalSharedManagerRemove(context.Background(), scope, json.RawMessage(`{"plugin_id":"Missing"}`)); err != nil || string(result) != "null" {
		t.Fatal("missing not a no-op", string(result), err)
	}
	active.Store(false)
	if _, err = a.originalSharedManagerRemove(context.Background(), scope, json.RawMessage(`{"plugin_id":""}`)); err == nil {
		t.Fatal("revoked all-remove accepted")
	}
	if _, ok := a.pluginOriginalClasses.Load("Other"); !ok {
		t.Fatal("revoked call changed class")
	}
	active.Store(true)
	if result, err = a.originalSharedManagerRemove(context.Background(), scope, json.RawMessage(`{"plugin_id":""}`)); err != nil || string(result) != "null" {
		t.Fatal("empty all-remove failed", err)
	}
	if _, ok := a.pluginOriginalClasses.Load("Other"); ok {
		t.Fatal("empty ID did not clear classes")
	}
	managerMutationConfig(t, a, "Other", `{"persisted":"before-delete"}`)
	managerMutationData(t, a, "Other", `{"data":"retained"}`)
	// A can import first, fail init, then succeed after B: source all-stop
	// follows running insertion order B,A, not the class import order A,B.
	for _, id := range []string{"A", "B", "ClassOnly"} {
		a.storeOriginalPluginClass(id, originalPluginClass{})
	}
	a.pluginOriginalRunningMu.Lock()
	for _, id := range []string{"B", "A"} {
		a.pluginRunningInstances.Store(id, &originalPluginInstance{app: a, installation: PluginInstallation{PluginID: id, InstanceID: id}})
	}
	a.pluginOriginalRunningOrder = []string{"B", "A"}
	a.pluginOriginalRunningMu.Unlock()
	targets, err := a.snapshotOriginalManagerRemoval(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, target := range targets {
		ids = append(ids, target.id)
		if target.object != nil {
			target.object.releaseOriginalRetainedOwner(target.operation)
		}
	}
	if !slices.Equal(ids, []string{"B", "A", "ClassOnly"}) {
		t.Fatal("all-stop did not preserve source running order", ids)
	}
}
