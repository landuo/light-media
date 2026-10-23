package removelink

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

const ConfigureCapability = "cleanup.configure"
const ApplyCapability = "cleanup.apply"

func Manifest() plugins.Manifest {
	return plugins.Manifest{RuntimeSupport: &plugins.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}}, SchemaVersion: 1, PluginID: "RemoveLink", Name: "清理媒体文件", Description: "媒体文件清理工具：支持硬链接文件清理、STRM文件清理、刮削文件清理（元数据、图片、字幕）、转移记录清理、种子联动删除等功能", Version: "2.17", Icon: "Ombi_A.png", Author: "DzAvril", AuthorURL: "https://github.com/DzAvril", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{ConfigureCapability, ApplyCapability}}
}
func Form() json.RawMessage { return append(json.RawMessage(nil), form...) }

// Long-lived file identity state, watches and delayed queue belong to the host.
// Protocol stop closes only this short-lived SDK process, never the source's
// stop_service (which drains even not-yet-due deletion tasks).
func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "config_form":
		return Form(), nil
	case "stop":
		return json.RawMessage(`null`), nil
	}
	var input plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &input) != nil {
		return nil, errors.New("invalid cleanup invocation")
	}
	switch input.Action {
	case "init", "reload":
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	case "event":
		var event struct {
			ID string `json:"event_id"`
		}
		d := json.NewDecoder(bytes.NewReader(input.Input))
		d.DisallowUnknownFields()
		if d.Decode(&event) != nil || d.Decode(new(any)) != io.EOF || event.ID == "" {
			return nil, errors.New("unbound cleanup event")
		}
		payload, _ := json.Marshal(event)
		return host(ctx, ApplyCapability, payload)
	default:
		return nil, errors.New("unsupported cleanup action")
	}
}
