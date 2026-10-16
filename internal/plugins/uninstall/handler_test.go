package uninstall

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestHandlerNoopAndScopedEmptyApply(t *testing.T) {
	ctx := context.Background()
	for _, config := range []string{`null`, `{}`, `{"plugin_ids":[]}`, `{"plugin_ids":null}`, `{"clear_config":true}`} {
		raw, _ := json.Marshal(plugins.Invocation{Action: "init", Config: json.RawMessage(config)})
		out, err := Handle(ctx, "invoke", raw, nil)
		if err != nil || string(out) != "null" {
			t.Fatal("empty source selection requested host authority", err)
		}
	}
	for _, action := range []string{"init", "reload"} {
		raw, _ := json.Marshal(plugins.Invocation{Action: action, Config: json.RawMessage(`{"plugin_ids":["WorkerCannotAuthorizeThis"],"clear_config":true}`), Input: json.RawMessage(`{"plugin_id":"Other","force":true}`)})
		calls := 0
		_, err := Handle(ctx, "invoke", raw, func(_ context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
			calls++
			if method != ApplyCapability || string(payload) != "{}" {
				t.Fatal("worker supplied destructive scope or force flag", method, string(payload))
			}
			return nil, errSynthetic
		})
		if calls != 1 || !errors.Is(err, errSynthetic) {
			t.Fatal("apply failed without surfacing host failure", calls, err)
		}
	}
	for _, action := range []string{"event", "page", "manual", "once"} {
		raw, _ := json.Marshal(plugins.Invocation{Action: action, Config: json.RawMessage(`{"plugin_ids":["A"]}`)})
		if _, err := Handle(ctx, "invoke", raw, nil); err == nil {
			t.Fatal("unsupported action accepted", action)
		}
	}
}

func TestSDKNegotiationBoundHostExecutionAndStop(t *testing.T) {
	manifest := Manifest()
	if err := manifest.Validate(); err != nil || State() || manifest.HasPage || len(manifest.Events) != 0 || !reflect.DeepEqual(manifest.Methods, []string{"config_form", "invoke", "stop"}) {
		t.Fatal("invalid or invented source surface", err)
	}
	hostState := newFixtureHost(t, referenceCase{Config: json.RawMessage(`{"plugin_ids":["Alpha"],"clear_config":true,"clear_data":true}`), Installed: []string{"Alpha", "Beta"}, Packages: []string{"Alpha", "Beta"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, worker := net.Pipe()
	t.Cleanup(func() { client.Close(); worker.Close() })
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- plugins.Serve(ctx, worker, worker, manifest, Handle) }()
	encoder, decoder := json.NewEncoder(client), json.NewDecoder(client)
	identity := plugins.Identity{PluginID: manifest.PluginID, InstanceID: manifest.PluginID, Version: manifest.Version}
	send := func(frame plugins.Frame) {
		t.Helper()
		if err := encoder.Encode(frame); err != nil {
			t.Fatal(err)
		}
	}
	receive := func() plugins.Frame {
		t.Helper()
		var frame plugins.Frame
		if err := decoder.Decode(&frame); err != nil {
			t.Fatal(err)
		}
		if frame.Identity != identity || frame.Protocol != plugins.ProtocolVersion || frame.Error != nil {
			t.Fatal("SDK identity/error mismatch", frame)
		}
		return frame
	}
	send(plugins.Frame{Protocol: plugins.ProtocolVersion, ID: "hello", Type: "call", Identity: identity, Method: "handshake", Payload: json.RawMessage(`{"protocol":1,"host_api":1}`)})
	hello := receive()
	var handshake plugins.Handshake
	if hello.Type != "result" || hello.ID != "hello" || json.Unmarshal(hello.Result, &handshake) != nil || !reflect.DeepEqual(handshake.Capabilities, manifest.Capabilities) {
		t.Fatal("SDK handshake did not negotiate compiled capabilities")
	}
	// The worker-supplied Config deliberately differs. Only the host's pinned
	// current config below may choose a destructive target.
	invocation, _ := json.Marshal(plugins.Invocation{Action: "init", Config: json.RawMessage(`{"plugin_ids":["Beta"]}`)})
	send(plugins.Frame{Protocol: plugins.ProtocolVersion, ID: "init", Type: "call", Identity: identity, Method: "invoke", Payload: invocation})
	call := receive()
	if call.Type != "host_call" || call.Method != ApplyCapability || string(call.Payload) != "{}" {
		t.Fatal("SDK call widened its destructive scope", call)
	}
	result, err := Run(ctx, hostState.ownConfig, hostState)
	if err != nil || !reflect.DeepEqual(result.Removed, []string{"Alpha"}) || len(result.ConfigCleared)+len(result.DataCleared) != 0 {
		t.Fatal("bound host did not execute installed source behavior", result, err)
	}
	resultJSON, _ := json.Marshal(result)
	send(plugins.Frame{Protocol: plugins.ProtocolVersion, ID: call.ID, Type: "host_result", Identity: identity, Result: resultJSON})
	finished := receive()
	if finished.Type != "result" || finished.ID != "init" || !reflect.DeepEqual(normalized(t, finished.Result), normalized(t, json.RawMessage(resultJSON))) {
		t.Fatal("SDK did not return the actual host receipt")
	}
	send(plugins.Frame{Protocol: plugins.ProtocolVersion, ID: "stop", Type: "call", Identity: identity, Method: "stop"})
	stopped := receive()
	if stopped.ID != "stop" || stopped.Type != "result" || string(stopped.Result) != "null" {
		t.Fatal("SDK stop changed source no-op")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hostState.installed, []string{"Beta"}) || !hostState.configs["Alpha"] || !hostState.data["Alpha"] {
		t.Fatal("bound execution deleted an unrelated plugin or retained data")
	}
}

func TestConfigFormUsesOnlyReadOnlyInventoryCapability(t *testing.T) {
	for _, input := range loadOracle(t).FormCases {
		if input.Error != "" {
			continue
		}
		t.Run(input.Name, func(t *testing.T) {
			calls := 0
			form, err := Handle(context.Background(), "config_form", nil, func(_ context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
				calls++
				if method != OptionsCapability || string(payload) != "{}" {
					t.Fatal("form requested write authority")
				}
				return json.Marshal(map[string]any{"plugins": input.Local})
			})
			if err != nil || calls != 1 || !reflect.DeepEqual(normalized(t, form), normalized(t, map[string]json.RawMessage{"conf": input.Form, "model": input.Model})) {
				t.Fatal("SDK form did not match source fixture", calls, err)
			}
		})
	}
}
