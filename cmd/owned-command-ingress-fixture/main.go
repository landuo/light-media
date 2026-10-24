// OwnedCommandIngressFixture is an actual persistent Base SDK process. Its
// command action deliberately does not name any method in its manifest.
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

const eventName = "owned.command"

type object struct {
	mu      sync.Mutex
	enabled bool
	events  []json.RawMessage
	ingress []json.RawMessage
	calls   map[string]int
}

func manifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "OwnedCommandIngressFixture", Name: "Owned command ingress fixture", Version: "1.0",
		Runtime: "go-executable", Entry: "owned-command-ingress-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "snapshot", "event", "close", "stop_service", "stop"},
		Capabilities: plugins.BaseMethods(), Events: []string{eventName, "command.excute"},
	}
}

func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.calls == nil {
		o.calls = map[string]int{}
	}
	o.calls[method]++
	switch method {
	case "init":
		var config struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, err
		}
		o.enabled = config.Enabled
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.Marshal(o.enabled)
	case "get_command":
		return json.Marshal([]map[string]any{
			{"cmd": "/owned_command", "event": eventName, "data": map[string]any{"action": "not_a_method", "nested": map[string]any{"values": []any{"kept", false, nil}}, "big": json.Number("9007199254740993")}},
			{"cmd": "/owned_noaction", "event": eventName, "data": map[string]any{"marker": "no-action", "arg_str": "preset"}},
			{"cmd": "/owned_nodata", "event": eventName, "data": nil},
			{"cmd": "/owned_hidden", "event": eventName, "show": false, "data": map[string]any{"marker": "hidden-but-registered"}},
		})
	case "event":
		var event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &event); err != nil || !json.Valid(event.Data) {
			return nil, errors.New("unexpected command event envelope")
		}
		if event.Type == "command.excute" {
			o.ingress = append(o.ingress, append(json.RawMessage(nil), raw...))
			return json.RawMessage(`null`), nil
		}
		if event.Type != eventName {
			return nil, errors.New("unexpected command event type")
		}
		o.events = append(o.events, append(json.RawMessage(nil), raw...))
		// This Base mutation is the genuine handler result, not an ingress
		// receipt or a synthetic command acknowledgment.
		if err := (plugins.Base{Host: host}).SaveData(ctx, "last_command_event", event.Data); err != nil {
			return nil, err
		}
		return json.RawMessage(`null`), nil
	case "snapshot":
		return json.Marshal(map[string]any{"nonce": os.Getpid(), "calls": o.calls, "events": o.events, "ingress": o.ingress})
	case "get_render_mode":
		return json.RawMessage(`["vuetify", ""]`), nil
	case "get_form":
		return json.RawMessage(`[[], {}]`), nil
	case "get_service", "get_api", "get_page", "get_dashboard", "get_sidebar_nav":
		return json.RawMessage(`[]`), nil
	case "close", "stop_service", "stop":
		return json.RawMessage(`null`), nil
	default:
		return nil, errors.New("method is not declared by owned command fixture")
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
	o := &object{}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, o.handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
