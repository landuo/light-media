// This compiled fixture verifies the ordinary configured-site HTTP capability.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"light-media/internal/plugins"
	"os"
	"sync"
)

var fixtureVersion = "1.0"
var mu sync.Mutex

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedHostSiteHTTP", Name: "站点HTTP平台验收", Version: fixtureVersion, Runtime: "go-executable", Entry: "plugin-host-site-http-fixture", HostAPIMin: 1, HostAPIMax: 1, HasPage: true, Methods: []string{"init", "stop", "stop_service", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "request", "credential_check", "verify", "http"}, Capabilities: append(plugins.BaseMethods(), plugins.SitesRequestCapability, plugins.SitesCredentialsCapability, plugins.SettingsReadCapability, plugins.SettingsWriteCapability), APIs: []plugins.HTTPAPI{{Key: "run", Path: "/run", Methods: []string{"POST"}, Auth: "bear", Body: "json"}, {Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}, {Key: "verify", Path: "/verify", Methods: []string{"POST"}, Auth: "bear", Body: "json"}}}
}
func record(ctx context.Context, base plugins.Base, result any) error {
	mu.Lock()
	defer mu.Unlock()
	key := "calls"
	raw, err := base.GetData(ctx, &key)
	if err != nil {
		return err
	}
	rows := []json.RawMessage{}
	_ = json.Unmarshal(raw, &rows)
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	rows = append(rows, encoded)
	return base.SaveData(ctx, key, rows)
}
func perform(ctx context.Context, host plugins.HostCaller, raw json.RawMessage) (json.RawMessage, error) {
	var input plugins.SiteHTTPRequestV1
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	out, err := (plugins.Sites{Host: host}).Request(ctx, input)
	if err != nil {
		return nil, err
	}
	if err = record(ctx, plugins.Base{Host: host}, publicResult(out)); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}
func configured(ctx context.Context, host plugins.HostCaller, config json.RawMessage) (json.RawMessage, error) {
	var value struct {
		Request string `json:"request_json"`
	}
	if err := json.Unmarshal(config, &value); err != nil {
		return nil, err
	}
	return perform(ctx, host, json.RawMessage(value.Request))
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
	case "stop_service":
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var values struct {
			Check bool   `json:"check_on_stop"`
			ID    string `json:"site_id"`
		}
		_ = json.Unmarshal(config, &values)
		if values.Check {
			value, err := (plugins.Sites{Host: host}).Credentials(ctx, values.ID)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(`null`), base.SaveData(ctx, "cleanup_credentials_accessed", value.ID == values.ID)
		}
		return json.RawMessage(`null`), nil
	case "credential_check":
		var input plugins.SiteCredentialsRequestV1
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		value, err := (plugins.Sites{Host: host}).Credentials(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(value.Cookie + "\x00" + value.UserAgent + "\x00" + value.APIKey + "\x00" + value.Token))
		return json.Marshal(map[string]any{"id": value.ID, "digest": hex.EncodeToString(sum[:]), "empty": value.Cookie == "" && value.UserAgent == "" && value.APIKey == "" && value.Token == ""})
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
		return json.RawMessage(`[[{"component":"VTextarea","props":{"model":"request_json","label":"独占测试站点请求JSON"}},{"component":"VSwitch","props":{"model":"run_once","label":"保存后请求一次"}}],{"request_json":"{\"id\":\"owned-site\",\"path\":\"/echo\",\"method\":\"GET\"}","run_once":false}]`), nil
	case "get_page":
		key := "calls"
		calls, err := base.GetData(ctx, &key)
		if err != nil {
			return nil, err
		}
		return json.Marshal([]any{map[string]any{"component": "VAlert", "props": map[string]any{"text": "HTTP调用：" + string(calls)}}})
	case "verify":
		var input struct {
			ID        string `json:"id"`
			Operation string `json:"operation"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		switch input.Operation {
		case "credentials":
			return handle(ctx, "credential_check", json.RawMessage(fmt.Sprintf(`{"id":%q}`, input.ID)), host)
		case "cookie_flow":
			first, err := (plugins.Sites{Host: host}).Request(ctx, plugins.SiteHTTPRequestV1{ID: input.ID, Path: "/set-cookie"})
			if err != nil || first.Response == nil {
				return nil, errors.New("owned cookie first request failed")
			}
			second, err := (plugins.Sites{Host: host}).Request(ctx, plugins.SiteHTTPRequestV1{ID: input.ID, Path: "/cookie-echo", Method: "POST", CookieOverride: &first.Response.Cookies})
			if err != nil {
				return nil, err
			}
			return json.Marshal(publicResult(second))
		case "user_agent":
			setting, err := (plugins.SystemSettings{Host: host}).Get(ctx, "USER_AGENT")
			if err != nil {
				return nil, err
			}
			var value string
			if json.Unmarshal(setting.Value, &value) != nil {
				return nil, errors.New("invalid owned USER_AGENT")
			}
			_, denied := (plugins.SystemSettings{Host: host}).Set(ctx, "USER_AGENT", "must-not-write")
			out, err := (plugins.Sites{Host: host}).Request(ctx, plugins.SiteHTTPRequestV1{ID: input.ID, Path: "/agent", UseHostUserAgent: true})
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"read": value, "write_denied": denied != nil, "response": publicResult(out)})
		}
		return nil, errors.New("unknown owned verification")
	case "request":
		return perform(ctx, host, raw)
	case "http":
		var call plugins.HTTPRequest
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		if call.Key == "verify" {
			result, err := handle(ctx, "verify", call.Body, host)
			if err != nil {
				return nil, err
			}
			return response(result)
		}
		if call.Key == "run" {
			var input json.RawMessage = call.Body
			if len(input) == 0 || bytes.Equal(input, []byte(`{}`)) {
				config, err := base.GetConfig(ctx)
				if err != nil {
					return nil, err
				}
				result, err := configured(ctx, host, config)
				if err != nil {
					return nil, err
				}
				return response(publicResultJSON(result))
			}
			result, err := perform(ctx, host, input)
			if err != nil {
				return nil, err
			}
			return response(publicResultJSON(result))
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
	return nil, errors.New("unknown owned site HTTP method")
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

// Public test UI/API keeps only the synthetic response and non-secret metadata;
// cookie values stay solely in private worker IPC/memory.
func publicResult(out plugins.SiteHTTPResultV1) plugins.SiteHTTPResultV1 {
	if out.Response != nil {
		copyResponse := *out.Response
		copyResponse.Cookies = nil
		out.Response = &copyResponse
	}
	return out
}
func publicResultJSON(raw json.RawMessage) json.RawMessage {
	var out plugins.SiteHTTPResultV1
	if json.Unmarshal(raw, &out) != nil {
		return json.RawMessage(`null`)
	}
	value, _ := json.Marshal(publicResult(out))
	return value
}
