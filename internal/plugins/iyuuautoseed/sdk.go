package iyuuautoseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"light-media/internal/plugins"
)

const ConfigureCapability = "iyuu.configure"
const ExecuteCapability = "iyuu.execute"
const FormCapability = "iyuu.form"

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "IYUUAutoSeed", Name: "IYUU自动辅种", Description: "基于IYUU官方Api实现自动辅种。", Icon: "IYUU.png", Version: "2.19", Author: "jxxghp,CKun", AuthorURL: "https://github.com/jxxghp", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{ConfigureCapability, ExecuteCapability, FormCapability}, Events: []string{"site.deleted"}}
}

// Handle is the actual versioned process SDK adapter. The worker cannot choose
// a downloader, site, directory, torrent, URL, credential, clock or schedule;
// native deferred execution resolves its immutable run/occurrence/event record.
func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "config_form":
		return host(ctx, FormCapability, json.RawMessage(`{}`))
	case "stop":
		return json.RawMessage(`null`), nil
	}
	var inv plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &inv) != nil {
		return nil, errors.New("invalid IYUU invocation")
	}
	switch inv.Action {
	case "init", "reload":
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	case "run":
		return host(ctx, ExecuteCapability, json.RawMessage(`{"action":"auto_seed"}`))
	case "service", "once":
		var input struct {
			ID string `json:"occurrence_id"`
		}
		if sdkStrict(inv.Input, &input) != nil || input.ID == "" || len(input.ID) > 128 {
			return nil, errors.New("unbound IYUU schedule occurrence")
		}
		b, _ := json.Marshal(input)
		return host(ctx, ExecuteCapability, b)
	case "event":
		var input struct {
			ID string `json:"batch_id"`
		}
		if sdkStrict(inv.Input, &input) != nil || input.ID == "" || len(input.ID) > 128 {
			return nil, errors.New("unbound IYUU site event")
		}
		b, _ := json.Marshal(input)
		return host(ctx, ExecuteCapability, b)
	default:
		return nil, errors.New("unsupported IYUU action")
	}
}
func sdkStrict(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing SDK input")
	}
	return nil
}
