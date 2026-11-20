package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
	"time"
)

func TestPluginIdleTTLParsing(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		have    bool
		want    time.Duration
		wantErr bool
	}{
		{name: "unset keeps default", have: false, want: defaultPluginIdleTTL},
		{name: "blank keeps default", value: "   ", have: true, want: defaultPluginIdleTTL},
		{name: "duration", value: "90m", have: true, want: 90 * time.Minute},
		{name: "seconds", value: "45s", have: true, want: 45 * time.Second},
		{name: "off disables", value: "off", have: true, want: 0},
		{name: "zero disables", value: "0", have: true, want: 0},
		{name: "case insensitive", value: "OFF", have: true, want: 0},
		{name: "garbage fails", value: "soon", have: true, wantErr: true},
		{name: "negative fails", value: "-5m", have: true, wantErr: true},
		{name: "zero duration fails", value: "0s", have: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(key string) (string, bool) {
				if key != "LIGHT_MEDIA_PLUGIN_IDLE_TTL" {
					t.Fatalf("unexpected key %q", key)
				}
				return tc.value, tc.have
			}
			got, err := pluginIdleTTL(lookup, "")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestPluginIdleTTLFileFallback(t *testing.T) {
	dir := t.TempDir()
	lookup := func(string) (string, bool) { return "", false }
	got, err := pluginIdleTTL(lookup, dir)
	if err != nil || got != defaultPluginIdleTTL {
		t.Fatalf("no file must keep the default: %v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin-idle-ttl"), []byte("7m\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = pluginIdleTTL(lookup, dir)
	if err != nil || got != 7*time.Minute {
		t.Fatalf("file window ignored: %v %v", got, err)
	}
	t.Setenv("LIGHT_MEDIA_PLUGIN_IDLE_TTL", "11m")
	got, err = pluginIdleTTL(os.LookupEnv, dir)
	if err != nil || got != 11*time.Minute {
		t.Fatalf("environment must win over the file: %v %v", got, err)
	}
}

func TestPluginIdleExtraAllowlist(t *testing.T) {
	add, remove := pluginIdleExtraAllowlist(func(string) (string, bool) { return "", false })
	if len(add) != 0 || len(remove) != 0 {
		t.Fatal("unset extra list must be empty")
	}
	add, remove = pluginIdleExtraAllowlist(func(key string) (string, bool) {
		if key != "LIGHT_MEDIA_PLUGIN_IDLE_EXTRA" {
			t.Fatalf("unexpected key %q", key)
		}
		return "RemoveLink, SiteStatistic ,-MergeSiteSwitch, ", true
	})
	if len(add) != 2 || add[0] != "RemoveLink" || add[1] != "SiteStatistic" {
		t.Fatalf("add list parsed wrong: %v", add)
	}
	if len(remove) != 1 || remove[0] != "MergeSiteSwitch" {
		t.Fatalf("remove list parsed wrong: %v", remove)
	}
}

func TestPluginIdleEligibleRules(t *testing.T) {
	instance := func(id string) *originalPluginInstance {
		// A live object with a started session is the reaper's scope.
		return &originalPluginInstance{external: true, installation: PluginInstallation{PluginID: id}, sessionStarted: true}
	}
	if !pluginIdleEligible(instance("MergeSiteSwitch"), nil, nil) {
		t.Fatal("audited class must be eligible")
	}
	if pluginIdleEligible(instance("ScrapeFileClean"), nil, nil) {
		t.Fatal("resident watcher class must stay resident")
	}
	if pluginIdleEligible(instance("RemoveLink"), nil, nil) {
		t.Fatal("watcher class outside the audited list must stay resident")
	}
	if !pluginIdleEligible(instance("RemoveLink"), []string{"RemoveLink"}, nil) {
		t.Fatal("operator extra list must extend eligibility")
	}
	if pluginIdleEligible(instance("MergeSiteSwitch"), nil, []string{"MergeSiteSwitch"}) {
		t.Fatal("operator subtract must remove a built-in class")
	}
	internal := instance("MergeSiteSwitch")
	internal.external = false
	if pluginIdleEligible(internal, nil, nil) {
		t.Fatal("builtin adapter objects never retire")
	}
	stopping := instance("MergeSiteSwitch")
	stopping.stopping.Store(true)
	if pluginIdleEligible(stopping, nil, nil) {
		t.Fatal("a stopping object never retires")
	}
	retired := instance("MergeSiteSwitch")
	retired.sessionStarted = true
	retired.sessionRetired.Store(true)
	if pluginIdleEligible(retired, nil, nil) {
		t.Fatal("an already retired object is out of scope")
	}
	neverStarted := &originalPluginInstance{external: true, installation: PluginInstallation{PluginID: "MergeSiteSwitch"}}
	if pluginIdleEligible(neverStarted, nil, nil) {
		t.Fatal("an object without a session is out of scope")
	}
}

// The retire state machine runs against a real fixture worker: stop the
// session, prove the object stays loaded, then prove the next call rebuilds
// the process and replays the initializer.
func TestPluginIdleRetireRebuildsAndReplaysInit(t *testing.T) {
	a, _, object := ownedStandardSDKFixture(t)
	ctx := context.Background()
	before := ownedStandardVersionRead(t, object)
	if before.InitCount != 1 {
		t.Fatalf("startup initializer count %d", before.InitCount)
	}
	object.sessionMu.Lock()
	hadClient := object.session != nil
	object.sessionMu.Unlock()
	if !hadClient {
		t.Fatal("fixture must hold a resident session before retiring")
	}
	// The fixture class is deliberately outside the audited default list; the
	// state machine itself is what this test exercises.
	if pluginIdleEligible(object, nil, nil) {
		t.Fatal("fixture class must stay out of the audited default allowlist")
	}
	if err := object.retireSessionForIdle(ctx); err != nil {
		t.Fatalf("idle retire failed: %v", err)
	}
	object.sessionMu.Lock()
	retiredClient := object.session
	object.sessionMu.Unlock()
	if retiredClient != nil || !object.sessionRetired.Load() || !object.sessionNeedsInit.Load() {
		t.Fatal("retire must clear the session and mark the wake replay")
	}
	// The object stays loaded: its registration and installation are intact.
	if _, err := a.pluginStore.Installation(ctx, ownedStandardFixtureID); err != nil {
		t.Fatalf("installation lost after retire: %v", err)
	}
	after := ownedStandardVersionRead(t, object)
	// The rebuilt worker is a fresh process: its initializer ran exactly once
	// inside it, and the process identity changed.
	if after.InitCount != 1 {
		t.Fatalf("wake must replay the initializer in the rebuilt worker: %d", after.InitCount)
	}
	if after.Nonce == before.Nonce {
		t.Fatal("wake must serve from a rebuilt worker process")
	}
	if string(after.Config) != string(before.Config) {
		t.Fatalf("wake must replay with the same manager configuration: %s vs %s", after.Config, before.Config)
	}
	object.sessionMu.Lock()
	rebuilt := object.session != nil && !object.sessionRetired.Load() && !object.sessionNeedsInit.Load()
	object.sessionMu.Unlock()
	if !rebuilt {
		t.Fatal("wake must leave a live session with the replay gate cleared")
	}
}

func TestPluginIdleRetireGuards(t *testing.T) {
	_, _, object := ownedStandardSDKFixture(t)
	ctx := context.Background()
	// In-flight business traffic blocks retirement.
	object.inflight.Add(1)
	if err := object.retireSessionForIdle(ctx); err == nil {
		t.Fatal("retire must refuse while a call is in flight")
	}
	object.inflight.Add(-1)
	// The pure submitted-state gate covers leases, sealing and both stop
	// lanes; a live lease means an async continuation still owns the client.
	var leased originalSubmittedState
	leased.mu.Lock()
	leased.leases = map[*originalSubmittedLease]struct{}{{}: {}}
	gated := submittedRetireBlocked(&leased)
	leased.mu.Unlock()
	if !gated {
		t.Fatal("a live lease must block retirement")
	}
	if !submittedRetireBlocked(&originalSubmittedState{sealed: true}) ||
		!submittedRetireBlocked(&originalSubmittedState{privateClosing: true}) ||
		!submittedRetireBlocked(&originalSubmittedState{publicClosing: true}) ||
		!submittedRetireBlocked(&originalSubmittedState{publicRetiring: true}) {
		t.Fatal("every closing state must block retirement")
	}
	var quiet originalSubmittedState
	if submittedRetireBlocked(&quiet) {
		t.Fatal("a quiet state must allow retirement")
	}
	// With the guards clear the retire must go through and end the worker.
	if err := object.retireSessionForIdle(ctx); err != nil {
		t.Fatalf("unblocked retire must succeed: %v", err)
	}
	object.sessionMu.Lock()
	gone := object.session == nil
	object.sessionMu.Unlock()
	if !gone {
		t.Fatal("the guard test must end with the session retired")
	}
	// Repeated retire attempts observe the moved-on state without touching a
	// nil client, and a stopping object is terminal for retirement as well.
	if err := object.retireSessionForIdle(ctx); err == nil {
		t.Fatal("a second retire must observe the absent session")
	}
	object.stopping.Store(true)
	if err := object.retireSessionForIdle(ctx); err == nil {
		t.Fatal("a stopping object must never retire again")
	}
}

// The periodic market status probe must not reset the idle clock: a probe
// read keeps the retire guard (inflight) but the TTL window keeps running
// down, otherwise a 30-minute probe cycle starves a 30-minute TTL forever.
func TestPluginIdleMarketProbeDoesNotRefreshLastUsed(t *testing.T) {
	_, _, object := ownedStandardSDKFixture(t)
	probeCtx := context.WithValue(context.Background(), originalMarketProbeKey{}, true)
	before := object.lastUsed.Load()
	time.Sleep(20 * time.Millisecond)
	if _, err := object.callSession(probeCtx, plugins.Runner{Timeout: 10 * time.Second}, "snapshot", json.RawMessage(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	if object.lastUsed.Load() != before {
		t.Fatal("a market probe read must not refresh the idle clock")
	}
	if _, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "snapshot", json.RawMessage(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	if object.lastUsed.Load() == before {
		t.Fatal("an ordinary call must refresh the idle clock")
	}
}

// A retired object answers the market probe from its cached state instead of
// waking the worker; the cache invalidates on wake and on explicit stop.
func TestPluginIdleMarketStateCacheLifecycle(t *testing.T) {
	_, _, object := ownedStandardSDKFixture(t)
	if _, cached := object.cachedMarketState(); cached {
		t.Fatal("an awake object must never serve the cached market state")
	}
	object.rememberMarketState(true)
	if _, cached := object.cachedMarketState(); cached {
		t.Fatal("cache must stay invalid while the worker is awake")
	}
	object.sessionRetired.Store(true)
	state, cached := object.cachedMarketState()
	if !cached || !state {
		t.Fatalf("a retired object must serve its cached state, got %v %v", state, cached)
	}
	// The wake path invalidates the cache: state must be read live again.
	object.sessionRetired.Store(false)
	if _, cached := object.cachedMarketState(); cached {
		t.Fatal("the cache must invalidate once the worker is awake again")
	}
}
