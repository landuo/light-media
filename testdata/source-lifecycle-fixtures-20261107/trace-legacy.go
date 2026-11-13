package main

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/landuo/light-media-plugins/internal/sdk"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

func main() {
	m := sdk.Manifest{SchemaVersion: 1, PluginID: "OwnedStopOrder", Name: "Owned legacy stop order", Version: "1.0", Runtime: "go-executable", Entry: "owned-stop-order", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "close", "stop_service", "stop"}, Capabilities: sdk.BaseMethods()}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	handle := func(ctx context.Context, method string, raw json.RawMessage, host sdk.HostCaller) (json.RawMessage, error) {
		switch method {
		case "get_state":
			return json.RawMessage(`false`), nil
		case "get_service":
			return json.RawMessage(`[]`), nil
		case "close", "stop_service", "stop":
			b := sdk.Base{Host: host}
			meta, e := b.GetInstanceMetadata(ctx)
			if e != nil {
				return nil, e
			}
			raw, e := b.GetConfig(ctx)
			if e != nil {
				return nil, e
			}
			var cfg struct {
				Trace string `json:"owned_trace_endpoint"`
			}
			if e = json.Unmarshal(raw, &cfg); e != nil {
				return nil, e
			}
			u, e := url.Parse(cfg.Trace)
			if e != nil || u.User != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
				return nil, fmt.Errorf("owned trace requires loopback")
			}
			c := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("owned trace redirect denied") }}
			defer c.CloseIdleConnections()
			req, e := http.NewRequestWithContext(ctx, "GET", cfg.Trace+"/trace/"+url.PathEscape(meta.InstanceID)+"/"+method, nil)
			if e != nil {
				return nil, e
			}
			r, e := c.Do(req)
			if e != nil {
				return nil, e
			}
			_, e = io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if e != nil || r.StatusCode != 200 {
				return nil, fmt.Errorf("owned trace failed")
			}
			if e = b.SaveData(ctx, "actual_"+method, map[string]any{"instance": meta.InstanceID, "method": method}); e != nil {
				return nil, e
			}
		}
		return json.RawMessage(`null`), nil
	}
	if e := sdk.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, handle); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
