// A synchronized, ordinary Go SDK worker used only with owned test data.
// It has no downloader, filesystem-deletion or external-network implementation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"light-media/internal/plugins"
)

func main() {
	manifest := plugins.Manifest{SchemaVersion: 1, PluginID: "Owned", Name: "Owned manager lifecycle", Description: "Owned protocol verification", Version: "1.0", Runtime: "go-executable", Entry: "owned-manager-lifecycle-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "get_page", "get_render_mode", "query", "http", "job", "close", "stop_service", "stop"},
		// Final owned eight-method test contract; host dispatch stays closed for
		// pending methods until their coupled implementation is integrated.
		Capabilities: []string{plugins.SharedPluginInit, plugins.SharedPluginRemove, plugins.SharedPluginConfigDelete, plugins.SharedPluginDataDelete, plugins.SharedPluginState, plugins.SharedLocalPlugins, plugins.SharedSchedulerRemovePluginJob, plugins.SharedSchedulerUpdatePluginJob},
		APIs:         []plugins.HTTPAPI{{Key: "query", Path: "/query", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}}}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		if json.NewEncoder(os.Stdout).Encode(manifest) != nil {
			os.Exit(81)
		}
		return
	}
	if len(os.Args) != 1 {
		os.Exit(80)
	}
	var mu sync.Mutex
	state := json.RawMessage(`false`)
	conf := json.RawMessage(`null`)
	services := json.RawMessage(`[]`)
	trace := []json.RawMessage{}
	initError, stateError := false, false
	closeError := false
	serviceError := false
	counter := 0
	appendTrace := func(value any) { raw, _ := json.Marshal(value); trace = append(trace, raw) }
	handler := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		httpCall := method == "http"
		if httpCall {
			var request plugins.HTTPRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			if request.Key != "query" || request.Method != "POST" || request.Path != "/query" {
				return nil, errors.New("unsupported owned manager HTTP route")
			}
			method, raw = "query", request.Body
		}
		switch method {
		case "init":
			mu.Lock()
			conf = append(conf[:0], raw...)
			appendTrace([]any{"init", json.RawMessage(raw)})
			fail := initError
			mu.Unlock()
			if fail {
				return nil, errors.New("owned init failure")
			}
			return json.RawMessage(`null`), nil
		case "get_state":
			mu.Lock()
			appendTrace([]any{"get_state"})
			value, fail := append(json.RawMessage(nil), state...), stateError
			mu.Unlock()
			if fail {
				return nil, errors.New("owned state failure")
			}
			return value, nil
		case "get_service":
			mu.Lock()
			appendTrace([]any{method})
			value, fail := append(json.RawMessage(nil), services...), serviceError
			mu.Unlock()
			if fail {
				return nil, errors.New("owned service getter failure")
			}
			return value, nil
		case "get_command", "get_page":
			mu.Lock()
			appendTrace([]any{method})
			mu.Unlock()
			return json.RawMessage(`[]`), nil
		case "get_api":
			mu.Lock()
			appendTrace([]any{method})
			mu.Unlock()
			return json.Marshal(manifest.APIs)
		case "get_form":
			return json.RawMessage(`[[],{}]`), nil
		case "get_render_mode":
			return json.RawMessage(`["vuetify",""]`), nil
		case "close":
			mu.Lock()
			counter += 100
			appendTrace([]any{"close"})
			fail := closeError
			mu.Unlock()
			if fail {
				return nil, errors.New("owned close failure")
			}
			return json.RawMessage(`null`), nil
		case "job":
			mu.Lock()
			counter++
			appendTrace([]any{"job"})
			mu.Unlock()
			return json.RawMessage(`null`), nil
		case "stop_service", "stop":
			mu.Lock()
			appendTrace([]any{method})
			mu.Unlock()
			return json.RawMessage(`null`), nil
		case "query":
			var input struct {
				Op           string          `json:"op"`
				PluginID     string          `json:"plugin_id"`
				Conf         json.RawMessage `json:"conf"`
				State        json.RawMessage `json:"state"`
				InitError    bool            `json:"init_error"`
				StateError   bool            `json:"state_error"`
				CloseError   bool            `json:"close_error"`
				ServiceError bool            `json:"service_error"`
				Services     json.RawMessage `json:"services"`
				JobID        json.RawMessage `json:"job_id"`
				Force        bool            `json:"force"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			var value json.RawMessage
			var err error
			switch input.Op {
			case "configure":
				mu.Lock()
				if len(input.State) != 0 {
					state = append(state[:0], input.State...)
				}
				if len(input.Services) != 0 {
					services = append(services[:0], input.Services...)
				}
				serviceError = input.ServiceError
				initError, stateError, closeError, trace = input.InitError, input.StateError, input.CloseError, []json.RawMessage{}
				mu.Unlock()
				value = json.RawMessage(`null`)
			case "inspect":
				mu.Lock()
				value, err = json.Marshal(struct {
					Conf    json.RawMessage   `json:"conf"`
					Trace   []json.RawMessage `json:"trace"`
					Counter int               `json:"counter"`
				}{conf, trace, counter})
				mu.Unlock()
			case "init":
				err = (plugins.SharedPluginManager{Host: host}).InitPlugin(ctx, input.PluginID, input.Conf)
				value = json.RawMessage(`null`)
			case "state":
				value, err = (plugins.SharedPluginManager{Host: host}).GetPluginState(ctx, input.PluginID)
			case "remove":
				err = (plugins.SharedPluginManager{Host: host}).RemovePlugin(ctx, input.PluginID)
				value = json.RawMessage(`null`)
			case "scheduler_update":
				err = (plugins.SharedPluginScheduler{Host: host}).UpdatePluginJob(ctx, input.PluginID)
				value = json.RawMessage(`null`)
			case "scheduler_remove":
				err = (plugins.SharedPluginScheduler{Host: host}).RemovePluginJob(ctx, plugins.PluginSchedulerRemoveRequest{PluginID: input.PluginID, JobID: input.JobID})
				value = json.RawMessage(`null`)
			case "delete_config", "delete_data":
				var result bool
				manager := plugins.SharedPluginManager{Host: host}
				request := plugins.PluginManagerDeleteRequest{PluginID: input.PluginID, Force: input.Force}
				if input.Op == "delete_config" {
					result, err = manager.DeletePluginConfig(ctx, request)
				} else {
					result, err = manager.DeletePluginData(ctx, request)
				}
				if err == nil {
					value, _ = json.Marshal(result)
				}
			case "local":
				var rows []json.RawMessage
				rows, err = (plugins.SharedPluginManager{Host: host}).GetLocalPlugins(ctx)
				if err == nil {
					value, err = json.Marshal(rows)
				}
			default:
				return nil, errors.New("unsupported owned manager operation")
			}
			message := ""
			if err != nil {
				message = err.Error()
				value = json.RawMessage(`null`)
			}
			result, err := json.Marshal(struct {
				Value json.RawMessage `json:"value"`
				Error string          `json:"error"`
				PID   int             `json:"pid"`
			}{value, message, os.Getpid()})
			if err != nil || !httpCall {
				return result, err
			}
			return plugins.JSONHTTPResponse(200, json.RawMessage(result))
		}
		return nil, errors.New("unsupported owned manager method")
	}
	// Actual handshake opts in; merely enabling the host runner is not enough.
	if plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, manifest, handler) != nil {
		fmt.Fprintln(os.Stderr, "owned manager SDK stopped with an error")
		os.Exit(82)
	}
}
