// Command owned-history-ui-fixture is an ordinary installable SDK consumer for
// local browser acceptance. It reads live host history; it never seeds rows,
// writes media files, accepts SQL, or caches history in plugin data.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"

	"light-media/internal/plugins"
)

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedHistoryUI", Name: "Owned history queries", Description: "Read-only source/destination history lookup using the standard Go SDK", Version: "1.0", Runtime: "go-executable", Entry: "owned-history-ui-fixture", HostAPIMin: 1, HostAPIMax: 1, HasPage: true,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "http", "close", "stop_service", "stop"},
		Capabilities: append(plugins.BaseMethods(), plugins.TransferHistoryBySourceCapability, plugins.TransferHistoryByDestinationCapability),
		APIs:         []plugins.HTTPAPI{{Key: "query_source", Path: "/query-source", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}, {Key: "query_destination", Path: "/query-destination", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}},
	}
}

type options struct {
	Enabled *bool   `json:"enabled"`
	Path    *string `json:"path"`
	Storage *string `json:"storage"`
	View    string  `json:"view"`
}

func load(ctx context.Context, base plugins.Base) (options, map[string]json.RawMessage, error) {
	raw, err := base.GetConfig(ctx)
	if err != nil {
		return options{}, nil, err
	}
	var config options
	fields := map[string]json.RawMessage{}
	if len(raw) > 0 && string(raw) != "null" {
		if err = json.Unmarshal(raw, &config); err != nil {
			return config, nil, err
		}
		if err = json.Unmarshal(raw, &fields); err != nil {
			return config, nil, err
		}
	}
	return config, fields, nil
}

func query(ctx context.Context, host plugins.HostCaller, config options, view string) (*plugins.TransferHistoryRecord, error) {
	sdk := plugins.Histories{Host: host}
	if view == "destination" {
		return sdk.TransferByDestination(ctx, config.Path, config.Storage)
	}
	return sdk.TransferBySource(ctx, config.Path, config.Storage)
}

func handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init", "close", "stop_service", "stop":
		return json.RawMessage(`null`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_service", "get_command", "get_sidebar_nav":
		return json.RawMessage(`[]`), nil
	case "get_dashboard":
		return json.RawMessage(`[{},{},[]]`), nil
	case "get_render_mode":
		return json.RawMessage(`["vuetify",""]`), nil
	case "get_form":
		return json.RawMessage(`[[{"component":"VSwitch","props":{"model":"enabled","label":"Enabled"}},{"component":"VTextField","props":{"model":"path","label":"Exact history path"}},{"component":"VTextField","props":{"model":"storage","label":"Exact storage filter (empty means any)"}}],{"enabled":true,"path":"","storage":"","view":"source"}]`), nil
	}
	base := plugins.Base{Host: host}
	config, fields, err := load(ctx, base)
	if err != nil {
		return nil, err
	}
	switch method {
	case "get_state":
		return json.Marshal(config.Enabled == nil || *config.Enabled)
	case "get_page":
		instance, err := base.GetInstanceMetadata(ctx)
		if err != nil {
			return nil, err
		}
		view := config.View
		if view != "destination" {
			view = "source"
		}
		row, err := query(ctx, host, config, view)
		if err != nil {
			return nil, err
		}
		record, err := json.MarshalIndent(row, "", "  ")
		if err != nil {
			return nil, err
		}
		button := func(label, path string) map[string]any {
			return map[string]any{"component": "VBtn", "text": label, "props": map[string]any{"color": "primary", "class": "ma-2"}, "events": map[string]any{"click": map[string]any{"method": "POST", "api": "plugin/" + url.PathEscape(instance.InstanceID) + path, "params": map[string]any{}}}}
		}
		return json.Marshal([]any{map[string]any{"component": "VCard", "props": map[string]any{"title": "Live transfer history"}, "content": []any{
			map[string]any{"component": "VCardText", "text": fmt.Sprintf("Instance: %s | worker PID: %d | lookup: %s. Save the exact path/storage in Settings; both buttons read the authoritative host history.", instance.InstanceID, os.Getpid(), view)},
			button("Query by source", "/query-source"), button("Query by destination", "/query-destination"),
			map[string]any{"component": "pre", "text": string(record)},
		}}})
	case "http":
		var request plugins.HTTPRequest
		if err = json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		view := "source"
		switch {
		case request.Key == "query_source" && request.Path == "/query-source" && request.Method == "POST":
		case request.Key == "query_destination" && request.Path == "/query-destination" && request.Method == "POST":
			view = "destination"
		default:
			return nil, errors.New("unsupported history UI API")
		}
		row, err := query(ctx, host, config, view)
		if err != nil {
			return nil, err
		}
		// Persist only a presentation selector. The following get_page performs a
		// fresh SDK read; no history record is mirrored into Base config/data.
		fields["view"], _ = json.Marshal(view)
		if _, err = base.UpdateConfig(ctx, fields); err != nil {
			return nil, err
		}
		instance, err := base.GetInstanceMetadata(ctx)
		if err != nil {
			return nil, err
		}
		return plugins.JSONHTTPResponse(200, map[string]any{"success": true, "lookup": view, "record": row, "instance_id": instance.InstanceID, "pid": os.Getpid()})
	}
	return nil, errors.New("unsupported history UI method")
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
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "unsupported argument")
		os.Exit(2)
	}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
