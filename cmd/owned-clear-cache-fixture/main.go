// Owned clear-cache worker. Only test-supplied, exclusive directories and a
// loopback notification recipient are used; no production cache or service.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"light-media/internal/plugins"
)

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedClearCache", Name: "Owned clear cache", Version: "1.0", Runtime: "go-executable", Entry: "owned-clear-cache-fixture", HostAPIMin: 1, HostAPIMax: 1, Capabilities: plugins.BaseMethods(), Methods: []string{"init", "get_state", "get_module", "get_command", "get_service", "get_api", "clear", "notify", "event", "snapshot", "http", "close", "stop_service", "stop"}, Events: []string{"command.excute", "owned.clear_override", "system.error", "notice.message"}, APIs: []plugins.HTTPAPI{{Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}}}
}

type config struct {
	DelayMS   int             `json:"clear_delay_ms"`
	Enabled   bool            `json:"enabled"`
	Override  bool            `json:"override"`
	Fail      bool            `json:"fail"`
	Result    json.RawMessage `json:"result"`
	Directory string          `json:"directory"`
	Endpoint  string          `json:"endpoint"`
}
type worker struct {
	PID     int               `json:"pid"`
	Calls   map[string]int    `json:"calls"`
	Events  []json.RawMessage `json:"events"`
	Notices []json.RawMessage `json:"notices"`
	Inputs  []json.RawMessage `json:"inputs"`
}

func (f *worker) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	f.Calls[method]++
	b := plugins.Base{Host: host}
	var cfg config
	stored, e := b.GetConfig(ctx)
	if e != nil {
		return nil, e
	}
	if len(stored) > 0 && string(stored) != "null" {
		if e = json.Unmarshal(stored, &cfg); e != nil {
			return nil, e
		}
	}
	switch method {
	case "init", "close", "stop_service", "stop":
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.Marshal(cfg.Enabled)
	case "get_module":
		return json.RawMessage(`{"clear_cache":"clear","post_message":"notify"}`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_service":
		return json.RawMessage(`[]`), nil
	case "get_command":
		if cfg.Override {
			return json.RawMessage(`[{"cmd":"/clear_cache","event":"owned.clear_override","data":{"action":"owned"},"desc":"Owned override"}]`), nil
		}
		return json.RawMessage(`[]`), nil
	case "clear":
		f.Inputs = append(f.Inputs, append(json.RawMessage(nil), raw...))
		if cfg.Fail {
			return nil, errors.New("owned clear failure")
		}
		if cfg.Directory != "" {
			if !filepath.IsAbs(cfg.Directory) || filepath.Clean(cfg.Directory) != cfg.Directory {
				return nil, errors.New("owned directory invalid")
			}
			if st, e := os.Lstat(cfg.Directory); e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("owned directory missing")
			}
			if cfg.DelayMS > 0 {
				if e = os.WriteFile(filepath.Join(cfg.Directory, "clear.entered"), []byte("entered"), 0600); e != nil {
					return nil, e
				}
				time.Sleep(time.Duration(cfg.DelayMS) * time.Millisecond)
			}
			if e = os.Remove(filepath.Join(cfg.Directory, "cache.value")); e != nil && !os.IsNotExist(e) {
				return nil, e
			}
		}
		saved, e := b.GetData(ctx, new("clears"))
		if e != nil {
			return nil, e
		}
		n := 0
		_ = json.Unmarshal(saved, &n)
		if e = b.SaveData(ctx, "clears", n+1); e != nil {
			return nil, e
		}
		if len(cfg.Result) == 0 {
			return json.RawMessage(`null`), nil
		}
		return cfg.Result, nil
	case "notify":
		f.Notices = append(f.Notices, append(json.RawMessage(nil), raw...))
		if cfg.Endpoint != "" {
			u, e := url.Parse(cfg.Endpoint)
			if e != nil || u.Scheme != "http" || u.User != nil || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
				return nil, errors.New("owned recipient must be loopback")
			}
			req, e := http.NewRequestWithContext(ctx, "POST", cfg.Endpoint, bytes.NewReader(raw))
			if e != nil {
				return nil, e
			}
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("owned redirect denied") }}
			resp, e := client.Do(req)
			if e != nil {
				return nil, e
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != 204 {
				return nil, errors.New("owned recipient failed")
			}
		}
		return json.RawMessage(`null`), nil
	case "event":
		f.Events = append(f.Events, append(json.RawMessage(nil), raw...))
		return json.RawMessage(`null`), nil
	case "snapshot", "http":
		saved, e := b.GetData(ctx, new("clears"))
		if e != nil {
			return nil, e
		}
		snap := map[string]any{"pid": f.PID, "calls": f.Calls, "events": f.Events, "notices": f.Notices, "inputs": f.Inputs, "clears": saved}
		if method == "http" {
			body, err := json.Marshal(snap)
			if err != nil {
				return nil, err
			}
			return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
		}
		return json.Marshal(snap)
	}
	return nil, errors.New("unknown method")
}
func main() {
	m := manifest()
	if e := m.Validate(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	f := &worker{PID: os.Getpid(), Calls: map[string]int{}}
	if e := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, f.handle); e != nil {
		fmt.Fprintln(os.Stderr, "owned worker stopped")
		os.Exit(1)
	}
}
