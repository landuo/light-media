// Owned control provider. Endpoints are supplied only by acceptance fixtures.
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
	"sync"
	"time"

	"light-media/internal/plugins"
)

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedDownloaderControl", Name: "Owned downloader control", Version: "1.0", Runtime: "go-executable", Entry: "owned-downloader-control-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_module", "execute", "get_service", "get_command", "get_api", "http", "stop", "stop_service", "close"}, Capabilities: plugins.BaseMethods(),
		APIs: []plugins.HTTPAPI{{Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}}}
}

type object struct {
	mu    sync.Mutex
	Calls int             `json:"calls"`
	Input json.RawMessage `json:"input"`
	PID   int             `json:"pid"`
}

func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	config, err := (plugins.Base{Host: host}).GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Enabled  bool            `json:"enabled"`
		Module   string          `json:"module"`
		Endpoint string          `json:"endpoint"`
		Fail     bool            `json:"fail"`
		Result   json.RawMessage `json:"result"`
	}
	if json.Unmarshal(config, &cfg) != nil {
		return nil, errors.New("invalid owned configuration")
	}
	switch method {
	case "init", "stop", "stop_service", "close":
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.Marshal(cfg.Enabled)
	case "get_module":
		return json.Marshal(map[string]string{cfg.Module: "execute"})
	case "execute":
		o.Calls++
		o.Input = append(json.RawMessage(nil), raw...)
		if cfg.Fail {
			return nil, errors.New("owned control failure")
		}
		if cfg.Endpoint != "" {
			req, err := http.NewRequestWithContext(ctx, "POST", cfg.Endpoint, bytes.NewReader(raw))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
			if err != nil {
				return nil, err
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				return nil, errors.New("owned control recipient rejected")
			}
		}
		if len(cfg.Result) == 0 {
			return json.RawMessage(`null`), nil
		}
		return cfg.Result, nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "http":
		var request plugins.HTTPRequest
		if json.Unmarshal(raw, &request) != nil || request.Key != "snapshot" {
			return nil, errors.New("invalid owned observation request")
		}
		o.PID = os.Getpid()
		body, err := json.Marshal(o)
		if err != nil {
			return nil, err
		}
		return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	default:
		return nil, errors.New("unsupported owned method")
	}
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
	o := &object{}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, o.handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
