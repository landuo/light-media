// Independent standard SDK consumer. No built-in adapter or direct host handler.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"light-media/internal/plugins"
)

var fixtureCapabilities = "storage.local.exists,storage.local.list,storage.local.delete_file"

func manifest() plugins.Manifest {
	caps := []string{}
	if fixtureCapabilities != "" {
		caps = strings.Split(fixtureCapabilities, ",")
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedLocalStorage", Name: "Owned local storage", Version: "1.0", Runtime: "go-executable", Entry: "owned-local-storage-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_module", "storage_override", "event", "resource", "resource_raw", "snapshot", "consumer", "http", "close", "stop_service", "stop"}, Capabilities: caps, Events: []string{"system.error"}, APIs: []plugins.HTTPAPI{{Key: "storage", Path: "/storage", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}}}
}

type object struct {
	mu          sync.Mutex
	previous    plugins.HostCaller
	calls       int
	events      []json.RawMessage
	moduleCalls []json.RawMessage
	count       int
	config      map[string]json.RawMessage
}

func (o *object) resource(ctx context.Context, host plugins.HostCaller, raw json.RawMessage) (json.RawMessage, error) {
	o.mu.Lock()
	o.calls++
	o.mu.Unlock()
	var in struct {
		Method  string                      `json:"method"`
		Payload plugins.LocalStorageRequest `json:"payload"`
	}
	if e := json.Unmarshal(raw, &in); e != nil {
		return nil, e
	}
	s := plugins.SharedLocalStorage{Host: host}
	switch in.Method {
	case plugins.LocalStorageExistsCapability:
		v, e := s.Exists(ctx, in.Payload.FileItem)
		if e != nil {
			return nil, e
		}
		return json.Marshal(v)
	case plugins.LocalStorageListCapability:
		return s.List(ctx, in.Payload.FileItem, in.Payload.Recursion)
	case plugins.LocalStorageDeleteCapability:
		return s.DeleteFile(ctx, in.Payload.FileItem)
	}
	return nil, errors.New("unknown fixture method")
}
func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		var c map[string]json.RawMessage
		if e := json.Unmarshal(raw, &c); e != nil {
			return nil, e
		}
		o.mu.Lock()
		o.config = c
		o.moduleCalls = nil
		o.count++
		o.mu.Unlock()
		return json.RawMessage(`null`), nil
	case "close", "stop_service", "stop":
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.RawMessage(`true`), nil
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_module":
		o.mu.Lock()
		defer o.mu.Unlock()
		var method string
		_ = json.Unmarshal(o.config["module_method"], &method)
		if method == "" {
			return json.RawMessage(`{}`), nil
		}
		return json.Marshal(map[string]string{method: "storage_override"})
	case "event":
		o.mu.Lock()
		o.events = append(o.events, append(json.RawMessage(nil), raw...))
		o.mu.Unlock()
		return json.RawMessage(`null`), nil
	case "storage_override":
		o.mu.Lock()
		o.moduleCalls = append(o.moduleCalls, append(json.RawMessage(nil), raw...))
		v := append(json.RawMessage(nil), o.config["module_result"]...)
		o.mu.Unlock()
		if len(v) == 0 {
			v = json.RawMessage(`null`)
		}
		return v, nil
	case "snapshot":
		o.mu.Lock()
		old := o.previous
		o.previous = host
		count := o.count
		calls := o.calls
		events := append([]json.RawMessage{}, o.events...)
		moduleCalls := append([]json.RawMessage{}, o.moduleCalls...)
		o.mu.Unlock()
		denied := true
		if old != nil {
			_, e := old(ctx, plugins.LocalStorageExistsCapability, json.RawMessage(`{"fileitem":{}}`))
			denied = e != nil
		}
		return json.Marshal(map[string]any{"pid": os.Getpid(), "init_count": count, "calls": calls, "events": events, "module_calls": moduleCalls, "stale_denied": denied, "had_previous": old != nil})
	case "resource_raw":
		var in struct {
			Method  string          `json:"method"`
			Payload json.RawMessage `json:"payload"`
		}
		if e := json.Unmarshal(raw, &in); e != nil {
			return nil, e
		}
		return host(ctx, in.Method, in.Payload)
	case "resource":
		return o.resource(ctx, host, raw)
	case "consumer":
		// Ordinary local RemoveLink algorithm: parent exists/list, prefix+extension
		// selection, delete selected item. No filename identity is manufactured.
		var in struct {
			Path string `json:"path"`
		}
		if e := json.Unmarshal(raw, &in); e != nil {
			return nil, e
		}
		parent, e := plugins.NewModuleFileItem(map[string]any{"path": filepath.Dir(in.Path) + "/", "type": "dir"})
		if e != nil {
			return nil, e
		}
		s := plugins.SharedLocalStorage{Host: host}
		exists, e := s.Exists(ctx, parent)
		if e != nil {
			return nil, e
		}
		if !exists {
			return json.RawMessage(`null`), nil
		}
		listed, e := s.List(ctx, parent, false)
		if e != nil {
			return nil, e
		}
		var items []*plugins.ModuleFileItem
		if e = json.Unmarshal(listed, &items); e != nil {
			return nil, e
		}
		for _, item := range items {
			var kind, name, ext string
			_ = json.Unmarshal(item.Fields["type"], &kind)
			_ = json.Unmarshal(item.Fields["name"], &name)
			_ = json.Unmarshal(item.Fields["extension"], &ext)
			if kind == "file" && strings.HasPrefix(name, filepath.Base(in.Path)) && strings.EqualFold(ext, "mkv") {
				return s.DeleteFile(ctx, item)
			}
		}
		return json.RawMessage(`null`), nil
	case "http":
		var request plugins.HTTPRequest
		if e := json.Unmarshal(raw, &request); e != nil {
			return nil, e
		}
		out, e := o.resource(ctx, host, request.Body)
		if e != nil {
			return nil, e
		}
		return plugins.JSONHTTPResponse(200, map[string]any{"result": out})
	}
	return nil, errors.New("unknown owned method")
}
func main() {
	m := manifest()
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	if e := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, (&object{}).handle); e != nil {
		fmt.Fprintln(os.Stderr, "owned storage worker failed")
		os.Exit(1)
	}
}
