package cloudflarespeedtest

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

//go:embed form.json
var form json.RawMessage

func Form() json.RawMessage { return append(json.RawMessage(nil), form...) }

func Manifest() Object {
	return Object{
		"schema_version": 1, "plugin_id": PluginID, "name": "Cloudflare IP优选", "description": "🌩 测试 Cloudflare CDN 延迟和速度，自动优选IP。",
		"icon": "cloudflare.jpg", "version": Version, "author": "thsrite", "author_url": "https://github.com/thsrite",
		"runtime": "go-executable", "entry": "light-media", "host_api_min": 1, "host_api_max": 1,
		"methods": []string{"config_form", "invoke", "stop"}, "capabilities": []string{ConfigureCapability, RunCapability}, "has_page": false,
	}
}

type Command struct {
	Cmd         string `json:"cmd"`
	Event       string `json:"event"`
	Description string `json:"desc"`
	Data        Object `json:"data"`
}

func Commands() []Command {
	return []Command{{Cmd: "/cloudflare_speedtest", Event: "PluginAction", Description: "Cloudflare IP优选", Data: Object{"action": Action}}}
}

type API struct {
	Key, Path, Endpoint, Auth, Summary, Description string
	Methods                                         []string
	CredentialQuery                                 string
}

func APIs() []API {
	return []API{{Key: Action, Path: "/cloudflare_speedtest", Endpoint: Action, Methods: []string{"GET"}, Auth: "apikey", CredentialQuery: "apikey", Summary: "Cloudflare IP优选", Description: "Cloudflare IP优选"}}
}

type Service struct {
	ID, Name, Cron, Function string
	Kwargs                   Object
}

// Cron validation is delegated to the same trusted scheduler the App uses;
// embedding a second permissive cron parser would silently alter its meaning.
func (m *Machine) Services(validateCron func(string) error) ([]Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.state() {
		return []Service{}, nil
	}
	s, ok := m.config["cron"].(string)
	if !ok {
		return nil, sourceError("TypeError", "cron")
	}
	if len(strings.Fields(s)) != 5 {
		return nil, sourceError("ValueError", "cron field count")
	}
	if validateCron == nil {
		return nil, errors.New("CloudflareSpeedTest scheduler validator unconfigured")
	}
	if err := validateCron(s); err != nil {
		return nil, err
	}
	return []Service{{ID: PluginID, Name: "Cloudflare IP优选服务", Cron: s, Function: Action, Kwargs: Object{}}}, nil
}

type APIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// API uses an already authenticated host fact, never a credential sent over
// worker IPC. Normal early returns in Main still produce source success=True.
func (m *Machine) API(ctx context.Context, credentialMatches bool) (APIResponse, error) {
	if !credentialMatches {
		return APIResponse{Message: "API密钥错误"}, nil
	}
	_, err := m.Main(ctx, nil)
	if err != nil {
		return APIResponse{}, err
	}
	return APIResponse{Success: true}, nil
}

const ConfigureCapability = "cloudflare.configure"
const RunCapability = "cloudflare.run"

type HostCaller func(context.Context, string, json.RawMessage) (json.RawMessage, error)

// The aliases are deliberately independent of the frozen App tree. Integration
// adapts these exact shapes to plugins.Manifest/Invocation/HostCaller.
func Handle(ctx context.Context, method string, raw json.RawMessage, host HostCaller) (json.RawMessage, error) {
	switch method {
	case "config_form":
		return Form(), nil
	case "stop":
		return json.RawMessage(`null`), nil
	}
	var in struct {
		Action string          `json:"action"`
		Input  json.RawMessage `json:"input"`
	}
	if method != "invoke" || json.Unmarshal(raw, &in) != nil || host == nil {
		return nil, errors.New("invalid CloudflareSpeedTest invocation")
	}
	if in.Action == "init" || in.Action == "reload" {
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	}
	var ref map[string]string
	key := ""
	switch in.Action {
	case "event":
		key = "event_id"
	case "schedule":
		key = "job_id"
	case "http":
		key = "request_id"
	default:
		return nil, errors.New("unsupported CloudflareSpeedTest action")
	}
	d := json.NewDecoder(bytes.NewReader(in.Input))
	if d.Decode(&ref) != nil || d.Decode(new(any)) != io.EOF || len(ref) != 1 || ref[key] == "" {
		return nil, errors.New("unbound CloudflareSpeedTest invocation")
	}
	payload, _ := json.Marshal(ref)
	return host(ctx, RunCapability, payload)
}
