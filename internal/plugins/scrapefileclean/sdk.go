package scrapefileclean

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

const ConfigureCapability = "scrape_clean.configure"
const ApplyCapability = "scrape_clean.apply"

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "ScrapeFileClean", Name: "源文件联动清理", Description: "为手动清理下载目录设计：手动删除源文件后，自动联动清理媒体库中对应的硬链接文件、刮削文件（元数据、图片、字幕）与转移记录，支持延迟删除防止误删", Version: "1.0.7", Icon: "clean.png", Author: "xlmc", AuthorURL: "https://github.com/xlmc", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{ConfigureCapability, ApplyCapability}}
}
func Form() json.RawMessage { return append(json.RawMessage(nil), form...) }

// Long-lived file identity state, watches and delayed queue belong to the host.
// Protocol stop closes only this short-lived SDK process, never the source's
// stop_service (which cancels and discards all pending deletion tasks).
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
