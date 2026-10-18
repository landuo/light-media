package torrenttransfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins"
)

const ConfigureCapability = "torrent_transfer.configure"
const ExecuteCapability = "torrent_transfer.execute"
const FormCapability = "torrent_transfer.form"

func Manifest(id string) plugins.Manifest {
	identity, ok := Lookup(id)
	if !ok {
		return plugins.Manifest{}
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: identity.ID, Name: identity.Name, Description: identity.Description, Icon: "seed.png", Version: "1.10.3", Author: "jxxghp", AuthorURL: "https://github.com/jxxghp", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{ConfigureCapability, ExecuteCapability, FormCapability}}
}
func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "config_form":
		return host(ctx, FormCapability, json.RawMessage(`{}`))
	case "stop":
		return json.RawMessage(`null`), nil
	}
	var inv plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &inv) != nil {
		return nil, errors.New("invalid transfer invocation")
	}
	switch inv.Action {
	case "init", "reload":
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	case "run":
		return host(ctx, ExecuteCapability, json.RawMessage(`{"action":"transfer"}`))
	case "service", "once":
		var input struct {
			ID string `json:"occurrence_id"`
		}
		if sdkStrict(inv.Input, &input) != nil || input.ID == "" || len(input.ID) > 128 {
			return nil, errors.New("unbound transfer schedule occurrence")
		}
		b, _ := json.Marshal(input)
		return host(ctx, ExecuteCapability, b)
	default:
		return nil, errors.New("unsupported transfer action")
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
