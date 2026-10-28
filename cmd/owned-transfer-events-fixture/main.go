// Owned local transfer acceptance worker; it only records real host calls.
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
	"path/filepath"
	"strings"
	"time"

	"light-media/internal/plugins"
)

func main() {
	manifest := plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedTransferEvents", Name: "Owned transfer events", Version: "1.0", Runtime: "go-executable", Entry: "owned-transfer-events", HostAPIMin: 1, HostAPIMax: 1, Capabilities: plugins.BaseMethods(), Methods: []string{"init", "get_state", "get_module", "recognize", "post_download", "storage_op", "get_api", "get_service", "get_command", "get_page", "get_form", "get_render_mode", "get_sidebar_nav", "get_dashboard", "chain", "event", "http", "stop_service", "stop", "close"}, APIs: []plugins.HTTPAPI{{Key: "observations", Path: "/observations", Methods: []string{"GET"}, Auth: "bear"}, {Key: "post_downloads", Path: "/post-downloads", Methods: []string{"GET"}, Auth: "bear"}}}
	manifest.APIs = append(manifest.APIs, plugins.HTTPAPI{Key: "state", Path: "/state", Methods: []string{"GET"}, Auth: "bear"})
	manifest.Methods = append(manifest.Methods, "hide_bluray")
	for _, event := range []string{"transfer.rename.build", "transfer.rename", "transfer.intercept", "transfer.overwrite.check", "storage.operation", "resource.download", "resource.selection"} {
		manifest.ChainHandlers = append(manifest.ChainHandlers, plugins.ChainHandler{Event: event, Method: "chain"})
	}
	for _, event := range []string{"transfer.complete", "transfer.failed", "transfer.subtitle.complete", "transfer.subtitle.failed", "transfer.audio.complete", "transfer.audio.failed", "metadata.scrape", "download.added", "downloadfile.deleted"} {
		manifest.BroadcastHandlers = append(manifest.BroadcastHandlers, plugins.BroadcastHandler{Event: event, Method: "event"})
	}
	handle := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		base := plugins.Base{Host: host}
		observations := "observations"
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var c struct {
			RenameSourceExtension bool                                  `json:"rename_source_extension"`
			Storage               bool                                  `json:"storage"`
			StorageRoot           string                                `json:"storage_root"`
			StorageMode           string                                `json:"storage_mode"`
			MediaInfo             json.RawMessage                       `json:"mediainfo"`
			PostURL               string                                `json:"post_url"`
			Enabled               bool                                  `json:"enabled"`
			Patches               map[string]map[string]json.RawMessage `json:"patches"`
			Fail                  map[string]bool                       `json:"fail"`
		}
		if len(config) > 0 && string(config) != "null" {
			if err = json.Unmarshal(config, &c); err != nil {
				return nil, err
			}
		}
		switch method {
		case "get_module":
			modules := map[string]string{}
			if c.StorageMode == "directory-no-bluray" {
				modules["get_file_item"] = "hide_bluray"
			}
			if len(c.MediaInfo) > 0 && string(c.MediaInfo) != "null" {
				modules["recognize_media"], modules["async_recognize_media"] = "recognize", "recognize"
			}
			if c.PostURL != "" {
				modules["download_added"] = "post_download"
			}
			return json.Marshal(modules)
		case "recognize":
			return c.MediaInfo, nil
		case "post_download":
			key := "post_downloads"
			stored, e := base.GetData(ctx, &key)
			var calls []json.RawMessage
			if e != nil {
				return nil, e
			}
			if len(stored) > 0 && string(stored) != "null" && json.Unmarshal(stored, &calls) != nil {
				return nil, errors.New("invalid owned post-Add data")
			}
			calls = append(calls, append(json.RawMessage(nil), raw...))
			if e = base.SaveData(ctx, key, calls); e != nil {
				return nil, e
			}
			req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.PostURL, bytes.NewReader(raw))
			if e != nil {
				return nil, e
			}
			response, e := (&http.Client{Timeout: 5 * time.Second}).Do(req)
			if e != nil {
				return nil, e
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				return nil, errors.New("owned post-Add peer failure")
			}
			return json.RawMessage(`null`), nil
		case "hide_bluray":
			return json.RawMessage(`false`), nil
		case "storage_op":
			return ownedStorageOperation(ctx, base, raw, c.StorageRoot, c.StorageMode)
		case "get_state":
			return json.Marshal(c.Enabled)
		case "get_api":
			return json.Marshal(manifest.APIs)
		case "get_service", "get_command", "get_page", "get_form", "get_sidebar_nav":
			return json.RawMessage(`[]`), nil
		case "get_dashboard":
			return json.RawMessage(`null`), nil
		case "get_render_mode":
			return json.RawMessage(`["vuetify",""]`), nil
		case "init":
			key := "lifecycle"
			stored, e := base.GetData(ctx, &key)
			if e != nil {
				return nil, e
			}
			var value struct {
				InitCount int `json:"init_count"`
			}
			if len(stored) > 0 && string(stored) != "null" {
				if e = json.Unmarshal(stored, &value); e != nil {
					return nil, e
				}
			}
			value.InitCount++
			if e = base.SaveData(ctx, key, value); e != nil {
				return nil, e
			}
			return json.RawMessage(`null`), nil
		case "stop_service", "stop", "close":
			return json.RawMessage(`null`), nil
		case "chain", "event":
			return plugins.HandleMutableEvent(raw, func(event *plugins.MutableEvent) (json.RawMessage, error) {
				var calls []json.RawMessage
				stored, e := base.GetData(ctx, &observations)
				if e != nil {
					return nil, e
				}
				if len(stored) > 0 && string(stored) != "null" {
					if e = json.Unmarshal(stored, &calls); e != nil {
						return nil, e
					}
				}
				calls = append(calls, append(json.RawMessage(nil), raw...))
				if e = base.SaveData(ctx, "observations", calls); e != nil {
					return nil, e
				}
				var fields map[string]json.RawMessage
				if json.Unmarshal(event.Data, &fields) != nil || fields == nil {
					return nil, errors.New("invalid owned transfer event")
				}
				for key, value := range c.Patches[event.Type] {
					fields[key] = append(json.RawMessage(nil), value...)
				}
				if event.Type == "transfer.rename" && c.RenameSourceExtension {
					var item struct{ Extension string }
					var path string
					if json.Unmarshal(fields["source_item"], &item) != nil || json.Unmarshal(fields["updated_str"], &path) != nil {
						return nil, errors.New("invalid owned rename extension input")
					}
					if item.Extension != "" {
						fields["updated_str"], _ = json.Marshal(strings.TrimSuffix(path, filepath.Ext(path)) + "." + item.Extension)
					}
				}
				if event.Type == "storage.operation" && c.Storage {
					definition, e := base.LocalStorageOperation(ctx, "owned-local", map[string]string{"get_folder": "storage_op", "get_item": "storage_op", "get_item_strict": "storage_op", "list": "storage_op", "delete": "storage_op", "copy": "storage_op", "link": "storage_op"})
					if e != nil {
						return nil, e
					}
					fields["storage_oper"], _ = json.Marshal(definition)
				}
				event.Data, _ = json.Marshal(fields)
				if c.Fail[event.Type] {
					return nil, errors.New("owned transfer callback failed after mutation")
				}
				return nil, nil
			})
		case "http":
			var request plugins.HTTPRequest
			if json.Unmarshal(raw, &request) != nil || request.Method != "GET" {
				return nil, errors.New("invalid owned observations request")
			}
			if request.Key == "state" {
				key := "lifecycle"
				stored, e := base.GetData(ctx, &key)
				if e != nil {
					return nil, e
				}
				var value struct {
					InitCount int `json:"init_count"`
				}
				if len(stored) > 0 && string(stored) != "null" {
					if e = json.Unmarshal(stored, &value); e != nil {
						return nil, e
					}
				}
				body, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "init_count": value.InitCount})
				return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body})
			}
			if request.Key != "observations" && request.Key != "post_downloads" {
				return nil, errors.New("invalid owned observation key")
			}
			key := request.Key
			body, e := base.GetData(ctx, &key)
			if e != nil {
				return nil, e
			}
			if len(body) == 0 || string(body) == "null" {
				body = json.RawMessage(`[]`)
			}
			return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body})
		}
		return nil, errors.New("unsupported owned transfer fixture method")
	}
	if err := manifest.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "owned_transfer_manifest_invalid")
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(manifest)
		return
	}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, handle); err != nil {
		fmt.Fprintln(os.Stderr, "owned_transfer_worker_failed")
		os.Exit(1)
	}
}
