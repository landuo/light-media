package pluginuninstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	plugins "light-media/builtinplugins/internal/sdk"
)

func TestUninstallProtocolWorker(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "plugin-uninstall-sdk-fixture") {
		return
	}
	if err := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, Manifest(), New().Handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

type uninstallPhase struct {
	Method string
	Calls  int
}
type uninstallProtocol struct {
	t                *testing.T
	ctx              context.Context
	cancel           context.CancelFunc
	c                *plugins.Client
	id               plugins.Identity
	mu               sync.Mutex
	phases           []*uninstallPhase
	trace            []string
	installed        []string
	own              json.RawMessage
	reject, block    string
	entered, release chan struct{}
	once             sync.Once
	children         sync.WaitGroup
}

func startUninstallProtocol(t *testing.T, ids []string) *uninstallProtocol {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest()
	id := plugins.Identity{PluginID: m.PluginID, InstanceID: "PluginUnInstallowned", Version: m.Version}
	c, err := (plugins.Runner{Timeout: 5 * time.Second, ConcurrentCalls: true, RequireConcurrent: true}).Start(ctx, plugins.Command{Path: exe, Args: []string{"-test.run=^TestUninstallProtocolWorker$", "--", "plugin-uninstall-sdk-fixture"}}, m, plugins.SessionBinding{Identity: id, Generation: 1})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	f := &uninstallProtocol{t: t, ctx: ctx, cancel: cancel, c: c, id: id, installed: append([]string{}, ids...), trace: []string{}, entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		cancel()
		ctx, x := context.WithTimeout(context.Background(), 4*time.Second)
		defer x()
		_ = c.Stop(ctx)
		f.children.Wait()
	})
	return f
}
func (f *uninstallProtocol) phase(method string) plugins.HostHandler {
	p := &uninstallPhase{Method: method}
	f.mu.Lock()
	f.phases = append(f.phases, p)
	f.mu.Unlock()
	return func(ctx context.Context, id plugins.Identity, m string, raw json.RawMessage) (json.RawMessage, error) {
		if id != f.id {
			return nil, errors.New("fixture identity mismatch")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f.mu.Lock()
		p.Calls++
		f.mu.Unlock()
		if m == plugins.BackgroundSubmitCapability {
			var in struct {
				Method  string
				Payload json.RawMessage
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			child, err := f.c.StartCallWithAdmission(ctx, f.ctx, in.Method, in.Payload, f.phase(in.Method), plugins.CallOptions{CallerDeadlineOnly: true})
			if err != nil {
				return nil, err
			}
			f.children.Add(1)
			go func() { defer f.children.Done(); _, _ = child.Wait() }()
			return json.RawMessage(`{"accepted":true}`), nil
		}
		var action string
		if m == plugins.PluginManagerControlCapability {
			var r plugins.PluginManagerRequest
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			action = r.Action
		}
		if m == plugins.SettingsWriteCapability {
			action = "installed.write"
		}
		if m == plugins.BaseUpdateConfig {
			action = "own.write"
		}
		f.mu.Lock()
		f.trace = append(f.trace, m+":"+action)
		reject, block := f.reject == action && action != "", f.block == action && action != ""
		f.mu.Unlock()
		if block {
			f.once.Do(func() { close(f.entered) })
			select {
			case <-f.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if reject {
			return nil, errors.New("synthetic transient authority denial")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch m {
		case plugins.SettingsReadCapability:
			f.mu.Lock()
			ids := append([]string{}, f.installed...)
			f.mu.Unlock()
			return json.Marshal(plugins.SettingsHostResultV1{SchemaVersion: 1, Key: "UserInstalledPlugins", Value: rawJSON(ids)})
		case plugins.SettingsWriteCapability:
			var r plugins.SettingsHostWriteV1
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			var ids []string
			if err := json.Unmarshal(r.Value, &ids); err != nil {
				return nil, err
			}
			f.mu.Lock()
			f.installed = ids
			f.mu.Unlock()
			return json.Marshal(plugins.SettingsHostResultV1{SchemaVersion: 1, Key: r.Key, Value: r.Value})
		case plugins.PluginManagerControlCapability:
			var r plugins.PluginManagerRequest
			_ = json.Unmarshal(raw, &r)
			result := json.RawMessage(`null`)
			switch r.Action {
			case "code.exists":
				result = json.RawMessage(`true`)
			case "config.delete", "data.delete":
				if r.Force {
					return nil, errors.New("force forbidden")
				}
				result = json.RawMessage(`false`)
			}
			return json.Marshal(plugins.PluginControlResult{Accepted: true, Completed: true, Result: result})
		case plugins.BaseUpdateConfig:
			var r struct{ Config json.RawMessage }
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			f.mu.Lock()
			f.own = append(json.RawMessage{}, r.Config...)
			f.mu.Unlock()
			return json.RawMessage(`null`), nil
		case plugins.LogWriteCapability:
			return json.RawMessage(`null`), nil
		}
		return nil, fmt.Errorf("unexpected fixture resource %s", m)
	}
}
func (f *uninstallProtocol) init(ctx context.Context, selection []string) error {
	_, err := f.c.CallWithOptions(ctx, "init", rawJSON(map[string]any{"plugin_ids": selection, "clear_config": true, "clear_data": true, "unknown": "lost"}), f.phase("init"), plugins.CallOptions{CallerDeadlineOnly: true})
	return err
}
func TestIndependentWorkerHundredIDsUsesFresh64Budgets(t *testing.T) {
	ids := []string{}
	for i := range 100 {
		ids = append(ids, fmt.Sprintf("Owned%03d", i))
	}
	f := startUninstallProtocol(t, ids)
	if err := f.init(f.ctx, ids); err != nil {
		t.Fatal(err)
	}
	f.children.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.installed) != 0 || !reflectConfig(f.own, json.RawMessage(`{"plugin_ids":[],"clear_config":true,"clear_data":true}`)) {
		t.Fatal("source final tail was not awaited", string(f.own), f.installed)
	}
	calls, max := 0, 0
	for _, p := range f.phases {
		calls += p.Calls
		if p.Calls > max {
			max = p.Calls
		}
		if p.Calls > 41 {
			t.Fatalf("phase %s used %d calls", p.Method, p.Calls)
		}
	}
	if calls < 700 || len(f.phases) < 18 {
		t.Fatal("large pass did not cross ordinary64 budget", calls, len(f.phases))
	}
	t.Logf("independent worker: IDs=%d phases=%d totalRPC=%d maxRPC=%d normalSDKlimit=64", len(ids), len(f.phases), calls, max)
}
func reflectConfig(a, b json.RawMessage) bool {
	return fmt.Sprint(normalizeRaw(a)) == fmt.Sprint(normalizeRaw(b))
}
func TestIndependentWorkerOpaqueFailureStopsSourceTail(t *testing.T) {
	for _, reject := range []string{"services.remove", "remove", "code.exists", "code.remove", "config.delete", "data.delete", "installed.write", "own.write"} {
		t.Run(reject, func(t *testing.T) {
			f := startUninstallProtocol(t, []string{"Alpha", "Beta"})
			f.reject = reject
			if err := f.init(f.ctx, []string{"Alpha", "Beta"}); err == nil {
				t.Fatal("opaque host failure was swallowed")
			}
			f.children.Wait()
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.trace) == 0 || !strings.HasSuffix(f.trace[len(f.trace)-1], ":"+reject) {
				t.Fatal("source continued after denied/unknown operation", f.trace)
			}
			if reject != "own.write" && len(f.own) != 0 {
				t.Fatal("unexpected final config write")
			}
		})
	}
}
func TestIndependentWorkerCancellationWaitsAndStops(t *testing.T) {
	f := startUninstallProtocol(t, []string{"Alpha", "Beta"})
	f.block = "code.exists"
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- f.init(ctx, []string{"Alpha", "Beta"}) }()
	select {
	case <-f.entered:
	case <-f.ctx.Done():
		t.Fatal("worker did not enter real callback")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel success")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("owner did not finish canceled pass")
	}
	f.cancel()
	f.children.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasSuffix(f.trace[len(f.trace)-1], ":code.exists") || len(f.own) != 0 {
		t.Fatal("side effects after cancellation", f.trace)
	}
}
