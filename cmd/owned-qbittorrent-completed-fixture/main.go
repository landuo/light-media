// An owned ordinary compiled SDK fixture, not a downloader business plugin.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sync"

	"light-media/internal/plugins"
)

var fixtureDenied = "false"

func fixtureManifest(denied bool) plugins.Manifest {
	id, entry := "OwnedQBittorrentCompleted", "owned-qbittorrent-completed-sdk-fixture"
	capabilities := []string{plugins.BaseGetConfig, plugins.BaseGetInstanceMetadata, plugins.QBittorrentCompletedQueryCapability}
	if denied {
		id, entry = "OwnedQBittorrentCompletedDenied", "owned-qbittorrent-completed-sdk-fixture-no-cap"
		capabilities = []string{plugins.BaseGetConfig, plugins.BaseGetInstanceMetadata}
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: id, Name: "Owned qBittorrent completed query SDK fixture", Version: "1.0", Runtime: "go-executable", Entry: entry, HostAPIMin: 1, HostAPIMax: 1, HasPage: true,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "http", "snapshot", "resource", "stop"}, Capabilities: capabilities,
		APIs: []plugins.HTTPAPI{{Key: "query", Path: "/query", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}}}
}

type uiOptions struct {
	DownloaderID string `json:"downloader_id"`
	IDs          string `json:"ids_json"`
	Tags         string `json:"tags_json"`
}

func uiQuery(ctx context.Context, host plugins.HostCaller, config uiOptions) (map[string]any, error) {
	request := plugins.QBittorrentCompletedSelection{DownloaderID: config.DownloaderID}
	for _, field := range []struct {
		raw string
		out *any
	}{{config.IDs, &request.IDs}, {config.Tags, &request.Tags}} {
		if field.raw == "" {
			continue
		}
		d := json.NewDecoder(bytes.NewBufferString(field.raw))
		d.UseNumber()
		if err := d.Decode(field.out); err != nil {
			return nil, errors.New("查询筛选必须为有效 JSON 值")
		}
		if d.Decode(new(any)) != io.EOF {
			return nil, errors.New("查询筛选只能包含一个 JSON 值")
		}
	}
	result, err := (plugins.QBittorrent{Host: host}).QueryCompletedTorrents(ctx, request)
	if err != nil {
		return nil, err
	}
	if result.Error {
		return nil, errors.New("下载器查询失败，不是空结果")
	}
	rows := []map[string]string{}
	for _, task := range result.Torrents {
		var name, status string
		if err := task.DecodeField("name", &name); err != nil {
			return nil, errors.New("下载器名称字段无效")
		}
		if err := task.DecodeField("state", &status); err != nil {
			status = ""
		}
		rows = append(rows, map[string]string{"name": name, "status": status})
	}
	// Intentional presentation projection only. The SDK above consumes complete
	// source fields; no private trackers/raw fields enter the page or HTTP API.
	return map[string]any{"count": len(rows), "tasks": rows, "downloader_id": config.DownloaderID}, nil
}
func uiHandle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	base := plugins.Base{Host: host}
	configRaw, err := base.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	var config uiOptions
	if len(configRaw) > 0 && string(configRaw) != "null" {
		if err = json.Unmarshal(configRaw, &config); err != nil {
			return nil, err
		}
	}
	instance, err := base.GetInstanceMetadata(ctx)
	if err != nil {
		return nil, err
	}
	if method == "http" {
		var request plugins.HTTPRequest
		if err = json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Key != "query" || request.Path != "/query" || request.Method != "POST" {
			return nil, errors.New("unsupported query API")
		}
		// Fixture-only process receipt on the existing authenticated query API.
		// The ordinary {} query remains unchanged; no downloader call occurs
		// for observe_only=true, and no raw task fields or credentials escape.
		var options map[string]json.RawMessage
		if err := json.Unmarshal(request.Body, &options); err != nil || options == nil || len(options) > 1 {
			return nil, errors.New("invalid owned query options")
		}
		if len(options) == 1 {
			value, exists := options["observe_only"]
			value = bytes.TrimSpace(value)
			if !exists || (!bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false"))) {
				return nil, errors.New("observe_only must be a boolean")
			}
			if bytes.Equal(value, []byte("true")) {
				return plugins.JSONHTTPResponse(200, map[string]any{"observe_only": true, "pid": os.Getpid(), "instance_id": instance.InstanceID})
			}
		}
		summary, err := uiQuery(ctx, host, config)
		if err != nil {
			return nil, err
		}
		summary["instance_id"] = instance.InstanceID
		return plugins.JSONHTTPResponse(200, map[string]any{"success": true, "summary": summary})
	}
	button := map[string]any{"component": "VBtn", "text": "查询", "props": map[string]any{"color": "primary"}, "events": map[string]any{"click": map[string]any{"method": "POST", "api": "plugin/" + url.PathEscape(instance.InstanceID) + "/query", "params": map[string]any{}}}}
	content := []any{map[string]any{"component": "VCardText", "text": fmt.Sprintf("下载器 ID: %s | IDs: %s | tags: %s", config.DownloaderID, config.IDs, config.Tags)}, button}
	summary, err := uiQuery(ctx, host, config)
	if err != nil {
		content = append(content, map[string]any{"component": "VAlert", "text": err.Error(), "props": map[string]any{"type": "error"}})
	} else {
		value, _ := json.MarshalIndent(summary, "", "  ")
		content = append(content, map[string]any{"component": "pre", "text": string(value)})
	}
	return json.Marshal(content)
}

