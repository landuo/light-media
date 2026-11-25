package cloudflarespeedtest

import (
	"context"
	_ "embed"
	"encoding/json"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/torrenttransfer"
)

//go:embed form.json
var form json.RawMessage

func formPair() (json.RawMessage, error) {
	var parts struct {
		Conf  json.RawMessage `json:"conf"`
		Model json.RawMessage `json:"model"`
	}
	if err := json.Unmarshal(form, &parts); err != nil {
		return nil, err
	}
	return json.Marshal([]json.RawMessage{parts.Conf, parts.Model})
}

func commands() []Object {
	return []Object{{"cmd": "/cloudflare_speedtest", "event": "plugin.action", "desc": "Cloudflare IP优选", "data": Object{"action": Action}}}
}

func httpAPIs() []sdk.HTTPAPI {
	return []sdk.HTTPAPI{{Key: Action, Path: "/cloudflare_speedtest", Methods: []string{"GET"}, Auth: "apikey",
		Query: []sdk.HTTPParameter{{Name: "apikey", Kind: "api_key", Required: true}}}}
}

func (m *Machine) services() ([]Object, error) {
	config := *m.view.Load()
	if !configState(config) {
		return []Object{}, nil
	}
	cron, ok := config["cron"].(string)
	if !ok {
		return nil, sourceError("TypeError", "cron")
	}
	if _, err := torrenttransfer.ParseCrontab(cron, "UTC"); err != nil {
		return nil, sourceError("ValueError", "cron")
	}
	return []Object{{"id": PluginID, "name": "Cloudflare IP优选服务", "handler": Action,
		"trigger": Object{"kind": "cron", "cron": cron, "local_timezone": true}, "input": Object{}}}, nil
}

type APIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// A correctly authenticated route reports success after normal early returns
// from the original operation. The credential itself stays inside the host.
func (m *Machine) api(ctx context.Context, credentialMatches bool) (APIResponse, error) {
	if !credentialMatches {
		return APIResponse{Message: "API密钥错误"}, nil
	}
	if _, err := m.Main(ctx, nil); err != nil {
		return APIResponse{}, err
	}
	return APIResponse{Success: true}, nil
}
