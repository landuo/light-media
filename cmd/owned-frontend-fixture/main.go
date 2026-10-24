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

func manifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "OwnedFrontendFixture", Name: "Owned frontend fixture", Version: fixtureVersion,
		Runtime: "go-executable", Entry: "owned-frontend-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "http", "stop"},
		Capabilities: []string{}, HasPage: true, Dashboards: []string{"main"},
		Frontend: &plugins.Frontend{Contract: plugins.FrontendFederationContract, Root: "frontend/assets", Entry: "remoteEntry.js", Exposes: []string{"Config", "Page", "Dashboard", "AppPage"}, Shared: []string{"vue"}, APIContract: plugins.FrontendAPIContract},
		APIs: []plugins.HTTPAPI{
			{Key: "status", Path: "/status", Methods: []string{"GET"}, Auth: "bear"},
			{Key: "key_status", Path: "/key-status", Methods: []string{"GET"}, Auth: "apikey"},
		},
	}
}

type object struct {
	mu     sync.Mutex
	config map[string]any
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
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.Marshal(o.config["enabled"] == true)
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		if o.config["wrong_render_root"] == true {
			return json.Marshal([]string{"vue", "other/assets"})
		}
		return json.Marshal([]string{"vue", "frontend/assets"})
	case "get_form":
		return json.Marshal([]any{[]any{}, o.config})
	case "get_page":
		return json.RawMessage(`[]`), nil
	case "get_dashboard":
		return json.Marshal([]any{map[string]any{"cols": 12}, map[string]any{}, []any{}})
	case "get_sidebar_nav":
		return json.Marshal([]map[string]any{{"nav_key": "details", "title": "Owned frontend details", "section": "system"}})
	case "http":
		var request plugins.HTTPRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Method != "GET" || (request.Key != "status" && request.Key != "key_status") {
			return nil, errors.New("unsupported fixture API")
		}
		body, err := json.Marshal(map[string]any{"success": true, "version": fixtureVersion, "marker": o.config["marker"]})
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
