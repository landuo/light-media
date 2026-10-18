package autobackup

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"

	"light-media/internal/plugins"
)

//go:embed form.json
var form json.RawMessage

//go:embed metadata.json
var metadata json.RawMessage

func Form(dataPath string) (json.RawMessage, error) {
	var v Object
	d := json.NewDecoder(bytes.NewReader(form))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	v["defaults"].(map[string]any)["back_path"] = dataPath
	return json.Marshal(v)
}
func Metadata() json.RawMessage { return append(json.RawMessage(nil), metadata...) }
func Commands() json.RawMessage { return json.RawMessage(`null`) }
func Page() json.RawMessage     { return json.RawMessage(`null`) }

const ConfigureCapability = "autobackup.configure"
const ExecuteCapability = "autobackup.execute"
const FormCapability = "autobackup.form"
const APICapability = "autobackup.api"

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "AutoBackup", Name: "自动备份", Description: "自动备份数据和配置文件。", Icon: "Time_machine_B.png", Author: "thsrite", AuthorURL: "https://github.com/thsrite", Version: "2.1.4", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{ConfigureCapability, ExecuteCapability, FormCapability, APICapability}, APIs: []plugins.HTTPAPI{{Key: "backup", Path: "/backup", Methods: []string{"GET"}, Auth: "apikey", Query: []plugins.HTTPParameter{{Name: "apikey", Kind: "api_key", Required: true}}}}}
}

// The worker only forwards opaque host-owned event/request identity. It never
// authorizes a destination, filesystem operation, DB connection or credentials.
func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	if method == "config_form" {
		return host(ctx, FormCapability, json.RawMessage(`{}`))
	}
	if method == "stop" {
		return json.RawMessage(`null`), nil
	}
	var inv plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &inv) != nil {
		return nil, errors.New("invalid backup invocation")
	}
	switch inv.Action {
	case "init", "reload":
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	case "run", "backup":
		return host(ctx, ExecuteCapability, json.RawMessage(`{}`))
	case "service", "once", "api":
		var token struct {
			EventID   string `json:"event_id,omitempty"`
			RequestID string `json:"request_id,omitempty"`
		}
		d := json.NewDecoder(bytes.NewReader(inv.Input))
		d.DisallowUnknownFields()
		if d.Decode(&token) != nil || d.Decode(new(any)) != io.EOF {
			return nil, errors.New("unbound backup occurrence")
		}
		if inv.Action == "api" {
			if token.RequestID == "" || len(token.RequestID) > 128 || token.EventID != "" {
				return nil, errors.New("unbound backup API")
			}
			return host(ctx, APICapability, inv.Input)
		}
		if token.EventID == "" || len(token.EventID) > 128 || token.RequestID != "" {
			return nil, errors.New("unbound backup event")
		}
		return host(ctx, ExecuteCapability, inv.Input)
	default:
		return nil, errors.New("unsupported backup action")
	}
}
