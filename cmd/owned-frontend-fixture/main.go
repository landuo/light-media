// OwnedFrontendFixture is a zero-capability standard SDK test worker. It is not
// shipped in the production bundle and does not adapt any legacy plugin host.
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

var fixtureVersion = "1.0"
var fixtureID = "OwnedFrontendFixture"
var fixtureProcessAPI = ""
var fixtureUI3133 = ""

func manifest() plugins.Manifest {
	m := plugins.Manifest{
		SchemaVersion: 1, PluginID: fixtureID, Name: "Owned frontend fixture", Version: fixtureVersion,
		Runtime: "go-executable", Entry: "owned-frontend-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "http", "stop"},
		Capabilities: []string{}, HasPage: true, Dashboards: []string{"main"},
		Frontend: &plugins.Frontend{Contract: plugins.FrontendFederationContract, Root: "frontend/assets", Entry: "remoteEntry.js", Exposes: []string{"Config", "Page", "Dashboard", "AppPage"}, Shared: []string{"vue"}, APIContract: plugins.FrontendAPIContract},
		APIs: []plugins.HTTPAPI{
			{Key: "status", Path: "/status", Methods: []string{"GET"}, Auth: "bear"},
			{Key: "key_status", Path: "/key-status", Methods: []string{"GET"}, Auth: "apikey"},
		},
	}
	if fixtureUI3133 == "true" {
		m.Methods = append(m.Methods, "get_dashboard_meta")
		m.Frontend.Shared = []string{"vue", "vuetify"}
	}
	return m
}

type object struct {
	mu             sync.Mutex
	config         map[string]any
	initCount      int
	dashboardCalls int
	sidebarCalls   int
	renderCalls    int
	lastDashboard  map[string]any
}

func (o *object) handle(_ context.Context, method string, raw json.RawMessage, _ plugins.HostCaller) (json.RawMessage, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch method {
	case "init":
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		if value == nil {
			value = map[string]any{}
		}
		o.config = value
		o.initCount++
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.Marshal(o.config["enabled"] == true)
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		o.renderCalls++
		if o.config["wrong_render_root"] == true {
			return json.Marshal([]string{"vue", "other/assets"})
		}
		return json.Marshal([]string{"vue", "frontend/assets"})
	case "get_form":
		return json.Marshal([]any{[]any{}, o.config})
	case "get_page":
		return json.RawMessage(`[]`), nil
	case "get_dashboard_meta":
		if o.config["dashboard_meta"] != nil {
			return json.Marshal(o.config["dashboard_meta"])
		}
		return json.RawMessage(`[{"key":"summary","name":"Owned summary"},{"key":"details","name":"Owned details"}]`), nil
	case "get_dashboard":
		o.dashboardCalls++
		if fixtureUI3133 == "true" {
			_ = json.Unmarshal(raw, &o.lastDashboard)
			if value, present := o.config["dashboard_value"]; present {
				return json.Marshal(value)
			}
			if o.config["dashboard_fail"] == true {
				return nil, errors.New("Owned dashboard failure")
			}
			if o.config["dashboard_none"] == true {
				return json.RawMessage(`null`), nil
			}
			return json.Marshal([]any{map[string]any{"sm": 2, "md": 8}, map[string]any{"refresh": 2, "border": false}, []any{}})
		}
		return json.Marshal([]any{map[string]any{"cols": 12}, map[string]any{}, []any{}})
	case "get_sidebar_nav":
		o.sidebarCalls++
		if fixtureUI3133 == "true" && o.config["sidebar_nav"] != nil {
			return json.Marshal(o.config["sidebar_nav"])
		}
		return json.Marshal([]map[string]any{{"nav_key": "details", "title": "Owned frontend details", "section": "system"}})
	case "http":
		var request plugins.HTTPRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Method != "GET" || (request.Key != "status" && request.Key != "key_status") {
			return nil, errors.New("unsupported fixture API")
		}
		if plugins.LastQuery(request.Query, "fail") == "true" {
			return json.Marshal(plugins.HTTPResponse{Status: 503, Body: json.RawMessage(`{"success":false,"message":"Owned API failure"}`), Headers: map[string]string{"Content-Type": "application/json"}})
		}
		observed := map[string]any{"success": true, "version": fixtureVersion, "marker": o.config["marker"]}
		if fixtureProcessAPI == "true" {
			_, cwdErr := os.Getwd()
			observed["nonce"] = os.Getpid()
			observed["init_count"] = o.initCount
			observed["cwd_ok"] = cwdErr == nil
			if fixtureUI3133 == "true" {
				observed["dashboard_calls"] = o.dashboardCalls
				observed["sidebar_calls"] = o.sidebarCalls
				observed["render_calls"] = o.renderCalls
				observed["last_dashboard"] = o.lastDashboard
			}
		}
		body, err := json.Marshal(observed)
		if err != nil {
			return nil, err
		}
		return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
	case "stop":
		return json.RawMessage(`null`), nil
	default:
		return nil, errors.New("unsupported fixture method")
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
	o := &object{config: map[string]any{}}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, o.handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