func main() {
	if fixtureDenied != "false" && fixtureDenied != "true" {
		os.Exit(80)
	}
	manifest := fixtureManifest(fixtureDenied == "true")
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		if json.NewEncoder(os.Stdout).Encode(manifest) != nil {
			os.Exit(81)
		}
		return
	}
	if len(os.Args) != 1 {
		os.Exit(80)
	}
	var previous plugins.HostCaller
	var mu sync.Mutex
	handle := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		switch method {
		case "init", "stop":
			return json.RawMessage(`null`), nil
		case "get_state":
			return json.RawMessage(`true`), nil
		case "get_service", "get_command":
			return json.RawMessage(`[]`), nil
		case "get_api":
			return json.Marshal(manifest.APIs)
		case "get_form":
			return json.RawMessage(`[[{"component":"VTextField","props":{"model":"downloader_id","label":"下载器配置 ID"}},{"component":"VTextField","props":{"model":"ids_json","label":"IDs（JSON；null 或 [] 为全部）"}},{"component":"VTextField","props":{"model":"tags_json","label":"tags（JSON 字符串或数组）"}}],{"downloader_id":"","ids_json":"null","tags_json":"null"}]`), nil
		case "get_page", "http":
			return uiHandle(ctx, method, raw, host)
		case "get_render_mode":
			return json.RawMessage(`["vuetify",""]`), nil
		case "resource":
			var request struct {
				Method  string          `json:"method"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			return host(ctx, request.Method, request.Payload)
		case "snapshot":
			var request plugins.QBittorrentCompletedSelection
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if err := d.Decode(&request); err != nil {
				return nil, err
			}
			mu.Lock()
			old := previous
			mu.Unlock()
			staleDenied := true
			if old != nil {
				probe, _ := json.Marshal(request)
				value, err := old(ctx, plugins.QBittorrentCompletedQueryCapability, probe)
				staleDenied = err != nil && len(value) == 0
			}
			sdk := plugins.QBittorrent{Host: host}
			var options struct {
				Convenience bool `json:"convenience"`
			}
			_ = json.Unmarshal(raw, &options)
			var result plugins.QBittorrentCompletedResult
			var err error
			if options.Convenience {
				var rows []plugins.QBittorrentTorrent
				rows, err = sdk.GetCompletedTorrents(ctx, request)
				result = plugins.QBittorrentCompletedResult{SchemaVersion: 1, DownloaderID: request.DownloaderID, Torrents: rows, Error: rows == nil}
				if rows == nil {
					result.Torrents = []plugins.QBittorrentTorrent{}
				}
			} else {
				result, err = sdk.QueryCompletedTorrents(ctx, request)
			}
			if err != nil {
				return nil, err
			}
			mu.Lock()
			previous = host
			mu.Unlock()
			return json.Marshal(struct {
				Result      plugins.QBittorrentCompletedResult `json:"result"`
				PID         int                                `json:"pid"`
				HadPrevious bool                               `json:"had_previous"`
				StaleDenied bool                               `json:"stale_denied"`
			}{result, os.Getpid(), old != nil, staleDenied})
		}
		return nil, errors.New("unsupported owned qBittorrent completed query method")
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, handle) != nil {
		fmt.Fprintln(os.Stderr, "owned qBittorrent completed query SDK stopped with an error")
		os.Exit(82)
	}
}
