package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func ordinaryOwnedConfig(a *App, t *testing.T, base string, enabled bool) []any {
	t.Helper()
	rows, ids := privateRows(base, 400, 64)
	retainedStoreSaveConfig(t, a, "CustomSites", map[string]any{"enabled": true, "sites": rows})
	cfg := privateConfig(ids, false)
	cfg["enabled"] = enabled
	cfg["notify"] = false
	cfg["retry_keyword"] = "^NEVER-MATCH$"
	retainedStoreSaveConfig(t, a, "AutoSignIn", cfg)
	return ids
}
func ordinaryPostAccepted(t *testing.T, a *App, cookie *http.Cookie) {
	t.Helper()
	r := callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"/site_signin"}`, cookie, "")
	var out struct {
		Data struct {
			Success  bool `json:"success"`
			Accepted bool `json:"accepted"`
		} `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || !out.Data.Success || !out.Data.Accepted {
		t.Fatal("actual registered command not accepted", r.Code, r.Body.String())
	}
}

// Fixed lifecycle/modules_initializer: scheduler join precedes PM.stop;
// ThreadHelper join follows PM.stop and precedes message/DB close.
func TestOrdinaryBoundGlobalEventSourceOrder(t *testing.T) {
	if os.Getenv("INDEPENDENT_ORDINARY_ASI") == "" {
		t.Skip("opt-in actual SDK fixture: INDEPENDENT_ORDINARY_ASI required")
	}
	peer := ordinaryEntryServer(t)
	defer peer.unblock()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ordinaryOwnedConfig(a, t, peer.server.URL, true)
	m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("INDEPENDENT_ORDINARY_ASI"))
	p := originalBaseSDKLoaded(t, a, m.PluginID)
	ordinaryPostAccepted(t, a, cookie)
	select {
	case <-peer.entered:
	case <-time.After(8 * time.Second):
		t.Fatal("actual command never entered HTTP")
	}
	schedulerDone := make(chan struct{})
	go func() { a.drainOriginalPluginScheduler(); close(schedulerDone) }()
	select {
	case <-schedulerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary event was incorrectly joined by scheduler.stop")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- a.Close() }()
	privateAwait(t, "source PM logically removed before event pool join", func() bool {
		_, exists := a.pluginRunningInstances.Load(m.PluginID)
		return !exists && p.stopping.Load()
	})
	a.pluginOriginalBackground.mu.Lock()
	closed := a.pluginOriginalBackground.closed
	a.pluginOriginalBackground.mu.Unlock()
	if closed {
		t.Fatal("background authority sealed before source event pool drained")
	}
	select {
	case e := <-stopped:
		t.Fatal("global shutdown returned while source event still held", e)
	case <-time.After(200 * time.Millisecond):
	}
	peer.unblock()
	select {
	case e := <-stopped:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("event pool never joined")
	}
	fresh, e := New(Options{DataDir: a.Dir})
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close()
	if n, e := ordinaryEntryHistory64(fresh); e != nil || n != 1 || peer.calls.Load() != 64 {
		t.Fatal("global source event did not finish64 before message/DB shutdown", n, e, peer.calls.Load())
	}
	start, finish := ordinaryEntryMessages(t, fresh, cookie)
	if start != 1 || finish != 1 {
		t.Fatal("global event join lost real Web replies", start, finish)
	}
}

// Source clone uninstall purges config/data before stop; the old bound
// callback may then recreate only its own namespace after manager returned.
func TestOrdinaryBoundCloneUninstallOwnNamespace(t *testing.T) {
	if os.Getenv("INDEPENDENT_ORDINARY_ASI") == "" {
		t.Skip("opt-in actual SDK fixture: INDEPENDENT_ORDINARY_ASI required")
	}
	peer := ordinaryEntryServer(t)
	defer peer.unblock()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ids := ordinaryOwnedConfig(a, t, peer.server.URL, false)
	m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("INDEPENDENT_ORDINARY_ASI"))
	base := originalBaseSDKLoaded(t, a, m.PluginID)
	marker := json.RawMessage(`{"owned":"base-only"}`)
	if e := a.pluginStore.originalBaseSaveData(context.Background(), m.PluginID, "ordinary_base_marker", marker); e != nil {
		t.Fatal(e)
	}
	before, e := a.pluginStore.Config(context.Background(), m.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	r := callTest(t, a.Handler(), "POST", "/api/plugins/AutoSignIn/clone", `{"suffix":"owned"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	id := "AutoSignInowned"
	cfg := privateConfig(ids, false)
	cfg["notify"] = false
	cfg["retry_keyword"] = "^NEVER-MATCH$"
	raw, _ := json.Marshal(cfg)
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	clone := originalBaseSDKLoaded(t, a, id)
	if base.originalEventEnabled() || !clone.originalEventEnabled() {
		t.Fatal("actual base/clone event enabled state not isolated")
	}
	_, selected, ok := a.lookupOriginalPluginCommand("/site_signin")
	if !ok || selected != id {
		t.Fatal("command registry did not select actual enabled clone", selected)
	}
	ordinaryPostAccepted(t, a, cookie)
	select {
	case <-peer.entered:
	case <-time.After(8 * time.Second):
		t.Fatal("clone command did not enter HTTP")
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- callTest(t, a.Handler(), "DELETE", "/api/plugins/"+id, "", cookie, "") }()
	select {
	case r := <-done:
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	case <-time.After(6 * time.Second):
		t.Fatal("clone individual uninstall joined source callback")
	}
	in, e := a.pluginStore.Installation(context.Background(), id)
	if e != nil || in.Installed {
		t.Fatal("clone uninstall not actually committed", e, in.Installed)
	}
	if _, loaded := a.pluginRunningInstances.Load(id); loaded {
		t.Fatal("clone old object still logically loaded")
	}
	if base.stopping.Load() {
		t.Fatal("clone uninstall stopped base")
	}
	peer.unblock()
	ordinaryEntryAwait(t, a)
	privateHistory(t, a, id, 64, true)
	after, e := a.pluginStore.Config(context.Background(), m.PluginID)
	if e != nil || after.Revision != before.Revision || string(after.Value) != string(before.Value) {
		t.Fatal("clone callback changed base configuration", e, before.Revision, after.Revision)
	}
	key := "ordinary_base_marker"
	data, e := a.pluginStore.originalBaseGetData(context.Background(), m.PluginID, &key)
	if e != nil || string(data) != string(marker) {
		t.Fatal("clone crossed base data namespace", e, string(data))
	}
	if n, e := ordinaryEntryHistory64(a); e != nil || n != 0 {
		t.Fatal("clone wrote base batch history", e, n)
	}
	clone.sessionMu.Lock()
	client := clone.session
	clone.sessionMu.Unlock()
	if client != nil {
		t.Fatal("final clone callback did not retire captured client")
	}
}
