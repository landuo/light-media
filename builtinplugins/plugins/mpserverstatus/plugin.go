package mpserverstatus

import (
	"context"
	"encoding/json"
	"errors"

	sdk "light-media/builtinplugins/internal/sdk"
)

func Manifest() sdk.Manifest {
	empty := []string{}
	return sdk.Manifest{SchemaVersion: 1, PluginID: "MPServerStatus", Name: "MoviePilot服务监控", Description: "在仪表板中实时显示MoviePilot公共服务器状态。", Icon: "Duplicati_A.png", Version: Version, Author: "jxxghp", AuthorURL: "https://github.com/jxxghp", PluginOrder: 99, Runtime: "go-executable", Entry: "mp-server-status", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_form", "get_page", "get_dashboard", "get_api", "get_command", "get_render_mode", "stop_service", "stop", "close"}, Capabilities: []string{sdk.NetworkProxyReadCapability, sdk.LogWriteCapability}, HasPage: true, FormModelPolicy: &sdk.FormModelPolicy{Installed: sdk.FormModelSource, Preview: sdk.FormModelSource}, DashboardParameters: &empty, RuntimeSupport: &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}}}
}

type Plugin struct {
	machine *Machine
	// Set only during construction; production always creates a fresh native
	// adapter for one call, retaining no HostCaller or credential snapshot.
	network func(sdk.HostCaller) Network
}

func NewPlugin() *Plugin {
	return &Plugin{machine: New(nil, nil), network: func(c sdk.HostCaller) Network { return &NativeNetwork{caller: c} }}
}
func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (result json.RawMessage, err error) {
	defer sourcePanic(&err)
	switch method {
	case "init":
		return nil, p.machine.Init(raw)
	case "get_state":
		return json.Marshal(p.machine.State())
	case "get_form":
		f := Form(p.machine.State())
		return json.Marshal([]any{f["conf"], f["model"]})
	case "get_command", "get_api":
		return json.RawMessage(`[]`), nil
	case "get_service", "stop_service", "stop", "close":
		return json.RawMessage(`null`), nil
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_page", "get_dashboard":
		dashboard := method == "get_dashboard"
		// A disabled plugin must not do any work, dashboard or not. The dashboard
		// branch skipped this check, so a disabled plugin was still probed on every
		// dashboard render — which pulled in the 22 MB encoding-detection model
		// tables and kept them resident for the life of the process.
		if !p.machine.State() {
			if dashboard {
				return json.Marshal([]any{})
			}
			return json.Marshal([]any{Object{"component": "div", "text": "插件未启用", "props": Object{"class": "text-center"}}})
		}
		log := func(ctx context.Context, level, message string) error {
			return (sdk.Logger{Host: caller}).Log(ctx, sdk.LogLevel(level), message)
		}
		snapshot, e := p.machine.LoadWith(ctx, !dashboard, p.network(caller), log)
		if e != nil {
			return nil, e
		}
		elements := Elements(snapshot, dashboard)
		if dashboard {
			return json.Marshal([]any{Object{"cols": 12, "md": 8}, Object{"refresh": 10}, elements})
		}
		return json.Marshal(elements)
	}
	return nil, errors.New("unsupported source method")
}
