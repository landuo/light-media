package serverstatus

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
)

const ConfigureCapability = "server.status.configure"
const SnapshotCapability = "server.status.snapshot"

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "MPServerStatus", Name: "MoviePilot服务监控", Description: "在仪表板中实时显示MoviePilot公共服务器状态。", Icon: "Duplicati_A.png", Author: "jxxghp", AuthorURL: "https://github.com/jxxghp", Version: Version, Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "page", "dashboard", "stop"}, Capabilities: []string{ConfigureCapability, SnapshotCapability}, HasPage: true, Dashboards: []string{"default"}}
}

// Handle leaves the last sample and DNS/TLS caches in the host. A worker may
// select only the compiled operation and page/detail flag, never an endpoint,
// timeout, TLS policy, configuration revision or cache contents.
func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	if method == "stop" {
		return json.RawMessage(`null`), nil
	}
	if method == "config_form" {
		return json.Marshal(Form(false))
	}
	var call plugins.Invocation
	if decode(raw, &call) != nil {
		return nil, errors.New("invalid server status invocation")
	}
	m := New(nil, nil)
	if err := m.Init(call.Config); err != nil {
		return nil, err
	}
	if method == "page" && !m.State() {
		return json.Marshal(Object{"render_mode": "vuetify", "page": m.Page(ctx)})
	}
	if method != "invoke" && method != "page" && method != "dashboard" {
		return nil, errors.New("invalid server status method")
	}
	if method == "invoke" && (call.Action != "init" && call.Action != "reload") {
		return nil, errors.New("invalid server status action")
	}
	if host == nil {
		return nil, errors.New("missing server status host")
	}
	if method == "invoke" {
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	}
	request, _ := json.Marshal(Object{"include_network_details": method == "page"})
	reply, err := host(ctx, SnapshotCapability, request)
	if err != nil {
		return nil, err
	}
	var snapshot Snapshot
	if decode(reply, &snapshot) != nil {
		return nil, errors.New("invalid server status snapshot")
	}
	if method == "page" {
		return json.Marshal(Object{"render_mode": "vuetify", "page": Elements(snapshot, false)})
	}
	// The installed get_dashboard method deliberately has no enable guard.
	return json.Marshal(Object{"cols": Object{"cols": 12, "md": 8}, "attrs": Object{"refresh": 10}, "elements": Elements(snapshot, true)})
}
