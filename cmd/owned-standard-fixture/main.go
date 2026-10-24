// OwnedStandardFixture is an owned persistent SDK object for the original
// public module contract. It has no invoke or native compatibility adapter.
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

// fixtureVersion is set only when building a distinct owned test artifact.
var fixtureVersion = "1.0"

type object struct {
	mu          sync.Mutex
	initCount   int
	initInput   json.RawMessage
	initAfter   json.RawMessage
	enabled     any
	calls       map[string]int
	lastPayload map[string]json.RawMessage
	previous    plugins.HostCaller
	staleDenied bool
	order       []string
}

func manifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "OwnedStandardFixture", Name: "Owned standard fixture", Version: fixtureVersion,
		Runtime: "go-executable", Entry: "owned-standard-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "close", "stop_service", "stop", "snapshot", "tick", "command", "event", "http"},
		Capabilities: plugins.BaseMethods(), Events: []string{"owned.standard"}, HasPage: true,
		APIs: []plugins.HTTPAPI{{Key: "echo", Path: "/echo", Methods: []string{"GET"}, Auth: "apikey"}},
	}
}

func rawValue(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return append(json.RawMessage(nil), raw...)
}

func (o *object) snapshot(ctx context.Context, base plugins.Base, config json.RawMessage) (json.RawMessage, error) {
	key := "shared"
	value, err := base.GetData(ctx, &key)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"version": fixtureVersion, "nonce": os.Getpid(), "init_count": o.initCount, "init_input": rawValue(o.initInput), "init_after": rawValue(o.initAfter),
		"config": rawValue(config), "calls": o.calls, "lastpayload": o.lastPayload, "stale_denied": o.staleDenied, "value": rawValue(value), "order": o.order,
	})
}

func saveShared(ctx context.Context, base plugins.Base, config json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(config, &fields); err != nil {
		return err
	}
	// No implicit data/default is created for None or an absent key. Explicit
	// false, zero and null remain distinct supplied values in Base storage.
	if value, present := fields["key"]; present {
		return base.SaveData(ctx, "shared", value)
	}
	return nil
}

func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.previous != nil {
		old := plugins.Base{Host: o.previous}
		err := old.SaveData(ctx, "owned_stale_must_not_exist", true)
		o.staleDenied = err != nil
		if err == nil {
			return nil, errors.New("prior callback survived its completed SDK phase")
		}
	}
	o.previous = host
	if o.calls == nil {
		o.calls = map[string]int{}
		o.lastPayload = map[string]json.RawMessage{}
	}
	o.calls[method]++
	o.lastPayload[method] = rawValue(raw)
	base := plugins.Base{Host: host}
	config, err := base.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	config = rawValue(config)
	switch method {
	case "init":
		// init receives the configuration itself, never an Invocation envelope.
		o.initCount++
		o.initInput, o.initAfter = rawValue(raw), rawValue(config)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(o.initInput, &fields); err != nil {
			return nil, err
		}
		if enabled, present := fields["enabled"]; present {
			if err := json.Unmarshal(enabled, &o.enabled); err != nil {
				return nil, err
			}
		}
		if err := saveShared(ctx, base, config); err != nil {
			return nil, err
		}
		// Only the genuine 1.0 executable writes this owned continuity marker.
		// Version 2.0 must retain it through publication rather than recreate it.
		if fixtureVersion == "1.0" {
			if _, supplied := fields["key"]; supplied {
				if err := base.SaveData(ctx, "owned_v1_preserved", map[string]any{"nonce": os.Getpid(), "value": fields["key"]}); err != nil {
					return nil, err
				}
			}
		}
		if string(fields["fail_init"]) == "true" {
			return nil, errors.New("owned init failure after actual member and Base mutation")
		}
		return o.snapshot(ctx, base, config)
	case "get_state":
		return json.Marshal(o.enabled)
	case "get_service":
		return json.Marshal([]map[string]any{{"id": "owned_standard_tick", "name": "Owned standard interval", "handler": "tick", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 1000}, "input": map[string]any{"tag": "service"}}})
	case "get_command":
		return json.Marshal([]map[string]any{{"cmd": "/owned_standard", "event": "command", "desc": "Owned standard command", "data": map[string]any{"action": "command", "tag": "command"}}})
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		return json.RawMessage(`["vuetify", ""]`), nil
	case "get_form":
		return json.RawMessage(`[[{"component":"VSwitch","props":{"model":"enabled","label":"Enabled"}},{"component":"VTextField","props":{"model":"key","label":"Owned value"}}],{"enabled":false,"key":"form-default"}]`), nil
	case "get_page":
		return json.RawMessage(`[{"component":"VCard","text":"Owned standard page"}]`), nil
	case "get_dashboard":
		return json.RawMessage(`[{},{},[{"component":"VCard","text":"Owned standard dashboard"}]]`), nil
	case "get_sidebar_nav":
		return json.RawMessage(`[{"nav_key":"main","title":"Owned standard","section":"system","order":1}]`), nil
	case "snapshot":
		return o.snapshot(ctx, base, config)
	case "tick", "command", "event":
		if err := saveShared(ctx, base, config); err != nil {
			return nil, err
		}
		return o.snapshot(ctx, base, config)
	case "http":
		var request plugins.HTTPRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Key != "echo" || request.Method != "GET" || request.Path != "/echo" {
			return nil, errors.New("unsupported owned standard HTTP request")
		}
		if err := saveShared(ctx, base, config); err != nil {
			return nil, err
		}
		body, err := o.snapshot(ctx, base, config)
		if err != nil {
			return nil, err
		}
		return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
	case "close", "stop_service":
		o.order = append(o.order, method)
		if err := base.SaveData(ctx, "owned_"+method, map[string]any{"version": fixtureVersion, "nonce": os.Getpid(), "init_count": o.initCount, "order": o.order}); err != nil {
			return nil, err
		}
		if method == "close" {
			var fields struct {
				FailClose bool `json:"fail_close"`
			}
			if err := json.Unmarshal(config, &fields); err != nil {
				return nil, err
			}
			if fields.FailClose {
				return nil, errors.New("owned close failure after observation")
			}
		}
		return json.RawMessage("null"), nil
	case "stop":
		o.order = append(o.order, method)
		if err := base.SaveData(ctx, "owned_stop", map[string]any{"version": fixtureVersion, "nonce": os.Getpid(), "init_count": o.initCount, "stop_count": o.calls["stop"], "stale_denied": o.staleDenied, "order": o.order}); err != nil {
			return nil, err
		}
		return json.RawMessage("null"), nil
	default:
		return nil, errors.New("unsupported owned standard method")
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
	o := &object{enabled: false}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, o.handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
