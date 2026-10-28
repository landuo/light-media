// Synthetic local SDK worker exercising the actual host input-interaction API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"

	"light-media/internal/plugins"
)

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedInput", Name: "Owned input interaction", Version: "1.0", Runtime: "go-executable", Entry: "owned-input-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:           []string{"init", "get_state", "get_service", "get_command", "get_api", "http", "event", "event_first", "event_second", "event_dynamic", "chain_fail", "sdk_chain", "run_service", "close", "stop_service", "stop"},
		Capabilities:      append(plugins.BaseMethods(), plugins.InputCreateCapability, plugins.InputGetCapability, plugins.InputPopCapability, plugins.InputRemoveCapability, plugins.ServiceProgressCapability, plugins.EventSendCapability, plugins.EventCheckCapability, plugins.EventManageCapability),
		Events:            []string{"message.action", "plugin.action", "system.error", "owned.sdk.broadcast"},
		BroadcastHandlers: []plugins.BroadcastHandler{{Event: "owned.sdk.multiple", Method: "event_first"}, {Event: "owned.sdk.multiple", Method: "event_second"}},
		ChainHandlers:     []plugins.ChainHandler{{Event: "owned.chain.fail", Method: "chain_fail"}, {Event: "owned.sdk.chain", Method: "sdk_chain"}},
		APIs:              []plugins.HTTPAPI{{Key: "input", Path: "/input", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}, {Key: "events", Path: "/events", Methods: []string{"GET"}, Auth: "bear"}}}
}

type object struct {
	mu       sync.Mutex
	retained plugins.HostCaller
	events   []json.RawMessage
}

