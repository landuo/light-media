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

var pluginID = "OwnedResetMutator"
var order = "0"
var priority = "-5"
var defaultDeclaration = "false"
var observer = "false"

func manifest() plugins.Manifest {
	n, _ := strconv.Atoi(order)
	p, _ := strconv.Atoi(priority)
	m := plugins.Manifest{SchemaVersion: 1, PluginID: pluginID, Name: "Owned handler order fixture", Version: "1.0", Runtime: "go-executable", Entry: "owned-reset-generic-fixture", HostAPIMin: 1, HostAPIMax: 1, PluginOrder: n,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "event", "reset_first", "reset_second", "system_error", "http", "close", "stop_service", "stop"}, Capabilities: plugins.BaseMethods(),
		ResetHandlers: []plugins.PluginDataResetHandler{{Method: "reset_first", Priority: &p}, {Method: "reset_second", Priority: &p}}}
	// Explicit generic declarations must win over BOTH compatibility formats.
	m.BroadcastHandlers = []plugins.BroadcastHandler{{Event: "system.error", Method: "system_error"}}
	m.APIs = []plugins.HTTPAPI{{Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}}
	m.ChainHandlers = []plugins.ChainHandler{{Event: "plugin.data.reset", Method: "reset_first", Priority: &p}, {Event: "plugin.data.reset", Method: "reset_second", Priority: &p}}
	m.ResetHandlers = []plugins.PluginDataResetHandler{{Method: "event"}}
	m.Events = []string{"plugin.data.reset"}
	if defaultDeclaration == "true" {
		m.PluginOrder = 0
		m.ResetHandlers = nil
		m.Events = []string{"plugin.data.reset"}
	}
	return m
}

type options struct {
	Enabled    bool            `json:"enabled"`
	Trace      string          `json:"trace_path"`
	Label      string          `json:"label"`
	FailInit   bool            `json:"fail_init"`
	FailMethod string          `json:"fail_method"`
	Mode       string          `json:"mode"`
	Payload    string          `json:"payload"`
	NameCase   string          `json:"name_case"`
	NameValue  json.RawMessage `json:"name_value"`
	SeedData   bool            `json:"seed_data"`
}
type fixture struct {
	mu          sync.Mutex
	namePresent bool
}

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
		if o.SeedData {
			if err := (plugins.Base{Host: host}).SaveData(ctx, "marker", "retained-data"); err != nil {
				return nil, err
			}
		}
		f.namePresent = o.NameCase != "missing" && o.NameCase != ""
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

		if observer == "true" {
			return json.RawMessage(`{"event_data":{"plugin_id":"ordinary-return-must-be-ignored"}}`), nil
		}
		if method == "reset_second" {
			switch o.Mode {
			case "invalid":
				return json.RawMessage(`{"event_mutation_contract":"light-media.mutable-event/v1","event":{"type":17,"data":"bad"},"handler_failed":false}`), nil
			case "ordinary":
				return json.RawMessage(`{"plugin_id":"ordinary-return-must-be-ignored","reset_config":true,"reset_data":true}`), nil
			case "none":
				return json.RawMessage(`null`), nil
			case "transport-error":
				return nil, errors.New("owned transport handler failure")
			}
		}
		return plugins.HandleMutableEventWithName(raw, func(event *plugins.MutableEvent) (json.RawMessage, error) {
			if method == "reset_first" {
				event.Data = json.RawMessage(`{"plugin_id":"changed-event-only","reset_config":false,"reset_data":false,"large":9007199254740993123456789,"extra":{"nested":[null,false,0,"owned"]}}`)
			} else {
				event.Data = append(json.RawMessage(nil), o.Payload...)
				if o.NameCase == "dynamic-delete" {
					f.namePresent = false
				}
				if o.Mode == "error" {
					return nil, errors.New("owned failure after mutation")
				}
				if o.Mode == "panic" {
					panic("owned panic after mutation")
				}
			}
			return json.RawMessage(`{"data":"ignored-return"}`), nil
		}, func() (json.RawMessage, bool, error) {
			if !f.namePresent {
				return nil, false, nil
			}
			old := method
			method = "name_access"
			err := record()
			method = old
			if err != nil {
				return nil, false, err
			}
			if o.NameCase == "getter-error" || o.NameCase == "success-with-getter-error" {
				return nil, false, errors.New("owned getter failure")
			}
			return o.NameValue, true, nil
		})
	case "system_error":
		if observer == "true" && o.NameCase != "" {
			if err := record(); err != nil {
				return nil, err
			}
		}
		return json.RawMessage(`null`), nil
	case "http":
		key := "marker"
		value, err := (plugins.Base{Host: host}).GetData(ctx, &key)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(map[string]any{"config": json.RawMessage(config), "data": value, "pid": os.Getpid(), "name_present": f.namePresent})
		if err != nil {
			return nil, err
		}
		return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body})
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_state":
		return json.Marshal(o.Enabled)
	case "get_render_mode":
		return json.RawMessage(`["vuetify", ""]`), nil
	case "get_form":
		return json.RawMessage(`[[],{}]`), nil
	case "get_dashboard":
		return json.RawMessage(`[{},{},[]]`), nil
	case "get_service", "get_command", "get_page", "get_sidebar_nav":
		return json.RawMessage(`[]`), nil
	case "close", "stop_service", "stop":
		if o.NameCase != "" {
			if err := record(); err != nil {
				return nil, err
			}
		}
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
