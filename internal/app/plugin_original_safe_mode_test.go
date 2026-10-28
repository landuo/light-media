package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Reopen a real installed artifact, proving safe startup preserves its data
// without starting its worker or contacting its configured repository.
func TestOriginalSafeModeKeepsManagementWithoutStartupExecution(t *testing.T) {
	a, cookie, source := originalBaseSDKFixture(t)
	applyOriginalBaseSDKPlan(t, a, cookie, source.installation.InstanceID, map[string]any{"enabled": true, "marker": "safe-startup"})
	before, err := a.pluginStore.Config(context.Background(), source.installation.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected safe-mode repository request", http.StatusInternalServerError)
	}))
	defer repository.Close()
	setOwnedSyncMarkets(t, a, repository.URL+"/index")
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	safe, err := New(Options{DataDir: a.Dir, SafeMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer safe.Close()
	safe.Start(context.Background())
	quiescent := make(chan struct{})
	go func() { safe.wg.Wait(); close(quiescent) }()
	select {
	case <-quiescent:
	case <-time.After(time.Second):
		t.Fatal("safe startup launched background workers")
	}
	if _, loaded := safe.pluginRunningInstances.Load(source.installation.InstanceID); loaded {
		t.Fatal("safe startup initialized the installed plugin")
	}
	if requests.Load() != 0 {
		t.Fatal("safe startup contacted a plugin repository")
	}
	after, err := safe.pluginStore.Config(context.Background(), source.installation.InstanceID)
	if err != nil || !originalBaseJSONEqual(before.Value, after.Value) {
		t.Fatal("safe startup changed config", err)
	}
	response := callTest(t, safe.Handler(), "GET", "/api/plugins/installed", "", cookie, "")
	if response.Code != http.StatusOK {
		t.Fatalf("management unavailable: %d %s", response.Code, response.Body.String())
	}
	// Explicit management still works, allowing the broken plugin to be removed.
	response = callTest(t, safe.Handler(), "DELETE", "/api/plugins/"+source.installation.InstanceID, "", cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if requests.Load() != 0 {
		t.Fatal("uninstall unexpectedly requested remote code")
	}
}
