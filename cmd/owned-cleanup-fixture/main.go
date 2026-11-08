// Owned protocol fixture, never installed outside isolated tests.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"light-media/internal/plugins"
)

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedCleanup", Name: "Owned cleanup continuation fixture", Version: "1.0", Runtime: "go-executable", Entry: "owned-cleanup-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "configure", "cleanup_continue", "close", "stop_service", "stop"}, Capabilities: append(plugins.BaseMethods(), plugins.BackgroundSubmitCapability, plugins.CleanupSubmitCapability)}
}

type object struct {
	mu    sync.Mutex
	count int
	done  chan struct{}
	first error
}

func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	base := plugins.Base{Host: host}
	manager := plugins.Manager{Host: host}
	switch method {
	case "init", "close", "stop":
		return json.RawMessage(`null`), nil
	case "configure":
		var in struct {
			Count int
			Probe bool
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		if in.Probe {
			err := manager.SubmitCleanup(ctx, "cleanup_continue", map[string]int{"n": 1})
			return json.Marshal(map[string]bool{"denied": err != nil, "cleanup": plugins.IsCleanupInvocation(ctx)})
		}
		o.mu.Lock()
		o.count = in.Count
		o.mu.Unlock()
		return json.RawMessage(`null`), nil
	case "stop_service":
		if !plugins.IsCleanupInvocation(ctx) {
			return json.Marshal(map[string]bool{"denied": manager.SubmitCleanup(ctx, "cleanup_continue", map[string]int{"n": 1}) != nil, "cleanup": false})
		}
		o.mu.Lock()
		n := o.count
		o.count = 0
		o.first = nil
		o.done = make(chan struct{})
		done := o.done
		o.mu.Unlock()
		if n == 0 {
			return json.RawMessage(`null`), nil
		}
		if err := manager.SubmitCleanup(ctx, "cleanup_continue", map[string]int{"n": 1, "count": n}); err != nil {
			return nil, err
		}
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		o.mu.Lock()
		err := o.first
		o.mu.Unlock()
		return json.RawMessage(`null`), err
	case "cleanup_continue":
		if !plugins.IsCleanupInvocation(ctx) {
			return nil, errors.New("ordinary child is not cleanup")
		}
		var in struct{ N, Count int }
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		err := base.SaveData(ctx, fmt.Sprintf("effect_%04d", in.N), map[string]int{"n": in.N, "pid": os.Getpid()})
		if err == nil && in.N < in.Count {
			err = manager.SubmitCleanup(ctx, "cleanup_continue", map[string]int{"n": in.N + 1, "count": in.Count})
		}
		if err != nil || in.N == in.Count {
			o.mu.Lock()
			o.first = err
			close(o.done)
			o.mu.Unlock()
		}
		return json.RawMessage(`null`), err
	case "get_state":
		return json.RawMessage(`false`), nil
	case "get_service", "get_command", "get_api", "get_page", "get_sidebar_nav":
		return json.RawMessage(`[]`), nil
	case "get_render_mode":
		return json.RawMessage(`["vuetify",""]`), nil
	case "get_form":
		return json.RawMessage(`[[],{}]`), nil
	case "get_dashboard":
		return json.RawMessage(`[{},{},[]]`), nil
	}
	return nil, errors.New("unknown fixture method")
}
func main() {
	m := manifest()
	if err := m.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	o := &object{}
	if err := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, o.handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
