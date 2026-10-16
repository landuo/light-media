package cleaninvalidseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"light-media/internal/plugins"
)

const ConfigureCapability = "clean_seed.configure"
const ExecuteCapability = "clean_seed.execute"
const FormCapability = "clean_seed.form"

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "CleanInvalidSeed", Name: "清理无效做种", Description: "清理已经被站点删除的种子及源文件，支持qBittorrent和Transmission", Icon: "clean_a.png", Version: "2.1", Author: "DzAvril", AuthorURL: "https://github.com/DzAvril", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{ConfigureCapability, ExecuteCapability, FormCapability}, Events: []string{"plugin_action"}}
}

// Invocation.Config, paths, hashes, downloader selection and remote command data
// are never trusted to authorize effects. Stateful execution stays in the bound
// host and resolves scheduled/command event IDs from its own durable context.
func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "config_form":
		return host(ctx, FormCapability, json.RawMessage(`{}`))
	case "stop":
		return json.RawMessage(`null`), nil
	}
	var v plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &v) != nil {
		return nil, errors.New("invalid cleanup invocation")
	}
	switch v.Action {
	case "init", "reload":
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	case "run":
		return host(ctx, ExecuteCapability, json.RawMessage(`{"action":"clean"}`))
	case "event", "service", "once", "command":
		var event struct {
			ID string `json:"event_id"`
		}
		d := json.NewDecoder(bytes.NewReader(v.Input))
		d.DisallowUnknownFields()
		if d.Decode(&event) != nil || d.Decode(new(any)) != io.EOF || event.ID == "" || len(event.ID) > 128 {
			return nil, errors.New("unbound cleanup event")
		}
		b, _ := json.Marshal(event)
		return host(ctx, ExecuteCapability, b)
	default:
		return nil, errors.New("unsupported cleanup action")
	}
}
