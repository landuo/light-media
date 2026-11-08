package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSourcePublicSDKReservedPreStopAtFullCapacity(t *testing.T) {
	c := publicSDKClient(t)
	entered := make(chan struct{}, maxConcurrentCalls)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	results := make(chan error, maxConcurrentCalls)
	for n := 0; n < maxConcurrentCalls; n++ {
		go func() {
			out, e := c.CallWithOptions(context.Background(), "invoke", json.RawMessage(`{}`), func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
				entered <- struct{}{}
				select {
				case <-release:
					return json.RawMessage(`true`), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}, CallOptions{CallerDeadlineOnly: true})
			if e == nil && string(out.Result) != "true" {
				e = fmt.Errorf("actual ordinary result lost")
			}
			results <- e
		}()
	}
	for n := 0; n < maxConcurrentCalls; n++ {
		select {
		case <-entered:
		case <-time.After(4 * time.Second):
			t.Fatal("full32 physical callbacks did not enter")
		}
	}
	if _, e := c.Call(context.Background(), "invoke", json.RawMessage(`{}`), nil); codeOf(e) != "concurrent_call_limit" {
		t.Fatal("ordinary cap not preserved", e)
	}
	if _, e := c.CallPublicServiceStop(context.Background(), "close", json.RawMessage(`{}`), nil); e != nil {
		t.Fatal("pre-stop close blocked by full ordinary table", e)
	}
	select {
	case e := <-results:
		t.Fatal("pre-stop close revoked existing callback", e)
	default:
	}
	stopped := 0
	_, e := c.CallPublicServiceStop(context.Background(), "stop_service", json.RawMessage(`{}`), func(ctx context.Context, id Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
		if ctx.Err() != nil || method != "test.stop" {
			t.Error("invalid fresh manager scope", ctx.Err(), method)
		}
		if IsCleanupInvocation(ctx) {
			t.Error("pre-stop forged post-fence cleanup authority")
		}
		var flags map[string]bool
		if json.Unmarshal(raw, &flags) != nil || flags["cleanup"] {
			t.Error("worker acquired post-fence cleanup context during private pre-stop", string(raw))
		}
		stopped++
		unblock()
		return json.RawMessage(`null`), nil
	})
	if e != nil || stopped != 1 {
		t.Fatal("reserved private stop failed", e, stopped)
	}
	for n := 0; n < maxConcurrentCalls; n++ {
		select {
		case e := <-results:
			if e != nil {
				t.Fatal("callback revoked before pre-stop join", e)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("callback physical reply not joined")
		}
	}
	c.concurrent.mu.Lock()
	fenced := c.concurrent.draining
	c.concurrent.mu.Unlock()
	if fenced {
		t.Fatal("pre-stop mutated global transport.shutdown")
	}
	if e := c.BeginShutdown(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := c.CallPublicServiceStop(context.Background(), "close", json.RawMessage(`{}`), nil); codeOf(e) != "session_closed" {
		t.Fatal("postfence private lane reopened", e)
	}
}
func TestSourcePublicSDKFeatureAndCleanupSeparation(t *testing.T) {
	c := publicSDKClient(t)
	if !c.SupportsPublicServiceStop() || c.SupportsPrivateSchedulerStop() {
		t.Fatal("public feature was inferred from private ownership")
	}
	if _, e := c.CallPrivateSchedulerStop(context.Background(), "close", json.RawMessage(`{}`), nil); codeOf(e) != "invalid_invocation" {
		t.Fatal("public feature acquired private ownership", e)
	}
	cleanup := privateSDKClient(t, false, true)
	if cleanup.SupportsPublicServiceStop() {
		t.Fatal("cleanup handoff was allowed before its fence")
	}
	if _, e := cleanup.CallPublicServiceStop(context.Background(), "close", json.RawMessage(`{}`), nil); codeOf(e) != "invalid_invocation" {
		t.Fatal("cleanup was granted pre-fence", e)
	}
}

func TestSourcePublicSDKWorkerFixture(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "owned-public-sdk-worker") {
		return
	}
	m := privateSDKManifest(false, false)
	m.ManagedPublicServices = true
	handle := func(ctx context.Context, method string, raw json.RawMessage, host HostCaller) (json.RawMessage, error) {
		if method == "invoke" {
			return host(ctx, "test.host", raw)
		}
		if method == "stop_service" {
			flags, _ := json.Marshal(map[string]bool{"cleanup": IsCleanupInvocation(ctx)})
			return host(ctx, "test.stop", flags)
		}
		return json.RawMessage(`null`), nil
	}
	if e := ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, handle); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	os.Exit(0)
}
func publicSDKClient(t *testing.T) *Client {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	m := privateSDKManifest(false, false)
	m.ManagedPublicServices = true
	c, e := (Runner{Timeout: 3 * time.Second, ConcurrentCalls: true, RequireConcurrent: true}).Start(context.Background(), Command{Path: exe, Args: []string{"-test.run=^TestSourcePublicSDKWorkerFixture$", "--", "owned-public-sdk-worker"}}, m, SessionBinding{Identity: Identity{PluginID: m.PluginID, InstanceID: "OwnedPublic", Version: m.Version}, Generation: 1})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := c.Stop(ctx); e != nil {
			t.Error(e)
		}
		if n, overflow := c.stderr.snapshot(); n != 0 || overflow {
			t.Error("owned SDK diagnostics/race", n, overflow)
		}
	})
	return c
}
func TestSourcePublicSDKCompiledOptInRequired(t *testing.T) {
	ordinary := privateSDKClient(t, false, false)
	if ordinary.SupportsPublicServiceStop() {
		t.Fatal("ordinary worker acquired source retention")
	}
	m := privateSDKManifest(false, true)
	m.ManagedPublicServices = true
	if codeOf(m.Validate()) != "invalid_manifest" {
		t.Fatal("source opt-in+cleanup invalid combination accepted")
	}
}

func TestSourcePublicSDKMissingFeatureRejectsBeforeBusiness(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	m := privateSDKManifest(false, false)
	m.ManagedPublicServices = true
	// The actual existing ordinary worker is built without the new opt-in. Its
	// methods/capabilities match, but its negotiated hello has no public feature.
	c, e := (Runner{Timeout: 3 * time.Second, ConcurrentCalls: true}).Start(context.Background(), Command{Path: exe, Args: []string{"-test.run=^TestSourcePrivateSDKWorkerFixture$", "--", "owned-private-scheduler-worker"}}, m, SessionBinding{Identity: Identity{PluginID: m.PluginID, InstanceID: "OwnedNoFeature", Version: m.Version}, Generation: 1})
	if c != nil || codeOf(e) != "public_service_unsupported" {
		if c != nil {
			_ = c.CloseTransport(context.Background())
		}
		t.Fatal("compiled claim without real feature did not fail early", e)
	}
}
