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

func privateSDKManifest(managed, cleanup bool) Manifest {
	m := Manifest{ManagedPrivateScheduler: managed, SchemaVersion: 1, PluginID: "PrivateSchedulerFixture", Name: "Owned private scheduler", Version: "1.0", Runtime: "go-executable", Entry: "worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"invoke", "close", "stop_service", "stop"}, Capabilities: []string{BackgroundSubmitCapability, "test.host", "test.stop"}}
	if cleanup {
		m.Capabilities = append(m.Capabilities, CleanupSubmitCapability)
	}
	return m
}
func TestSourcePrivateSDKWorkerFixture(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "owned-private-scheduler-worker") {
		return
	}
	managed := strings.Contains(strings.Join(os.Args, " "), "--managed")
	cleanup := strings.Contains(strings.Join(os.Args, " "), "--cleanup")
	handle := func(ctx context.Context, method string, raw json.RawMessage, host HostCaller) (json.RawMessage, error) {
		switch method {
		case "invoke":
			return host(ctx, "test.host", raw)
		case "stop_service":
			flags, _ := json.Marshal(map[string]bool{"cleanup": IsCleanupInvocation(ctx)})
			return host(ctx, "test.stop", flags)
		default:
			return json.RawMessage(`null`), nil
		}
	}
	if e := ServeConcurrent(context.Background(), os.Stdin, os.Stdout, privateSDKManifest(managed, cleanup), handle); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	os.Exit(0)
}
func privateSDKClient(t *testing.T, managed, cleanup bool) *Client {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	m := privateSDKManifest(managed, cleanup)
	args := []string{"-test.run=^TestSourcePrivateSDKWorkerFixture$", "--", "owned-private-scheduler-worker"}
	if managed {
		args = append(args, "--managed")
	}
	if cleanup {
		args = append(args, "--cleanup")
	}
	c, e := (Runner{Timeout: 3 * time.Second, ConcurrentCalls: true, RequireConcurrent: true}).Start(context.Background(), Command{Path: exe, Args: args}, m, SessionBinding{Identity: Identity{PluginID: m.PluginID, InstanceID: "OwnedPrivate", Version: m.Version}, Generation: 1})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := c.Stop(ctx); e != nil {
			t.Error("owned SDK stop", e)
		}
		if n, overflow := c.stderr.snapshot(); n != 0 || overflow {
			t.Error("worker diagnostics/race", n, overflow)
		}
	})
	return c
}
func TestSourcePrivateSDKReservedPreStopAtFullCapacity(t *testing.T) {
	c := privateSDKClient(t, true, false)
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
	if _, e := c.CallPrivateSchedulerStop(context.Background(), "close", json.RawMessage(`{}`), nil); e != nil {
		t.Fatal("pre-stop close blocked by full ordinary table", e)
	}
	select {
	case e := <-results:
		t.Fatal("pre-stop close revoked existing callback", e)
	default:
	}
	stopped := 0
	_, e := c.CallPrivateSchedulerStop(context.Background(), "stop_service", json.RawMessage(`{}`), func(ctx context.Context, id Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
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
	if _, e := c.CallPrivateSchedulerStop(context.Background(), "close", json.RawMessage(`{}`), nil); codeOf(e) != "session_closed" {
		t.Fatal("postfence private lane reopened", e)
	}
}
func TestSourcePrivateSDKLaneDeclarationAndCleanupSeparation(t *testing.T) {
	for _, row := range []struct {
		name             string
		managed, cleanup bool
	}{{"legacy", false, false}, {"cleanup-only", false, true}, {"new-combination", true, true}} {
		t.Run(row.name, func(t *testing.T) {
			c := privateSDKClient(t, row.managed, row.cleanup)
			if _, e := c.CallPrivateSchedulerStop(context.Background(), "close", json.RawMessage(`{}`), nil); codeOf(e) != "invalid_invocation" {
				t.Fatal("undeclared/cleanup object acquired private pre-stop lane", e)
			}
			c.concurrent.mu.Lock()
			fenced := c.concurrent.draining
			c.concurrent.mu.Unlock()
			if fenced {
				t.Fatal("rejected private request changed ordinary fence")
			}
			if e := c.BeginShutdown(context.Background()); e != nil {
				t.Fatal(e)
			}
			if _, e := c.CallCleanup(context.Background(), "close", json.RawMessage(`{}`), nil); e != nil {
				t.Fatal("old postfence cleanup path changed", e)
			}
		})
	}
}
