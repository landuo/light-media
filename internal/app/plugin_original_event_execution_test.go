package app

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
)

// The actual worker records every invoked event through Base.SaveData. Holding
// its legacy serial slot makes the submission/execution gap deterministic;
// the controlled flag change is the manager state changed by save/stop, not a
// worker-side fake callback. Public configuration and clone creation remain real.
func TestOriginalBroadcastActualSDKRechecksExecutionEnablement(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventTargetFixture(t, a)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedEventTargetFixtureID+"/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	ids := []string{ownedEventTargetFixtureID, ownedEventTargetFixtureID + "twin"}
	counts := map[string]int{}
	for _, id := range ids {
		ownedEventTargetConfig(t, a, cookie, id, true, false, false, "", nil)
	}
	for _, id := range ids {
		loaded, _ := a.pluginRunningInstances.Load(id)
		object := loaded.(*originalPluginInstance)
		for _, state := range []struct {
			name     string
			before   bool
			after    bool
			expected int
		}{
			{"disable after submission", true, false, 0},
			{"enable after submission", false, true, 1},
		} {
			t.Run(id+"/"+state.name, func(t *testing.T) {
				object.methodMu.Lock()
				object.configMu.Lock()
				object.eventsEnabled = state.before
				object.configMu.Unlock()
				data, _ := json.Marshal(map[string]string{"__mp_target_plugin_id": id, "marker": state.name})
				a.broadcastOriginalPluginEvent(context.Background(), "message.action", data)
				object.configMu.Lock()
				object.eventsEnabled = state.after
				object.configMu.Unlock()
				object.methodMu.Unlock()
				a.wg.Wait()
				counts[id] += state.expected
				for _, check := range ids {
					requireBusinessEventCount(t, a, check, "message.action", counts[check])
				}
			})
		}
	}
	// Real persisted disabling and re-enabling applies to the existing object,
	// not only its stored configuration. The other clone remains independent.
	ownedEventTargetConfig(t, a, cookie, ids[0], false, false, false, "", nil)
	a.broadcastOriginalPluginEvent(context.Background(), "message.action", json.RawMessage(`{"marker":"saved disable"}`))
	a.wg.Wait()
	counts[ids[1]]++
	for _, id := range ids {
		requireBusinessEventCount(t, a, id, "message.action", counts[id])
	}
}

func TestOriginalChainActualSDKSkipsSubscriberDisabledByEarlierHandler(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventTargetFixture(t, a)
	ownedEventTargetConfig(t, a, cookie, ownedEventTargetFixtureID, true, false, false, "", nil)
	loaded, _ := a.pluginRunningInstances.Load(ownedEventTargetFixtureID)
	object := loaded.(*originalPluginInstance)
	const kind = "owned.chain.disable"
	// A source handler may disable a later subscriber during synchronous
	// dispatch. Use the real authenticated save path to perform that transition.
	a.registerOriginalChainHandler(kind, originalMutableEventHandler{
		ID: "owned-disable", Priority: 0,
		Invoke: func(_ context.Context, event *plugins.MutableEvent) error {
			if string(event.Data) != `{}` {
				t.Fatal("initial falsey chain data did not follow the Event constructor", string(event.Data))
			}
			ownedEventTargetConfig(t, a, cookie, ownedEventTargetFixtureID, false, false, false, "", nil)
			event.Data = json.RawMessage(`null`)
			return nil
		},
	})
	a.registerOriginalChainHandler(kind, originalMutableEventHandler{
		ID: "owned-real-worker", Instance: ownedEventTargetFixtureID, Priority: 1,
		Enabled: func() bool {
			object.configMu.RLock()
			defer object.configMu.RUnlock()
			return object.eventsEnabled
		},
		Invoke: func(ctx context.Context, event *plugins.MutableEvent) error {
			return invokeOriginalMutableEvent(ctx, object, "event", event)
		},
	})
	seen := false
	a.registerOriginalChainHandler(kind, originalMutableEventHandler{
		ID: "owned-observer", Priority: 2,
		Invoke: func(_ context.Context, event *plugins.MutableEvent) error {
			seen = true
			if string(event.Data) != "null" {
				t.Fatal("falsey mutation was reconstructed as a new Event", string(event.Data))
			}
			return nil
		},
	})
	event := &plugins.MutableEvent{Type: kind, Data: json.RawMessage(`false`)}
	if !a.sendOriginalChainEvent(context.Background(), event) || !seen {
		t.Fatal("selected chain did not continue")
	}
	requireBusinessEventCount(t, a, ownedEventTargetFixtureID, kind, 0)
}

func TestOriginalBroadcastActualSDKEventConstructor(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventTargetFixture(t, a)
	ownedEventTargetConfig(t, a, cookie, ownedEventTargetFixtureID, true, false, false, "", nil)
	for n, raw := range []string{`null`, `false`, `0`, `-0.0`, `1e-10000`, `""`, `[]`, `{}`, `9007199254740993123456789`, `[false]`} {
		a.broadcastOriginalPluginEvent(context.Background(), "plugin.action", json.RawMessage(raw))
		a.wg.Wait()
		events := requireBusinessEventCount(t, a, ownedEventTargetFixtureID, "plugin.action", n+1)
		want := `{}`
		if n >= 8 {
			want = raw
		}
		if string(events[len(events)-1].Data) != want {
			t.Fatal("actual SDK event constructor mismatch", raw, string(events[len(events)-1].Data), want)
		}
	}
}

