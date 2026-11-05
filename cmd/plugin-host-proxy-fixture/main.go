// This acceptance fixture consumes private host proxy settings in trusted Go memory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var fixtureVersion = "1.0"
var mu sync.Mutex

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedHostProxy", Name: "宿主代理平台验收", Version: fixtureVersion, Runtime: "go-executable", Entry: "plugin-host-proxy-fixture", HostAPIMin: 1, HostAPIMax: 1, HasPage: true, Methods: []string{"init", "stop", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "post", "http"}, Capabilities: append(plugins.BaseMethods(), plugins.NetworkProxyReadCapability), APIs: []plugins.HTTPAPI{{Key: "run", Path: "/run", Methods: []string{"POST"}, Auth: "bear", Body: "json"}, {Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}}}
}

type nativeInput struct {
	URL             string `json:"url"`
	Body            string `json:"body"`
	EnvironmentOnly bool   `json:"environment_only"`
}
type publicResult struct {
	Status           int    `json:"status"`
	Body             string `json:"body"`
	ConfiguredProxy  bool   `json:"configured_proxy"`
	EnvironmentCount int    `json:"environment_count"`
}

func post(ctx context.Context, host plugins.HostCaller, input nativeInput) (json.RawMessage, error) {
	snapshot, err := (plugins.Network{Host: host}).ProxySettings(ctx)
	if err != nil {
		return nil, err
	}
	address, err := url.Parse(input.URL)
	if err != nil || address.Hostname() == "" || address.User != nil || address.Scheme != "http" && address.Scheme != "https" {
		return nil, errors.New("owned native target invalid")
	}
	explicit := snapshot.Proxies
	if input.EnvironmentOnly {
		explicit = nil
	}
	transport, err := networkproxy.NewRequestsProxyTransport(networkproxy.NewRequestsTimeoutTransport(2*time.Second), snapshot.Environment, explicit, address)
	if err != nil {
		return nil, errors.New("owned private proxy configuration invalid")
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, "POST", address.String(), strings.NewReader(input.Body))
	if err != nil {
		return nil, errors.New("owned native request invalid")
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return nil, errors.New("owned native POST failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return nil, errors.New("owned native response failed")
	}
	// No proxy URL, userinfo, environment values or DTO is persisted or returned.
	value := publicResult{Status: response.StatusCode, Body: string(body), ConfiguredProxy: snapshot.Proxies != nil, EnvironmentCount: len(snapshot.Environment)}
	mu.Lock()
	defer mu.Unlock()
	base := plugins.Base{Host: host}
	key := "calls"
	raw, err := base.GetData(ctx, &key)
	if err != nil {
		return nil, err
	}
	calls := []json.RawMessage{}
	_ = json.Unmarshal(raw, &calls)
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	calls = append(calls, encoded)
	if err = base.SaveData(ctx, key, calls); err != nil {
		return nil, err
	}
	return encoded, nil
}
func configured(ctx context.Context, host plugins.HostCaller, config json.RawMessage) (json.RawMessage, error) {
	var input nativeInput
	if err := json.Unmarshal(config, &input); err != nil {
		return nil, err
	}
	return post(ctx, host, input)
}
func response(value any) (json.RawMessage, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(plugins.HTTPResponse{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: body})
}
func handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	base := plugins.Base{Host: host}
	switch method {
	case "stop":
		return json.RawMessage(`null`), nil
	case "init":
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var values map[string]json.RawMessage
		_ = json.Unmarshal(config, &values)
		if string(values["run_once"]) == "true" {
			values["run_once"] = json.RawMessage(`false`)
			if _, err = base.UpdateConfig(ctx, values); err != nil {
				return nil, err
			}
			return configured(ctx, host, config)
		}
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.RawMessage(`true`), nil
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",""]`), nil
	case "get_form":
		return json.RawMessage(`[[{"component":"VTextField","props":{"model":"url","label":"独占测试目标URL"}},{"component":"VTextField","props":{"model":"body","label":"测试POST正文"}},{"component":"VSwitch","props":{"model":"run_once","label":"保存后POST一次"}}],{"url":"http://owned-target.invalid/owned-native","body":"owned-post","run_once":false}]`), nil
	case "get_page":
		key := "calls"
		calls, err := base.GetData(ctx, &key)
		if err != nil {
			return nil, err
		}
		return json.Marshal([]any{map[string]any{"component": "VAlert", "props": map[string]any{"text": "原生POST：" + string(calls)}}})
	case "post":
		var input nativeInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return post(ctx, host, input)
	case "http":
		var call plugins.HTTPRequest
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		if call.Key == "run" {
			var result json.RawMessage
			var err error
			if len(call.Body) == 0 || string(call.Body) == "{}" {
				config, e := base.GetConfig(ctx)
				if e != nil {
					return nil, e
				}
				result, err = configured(ctx, host, config)
			} else {
				result, err = handle(ctx, "post", call.Body, host)
			}
			if err != nil {
				return nil, err
			}
			return response(result)
		}
		if call.Key == "snapshot" {
			config, err := base.GetConfig(ctx)
			if err != nil {
				return nil, err
			}
			key := "calls"
			calls, err := base.GetData(ctx, &key)
			if err != nil {
				return nil, err
			}
			return response(map[string]any{"pid": os.Getpid(), "version": fixtureVersion, "config": config, "calls": calls})
		}
	}
	return nil, errors.New("unknown owned private proxy fixture method")
}
func main() {
	m := manifest()
	if err := m.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	if err := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, handle); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
