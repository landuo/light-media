package main

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"os"
)

func main() {
	m := plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedSourceView", Name: "Owned source view serializer", Version: "1.0", Runtime: "go-executable", Entry: "owned-source-view", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_api", "get_command", "get_service", "get_render_mode", "get_page", "get_form", "set_case", "snapshot", "close", "stop_service", "stop"}, Capabilities: []string{}}
	if err := m.Validate(); err != nil {
		panic(err)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	current := map[string]json.RawMessage{"mode": json.RawMessage(`"vuetify"`), "page": json.RawMessage(`[]`), "form": json.RawMessage(`[[],{}]`)}
	calls := map[string]int{}
	handle := func(_ context.Context, method string, raw json.RawMessage, _ plugins.HostCaller) (json.RawMessage, error) {
		calls[method]++
		switch method {
		case "set_case":
			return json.RawMessage(`null`), json.Unmarshal(raw, &current)
		case "init", "close", "stop_service", "stop":
			return json.RawMessage(`null`), nil
		case "get_state":
			return json.RawMessage(`false`), nil
		case "get_api", "get_command", "get_service":
			return json.RawMessage(`[]`), nil
		case "snapshot":
			return json.Marshal(map[string]any{"pid": os.Getpid(), "calls": calls})
		case "get_render_mode":
			return json.Marshal([]json.RawMessage{current["mode"], json.RawMessage(`""`)})
		case "get_page", "get_form":
			var failure string
			_ = json.Unmarshal(current["fail"], &failure)
			if failure == "both" || failure == method[4:] {
				return nil, errors.New("owned source method error")
			}
			return current[method[4:]], nil
		}
		return nil, errors.New("unknown owned method")
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, handle) != nil {
		os.Exit(1)
	}
}
