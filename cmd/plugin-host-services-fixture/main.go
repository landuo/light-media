// Owned acceptance fixture for the standard plugin-to-host services contract.
// It is not a business plugin and never receives host credentials.
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
var fixtureSerial = "false"

func manifest() plugins.Manifest {
	capabilities := append(plugins.BaseMethods(), plugins.StorageExistsCapability, plugins.StorageListCapability, plugins.StorageDeleteCapability, plugins.MediaServersListCapability, plugins.MediaServersRefreshCapability, plugins.MediaServerLibraryRefreshCapability, plugins.MediaRecognizeCapability, plugins.PluginManagerReadCapability, plugins.PluginManagerControlCapability, plugins.SettingsReadCapability, plugins.SettingsWriteCapability)
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedHostServices", Name: "宿主服务验收", Version: fixtureVersion, Runtime: "go-executable", Entry: "plugin-host-services-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_module", "recognize", "resource", "http", "stop"}, HasPage: true, Capabilities: capabilities, APIs: []plugins.HTTPAPI{{Key: "run", Path: "/run", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}, {Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}}}
}

func main() {
	m := manifest()
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	var mu sync.Mutex
	config := map[string]json.RawMessage{}
	var handle plugins.Handler
	handle = func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		base := plugins.Base{Host: host}
		switch method {
		case "init":
			var value map[string]json.RawMessage
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			mu.Lock()
			config = value
			mu.Unlock()
			return json.RawMessage(`null`), nil
		case "stop":
			return json.RawMessage(`null`), nil
		case "get_state":
			mu.Lock()
			value := config["enabled"]
			var fail bool
			_ = json.Unmarshal(config["fail_state"], &fail)
			mu.Unlock()
			if fail {
				return nil, errors.New("owned state getter failure")
			}
			if len(value) == 0 {
				return json.RawMessage(`true`), nil
			}
			return value, nil
		case "get_service":
			mu.Lock()
			value := append(json.RawMessage(nil), config["services"]...)
			mu.Unlock()
			if len(value) == 0 {
				return json.RawMessage(`[]`), nil
			}
			return value, nil
		case "get_command":
			return json.RawMessage(`[]`), nil
		case "get_api":
			return json.Marshal(m.APIs)
		case "get_render_mode":
			return json.RawMessage(`["vuetify",""]`), nil
		case "get_form":
			return json.RawMessage(`[[{"component":"VTextField","props":{"model":"marker","label":"验收标记"}}],{"enabled":true,"marker":"owned"}]`), nil
		case "get_page":
			key := "last"
			value, err := base.GetData(ctx, &key)
			if err != nil {
				return nil, err
			}
			configuration, err := base.GetConfig(ctx)
			if err != nil {
				return nil, err
			}
			return json.Marshal([]any{map[string]any{"component": "VAlert", "props": map[string]any{"type": "info", "text": "宿主服务验收：" + string(value)}}, map[string]any{"component": "VAlert", "props": map[string]any{"text": "配置：" + string(configuration)}}})
		case "get_module":
			mu.Lock()
			value := config["recognized"]
			mu.Unlock()
			if len(value) > 0 {
				return json.RawMessage(`{"recognize_media":"recognize"}`), nil
			}
			return json.RawMessage(`{}`), nil
		case "recognize":
			if err := base.SaveData(ctx, "recognize_call", raw); err != nil {
				return nil, err
			}
			mu.Lock()
			value := append(json.RawMessage(nil), config["recognized"]...)
			mu.Unlock()
			if len(value) == 0 {
				return json.RawMessage(`null`), nil
			}
			return value, nil
		case "http":
			var request plugins.HTTPRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			if request.Key == "snapshot" && request.Method == "GET" {
				configuration, err := base.GetConfig(ctx)
				if err != nil {
					return nil, err
				}
				key := "last"
				last, err := base.GetData(ctx, &key)
				if err != nil {
					return nil, err
				}
				body, err := json.Marshal(map[string]any{"pid": os.Getpid(), "version": fixtureVersion, "config": configuration, "last": last})
				if err != nil {
					return nil, err
				}
				return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
			}
			if request.Key != "run" || request.Method != "POST" {
				return nil, errors.New("unexpected fixture HTTP")
			}
			result, err := handle(ctx, "resource", request.Body, host)
			if err != nil {
				return nil, err
			}
			return json.Marshal(plugins.HTTPResponse{Status: 200, Body: result, Headers: map[string]string{"Content-Type": "application/json"}})
		case "resource":
			var request struct {
				Method string          `json:"method"`
				Input  json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			var value any
			switch request.Method {
			case plugins.SettingsReadCapability:
				var input plugins.SettingsHostRequestV1
				if err := json.Unmarshal(request.Input, &input); err != nil {
					return nil, err
				}
				var err error
				value, err = (plugins.SystemSettings{Host: host}).Get(ctx, input.Key)
				if err != nil {
					return nil, err
				}
			case plugins.SettingsWriteCapability:
				var input plugins.SettingsHostWriteV1
				if err := json.Unmarshal(request.Input, &input); err != nil {
					return nil, err
				}
				var err error
				value, err = (plugins.SystemSettings{Host: host}).Set(ctx, input.Key, input.Value)
				if err != nil {
					return nil, err
				}
			case plugins.StorageExistsCapability, plugins.StorageListCapability, plugins.StorageDeleteCapability:
				var input plugins.StorageFilesRequest
				if err := json.Unmarshal(request.Input, &input); err != nil {
					return nil, err
				}
				var err error
				switch request.Method {
				case plugins.StorageExistsCapability:
					value, err = (plugins.Storage{Host: host}).Exists(ctx, input.FileItem)
				case plugins.StorageListCapability:
					value, err = (plugins.Storage{Host: host}).List(ctx, input.FileItem, input.Recursion)
				case plugins.StorageDeleteCapability:
					value, err = (plugins.Storage{Host: host}).Delete(ctx, input.FileItem)
				}
				if err != nil {
					return nil, err
				}
			case plugins.MediaServersListCapability:
				var input plugins.MediaServersRequest
				if err := json.Unmarshal(request.Input, &input); err != nil {
					return nil, err
				}
				var err error
				value, err = (plugins.MediaHost{Host: host}).Servers(ctx, input)
				if err != nil {
					return nil, err
				}
			case plugins.MediaServersRefreshCapability:
				var input plugins.MediaRefreshRequest
				if err := json.Unmarshal(request.Input, &input); err != nil {
					return nil, err
				}
				var err error
				value, err = (plugins.MediaHost{Host: host}).Refresh(ctx, input)
				if err != nil {
					return nil, err
				}
			case plugins.MediaServerLibraryRefreshCapability:
				var input plugins.MediaRefreshLibraryRequest
				if err := json.Unmarshal(request.Input, &input); err != nil {
					return nil, err
				}
				var err error
				value, err = (plugins.MediaHost{Host: host}).RefreshLibrary(ctx, input)
				if err != nil {
					return nil, err
				}
			case plugins.MediaRecognizeCapability:
				var input plugins.MediaRecognizeRequest
				if err := json.Unmarshal(request.Input, &input); err != nil {
					return nil, err
				}
				var err error
				value, err = (plugins.MediaHost{Host: host}).Recognize(ctx, input)
				if err != nil {
					return nil, err
				}
			case plugins.PluginManagerReadCapability, plugins.PluginManagerControlCapability:
				var input plugins.PluginManagerRequest
				if err := json.Unmarshal(request.Input, &input); err != nil {
					return nil, err
				}
				var err error
				if request.Method == plugins.PluginManagerReadCapability {
					value, err = (plugins.PluginHost{Host: host}).Read(ctx, input)
				} else {
					value, err = (plugins.PluginHost{Host: host}).ControlRequest(ctx, input)
				}
				if err != nil {
					return nil, err
				}
			default:
				return nil, errors.New("unknown fixture operation")
			}
			if err := base.SaveData(ctx, "last", value); err != nil {
				return nil, err
			}
			return json.Marshal(value)
		}
		return nil, errors.New("unknown fixture method")
	}
	serve := plugins.ServeConcurrent
	if fixtureSerial == "true" {
		serve = plugins.Serve
	}
	if err := serve(context.Background(), os.Stdin, os.Stdout, m, handle); err != nil {
		fmt.Fprintln(os.Stderr, "owned host services fixture stopped")
		os.Exit(1)
	}
}
