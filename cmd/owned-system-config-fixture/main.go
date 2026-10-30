// An ordinary compiled external SDK worker used only with owned fixtures.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/plugins"
	"os"
	"strings"
	"sync"
)

var fixtureCapabilities = "system.config.get,system.config.set"
var fixtureVersion = "1.0"

func manifest() plugins.Manifest {
	caps := []string{plugins.BaseGetInstanceMetadata}
	if fixtureCapabilities != "" {
		caps = append(caps, strings.Split(fixtureCapabilities, ",")...)
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedSystemConfig", Name: "Owned global recognition words", Version: fixtureVersion, Runtime: "go-executable", Entry: "owned-system-config-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "resource", "snapshot", "http", "event", "close", "stop_service", "stop"}, Capabilities: caps, HasPage: true, Events: []string{"config.updated"}, APIs: []plugins.HTTPAPI{{Key: "shared", Path: "/shared", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}, {Key: "exercise", Path: "/exercise", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}, {Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}}}
}

type object struct {
	mu        sync.Mutex
	config    map[string]json.RawMessage
	initCount int
	previous  plugins.HostCaller
	events    []json.RawMessage
	last      any
}

func (o *object) snapshot(ctx context.Context, host plugins.HostCaller) (json.RawMessage, error) {
	o.mu.Lock()
	old := o.previous
	o.previous = host
	count := o.initCount
	events := append([]json.RawMessage{}, o.events...)
	o.mu.Unlock()
	denied := true
	if old != nil {
		_, e := (plugins.SharedSystemConfig{Host: old}).Get(ctx, plugins.SystemConfigCustomIdentifiers)
		denied = e != nil
	}
	return json.Marshal(map[string]any{"pid": os.Getpid(), "init_count": count, "events": events, "stale_denied": denied, "had_previous": old != nil})
}
func typed(ctx context.Context, host plugins.HostCaller, method string, raw json.RawMessage) (json.RawMessage, error) {
	s := plugins.SharedSystemConfig{Host: host}
	switch method {
	case "owned.remote.empty_url_sync":
		return remoteEmptyURLService(ctx, s)
	case plugins.SharedSystemConfigGet:
		var in plugins.SharedSystemConfigGetV1
		if e := json.Unmarshal(raw, &in); e != nil {
			return nil, e
		}
		return s.Get(ctx, in.Key)
	case plugins.SharedSystemConfigSet:
		var in plugins.SharedSystemConfigSetV1
		if e := json.Unmarshal(raw, &in); e != nil {
			return nil, e
		}
		v, e := s.Set(ctx, in.Key, in.Value)
		if e != nil {
			return nil, e
		}
		return json.Marshal(v)
	}
	return nil, errors.New("unsupported owned operation")
}
func (o *object) resource(ctx context.Context, host plugins.HostCaller, raw json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Method  string          `json:"method"`
		Payload json.RawMessage `json:"payload"`
	}
	if e := json.Unmarshal(raw, &in); e != nil {
		return nil, e
	}
	return typed(ctx, host, in.Method, in.Payload)
}
func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		var c map[string]json.RawMessage
		if e := json.Unmarshal(raw, &c); e != nil {
			return nil, e
		}
		o.mu.Lock()
		o.config = c
		o.initCount++
		o.mu.Unlock()
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.RawMessage(`true`), nil
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",""]`), nil
	case "stop", "stop_service":
		return json.RawMessage(`null`), nil
	case "close":
		o.mu.Lock()
		v, ok := o.config["close_value"]
		o.mu.Unlock()
		if ok {
			var text string
			if json.Unmarshal(v, &text) == nil && text != "" {
				_, e := (plugins.SharedSystemConfig{Host: host}).Set(ctx, plugins.SystemConfigCustomIdentifiers, json.RawMessage(text))
				if e != nil {
					return nil, e
				}
			}
		}
		return json.RawMessage(`null`), nil
	case "resource":
		return o.resource(ctx, host, raw)
	case "snapshot":
		return o.snapshot(ctx, host)
	case "event":
		o.mu.Lock()
		o.events = append(o.events, append(json.RawMessage(nil), raw...))
		o.mu.Unlock()
		return json.RawMessage(`null`), nil
	case "get_form":
		return json.Marshal([]any{[]any{map[string]any{"component": "VTextarea", "props": map[string]any{"model": "value_json", "label": "共享识别词 JSON"}}}, map[string]any{"value_json": `["Raw => Shared"]`}})
	case "get_page":
		metadata, e := (plugins.Base{Host: host}).GetInstanceMetadata(ctx)
		if e != nil {
			return nil, e
		}
		value, readErr := (plugins.SharedSystemConfig{Host: host}).Get(ctx, plugins.SystemConfigCustomIdentifiers)
		if readErr == nil {
			readErr = plugins.ValidateSystemConfigUTF8JSON(value)
		}
		if readErr != nil {
			value = nil
		}
		o.mu.Lock()
		last := o.last
		count := o.initCount
		o.mu.Unlock()
		view := map[string]any{"pid": os.Getpid(), "init_count": count, "value": value, "last_operation": last}
		if readErr != nil {
			view["read_error"] = readErr.Error()
		}
		text, _ := json.MarshalIndent(view, "", "  ")
		buttons := []any{}
		for _, op := range []string{"get", "set"} {
			buttons = append(buttons, map[string]any{"component": "VBtn", "props": map[string]any{"class": "ma-1", "variant": "outlined"}, "text": op, "events": map[string]any{"click": map[string]any{"api": "plugin/" + metadata.InstanceID + "/exercise", "method": "POST", "params": map[string]any{"operation": op}}}})
		}
		return json.Marshal([]any{map[string]any{"component": "div", "content": buttons}, map[string]any{"component": "pre", "text": string(text)}})
	case "http":
		var r plugins.HTTPRequest
		if e := json.Unmarshal(raw, &r); e != nil {
			return nil, e
		}
		if r.Key == "snapshot" {
			v, e := o.snapshot(ctx, host)
			if e != nil {
				return nil, e
			}
			return plugins.JSONHTTPResponse(200, v)
		}
		payload := r.Body
		if r.Key == "exercise" {
			var action struct{ Operation string }
			if e := json.Unmarshal(payload, &action); e != nil {
				return nil, e
			}
			method := plugins.SharedSystemConfigGet
			in := map[string]any{"key": plugins.SystemConfigCustomIdentifiers}
			if action.Operation == "set" {
				method = plugins.SharedSystemConfigSet
				o.mu.Lock()
				v := o.config["value_json"]
				o.mu.Unlock()
				text := `["Raw => Shared"]`
				if len(v) > 0 {
					if e := json.Unmarshal(v, &text); e != nil {
						return nil, e
					}
				}
				in["value"] = json.RawMessage(text)
			} else if action.Operation != "get" {
				return nil, errors.New("unknown exercise")
			}
			payload, _ = json.Marshal(map[string]any{"method": method, "payload": in})
		} else if r.Key != "shared" {
			return nil, errors.New("unknown owned endpoint")
		}
		result, e := o.resource(ctx, host, payload)
		if e == nil {
			e = plugins.ValidateSystemConfigUTF8JSON(result)
		}
		status := 200
		body := map[string]any{"success": e == nil, "result": result, "pid": os.Getpid()}
		if e != nil {
			status = 422
			body["message"] = e.Error()
			body["result"] = nil
		}
		o.mu.Lock()
		o.last = body
		o.mu.Unlock()
		return plugins.JSONHTTPResponse(status, body)
	}
	return nil, errors.New("unknown owned method")
}
func main() {
	m := manifest()
	if e := m.Validate(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	o := &object{}
	if e := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, o.handle); e != nil {
		fmt.Fprintln(os.Stderr, "owned system config worker failed")
		os.Exit(1)
	}
}