func TestOriginalBroadcastActualSDKResolvesReloadedClass(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventTargetFixture(t, a)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedEventTargetFixtureID+"/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	ids := []string{ownedEventTargetFixtureID, ownedEventTargetFixtureID + "twin"}
	for _, id := range ids {
		ownedEventTargetConfig(t, a, cookie, id, true, false, false, "", nil)
	}
	pid := func(object *originalPluginInstance) int {
		t.Helper()
		var state struct {
			PID int `json:"pid"`
		}
		if err := object.readSession(context.Background(), "snapshot", &state); err != nil || state.PID == 0 {
			t.Fatal("actual process not available", err)
		}
		return state.PID
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			old := originalBaseSDKLoaded(t, a, id)
			oldPID := pid(old)
			old.methodMu.Lock()
			data, _ := json.Marshal(map[string]string{"__mp_target_plugin_id": id, "marker": "submitted before reload"})
			a.broadcastOriginalPluginEvent(context.Background(), "message.action", data)
			response := callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/reload", `{}`, cookie, "")
			old.methodMu.Unlock()
			assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
			a.wg.Wait()
			current := originalBaseSDKLoaded(t, a, id)
			if current == old || pid(current) == oldPID {
				t.Fatal("HTTP reload did not replace the real SDK process")
			}
			events := requireBusinessEventCount(t, a, id, "message.action", 1)
			if string(events[0].Data) != `{"marker":"submitted before reload"}` {
				t.Fatal("queued target event changed or used another instance", string(events[0].Data))
			}
			// An already stopped class resolves to no worker, not an error or an
			// implicit restart. The previously captured callback remains harmless.
			if err := a.stopOriginalPlugin(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if _, invoked, err := old.callOriginalEventMethod(context.Background(), "event", mustOriginalEventJSON("message.action", json.RawMessage(`{}`))); err != nil || invoked {
				t.Fatal("stopped event callback did not remain a no-op", invoked, err)
			}
			requireBusinessEventCount(t, a, id, "message.action", 1)
		})
	}
}

func TestOriginalQueuedEventClassIdentityRejectsUnrelatedReplacement(t *testing.T) {
	manifest := func(instance, source string) json.RawMessage {
		if source == "" {
			return json.RawMessage(`{}`)
		}
		raw, _ := json.Marshal(map[string]any{"host_instance": pluginInstanceMarker{Purpose: "instance-clone/v1", InstanceID: instance, SourceInstanceID: source}})
		return raw
	}
	a := &App{}
	old := &originalPluginInstance{app: a, installation: PluginInstallation{InstanceID: "SourceClone", PluginID: "Source", Manifest: manifest("SourceClone", "Source")}}
	for _, test := range []struct {
		name, plugin, source string
		want                 bool
	}{
		{"same class and lineage", "Source", "Source", true},
		{"different plugin", "Other", "Source", false},
		{"different clone parent", "Source", "Other", false},
		{"replaced by default class", "Source", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := &originalPluginInstance{app: a, installation: PluginInstallation{InstanceID: "SourceClone", PluginID: test.plugin, Version: "2.0", Manifest: manifest("SourceClone", test.source)}}
			a.pluginRunningInstances.Store("SourceClone", current)
			if got := old.currentOriginalEventObject(); (got == current) != test.want {
				t.Fatal("queued class binding changed authority", got != nil, test.want)
			}
		})
	}
}

func TestOriginalChainActualSDKResolvesReloadedSubscriber(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventTargetFixture(t, a)
	ownedEventTargetConfig(t, a, cookie, ownedEventTargetFixtureID, true, false, false, "", nil)
	old := originalBaseSDKLoaded(t, a, ownedEventTargetFixtureID)
	const kind = "owned.chain.reload"
	a.registerOriginalChainHandler(kind, originalMutableEventHandler{
		ID: "reload", Priority: 0,
		Invoke: func(context.Context, *plugins.MutableEvent) error {
			response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedEventTargetFixtureID+"/reload", `{}`, cookie, "")
			assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
			return nil
		},
	})
	a.registerOriginalChainHandler(kind, originalMutableEventHandler{
		ID: "queued", Instance: ownedEventTargetFixtureID, Priority: 1,
		Enabled: old.originalEventEnabled,
		Invoke: func(ctx context.Context, event *plugins.MutableEvent) error {
			return invokeOriginalMutableEvent(ctx, old, "event", event)
		},
	})
	if !a.sendOriginalChainEvent(context.Background(), &plugins.MutableEvent{Type: kind, Data: json.RawMessage(`{"queued":true}`)}) {
		t.Fatal("source chain did not dispatch")
	}
	a.wg.Wait()
	if current := originalBaseSDKLoaded(t, a, ownedEventTargetFixtureID); current == old {
		t.Fatal("chain reload retained the old object")
	}
	requireBusinessEventCount(t, a, ownedEventTargetFixtureID, kind, 1)
}
