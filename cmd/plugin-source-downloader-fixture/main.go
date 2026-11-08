// Owned acceptance worker for finite downloader source outcomes, not business.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"os"
)

func main() {
	caps := []string{plugins.DownloaderSourceFindByTagCapability, plugins.DownloaderFindByTagCapability, plugins.DownloaderSourceConnectionCapability, plugins.DownloaderConnectionCapability, plugins.DownloaderConfigurationsReadCapability, plugins.DownloaderSourceTasksCapability, plugins.DownloaderSourceCompletedCapability, plugins.DownloaderSourceAddCapability, plugins.DownloaderSourceControlCapability, plugins.DownloaderTasksReadCapability, plugins.DownloaderCompletedCapability, plugins.DownloaderAddCapability, plugins.DownloaderControlCapability}
	manifest := plugins.Manifest{SchemaVersion: 1, PluginID: "SourceDownloaderTest", Name: "Owned source downloader outcomes", Version: "1.0", Runtime: "go-executable", Entry: "plugin-source-downloader-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "configurations", "source_connection", "legacy_connection", "source_find", "legacy_find", "source_tasks", "source_completed", "source_add", "source_control", "legacy_tasks", "legacy_completed", "legacy_add", "legacy_control", "resource", "stop"}, Capabilities: caps}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		json.NewEncoder(os.Stdout).Encode(manifest)
		return
	}
	handle := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		d := plugins.Downloaders{Host: host}
		var out any
		var err error
		switch method {
		case "init", "stop":
			return json.RawMessage(`null`), nil
		case "get_state":
			return json.RawMessage(`false`), nil
		case "get_service", "get_command", "get_api":
			return json.RawMessage(`[]`), nil
		case "get_render_mode":
			return json.RawMessage(`["vuetify",null]`), nil
		case "configurations":
			out, err = d.Configurations(ctx)
		case "source_find", "legacy_find":
			var r plugins.DownloaderFindByTagRequestV1
			if err = json.Unmarshal(raw, &r); err == nil {
				if method == "source_find" {
					out, err = d.SourceFindByTag(ctx, r)
				} else {
					out, err = d.FindByTag(ctx, r)
				}
			}
		case "source_connection", "legacy_connection":
			var r plugins.DownloaderConnectionRequestV1
			if err = json.Unmarshal(raw, &r); err == nil {
				if method == "source_connection" {
					out, err = d.SourceConnection(ctx, r)
				} else {
					out, err = d.Connection(ctx, r)
				}
			}
		case "source_tasks", "legacy_tasks":
			var r plugins.DownloaderTasksRequestV1
			if err = json.Unmarshal(raw, &r); err == nil {
				if method == "source_tasks" {
					out, err = d.SourceTasks(ctx, r)
				} else {
					out, err = d.Tasks(ctx, r)
				}
			}
		case "source_completed", "legacy_completed":
			var r plugins.DownloaderCompletedRequestV1
			if err = json.Unmarshal(raw, &r); err == nil {
				if method == "source_completed" {
					out, err = d.SourceCompleted(ctx, r)
				} else {
					out, err = d.Completed(ctx, r)
				}
			}
		case "source_add", "legacy_add":
			var r plugins.DownloaderAddRequestV1
			if err = json.Unmarshal(raw, &r); err == nil {
				if method == "source_add" {
					var source plugins.DownloaderSourceAddRequestV1
					if err = json.Unmarshal(raw, &source); err == nil {
						out, err = d.SourceAdd(ctx, source)
					}
				} else {
					out, err = d.Add(ctx, r)
				}
			}
		case "source_control", "legacy_control":
			var r plugins.DownloaderControlRequestV1
			if err = json.Unmarshal(raw, &r); err == nil {
				if method == "source_control" {
					out, err = d.SourceControl(ctx, r)
				} else {
					out, err = d.Control(ctx, r)
				}
			}
		case "resource":
			var r struct {
				Method string          `json:"method"`
				Value  json.RawMessage `json:"value"`
			}
			if err = json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			return host(ctx, r.Method, r.Value)
		default:
			return nil, errors.New("unsupported owned source outcome operation")
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, handle) != nil {
		os.Exit(1)
	}
}
