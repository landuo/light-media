// OwnedStandardFixture is an owned persistent SDK object for the original
// public module contract. It has no invoke or native compatibility adapter.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"light-media/internal/plugins"
)

// fixtureVersion is set only when building a distinct owned test artifact.
var fixtureVersion = "1.0"
var fixtureRawState = ""
var fixturePluginID = "OwnedStandardFixture"
var fixtureName = "Owned standard fixture"

// A link-time test-only switch keeps the ordinary fixture manifest unchanged.
// Each variant is a genuine executable whose handshake declares its methods.
var fixtureDashboardMethods = ""
var fixtureDashboardParameters = ""
var fixtureAuthLevel = ""
var fixturePageEvents = ""
var fixturePageDelay = ""
var fixtureRichForm = ""

//go:embed rich-form.json
var ownedRichForm json.RawMessage

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
	m := plugins.Manifest{
		SchemaVersion: 1, PluginID: fixturePluginID, Name: fixtureName, Version: fixtureVersion,
		Runtime: "go-executable", Entry: "owned-standard-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "close", "stop_service", "stop", "snapshot", "tick", "event", "http"},
		Capabilities: plugins.BaseMethods(), Events: []string{"owned.standard", "command"}, HasPage: true,
		APIs: []plugins.HTTPAPI{{Key: "echo", Path: "/echo", Methods: []string{"GET"}, Auth: "apikey"}},
	}
	if fixtureAuthLevel != "" {
		level, err := strconv.Atoi(fixtureAuthLevel)
		if err != nil {
			panic(err)
		}
		m.AuthLevel = &level
	}
	if fixturePageEvents != "" {
		m.APIs[0].Auth = "bear"
		m.APIs[0].Methods = []string{"GET", "POST"}
		m.APIs[0].Body = "json"
	}
	if fixtureDashboardParameters != "" {
		var parameters []string
		if err := json.Unmarshal([]byte(fixtureDashboardParameters), &parameters); err != nil {
			panic(err)
		}
		m.DashboardParameters = &parameters
	}
	if fixtureDashboardMethods == "multi" || fixtureDashboardMethods == "none" {
		m.Methods = append(m.Methods, "get_dashboard_meta")
	}
	if fixtureDashboardMethods == "none" {
		methods := make([]string, 0, len(m.Methods))
		for _, method := range m.Methods {
			if method != "get_dashboard" {
				methods = append(methods, method)
			}
		}
		m.Methods = methods
	}
	return m
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
	var dataPath string
	if fixturePageEvents != "" {
		dataPath, err = base.GetDataPath(ctx)
		if err != nil {
			return nil, err
		}
	}
	observation := map[string]any{
		"version": fixtureVersion, "nonce": os.Getpid(), "init_count": o.initCount, "init_input": rawValue(o.initInput), "init_after": rawValue(o.initAfter),
		"config": rawValue(config), "calls": o.calls, "lastpayload": o.lastPayload, "stale_denied": o.staleDenied, "value": rawValue(value), "order": o.order,
	}
	if dataPath != "" {
		observation["data_path"] = dataPath
	}
	return json.Marshal(observation)
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
	var controls struct {
		RenderMode        json.RawMessage `json:"render_mode"`
		Sidebar           json.RawMessage `json:"sidebar_nav"`
		FailState         bool            `json:"fail_state"`
		FailRender        bool            `json:"fail_render"`
		FailSidebar       bool            `json:"fail_sidebar"`
		DashboardMeta     json.RawMessage `json:"dashboard_meta"`
		FailDashboardMeta bool            `json:"fail_dashboard_meta"`
	}
	if err := json.Unmarshal(config, &controls); err != nil {
		return nil, err
	}
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
			if fixtureRawState == "true" {
				o.enabled = rawValue(enabled)
			} else if err := json.Unmarshal(enabled, &o.enabled); err != nil {
				return nil, err
			}
		}
		if err := saveShared(ctx, base, config); err != nil {
			return nil, err
		}
		if fixturePageEvents != "" {
			path, err := base.GetDataPath(ctx)
			if err != nil {
				return nil, err
			}
			file, err := os.OpenFile(filepath.Join(path, "owned-file.txt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err == nil {
				_, writeErr := file.WriteString("owned initial file")
				closeErr := file.Close()
				if writeErr != nil {
					return nil, writeErr
				}
				if closeErr != nil {
					return nil, closeErr
				}
			} else if !errors.Is(err, os.ErrExist) {
				return nil, err
			}
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
		if controls.FailState {
			return nil, errors.New("owned state failure")
		}
		return json.Marshal(o.enabled)
	case "get_service":
		return json.Marshal([]map[string]any{{"id": "owned_standard_tick", "name": "Owned standard interval", "handler": "tick", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 1000}, "input": map[string]any{"tag": "service"}}})
	case "get_command":
		return json.Marshal([]map[string]any{{"cmd": "/owned_standard", "event": "command", "desc": "Owned standard command", "data": map[string]any{"action": "command", "tag": "command"}}})
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		if controls.FailRender {
			return nil, errors.New("owned render failure")
		}
		if len(controls.RenderMode) > 0 {
			return controls.RenderMode, nil
		}
		return json.RawMessage(`["vuetify", ""]`), nil
	case "get_form":
		if fixtureRichForm != "" {
			return rawValue(ownedRichForm), nil
		}
		return json.RawMessage(`[[{"component":"VSwitch","props":{"model":"enabled","label":"Enabled"}},{"component":"VTextField","props":{"model":"key","label":"Owned value"}}],{"enabled":false,"key":"form-default"}]`), nil
	case "get_page":
		if fixturePageEvents != "" {
			key := "shared"
			value, err := base.GetData(ctx, &key)
			if err != nil {
				return nil, err
			}
			return json.Marshal([]any{
				map[string]any{"component": "style", "props": map[string]any{"media": "screen"}, "text": ".owned-page-style-probe { color:rgb(12,34,56); font-variant:small-caps; letter-spacing:2px }"},
				map[string]any{"component": "VCard", "props": map[string]any{"title": "{{ owned_literal_title }}"}, "content": []any{
					map[string]any{"component": "VCardText", "html": "<b class=\"owned-page-style-probe\">Owned page CSS probe</b>"},
					map[string]any{"component": "VCardText", "text": "Owned JSON value: " + string(value)},
					map[string]any{"component": "VBtn", "text": "Write owned JSON value", "props": map[string]any{"color": "primary"}, "events": map[string]any{"click": map[string]any{"method": "POST", "api": "plugin/" + fixturePluginID + "/echo", "params": map[string]any{"value": "browser-page-write"}}}},
					map[string]any{"component": "VBtn", "text": "Fail owned JSON API", "events": map[string]any{"click": map[string]any{"method": "GET", "api": "plugin/" + fixturePluginID + "/echo", "params": map[string]any{"fail": true}}}},
				}}})
		}
		return json.RawMessage(`[{"component":"VCard","text":"Owned standard page"}]`), nil
	case "get_dashboard_meta":
		if controls.FailDashboardMeta {
			return nil, errors.New("owned dashboard metadata failure")
		}
		return rawValue(controls.DashboardMeta), nil
	case "get_dashboard":
		return json.RawMessage(`[{},{},[{"component":"VCard","text":"Owned standard dashboard"}]]`), nil
	case "get_sidebar_nav":
		if controls.FailSidebar {
			return nil, errors.New("owned sidebar failure")
		}
		if len(controls.Sidebar) > 0 {
			return controls.Sidebar, nil
		}
		return json.RawMessage(`[{"nav_key":"main","title":"Owned standard","section":"system","order":1}]`), nil
	case "snapshot":
		return o.snapshot(ctx, base, config)
	case "tick", "event":
		if method == "event" {
			var event struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(raw, &event); err != nil {
				return nil, err
			}
			if event.Type == "command" {
				var data struct {
					Action string `json:"action"`
				}
				if err := json.Unmarshal(event.Data, &data); err != nil || data.Action != "command" {
					return nil, errors.New("invalid owned command event")
				}
				o.calls["command"]++
				o.lastPayload["command"] = rawValue(event.Data)
			}
		}
		if err := saveShared(ctx, base, config); err != nil {
			return nil, err
		}
		return o.snapshot(ctx, base, config)
	case "http":
		var request plugins.HTTPRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Key != "echo" || request.Path != "/echo" || request.Method != "GET" && (fixturePageEvents == "" || request.Method != "POST") {
			return nil, errors.New("unsupported owned standard HTTP request")
		}
		if fixturePageEvents != "" && plugins.LastQuery(request.Query, "fail") == "true" {
			return json.Marshal(plugins.HTTPResponse{Status: 503, Body: json.RawMessage(`{"success":false,"message":"Owned JSON API failure"}`), Headers: map[string]string{"Content-Type": "application/json"}})
		}
		if fixturePageEvents != "" && request.Method == "POST" {
			if fixturePageDelay != "" {
				timer := time.NewTimer(2 * time.Second)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
			var input struct {
				Value string `json:"value"`
			}
			if json.Unmarshal(request.Body, &input) != nil {
				return nil, errors.New("invalid owned page input")
			}
			if err := base.SaveData(ctx, "shared", input.Value); err != nil {
				return nil, err
			}
			return json.Marshal(plugins.HTTPResponse{Status: 200, Body: json.RawMessage(`{"success":true}`), Headers: map[string]string{"Content-Type": "application/json"}})
		}
		if fixturePageEvents == "" {
			if err := saveShared(ctx, base, config); err != nil {
				return nil, err
			}
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
