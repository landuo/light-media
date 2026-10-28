// Disposable standard SDK plugin for fixed API platform acceptance only.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"os"
	"time"
)

var fixtureVersion = "1.0"

func main() {
	m := plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedAPI35Fixture", Name: "Owned API platform fixture", Version: fixtureVersion, Runtime: "go-executable", Entry: "owned-api35-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "http", "get_render_mode", "get_form", "get_page", "stop_service", "stop"}, Capabilities: []string{}, HasPage: true, Frontend: &plugins.Frontend{Contract: plugins.FrontendFederationContract, Root: "frontend/assets", Entry: "remoteEntry.js", Exposes: []string{"Config", "Page"}, Shared: []string{"vue"}, APIContract: plugins.FrontendAPIContract}}
	if m.Validate() != nil {
		panic("fixture manifest invalid")
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	config := map[string]any{}
	inits := 0
	calls := 0
	handle := func(callCtx context.Context, method string, raw json.RawMessage, _ plugins.HostCaller) (json.RawMessage, error) {
		switch method {
		case "init":
			var next map[string]any
			if json.Unmarshal(raw, &next) != nil {
				return nil, errors.New("invalid owned config")
			}
			config = next
			if config == nil {
				config = map[string]any{}
			}
			inits++
			return json.RawMessage(`null`), nil
		case "get_render_mode":
			return json.Marshal([]string{"vue", "frontend/assets"})
		case "get_form":
			return json.Marshal([]any{[]any{}, config})
		case "get_page":
			return json.RawMessage(`[]`), nil
		case "get_state":
			return json.RawMessage(`false`), nil
		case "get_service", "get_command":
			return json.RawMessage(`[]`), nil
		case "stop_service", "stop":
			return json.RawMessage(`null`), nil
		case "get_api":
			path := "/state"
			if config["mode"] == "alternate" {
				path = "/state_changed"
			}
			if fixtureVersion == "2.0" {
				path = "/updated"
			}
			return json.Marshal([]map[string]any{
				{"key": "state", "path": path, "methods": []string{"GET"}, "auth": "bear"},
				{"key": "anonymous", "path": "/anonymous", "auth": "bear", "allow_anonymous": true},
				{"key": "fallback", "path": "/default", "auth": "unrecognized"},
				{"key": "change", "path": "/change/{item}", "methods": []string{"post", "POST", "patch"}, "auth": "bear", "query": []plugins.HTTPParameter{{Name: "name", Kind: "string", Required: true}, {Name: "proof", Kind: "api_key"}}, "body": "raw", "body_required": true, "headers": []string{"content-type", "x-correlation-id"}},
				{"key": "binary", "path": "/binary", "methods": []string{"GET", "POST"}, "auth": "bear", "body": "raw"},
				{"key": "empty", "path": "/empty", "methods": []string{"DELETE"}, "auth": "bear"},
				{"key": "failure", "path": "/failure", "methods": []string{"GET"}, "auth": "bear"},
				{"key": "noop", "path": "/noop", "methods": []string{"POST"}, "auth": "bear"},
				{"key": "ordinary_object", "path": "/object", "methods": []string{"GET"}, "auth": "bear"},
				{"key": "slow", "path": "/slow", "methods": []string{"GET"}, "auth": "bear"},
			})
		case "http":
			var request plugins.HTTPRequest
			if json.Unmarshal(raw, &request) != nil {
				return nil, errors.New("invalid HTTP transport")
			}
			calls++
			if request.Key == "binary" {
				data := request.Body
				if len(data) == 0 {
					data = []byte{255, 0, 1, 254, 10}
				}
				return json.Marshal(plugins.HTTPResponse{Status: 200, Body: data, Headers: map[string]string{"Content-Type": "application/octet-stream", "Content-Disposition": `attachment; filename="owned.bin"`}})
			}
			if request.Key == "empty" {
				return json.Marshal(plugins.HTTPResponse{Status: 204})
			}
			if request.Key == "failure" {
				return plugins.JSONHTTPResponse(503, map[string]any{"success": false, "message": "owned API failure"})
			}
			if request.Key == "slow" {
				timer := time.NewTimer(8 * time.Second)
				defer timer.Stop()
				select {
				case <-callCtx.Done():
					return nil, callCtx.Err()
				case <-timer.C:
				}
				return plugins.JSONHTTPResponse(200, map[string]any{"success": true, "pid": os.Getpid(), "slow_completed": true})
			}
			if request.Key == "noop" {
				return plugins.JSONHTTPResponse(200, map[string]any{"success": false, "message": "owned no-op", "pid": os.Getpid()})
			}
			if request.Key == "ordinary_object" {
				return plugins.JSONHTTPResponse(200, map[string]any{"error": map[string]any{"message": "ordinary business field"}})
			}
			status := 200
			if request.Key == "change" {
				status = 201
			}
			return plugins.JSONHTTPResponse(status, map[string]any{"success": true, "pid": os.Getpid(), "init_count": inits, "http_calls": calls, "version": fixtureVersion, "config": config, "request": request})
		}
		return nil, errors.New("unsupported fixture method")
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, handle) != nil {
		os.Exit(1)
	}
}
