package main

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/landuo/light-media-plugins/internal/sdk"
	"os"
)

var mode string

func main() {
	m := sdk.Manifest{ManagedPublicServices: true, SchemaVersion: 1, PluginID: "OwnedPublicService", Name: "Owned public lifetime", Version: "1.0", Runtime: "go-executable", Entry: "worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "root", "child", "grandchild", "close", "stop_service", "stop"}, Capabilities: append(sdk.BaseMethods(), sdk.BackgroundSubmitCapability, sdk.NetworkGetCapability, sdk.ServiceProgressCapability)}
	if mode == "cleanup" {
		m.Capabilities = append(m.Capabilities, sdk.CleanupSubmitCapability)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	handle := func(ctx context.Context, method string, raw json.RawMessage, host sdk.HostCaller) (json.RawMessage, error) {
		b := sdk.Base{Host: host}
		switch method {
		case "init":
			if e := b.SaveData(ctx, "init_ran", true); e != nil {
				return nil, e
			}
		case "get_state":
			return json.RawMessage(`true`), nil
		case "get_service":
			if mode != "" {
				return json.RawMessage(`[{"id":"owned","name":"Owned","handler":"root","trigger":{"kind":"interval","interval_ms":3600000,"timezone":"Asia/Shanghai"}}]`), nil
			}
			return json.RawMessage(`[]`), nil
		case "root":
			var input map[string]any
			if e := json.Unmarshal(raw, &input); e != nil {
				return nil, e
			}
			if e := (sdk.Manager{Host: host}).Submit(ctx, "child", input); e != nil {
				return nil, e
			}
		case "child":
			var input struct {
				URL string `json:"url"`
			}
			if e := json.Unmarshal(raw, &input); e != nil {
				return nil, e
			}
			out, e := (sdk.Network{Host: host}).Get(ctx, sdk.NetworkGetRequestV1{URL: input.URL})
			if e != nil {
				return nil, e
			}
			if out.Response == nil || out.Response.StatusCode != 200 {
				return nil, fmt.Errorf("owned peer failed")
			}
			progressErr := sdk.UpdateServiceProgress(ctx, host, sdk.ServiceProgressUpdate{Text: new("child must not borrow root progress")})
			if e = (sdk.Manager{Host: host}).Submit(ctx, "grandchild", nil); e != nil {
				return nil, e
			}
			if e = b.SaveData(ctx, "child_done", map[string]any{"pid": os.Getpid(), "progress_denied": progressErr != nil}); e != nil {
				return nil, e
			}
		case "grandchild":
			if e := b.SaveData(ctx, "grandchild_done", map[string]int{"pid": os.Getpid()}); e != nil {
				return nil, e
			}
		case "close", "stop_service", "stop":
			if e := b.SaveData(ctx, "hook_"+method, map[string]int{"pid": os.Getpid()}); e != nil {
				return nil, e
			}
			if method == "stop_service" {
				raw, e := b.GetConfig(ctx)
				if e != nil {
					return nil, e
				}
				var cfg struct {
					StopError bool `json:"stop_error"`
				}
				if e = json.Unmarshal(raw, &cfg); e != nil {
					return nil, e
				}
				if cfg.StopError {
					return nil, fmt.Errorf("owned stop failure")
				}
			}
		}
		return json.RawMessage(`null`), nil
	}
	var err error
	if mode == "serial" {
		err = sdk.Serve(context.Background(), os.Stdin, os.Stdout, m, handle)
	} else {
		err = sdk.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, handle)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
