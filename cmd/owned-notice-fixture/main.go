package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"light-media/internal/plugins"
)

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedNotice", Name: "Owned notice", Version: "1.0", Runtime: "go-executable", Entry: "owned-notice-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_module", "deliver", "post_notice", "get_service", "get_command", "get_api", "event", "close", "stop_service", "stop"},
		Events:       []string{"notice.message", "system.error"},
		Capabilities: append(plugins.BaseMethods(), plugins.PostMessageCapability)}
}

func handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	b := plugins.Base{Host: host}
	cfg, err := b.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	var config struct {
		Enabled      bool   `json:"enabled"`
		ModuleOff    bool   `json:"module_off"`
		Endpoint     string `json:"endpoint"`
		Fail         bool   `json:"fail"`
		EventDelayMS int    `json:"event_delay_ms"`
	}
	_ = json.Unmarshal(cfg, &config)
	switch method {
	case "init", "close", "stop_service", "stop":
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.Marshal(config.Enabled)
	case "get_module":
		if config.ModuleOff {
			return json.RawMessage(`{}`), nil
		}
		return json.RawMessage(`{"post_message":"deliver"}`), nil
	case "get_api", "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "post_notice":
		var fields map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err = decoder.Decode(&fields); err != nil {
			return nil, err
		}
		result, err := b.PostMessage(ctx, fields)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "deliver":
		if config.Fail {
			return nil, errors.New("owned sender failure")
		}
		if config.Endpoint == "" {
			return nil, errors.New("owned sender not configured")
		}
		request, err := http.NewRequestWithContext(ctx, "POST", config.Endpoint, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		if response.StatusCode != 204 {
			return nil, errors.New("owned sender rejected")
		}
		return json.RawMessage(`null`), nil
	case "event":
		if config.EventDelayMS > 0 {
			select {
			case <-time.After(time.Duration(config.EventDelayMS) * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		var event struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &event)
		if event.Type == "system.error" {
			key := "system_error_count"
			prior, err := b.GetData(ctx, &key)
			if err != nil {
				return nil, err
			}
			var count int
			_ = json.Unmarshal(prior, &count)
			if err = b.SaveData(ctx, key, count+1); err != nil {
				return nil, err
			}
			return json.RawMessage(`null`), b.SaveData(ctx, "system_error_event", raw)
		}
		return json.RawMessage(`null`), b.SaveData(ctx, "notice_event", raw)
	}
	return nil, errors.New("unknown method")
}

func main() {
	m := manifest()
	if err := m.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	if err := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
