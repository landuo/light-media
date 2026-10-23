// A real no-capability SDK worker for get_api tests; not a builtin plugin.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"light-media/internal/plugins"
)

func main() {
	m := plugins.Manifest{SchemaVersion: 1, PluginID: "OriginalAPIFixture", Name: "Original API fixture", Version: "1.0", Runtime: "go-executable", Entry: "original-api-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"invoke", "init", "get_state", "get_service", "get_command", "get_api", "set_mode", "stop"}, Capabilities: []string{},
		APIs: []plugins.HTTPAPI{{Key: "declared", Path: "/declared", Methods: []string{"POST"}, Auth: "bear"}}}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	mode := ""
	handle := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		switch method {
		case "invoke", "init", "stop":
			return json.RawMessage(`null`), nil
		case "get_service", "get_command":
			return json.RawMessage(`[]`), nil
		case "get_state":
			return json.RawMessage(`false`), nil
		case "set_mode":
			return json.RawMessage(`null`), json.Unmarshal(raw, &mode)
		case "get_api":
			switch mode {
			case "error":
				return nil, errors.New("actual get_api failure")
			case "null":
				return json.RawMessage(`null`), nil
			case "invalid":
				return json.RawMessage(`{"not":"a route list"}`), nil
			case "host":
				_, err := host(ctx, "forbidden.fixture.capability", json.RawMessage(`{}`))
				if err == nil { return nil, errors.New("unexpected host grant") }
				return json.Marshal([]plugins.HTTPAPI{{Key: "host_denied", Path: "/denied", Methods: []string{"GET"}}})
			}
			// A separate mutation is observed through this same live object.
			path := "/actual"
			if mode == "changed" { path = "/actual_again" }
			return json.Marshal([]plugins.HTTPAPI{{Key: "actual", Path: path, Methods: []string{"GET", "PATCH"}}})
		default:
			return nil, errors.New("unknown fixture method")
		}
	}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, handle); err != nil { os.Exit(1) }
}
