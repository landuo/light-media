// OwnedEventFixture is an actual out-of-process Base SDK subscriber. It never
// synthesizes business events and has no native host or legacy invoke adapter.
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

type fixture struct {
	mu sync.Mutex
}

func manifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "OwnedEventFixture", Name: "Owned event fixture", Version: "1.0",
		Runtime: "go-executable", Entry: "owned-event-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "event", "close", "stop_service", "stop"},
		Capabilities: plugins.BaseMethods(),
		Events:       []string{"download.added", "subscribe.added", "site.refreshed", "site.deleted", "site.updated", "transfer.complete", "downloadfile.deleted", "download.deleted"},
	}
}

func (f *fixture) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	base := plugins.Base{Host: host}
	config, err := base.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	var options struct {
		Enabled   bool `json:"enabled"`
		FailEvent bool `json:"fail_event"`
	}
	if len(config) > 0 && string(config) != "null" {
		if err = json.Unmarshal(config, &options); err != nil {
			return nil, err
		}
	}
	switch method {
	case "init", "close", "stop_service", "stop":
		return json.RawMessage("null"), nil
	case "get_state":
		return json.Marshal(options.Enabled)
	case "get_render_mode":
		return json.RawMessage(`["vuetify", ""]`), nil
	case "get_form":
		return json.RawMessage(`[[], {"enabled":false}]`), nil
	case "get_dashboard":
		return json.RawMessage(`[{},{},[]]`), nil
	case "get_service", "get_command", "get_api", "get_page", "get_sidebar_nav":
		return json.RawMessage(`[]`), nil
	case "event":
		var event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err = json.Unmarshal(raw, &event); err != nil || event.Type == "" || !json.Valid(event.Data) {
			return nil, errors.New("invalid actual event envelope")
		}
		key := "events"
		prior, err := base.GetData(ctx, &key)
		if err != nil {
			return nil, err
		}
		events := []json.RawMessage{}
		if len(prior) > 0 && string(prior) != "null" {
			if err = json.Unmarshal(prior, &events); err != nil {
				return nil, err
			}
		}
		events = append(events, append(json.RawMessage(nil), raw...))
		if err = base.SaveData(ctx, key, events); err != nil {
			return nil, err
		}
		if options.FailEvent {
			return nil, errors.New("owned subscriber failure after recording attempt")
		}
		return json.RawMessage("null"), nil
	default:
		return nil, errors.New("unsupported owned event fixture method")
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
	f := &fixture{}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, f.handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
