// An owned protocol fixture, not a business plugin. Its only external effect
// is appending observations to the exclusive test directory in its config.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/plugins"
	"os"
	"strconv"
	"sync"
)

var pluginID = "OwnedOrderAlpha"
var order = "20"
var priority = "-5"
var defaultDeclaration = "false"

func manifest() plugins.Manifest {
	n, _ := strconv.Atoi(order)
	p, _ := strconv.Atoi(priority)
	m := plugins.Manifest{SchemaVersion: 1, PluginID: pluginID, Name: "Owned handler order fixture", Version: "1.0", Runtime: "go-executable", Entry: "owned-handler-order-fixture", HostAPIMin: 1, HostAPIMax: 1, PluginOrder: n,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "event", "reset_first", "reset_second", "close", "stop_service", "stop"}, Capabilities: plugins.BaseMethods(),
		ResetHandlers: []plugins.PluginDataResetHandler{{Method: "reset_first", Priority: &p}, {Method: "reset_second", Priority: &p}}}
	if defaultDeclaration == "true" {
		m.PluginOrder = 0
		m.ResetHandlers = nil
		m.Events = []string{"plugin_data_reset"}
	}
	return m
}

type options struct {
	Enabled    bool   `json:"enabled"`
	Trace      string `json:"trace_path"`
	Label      string `json:"label"`
	FailInit   bool   `json:"fail_init"`
	FailMethod string `json:"fail_method"`
}
type fixture struct{ mu sync.Mutex }

func (f *fixture) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	config, err := (plugins.Base{Host: host}).GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	var o options
	if len(config) > 0 && string(config) != "null" {
		if err = json.Unmarshal(config, &o); err != nil {
			return nil, err
		}
	}
	record := func() error {
		if o.Trace == "" {
			return nil
		}
		file, err := os.OpenFile(o.Trace, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		return json.NewEncoder(file).Encode(map[string]any{"label": o.Label, "method": method, "pid": os.Getpid(), "input": json.RawMessage(raw)})
	}
	switch method {
	case "init":
		if err = record(); err != nil {
			return nil, err
		}
		if o.FailInit {
			return nil, errors.New("owned init failure")
		}
		return json.RawMessage("null"), nil
	case "event", "reset_first", "reset_second":
		if err = record(); err != nil {
			return nil, err
		}
		if method == o.FailMethod {
			return nil, errors.New("owned handler failure after recording")
		}
		return json.RawMessage("null"), nil
	case "get_state":
		return json.Marshal(o.Enabled)
	case "get_render_mode":
		return json.RawMessage(`["vuetify", ""]`), nil
	case "get_form":
		return json.RawMessage(`[[],{}]`), nil
	case "get_dashboard":
		return json.RawMessage(`[{},{},[]]`), nil
	case "get_service", "get_command", "get_api", "get_page", "get_sidebar_nav":
		return json.RawMessage(`[]`), nil
	case "close", "stop_service", "stop":
		return json.RawMessage(`null`), nil
	default:
		return nil, errors.New("unsupported method")
	}
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
	f := &fixture{}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, f.handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
