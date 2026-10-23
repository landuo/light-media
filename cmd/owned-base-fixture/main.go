// OwnedBaseFixture exercises the real SDK Base contract against an owned App.
// It is not a builtin, a mock HostHandler, or a native capability adapter.
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

type plan struct {
	Target *string         `json:"target"`
	Config json.RawMessage `json:"config"`
	Key    string          `json:"key"`
	Value  json.RawMessage `json:"value"`
	Delete *string         `json:"delete"`
}

type object struct {
	mu          sync.Mutex
	initCount   int
	initInput   json.RawMessage
	initAfter   json.RawMessage
	planResult  *bool
	previous    plugins.HostCaller
	staleDenied bool
}

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedBaseFixture", Name: "Owned base fixture", Version: "1.0", Runtime: "go-executable", Entry: "owned-base-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"invoke", "init", "get_state", "get_service", "get_command", "get_api", "snapshot", "stop"}, Capabilities: plugins.BaseMethods()}
}

func targetArgs(target *string) []string {
	if target == nil {
		return nil
	}
	return []string{*target}
}

func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// A retained prior callback is deliberately attempted in the next real call.
	// The SDK must reject it before emitting any new reverse RPC.
	if o.previous != nil {
		old := plugins.Base{Host: o.previous}
		err := old.SaveData(ctx, "owned_stale_must_not_exist", true)
		o.staleDenied = err != nil
		if err == nil {
			return nil, errors.New("old callback survived its completed phase")
		}
	}
	o.previous = host
	base := plugins.Base{Host: host}
	if method == "invoke" {
		var inv plugins.Invocation
		if err := json.Unmarshal(raw, &inv); err != nil {
			return nil, err
		}
		method = inv.Action
		if method == "reload" {
			method = "init"
		}
		if method == "init" {
			raw = inv.Config
		} else {
			raw = inv.Input
		}
	}
	switch method {
	case "init":
		if len(raw) == 0 {
			raw = json.RawMessage("null")
		}
		o.initCount++
		o.initInput = append(json.RawMessage(nil), raw...)
		var config struct {
			Plan *plan `json:"owned_plan"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, err
		}
		if config.Plan != nil {
			p := config.Plan
			target := targetArgs(p.Target)
			if len(p.Config) != 0 {
				changed, err := base.UpdateConfig(ctx, p.Config, target...)
				if err != nil {
					return nil, err
				}
				o.planResult = changed
			}
			after, err := base.GetConfig(ctx, target...)
			if err != nil {
				return nil, err
			}
			o.initAfter = after
			if len(p.Value) != 0 {
				if err = base.SaveData(ctx, p.Key, p.Value, target...); err != nil {
					return nil, err
				}
			}
			if p.Delete != nil {
				if err = base.DelData(ctx, p.Delete, target...); err != nil {
					return nil, err
				}
			}
			// This path call uses the same explicit/default namespace as config/data.
			if _, err = base.GetDataPath(ctx, target...); err != nil {
				return nil, err
			}
		} else {
			var err error
			o.initAfter, err = base.GetConfig(ctx)
			if err != nil {
				return nil, err
			}
		}
		return json.RawMessage(`null`), nil
	case "get_state":
		if _, err := base.GetConfig(ctx); err != nil {
			return nil, err
		}
		return json.RawMessage(`false`), nil
	case "get_service", "get_command", "get_api":
		if _, err := base.GetConfig(ctx); err != nil {
			return nil, err
		}
		return json.RawMessage(`[]`), nil
	case "snapshot":
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		key := "shared"
		value, err := base.GetData(ctx, &key)
		if err != nil {
			return nil, err
		}
		rows, err := base.GetData(ctx, nil)
		if err != nil {
			return nil, err
		}
		path, err := base.GetDataPath(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"init_count": o.initCount, "init_input": o.initInput, "init_after": o.initAfter, "update_result": o.planResult, "config": config, "value": value, "rows": rows, "path": path, "stale_denied": o.staleDenied})
	case "stop":
		// Fresh stop callback must work even though the retained prior call failed.
		if err := base.SaveData(ctx, "owned_stop", map[string]any{"stale_denied": o.staleDenied, "init_count": o.initCount}); err != nil {
			return nil, err
		}
		return json.RawMessage(`null`), nil
	default:
		return nil, errors.New("unsupported owned base method")
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
