// Owned protocol fixture only. No message-provider or business-plugin logic.
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

var pluginID = "OwnedCommandMutator"
var priority = "0"
var shorthand = "false"

func manifest() plugins.Manifest {
	p, _ := strconv.Atoi(priority)
	m := plugins.Manifest{SchemaVersion: 1, PluginID: pluginID, Name: "Owned command registration fixture", Version: "1.0", Runtime: "go-executable", Entry: "owned-command-register-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "event", "chain_first", "chain_second", "close", "stop_service", "stop"}, Capabilities: plugins.BaseMethods(),
		Events: []string{"command.register"}, ChainHandlers: []plugins.ChainHandler{{Event: "command.register", Method: "chain_first", Priority: &p}, {Event: "command.register", Method: "chain_second", Priority: &p}}}
	if shorthand == "true" {
		m.ChainHandlers = nil
	}
	return m
}

type options struct {
	Enabled  bool   `json:"enabled"`
	Trace    string `json:"trace_path"`
	Label    string `json:"label"`
	Mode     string `json:"mode"`
	Source   string `json:"source"`
	Command  string `json:"command"`
	FailInit bool   `json:"fail_init"`
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
		file, err := os.OpenFile(o.Trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		return json.NewEncoder(file).Encode(map[string]any{"label": o.Label, "method": method, "pid": os.Getpid(), "input": json.RawMessage(raw)})
	}
	switch method {
	case "init":
		if o.FailInit {
			return nil, errors.New("owned init failure")
		}
		return json.RawMessage(`null`), nil
	case "chain_first", "chain_second", "event":
		if err = record(); err != nil {
			return nil, err
		}
		if method == "chain_second" || o.Mode == "observe" {
			return json.RawMessage(`null`), nil
		}
		if o.Mode == "ordinary" {
			return json.RawMessage(`{"commands":{},"cancel":true,"source":"ordinary-return"}`), nil
		}
		return plugins.HandleMutableEvent(raw, func(event *plugins.MutableEvent) (json.RawMessage, error) {
			var data map[string]json.RawMessage
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return nil, err
			}
			if o.Source != "" {
				data["source"], _ = json.Marshal(o.Source)
			}
			switch o.Mode {
			case "cancel":
				data["cancel"] = json.RawMessage(`true`)
			case "empty":
				data["commands"] = json.RawMessage(`{}`)
			case "unknown":
				data["commands"] = json.RawMessage(`{"/not_executable":{"description":"unknown"}}`)
			case "mutate", "mutate_error", "mutate_panic":
				var commands map[string]json.RawMessage
				if err := json.Unmarshal(data["commands"], &commands); err != nil {
					return nil, err
				}
				name := o.Command
				if name == "" {
					name = "/owned_register"
				}
				commands[name] = json.RawMessage(`{"type":"plugin","description":"changed menu only","category":null,"opaque":900719925474099312345}`)
				commands["/not_executable"] = json.RawMessage(`{"description":"unknown"}`)
				delete(commands, "/redo")
				data["commands"], _ = json.Marshal(commands)
			}
			event.Data, _ = json.Marshal(data)
			if o.Mode == "mutate_error" {
				return nil, errors.New("owned failure after mutation")
			}
			if o.Mode == "mutate_panic" {
				panic("owned panic after mutation")
			}
			return json.RawMessage(`{"cancel":true}`), nil
		})
	case "get_state":
		return json.Marshal(o.Enabled)
	case "get_command":
		name := o.Command
		if name == "" {
			name = "/owned_register"
		}
		return json.Marshal([]map[string]any{
			{"cmd": name, "desc": "original executable", "category": "owned", "event": "owned.command", "data": map[string]any{"action": "not_a_method", "opaque": json.Number("900719925474099312345")}},
			{"cmd": name + "_hidden", "desc": "hidden executable", "show": false, "event": "owned.command", "data": map[string]any{}},
		})
	case "get_render_mode":
		return json.RawMessage(`["vuetify",""]`), nil
	case "get_form":
		return json.RawMessage(`[[],{}]`), nil
	case "get_dashboard":
		return json.RawMessage(`[{},{},[]]`), nil
	case "get_service", "get_api", "get_page", "get_sidebar_nav":
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
