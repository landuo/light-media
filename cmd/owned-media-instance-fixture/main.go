// OwnedMediaInstanceFixture is a real persistent SDK worker for the retained
// object bridge. It constructs no vendor, socket or private reference table.
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

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedMediaInstanceFixture", Name: "Owned media instance fixture", Version: "1.0", Runtime: "go-executable", Entry: "owned-media-instance-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "capture", "retained", "refresh", "resource", "close", "stop_service", "stop"},
		Capabilities: []string{plugins.BaseSaveData, plugins.MediaServerGetServicesCapability, plugins.MediaServerGetServiceCapability, plugins.MediaServerIsInactiveCapability, plugins.MediaServerRefreshRootCapability, plugins.MediaServerRefreshItemsCapability}}
}

type object struct {
	mu       sync.Mutex
	retained *plugins.MediaServerInstance
	previous plugins.HostCaller
}

func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch method {
	case "init":
		return json.RawMessage("null"), nil
	case "get_state":
		return json.RawMessage("false"), nil
	case "get_service", "get_command", "get_api":
		return json.RawMessage("[]"), nil
	case "get_form":
		return json.RawMessage("[[],{}]"), nil
	case "resource":
		var input struct {
			Method  string          `json:"method"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return host(ctx, input.Method, input.Payload)
	case "refresh":
		var input struct {
			Name      json.RawMessage                   `json:"name"`
			Reference string                            `json:"reference"`
			Root      bool                              `json:"root"`
			Previous  bool                              `json:"previous"`
			Items     []plugins.MediaServerRefreshItem  `json:"items"`
			Options   plugins.MediaServerRefreshOptions `json:"options"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		instance := plugins.MediaServerInstance{Reference: input.Reference}
		if instance.Reference == "" {
			service, err := (plugins.SharedMediaServerServices{Host: host}).GetService(ctx, plugins.MediaServerServiceRequest{Name: input.Name})
			if err != nil {
				return nil, err
			}
			if service == nil {
				return nil, errors.New("owned refresh service missing")
			}
			instance = service.Instance
		}
		call := host
		if input.Previous {
			if o.previous == nil {
				return nil, errors.New("owned prior callback missing")
			}
			call = o.previous
		}
		var result plugins.MediaServerRefreshResult
		var err error
		if input.Root {
			result, err = instance.RefreshRootLibrary(ctx, call, input.Options)
		} else {
			result, err = instance.RefreshLibraryByItems(ctx, call, input.Items, input.Options)
		}
		if input.Previous {
			if err == nil {
				return nil, errors.New("expired refresh callback remained callable")
			}
			return json.Marshal(map[string]any{"pid": os.Getpid(), "stale_denied": true})
		}
		if err != nil {
			return nil, err
		}
		o.previous = host
		return json.Marshal(map[string]any{"pid": os.Getpid(), "instance": instance, "result": result})
	case "capture":
		sdk := plugins.SharedMediaServerServices{Host: host}
		all, err := sdk.GetServices(ctx, plugins.MediaServerServicesRequest{})
		if err != nil {
			return nil, err
		}
		first, err := sdk.GetService(ctx, plugins.MediaServerServiceRequest{Name: json.RawMessage(`"alpha"`)})
		if err != nil {
			return nil, err
		}
		if first == nil {
			return nil, errors.New("owned first instance missing")
		}
		o.retained = &first.Instance
		inactive, err := o.retained.IsInactive(ctx, host)
		if err != nil {
			return nil, err
		}
		o.previous = host
		return json.Marshal(map[string]any{"pid": os.Getpid(), "all": all, "first": first, "inactive": inactive})
	case "retained":
		if o.retained == nil || o.previous == nil {
			return nil, errors.New("owned retained instance missing")
		}
		_, staleErr := o.retained.IsInactive(ctx, o.previous)
		if staleErr == nil {
			return nil, errors.New("expired owned callback remained callable")
		}
		inactive, err := o.retained.IsInactive(ctx, host)
		if err != nil {
			return nil, err
		}
		fresh, err := (plugins.SharedMediaServerServices{Host: host}).GetService(ctx, plugins.MediaServerServiceRequest{Name: json.RawMessage(`"alpha"`)})
		if err != nil {
			return nil, err
		}
		o.previous = host
		return json.Marshal(map[string]any{"pid": os.Getpid(), "retained": o.retained, "inactive": inactive, "current": fresh, "stale_denied": true})
	case "close", "stop_service", "stop":
		if o.retained != nil {
			inactive, err := o.retained.IsInactive(ctx, host)
			if err != nil {
				return nil, err
			}
			if err := (plugins.Base{Host: host}).SaveData(ctx, "owned_media_"+method, map[string]any{"pid": os.Getpid(), "inactive": inactive}); err != nil {
				return nil, err
			}
		}
		return json.RawMessage("null"), nil
	}
	return nil, errors.New("unsupported owned media instance method")
}
func main() {
	m := manifest()
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		if err := json.NewEncoder(os.Stdout).Encode(m); err != nil {
			os.Exit(81)
		}
		return
	}
	if len(os.Args) != 1 {
		os.Exit(80)
	}
	object := &object{}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, object.handle); err != nil {
		fmt.Fprintln(os.Stderr, "owned media instance SDK stopped with an error")
		os.Exit(82)
	}
}
