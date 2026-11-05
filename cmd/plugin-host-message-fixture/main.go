// OwnedHostMessage exercises ordinary incoming messages through the standard
// worker protocol. It is an acceptance fixture, never a business plugin.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"light-media/internal/plugins"
)

var fixtureVersion = "1.0"
var traceMu sync.Mutex

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedHostMessage", Name: "普通入站消息验收", Version: fixtureVersion, Runtime: "go-executable", Entry: "plugin-host-message-fixture", HostAPIMin: 1, HostAPIMax: 1, HasPage: true,
		Methods:      []string{"init", "stop", "get_state", "get_service", "get_module", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "receive", "parse", "processing_begin", "processing_end", "post", "event", "pending", "http"},
		Capabilities: append(plugins.BaseMethods(), plugins.MessageReceiveCapability, plugins.InputCreateCapability),
		Events:       []string{"user.message", "command.excute", "message.action", "owned.inbound.command"},
		APIs:         []plugins.HTTPAPI{{Key: "run", Path: "/run", Methods: []string{"POST"}, Auth: "bear", Body: "json"}, {Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}}}
}

func trace(ctx context.Context, host plugins.HostCaller, item any) error {
	traceMu.Lock()
	defer traceMu.Unlock()
	base := plugins.Base{Host: host}
	key := "trace"
	raw, err := base.GetData(ctx, &key)
	if err != nil {
		return err
	}
	rows := []json.RawMessage{}
	_ = json.Unmarshal(raw, &rows)
	encoded, err := json.Marshal(item)
	if err != nil {
		return err
	}
	rows = append(rows, encoded)
	return base.SaveData(ctx, key, rows)
}

func handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	base := plugins.Base{Host: host}
	switch method {
	case "stop":
		return json.RawMessage(`null`), nil
	case "init":
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var values map[string]json.RawMessage
		_ = json.Unmarshal(config, &values)
		if string(values["run_once"]) == "true" {
			values["run_once"] = json.RawMessage(`false`)
			if _, err = base.UpdateConfig(ctx, values); err != nil {
				return nil, err
			}
			return configuredReceive(ctx, host, config)
		}
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.RawMessage(`true`), nil
	case "get_service":
		return json.RawMessage(`[]`), nil
	case "get_module":
		return json.RawMessage(`{"message_parser":"parse","mark_message_processing_started":"processing_begin","mark_message_processing_finished":"processing_end","post_message":"post"}`), nil
	case "get_command":
		return json.RawMessage(`[{"cmd":"/owned_inbound","event":"owned.inbound.command","desc":"普通入站验收","data":{"action":"owned"}}]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",""]`), nil
	case "get_form":
		return json.RawMessage(`[[{"component":"VTextField","props":{"model":"source","label":"验收来源"}},{"component":"VTextarea","props":{"model":"body","label":"独占测试消息JSON"}},{"component":"VSwitch","props":{"model":"run_once","label":"保存后接收一次"}}],{"source":"Owned","body":"{\"info\":{\"channel\":\"飞书\",\"source\":\"Owned\",\"userid\":\"owned-user\",\"text\":\"ordinary message\"}}","run_once":false}]`), nil
	case "receive":
		var input plugins.ReceiveMessageRequestV1
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		result, err := (plugins.Messages{Host: host}).Receive(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "pending":
		var input plugins.InputInteractionRequest
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&input); err != nil {
			return nil, err
		}
		returnValue, err := plugins.CreateInputInteraction(ctx, host, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(returnValue)
	case "parse":
		input, err := plugins.DecodeMessageParserInput(raw)
		if err != nil {
			return nil, err
		}
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var settings struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(config, &settings)
		if settings.Source == "" {
			settings.Source = "Owned"
		}
		if input.Source == nil || *input.Source != settings.Source {
			return json.RawMessage(`null`), nil
		}
		digest := sha256.Sum256(input.Body)
		if err = trace(ctx, host, map[string]any{"kind": "parse", "source": input.Source, "body_sha256": hex.EncodeToString(digest[:]), "body_size": len(input.Body), "form": input.Form, "args": input.Args}); err != nil {
			return nil, err
		}
		var payload struct {
			Info json.RawMessage `json:"info"`
			Mode string          `json:"mode"`
		}
		if json.Unmarshal(input.Body, &payload) != nil {
			return json.RawMessage(`null`), nil
		}
		if payload.Mode == "fail" {
			return nil, errors.New("owned parser failure")
		}
		if payload.Mode == "block" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if payload.Mode == "null" || len(payload.Info) == 0 {
			return json.RawMessage(`null`), nil
		}
		return payload.Info, nil
	case "processing_begin", "processing_end":
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var settings struct {
			Source              string          `json:"source"`
			PendingOnProcessing bool            `json:"pending_on_processing"`
			ProcessingStatus    json.RawMessage `json:"processing_status"`
		}
		_ = json.Unmarshal(config, &settings)
		var call struct {
			Kwargs struct {
				Source *string `json:"source"`
			} `json:"kwargs"`
		}
		if json.Unmarshal(raw, &call) != nil {
			return nil, errors.New("invalid processing fixture call")
		}
		if settings.Source != "" && (call.Kwargs.Source == nil || *call.Kwargs.Source != settings.Source) {
			return json.RawMessage(`null`), nil
		}
		if err := trace(ctx, host, map[string]any{"kind": method, "call": raw}); err != nil {
			return nil, err
		}
		if method == "processing_begin" {
			if settings.PendingOnProcessing {
				var fields struct {
					Kwargs struct {
						User    any     `json:"userid"`
						Channel *string `json:"channel"`
						Source  *string `json:"source"`
						Chat    any     `json:"chat_id"`
					} `json:"kwargs"`
				}
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.UseNumber()
				if err := decoder.Decode(&fields); err != nil {
					return nil, err
				}
				if _, err := plugins.CreateInputInteraction(ctx, host, plugins.InputInteractionRequest{UserID: fields.Kwargs.User, Channel: fields.Kwargs.Channel, Source: fields.Kwargs.Source, ChatID: fields.Kwargs.Chat}); err != nil {
					return nil, err
				}
			}
			if len(settings.ProcessingStatus) > 0 {
				return settings.ProcessingStatus, nil
			}
			return json.RawMessage(`{"metadata":{"owned":true}}`), nil

		}
		return json.RawMessage(`null`), nil
	case "post":
		return json.RawMessage(`null`), trace(ctx, host, map[string]any{"kind": "post", "call": raw})
	case "event":
		return json.RawMessage(`null`), trace(ctx, host, map[string]any{"kind": "event", "value": raw})
	case "get_page":
		key := "trace"
		data, err := base.GetData(ctx, &key)
		if err != nil {
			return nil, err
		}
		identity, err := base.GetInstanceMetadata(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal([]any{map[string]any{"component": "VAlert", "props": map[string]any{"text": "轨迹：" + string(data)}}, map[string]any{"component": "VBtn", "text": "接收一次", "events": map[string]any{"click": map[string]any{"api": "plugin/" + identity.InstanceID + "/run", "method": "POST", "params": map[string]any{}}}}})
	case "http":
		var call plugins.HTTPRequest
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		if call.Key == "snapshot" {
			key := "trace"
			data, err := base.GetData(ctx, &key)
			if err != nil {
				return nil, err
			}
			return response(200, map[string]any{"pid": os.Getpid(), "version": fixtureVersion, "config": config, "trace": data})
		}
		if call.Key == "run" {
			result, err := configuredReceive(ctx, host, config)
			if err != nil {
				return nil, err
			}
			return response(200, map[string]any{"success": true, "result": result})
		}
	}
	return nil, errors.New("unknown owned message fixture method")
}

func configuredReceive(ctx context.Context, host plugins.HostCaller, config json.RawMessage) (json.RawMessage, error) {
	var values struct {
		Source string `json:"source"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(config, &values); err != nil {
		return nil, err
	}
	if values.Source == "" {
		values.Source = "Owned"
	}
	result, err := (plugins.Messages{Host: host}).Receive(ctx, plugins.ReceiveMessageRequestV1{Source: &values.Source, Body: []byte(values.Body)})
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func response(status int, value any) (json.RawMessage, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(plugins.HTTPResponse{Status: status, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
}

func main() {
	m := manifest()
	if err := m.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	if err := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, handle); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
