package mergemessagenotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins"
)

const ConfigureCapability = "merge_message.configure"
const EventCapability = "merge_message.event"
const FormCapability = "merge_message.form"

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "MergeMessageNotify", Name: "聚合消息通知", Description: "消息通知，一个插件就够了。", Icon: "https://raw.githubusercontent.com/hotlcc/MoviePilot-Plugins-Third/main/icons/MergeMessageNotify_121.png", Version: "1.20.1", Author: "hotlcc", AuthorURL: "https://github.com/hotlcc", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{ConfigureCapability, EventCapability, FormCapability}, Events: []string{"notice.message"}}
}

// The worker never supplies channel config, URLs, credentials, notice content or
// a save-request origin. Host owners resolve those from frozen granted context.
func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "config_form":
		return host(ctx, FormCapability, json.RawMessage(`{}`))
	case "stop":
		// Runner sends worker stop after every invocation. It is not an owner
		// retirement and must not clear the long-lived host's registrations.
		return json.RawMessage(`null`), nil
	}
	var v plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &v) != nil {
		return nil, errors.New("invalid merge-message invocation")
	}
	switch v.Action {
	case "init", "reload":
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	case "event":
		var event struct {
			ID string `json:"event_id"`
		}
		d := json.NewDecoder(bytes.NewReader(v.Input))
		d.DisallowUnknownFields()
		if d.Decode(&event) != nil || d.Decode(new(any)) != io.EOF || event.ID == "" || len(event.ID) > 128 {
			return nil, errors.New("unbound notice event")
		}
		b, _ := json.Marshal(event)
		return host(ctx, EventCapability, b)
	default:
		return nil, errors.New("unsupported merge-message action")
	}
}
