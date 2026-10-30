// Owned ordinary manager-query verification plugin. It uses only public SDK
// calls and standard form/page/API declarations, never a host testing route.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"light-media/internal/plugins"
)

var pluginID = "OwnedAlpha"
var pluginOrder = "20"
var defaultState = "true"
var failInit = "false"

// The read-only outage variant retains all real query calls but does not add
// fixture-only persistent trace writes absent from the source oracle's init.
var saveObservations = "true"
var queryCapabilities = "plugin.manager.get_plugin_ids,plugin.manager.get_running_plugin_ids,plugin.manager.get_plugin_config"

func manifest() plugins.Manifest {
	order, _ := strconv.Atoi(pluginOrder)
	caps := plugins.BaseMethods()
	if queryCapabilities != "" {
		caps = append(caps, strings.Split(queryCapabilities, ",")...)
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: pluginID, Name: pluginID, Description: "Ordinary bound registry and configuration queries", Version: "1.0", PluginOrder: order, Runtime: "go-executable", Entry: "owned-manager-query-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "get_page", "get_render_mode", "query", "snapshot", "base", "http", "close", "stop_service", "stop"}, Capabilities: caps, HasPage: true, APIs: []plugins.HTTPAPI{{Key: "query", Path: "/query", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}, {Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}}}
}

type object struct {
	config    map[string]json.RawMessage
	calls     map[string]int
	initCount int
	previous  plugins.HostCaller
}

func query(ctx context.Context, host plugins.HostCaller, raw json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Method string          `json:"method"`
		Target json.RawMessage `json:"target"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	manager := plugins.SharedPluginManager{Host: host}
	switch in.Method {
	case "ids":
		ids, err := manager.GetPluginIDs(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(ids)
	case "running":
		ids, err := manager.GetRunningPluginIDs(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(ids)
	case "config":
		if len(in.Target) == 0 {
			return nil, errors.New("target required")
		}
		return manager.GetPluginConfig(ctx, in.Target)
	}
	return nil, errors.New("unknown query")
}

func (o *object) observation(ctx context.Context, host plugins.HostCaller) (map[string]any, error) {
	m := plugins.SharedPluginManager{Host: host}
	base := plugins.Base{Host: host}
	meta, err := base.GetInstanceMetadata(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := m.GetPluginIDs(ctx)
	if err != nil {
		return nil, err
	}
	running, err := m.GetRunningPluginIDs(ctx)
	if err != nil {
		return nil, err
	}
	conf, err := m.GetPluginConfig(ctx, meta.InstanceID)
	if err != nil {
		return nil, err
	}
	raw, err := base.GetConfig(ctx)
	return map[string]any{"id": meta.InstanceID, "classes": ids, "running": running, "config": conf, "base": raw, "pid": os.Getpid()}, err
}

func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	o.calls[method]++
	switch method {
	case "init":
		o.initCount++
		if err := json.Unmarshal(raw, &o.config); err != nil {
			return nil, err
		}
		if o.config == nil {
			o.config = map[string]json.RawMessage{}
		}
		if queryCapabilities != "" {
			value, err := o.observation(ctx, host)
			if err != nil {
				return nil, err
			}
			if saveObservations == "true" {
				if err = (plugins.Base{Host: host}).SaveData(ctx, "init_observation", value); err != nil {
					return nil, err
				}
			}
		}
		if failInit == "true" || string(o.config["fail_init"]) == "true" {
			return nil, errors.New("owned init failure")
		}
		return json.RawMessage(`null`), nil
	case "get_state":
		if value, ok := o.config["enabled"]; ok {
			return value, nil
		}
		return json.RawMessage(defaultState), nil
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",""]`), nil
	case "get_form":
		return json.Marshal([]any{[]any{map[string]any{"component": "VTextField", "props": map[string]any{"model": "target_id", "label": "Ordinary plugin configuration target"}}, map[string]any{"component": "VSwitch", "props": map[string]any{"model": "enabled", "label": "Enabled"}}}, map[string]any{"target_id": pluginID, "enabled": defaultState == "true"}})
	case "get_page":
		value, err := o.observation(ctx, host)
		if err != nil {
			return nil, err
		}
		target := pluginID
		if raw := o.config["target_id"]; len(raw) != 0 {
			_ = json.Unmarshal(raw, &target)
		}
		value["target_id"] = target
		value["target_config"], err = (plugins.SharedPluginManager{Host: host}).GetPluginConfig(ctx, target)
		if err != nil {
			return nil, err
		}
		value["init_count"] = o.initCount
		text, _ := json.MarshalIndent(value, "", "  ")
		return json.Marshal([]any{map[string]any{"component": "pre", "text": string(text)}})
	case "query":
		return query(ctx, host, raw)
	case "base":
		var in struct {
			Target string          `json:"target"`
			Value  json.RawMessage `json:"value"`
			Write  bool            `json:"write"`
			Key    *string         `json:"key"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		base := plugins.Base{Host: host}
		if in.Write {
			result, err := base.UpdateConfig(ctx, in.Value, in.Target)
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		}
		if in.Key != nil {
			return base.GetData(ctx, in.Key, in.Target)
		}
		return base.GetConfig(ctx, in.Target)
	case "snapshot":
		denied := true
		if o.previous != nil {
			_, err := o.previous(ctx, plugins.SharedPluginIDs, json.RawMessage(`{}`))
			denied = err != nil
		}
		o.previous = host
		return json.Marshal(map[string]any{"pid": os.Getpid(), "init_count": o.initCount, "calls": o.calls, "stale_denied": denied})
	case "http":
		var in plugins.HTTPRequest
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		var value json.RawMessage
		var err error
		if in.Key == "snapshot" {
			value, err = o.handle(ctx, "snapshot", json.RawMessage(`{}`), host)
		} else {
			value, err = query(ctx, host, in.Body)
		}
		if err != nil {
			return nil, err
		}
		return plugins.JSONHTTPResponse(200, value)
	case "stop_service":
		if queryCapabilities != "" {
			value, err := o.observation(ctx, host)
			if err != nil {
				return nil, err
			}
			if saveObservations == "true" {
				if err = (plugins.Base{Host: host}).SaveData(ctx, "stop_observation", value); err != nil {
					return nil, err
				}
			}
		}
		return json.RawMessage(`null`), nil
	case "close", "stop":
		return json.RawMessage(`null`), nil
	}
	return nil, errors.New("unknown method")
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(manifest())
		return
	}
	o := &object{calls: map[string]int{}}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest(), o.handle); err != nil {
		fmt.Fprintln(os.Stderr, "owned manager query worker stopped")
		os.Exit(1)
	}
}
