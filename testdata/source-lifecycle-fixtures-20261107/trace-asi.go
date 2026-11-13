package main

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/landuo/light-media-plugins/internal/sdk"
	"github.com/landuo/light-media-plugins/plugins/autosignin"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

func main() {
	manifest := autosignin.Manifest()
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(manifest)
		return
	}
	plugin := autosignin.NewPlugin()
	var mu sync.Mutex
	trace := ""
	handle := func(ctx context.Context, method string, raw json.RawMessage, host sdk.HostCaller) (json.RawMessage, error) {
		if method == "init" {
			var config struct {
				Trace string `json:"owned_trace_endpoint"`
			}
			_ = json.Unmarshal(raw, &config)
			if config.Trace != "" {
				mu.Lock()
				trace = config.Trace
				mu.Unlock()
			}
		}
		if method == "close" || method == "stop_service" || method == "stop" {
			mu.Lock()
			endpoint := trace
			mu.Unlock()
			if endpoint != "" {
				meta, e := (sdk.Base{Host: host}).GetInstanceMetadata(ctx)
				if e != nil {
					return nil, e
				}
				u, e := url.Parse(endpoint)
				if e != nil || u.User != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
					return nil, fmt.Errorf("owned trace requires loopback")
				}
				client := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("owned trace redirect denied") }}
				defer client.CloseIdleConnections()
				req, e := http.NewRequestWithContext(ctx, "GET", endpoint+"/trace/"+url.PathEscape(meta.InstanceID)+"/"+method, nil)
				if e != nil {
					return nil, e
				}
				r, e := client.Do(req)
				if e != nil {
					return nil, e
				}
				_, e = io.Copy(io.Discard, r.Body)
				r.Body.Close()
				if e != nil || r.StatusCode != 200 {
					return nil, fmt.Errorf("owned trace failed")
				}
			}
		}
		return plugin.Handle(ctx, method, raw, host)
	}
	if e := sdk.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, manifest, handle); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
