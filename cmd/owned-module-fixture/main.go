// OwnedModuleFixture is a test artifact, not a builtin or a host capability.
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

// Build two actual artifacts with -ldflags '-X main.fixtureVersion=2.0'.
var fixtureVersion = "1.0"

type object struct {
	mu           sync.Mutex
	initCount    int
	stopCount    int
	failureCount int
	config       json.RawMessage
	stateCount   int
	serviceCount int
	commandCount int
	calls        []string
}

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedModuleFixture", Name: "Owned module fixture", Version: fixtureVersion, Runtime: "go-executable", Entry: "owned-module-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"invoke", "init", "get_state", "get_service", "get_command", "get_api", "snapshot", "http", "stop"}, Capabilities: []string{},
		APIs: []plugins.HTTPAPI{{Key: "owned_status", Path: "/status", Methods: []string{"GET"}, Auth: "bear"}}}
}
func (o *object) snapshot() map[string]any {
	cfg := append(json.RawMessage(nil), o.config...)
	if len(cfg) == 0 {
		cfg = json.RawMessage("null")
	}
	return map[string]any{"version": fixtureVersion, "init_count": o.initCount, "stop_count": o.stopCount, "failure_count": o.failureCount, "config": cfg, "state_count": o.stateCount, "service_count": o.serviceCount, "command_count": o.commandCount, "calls": append([]string(nil), o.calls...)}
}
func (o *object) handle(_ context.Context, method string, raw json.RawMessage, _ plugins.HostCaller) (json.RawMessage, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if method == "invoke" {
		var invocation plugins.Invocation
		if err := json.Unmarshal(raw, &invocation); err != nil {
			return nil, err
		}
		method = invocation.Action
		if method == "reload" {
			method = "init"
		}
		if method == "init" {
			raw = invocation.Config
		} else {
			raw = invocation.Input
		}
	}
	switch method {
	case "init":
		if len(raw) == 0 {
			raw = json.RawMessage("null")
		}
		var cfg map[string]any
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
		o.initCount++
		o.config = append(json.RawMessage(nil), raw...)
		// Mutation precedes a genuine business error; the next separate IPC call
		// must observe it. No host persistence or process restart restores this state.
		if cfg["fail_after_mutation"] == true {
			o.failureCount++
			return nil, errors.New("owned initialization failure after mutation")
		}
		return json.Marshal(o.snapshot())
	case "get_state":
		o.stateCount++
		var cfg map[string]any
		_ = json.Unmarshal(o.config, &cfg)
		o.calls = append(o.calls, fmt.Sprintf("get_state:%v", cfg["enabled"]))
		return json.Marshal(cfg["enabled"])
	case "get_service":
		o.serviceCount++
		o.calls = append(o.calls, "get_service")
		var cfg map[string]any
		_ = json.Unmarshal(o.config, &cfg)
		cron, _ := cfg["cron"].(string)
		if cron == "" {
			return json.RawMessage(`[]`), nil
		}
		return json.Marshal([]map[string]any{{"id": "owned_module_tick", "name": "Owned no-effect service", "handler": "owned_tick", "trigger": map[string]any{"kind": "cron", "cron": cron, "timezone": "UTC"}, "misfire_grace_ms": 1000}})
	case "get_command":
		o.commandCount++
		o.calls = append(o.calls, "get_command")
		var cfg map[string]any
		_ = json.Unmarshal(o.config, &cfg)
		return json.Marshal([]map[string]any{{"cmd": "/owned_module", "event": "owned_tick", "desc": "Owned no-effect command", "data": map[string]any{"init_count": o.initCount, "action": "owned_tick"}}})
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "snapshot", "owned_tick":
		return json.Marshal(o.snapshot())
	case "http", "api":
		var request plugins.HTTPRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Key != "owned_status" || request.Method != "GET" || request.Path != "/status" {
			return nil, errors.New("unsupported owned request")
		}
		body, err := json.Marshal(o.snapshot())
		if err != nil {
			return nil, err
		}
		return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
	case "stop":
		o.stopCount++
		result, err := json.Marshal(o.snapshot())
		// An owned diagnostic without config/credentials also observes stop if the
		// App consumes the successful stop result while closing the session.
		fmt.Fprintf(os.Stderr, "owned_module_stop version=%s init_count=%d stop_count=%d\n", fixtureVersion, o.initCount, o.stopCount)
		return result, err
	default:
		return nil, errors.New("unsupported owned method")
	}
}
func main() {
	m := manifest()
	if err := m.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		if err := json.NewEncoder(os.Stdout).Encode(m); err != nil {
			os.Exit(2)
		}
		return
	}
	o := &object{}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, o.handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