func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	base := plugins.Base{Host: host}
	switch method {
	case "init":
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var options struct {
			Controls []plugins.EventControl `json:"event_controls"`
		}
		if err = json.Unmarshal(config, &options); err != nil {
			return nil, err
		}
		for _, control := range options.Controls {
			if _, err = plugins.ManageEvent(ctx, host, control); err != nil {
				return nil, err
			}
		}
		return json.RawMessage(`null`), nil
	case "close", "stop_service", "stop":
		return json.RawMessage(`null`), nil
	case "get_state":
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var options struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.Unmarshal(config, &options)
		return json.Marshal(options.Enabled)
	case "get_service":
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var options struct {
			Services json.RawMessage `json:"services"`
		}
		_ = json.Unmarshal(config, &options)
		if len(options.Services) > 0 {
			return options.Services, nil
		}
		return json.RawMessage(`[]`), nil
	case "run_service":
		var input struct {
			Mode     string  `json:"mode"`
			Gate     string  `json:"gate"`
			Progress float64 `json:"progress"`
			Text     string  `json:"text"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		if err := plugins.UpdateServiceProgress(ctx, host, plugins.ServiceProgressUpdate{Value: &input.Progress, Text: &input.Text, Data: map[string]json.RawMessage{"owned": json.RawMessage(`9007199254740993`), "actual_input": append(json.RawMessage(nil), raw...)}}); err != nil {
			return nil, err
		}
		if input.Gate != "" {
			req, err := http.NewRequestWithContext(ctx, "GET", input.Gate, nil)
			if err != nil {
				return nil, err
			}
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				return nil, err
			}
			_ = response.Body.Close()
		}
		switch input.Mode {
		case "tuple-fail":
			return plugins.ServiceResultTuple(false, "owned service failure")
		case "tuple-empty":
			return plugins.ServiceResultTuple(false)
		case "list-fail":
			return json.RawMessage(`[false,"ordinary list"]`), nil
		case "error":
			return nil, errors.New("owned callback error")
		}
		return plugins.ServiceResultTuple(true)
	case "get_command":
		return json.RawMessage(`[{"cmd":"/owned","desc":"Owned","event":"plugin.action","data":{"action":"command"}},{"cmd":"/fail","desc":"Owned failure","event":"plugin.action","data":{"action":"fail"}}]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "event", "event_first", "event_second", "event_dynamic":
		if method != "event" {
			// A fresh Base call proves reload resolved this worker's own host scope.
			config, err := base.GetConfig(ctx)
			if err != nil {
				return nil, err
			}
			var event map[string]json.RawMessage
			if err = json.Unmarshal(raw, &event); err != nil {
				return nil, err
			}
			event["handler"], _ = json.Marshal(method)
			event["config"] = config
			raw, err = json.Marshal(event)
			if err != nil {
				return nil, err
			}
		}
		o.mu.Lock()
		defer o.mu.Unlock()
		o.events = append(o.events, append(json.RawMessage(nil), raw...))
		var event struct {
			Type string `json:"type"`
			Data struct {
				Action string `json:"action"`
			} `json:"data"`
		}
		_ = json.Unmarshal(raw, &event)
		if event.Type == "plugin.action" && event.Data.Action == "fail" {
			return nil, errors.New("OWNED_PRIVATE_EXCEPTION_TEXT")
		}
		return json.RawMessage(`null`), nil
	case "chain_fail":
		return plugins.HandleMutableEvent(raw, func(event *plugins.MutableEvent) (json.RawMessage, error) {
			event.Data = json.RawMessage(`{"mutated":true}`)
			return nil, errors.New("OWNED_PRIVATE_CHAIN_EXCEPTION")
		})
	case "sdk_chain":
		config, err := base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var options struct {
			Label string `json:"label"`
			Fail  bool   `json:"chain_fail"`
		}
		_ = json.Unmarshal(config, &options)
		return plugins.HandleMutableEvent(raw, func(event *plugins.MutableEvent) (json.RawMessage, error) {
			var data map[string]json.RawMessage
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return nil, err
			}
			var visits []string
			_ = json.Unmarshal(data["visits"], &visits)
			visits = append(visits, options.Label)
			data["visits"], _ = json.Marshal(visits)
			event.Data, _ = json.Marshal(data)
			if options.Fail {
				return nil, errors.New("OWNED_PRIVATE_CHAIN_ERROR")
			}
			return json.RawMessage(`{"ordinary_return":"ignored"}`), nil
		})
	case "http":
		var request plugins.HTTPRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Key == "events" {
			o.mu.Lock()
			defer o.mu.Unlock()
			body, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "events": o.events})
			return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body})
		}
		var envelope struct {
			Operation string          `json:"operation"`
			Input     json.RawMessage `json:"input"`
			Stale     bool            `json:"stale"`
		}
		if err := json.Unmarshal(request.Body, &envelope); err != nil {
			return nil, err
		}
		caller := host
		o.mu.Lock()
		if envelope.Stale {
			caller = o.retained
		}
		o.retained = host
		o.mu.Unlock()
		if caller == nil {
			return nil, errors.New("missing caller")
		}
		capability := "input." + envelope.Operation
		if envelope.Operation == "progress" {
			capability = plugins.ServiceProgressCapability
		}
		var result json.RawMessage
		var err error
		if envelope.Operation == "event.send" {
			var input plugins.EventSendRequest
			if err = json.Unmarshal(envelope.Input, &input); err == nil {
				var event *plugins.MutableEvent
				event, err = plugins.SendEvent(ctx, caller, input)
				if err == nil {
					result, err = json.Marshal(event)
				}
			}
		} else if envelope.Operation == "event.check" {
			var input plugins.EventQuery
			if err = json.Unmarshal(envelope.Input, &input); err == nil {
				var present bool
				present, err = plugins.CheckEvent(ctx, caller, input)
				if err == nil {
					result, err = json.Marshal(present)
				}
			}
		} else if envelope.Operation == "event.manage" {
			var input plugins.EventControl
			if err = json.Unmarshal(envelope.Input, &input); err == nil {
				var handlers []plugins.EventHandlerInfo
				handlers, err = plugins.ManageEvent(ctx, caller, input)
				if err == nil {
					result, err = json.Marshal(handlers)
				}
			}
		} else {
			result, err = caller(ctx, capability, envelope.Input)
		}
		status := 200
		if err != nil {
			status = 403
			result = json.RawMessage(`{"denied":true}`)
		}
		return json.Marshal(plugins.HTTPResponse{Status: status, Body: result})
	}
	return nil, errors.New("unsupported owned method")
}
func main() {
	m := manifest()
	if err := m.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	if err := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, (&object{}).handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
