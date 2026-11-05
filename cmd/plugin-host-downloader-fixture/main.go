// Owned acceptance plugin. This is not a migrated business plugin.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/plugins"
	"os"
	"sync"
)

var fixtureVersion = "1.0"

type configuration struct {
	DownloaderID string `json:"downloader_id"`
	Operation    string `json:"operation"`
	PayloadJSON  string `json:"payload_json"`
	RunOnce      bool   `json:"run_once"`
}
type observation struct {
	InstanceID string          `json:"instance_id"`
	PID        int             `json:"pid"`
	Operation  string          `json:"operation"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
}

func key() *string { s := "observation"; return &s }
func main() {
	caps := append(plugins.BaseMethods(), plugins.DownloaderCompletedCapability, plugins.DownloaderAddCapability, plugins.DownloaderControlCapability, plugins.DownloaderTagsCapability, plugins.DownloaderFindByTagCapability, plugins.DownloaderDeleteTagsCapability, plugins.DownloaderListCapability, plugins.DownloaderTrackersCapability, plugins.DownloaderDownloadingCapability, plugins.DownloaderDeleteFilesCapability, plugins.DownloaderConnectionCapability)
	manifest := plugins.Manifest{SchemaVersion: 1, PluginID: "HostDownloaderTest", Name: "下载器宿主验收", Version: fixtureVersion, Runtime: "go-executable", Entry: "plugin-host-downloader-fixture", HostAPIMin: 1, HostAPIMax: 1, HasPage: true,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "http", "completed", "add", "control", "tags", "find_by_tag", "delete_tags", "list", "trackers", "downloading", "delete_files", "connection", "resource", "worker_pid", "stop"}, Capabilities: caps,
		APIs: []plugins.HTTPAPI{{Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}, {Key: "run", Path: "/run", Methods: []string{"POST"}, Auth: "bear"}}}
	for _, op := range []string{"completed", "downloading", "add", "control", "tags", "find_by_tag", "delete_tags", "list", "trackers", "delete_files", "connection"} {
		manifest.APIs = append(manifest.APIs, plugins.HTTPAPI{Key: op, Path: "/" + op, Methods: []string{"POST"}, Auth: "bear", Body: "raw"})
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(manifest)
		return
	}
	var mu sync.Mutex
	cfg := configuration{Operation: "completed", PayloadJSON: "{}"}
	handle := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		base := plugins.Base{Host: host}
		d := plugins.Downloaders{Host: host}
		invoke := func(operation string, payload json.RawMessage) (json.RawMessage, error) {
			switch operation {
			case "connection":
				var request plugins.DownloaderConnectionRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.Connection(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			case "delete_files":
				var request plugins.DownloaderDeleteFilesRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.DeleteFiles(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			case "downloading":
				var request plugins.DownloaderCompletedRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.Downloading(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			case "trackers":
				var request plugins.DownloaderTrackersRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.Trackers(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			case "list":
				var request plugins.DownloaderListRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.List(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			case "delete_tags":
				var request plugins.DownloaderDeleteTagsRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.DeleteTags(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			case "find_by_tag":
				var request plugins.DownloaderFindByTagRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.FindByTag(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			case "completed":
				var request plugins.DownloaderCompletedRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.Completed(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			case "add":
				var request plugins.DownloaderAddRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.Add(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			case "control":
				var request plugins.DownloaderControlRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.Control(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			case "tags":
				var request plugins.DownloaderTagsRequestV1
				if e := json.Unmarshal(payload, &request); e != nil {
					return nil, e
				}
				out, e := d.Tags(ctx, request)
				if e != nil {
					return nil, e
				}
				return json.Marshal(out)
			}
			return nil, errors.New("unsupported downloader fixture operation")
		}
		run := func() (observation, error) {
			meta, e := base.GetInstanceMetadata(ctx)
			if e != nil {
				return observation{}, e
			}
			out := observation{InstanceID: meta.InstanceID, PID: os.Getpid(), Operation: cfg.Operation}
			var payload map[string]json.RawMessage
			if e = json.Unmarshal([]byte(cfg.PayloadJSON), &payload); e != nil || payload == nil {
				e = errors.New("验收参数必须为JSON对象")
			} else {
				if _, ok := payload["downloader_id"]; !ok {
					payload["downloader_id"], _ = json.Marshal(cfg.DownloaderID)
				}
				body, _ := json.Marshal(payload)
				out.Result, e = invoke(cfg.Operation, body)
			}
			if e != nil {
				out.Error = e.Error()
			}
			if se := base.SaveData(ctx, "observation", out); se != nil {
				return out, se
			}
			return out, e
		}
		switch method {
		case "worker_pid":
			return json.Marshal(os.Getpid())
		case "init":
			cfg = configuration{Operation: "completed", PayloadJSON: "{}"}
			if len(raw) > 0 && string(raw) != "null" {
				if e := json.Unmarshal(raw, &cfg); e != nil {
					return nil, e
				}
			}
			if cfg.RunOnce {
				cfg.RunOnce = false
				if _, e := base.UpdateConfig(ctx, cfg); e != nil {
					return nil, e
				}
				_, _ = run()
			}
			return json.RawMessage(`null`), nil
		case "stop":
			return json.RawMessage(`null`), nil
		case "get_state":
			return json.RawMessage(`false`), nil
		case "get_service", "get_command":
			return json.RawMessage(`[]`), nil
		case "get_api":
			return json.Marshal(manifest.APIs)
		case "get_render_mode":
			return json.RawMessage(`["vuetify",""]`), nil
		case "get_form":
			return json.Marshal([]any{[]any{map[string]any{"component": "VTextField", "props": map[string]any{"model": "downloader_id", "label": "测试下载器 ID"}}, map[string]any{"component": "VSelect", "props": map[string]any{"model": "operation", "label": "验收操作", "items": []string{"completed", "downloading", "add", "control", "tags", "find_by_tag", "delete_tags", "list", "trackers", "delete_files", "connection"}}}, map[string]any{"component": "VTextarea", "props": map[string]any{"model": "payload_json", "label": "验收参数 JSON"}}, map[string]any{"component": "VSwitch", "props": map[string]any{"model": "run_once", "label": "保存后执行一次"}}}, cfg})
		case "get_page":
			saved, e := base.GetData(ctx, key())
			if e != nil {
				return nil, e
			}
			return json.Marshal([]any{map[string]any{"component": "VAlert", "props": map[string]any{"type": "info", "text": string(saved)}}})
		case "completed", "downloading", "add", "control", "tags", "find_by_tag", "delete_tags", "list", "trackers", "delete_files", "connection":
			return invoke(method, raw)
		case "resource":
			var req struct {
				Method string          `json:"method"`
				Value  json.RawMessage `json:"value"`
			}
			if e := json.Unmarshal(raw, &req); e != nil {
				return nil, e
			}
			return host(ctx, req.Method, req.Value)
		case "http":
			var req plugins.HTTPRequest
			if e := json.Unmarshal(raw, &req); e != nil {
				return nil, e
			}
			var result any
			status := 200
			switch req.Key {
			case "snapshot":
				saved, e := base.GetData(ctx, key())
				if e != nil {
					return nil, e
				}
				result = map[string]any{"pid": os.Getpid(), "version": fixtureVersion, "config": cfg, "observation": saved}
			case "run":
				out, e := run()
				result = map[string]any{"success": e == nil, "observation": out}
				if e != nil {
					status = 422
				}
			default:
				out, e := invoke(req.Key, req.Body)
				if e != nil {
					status = 422
					result = map[string]any{"success": false, "error": e.Error()}
				} else {
					result = out
				}
			}
			body, e := json.Marshal(result)
			if e != nil {
				return nil, e
			}
			return json.Marshal(plugins.HTTPResponse{Status: status, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
		}
		return nil, errors.New("unsupported downloader fixture method")
	}
	if e := plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, handle); e != nil {
		fmt.Fprintln(os.Stderr, "downloader fixture stopped")
		os.Exit(1)
	}
}
