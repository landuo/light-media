package autosignin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"light-media/internal/plugins"
)

const (
	Version             = "2.9.14"
	OptionsCapability   = "site.signin.options"
	ConfigureCapability = "site.signin.configure"
	RunCapability       = "site.signin.run"
	SnapshotCapability  = "site.signin.snapshot"
	DeleteCapability    = "site.signin.deleted"
)

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "AutoSignIn", Name: "站点自动签到", Description: "自动模拟登录、签到站点。", Icon: "signin.png", Author: "thsrite", AuthorURL: "https://github.com/thsrite", Version: Version, Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "get_page", "page", "http", "stop"}, Capabilities: []string{OptionsCapability, ConfigureCapability, RunCapability, SnapshotCapability, DeleteCapability}, Events: []string{"plugin.action", "site.deleted"}, HasPage: true, APIs: []plugins.HTTPAPI{{Key: "signin_by_domain", Path: "/signin_by_domain", Methods: []string{"GET"}, Auth: "anonymous", Query: []plugins.HTTPParameter{{Name: "url", Kind: "string", Required: true}, {Name: "apikey", Kind: "api_key", Required: true}}}}}
}
func sdkDecode(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing SDK value")
	}
	return nil
}
func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	// Keep legacy SDK aliases while exposing the canonical extension methods.
	if method == "get_page" {
		method = "page"
	}
	if method == "stop" {
		return json.RawMessage(`null`), nil
	}
	if host == nil {
		return nil, errors.New("missing signin host")
	}
	if method == "config_form" {
		data, err := host(ctx, OptionsCapability, json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		var inventory Inventory
		if sdkDecode(data, &inventory) != nil {
			return nil, errors.New("invalid signin inventory")
		}
		form, err := Form(inventory)
		if err != nil {
			return nil, err
		}
		return json.Marshal(form)
	}
	capability := ""
	switch method {
	case "page":
		capability = SnapshotCapability
	case "http":
		capability = RunCapability
	case "invoke":
		var call plugins.Invocation
		if sdkDecode(raw, &call) != nil {
			return nil, errors.New("invalid signin invocation")
		}
		switch call.Action {
		case "init", "reload":
			capability = ConfigureCapability
		case "service", "event":
			capability = RunCapability
		case "site_deleted":
			capability = DeleteCapability
		default:
			return nil, errors.New("invalid signin action")
		}
	default:
		return nil, errors.New("invalid signin method")
	}
	// The empty reverse call cannot select sites, config, event user/channel,
	// HTTP query/key, source action, URLs, or target paths. Host binds these from
	// its validated incoming run, not from worker Invocation.Config/Input.
	return host(ctx, capability, json.RawMessage(`{}`))
}

func Commands() []any {
	return []any{Object{"cmd": "/site_signin", "event": "EventType.PluginAction", "desc": "站点签到", "category": "站点", "data": Object{"action": "site_signin"}}}
}
func publicInventory(inventory Inventory) Inventory {
	clean := func(rows []Object) []Object {
		out := []Object{}
		for _, row := range rows {
			item := Object{}
			for _, key := range []string{"id", "name", "public"} {
				if value, ok := row[key]; ok {
					item[key] = clone(value)
				}
			}
			out = append(out, item)
		}
		return out
	}
	return Inventory{Configured: clean(inventory.Configured), Indexers: clean(inventory.Indexers), Custom: clean(inventory.Custom), CustomEnabled: clone(inventory.CustomEnabled)}
}
