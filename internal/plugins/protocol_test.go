package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func fixtureManifest() Manifest {
	return Manifest{SchemaVersion: 1, PluginID: "Fixture", Name: "Fixture", Version: "1.2.3", Runtime: "go-executable", Entry: "lm-plugin", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"invoke", "stop"}, Capabilities: []string{"data.read"}}
}

func fixtureCommand(t *testing.T, scenario string) Command {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Path: path, Args: []string{"-test.run=^TestWorkerFixture$", "--", scenario}}
}

func codeOf(err error) string {
	var public *Error
	if errors.As(err, &public) {
		return public.Code
	}
	return ""
}

func TestRunnerSDKLifecycleAndHostIdentity(t *testing.T) {
	secret := "synthetic-secret-not-in-errors"
	t.Setenv("PROVIDER_SECRET", secret)
	calls := 0
	result, err := (Runner{Timeout: 5 * time.Second}).Run(context.Background(), fixtureCommand(t, "sdk"), fixtureManifest(), "Fixture.clone", "invoke", json.RawMessage(`{"value":"synthetic-input"}`), func(ctx context.Context, identity Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
		calls++
		if identity != (Identity{PluginID: "Fixture", InstanceID: "Fixture.clone", Version: "1.2.3"}) || method != "data.read" || string(payload) != `{"key":"history"}` {
			t.Errorf("host invocation identity/scope changed: %+v %s %s", identity, method, payload)
		}
		return json.RawMessage(`{"value":"from-host"}`), nil
	})
	if err != nil || string(result.Result) != `{"host":"from-host","inherited_secret":false}` || result.HostCalls != 1 || calls != 1 || result.StderrBytes != 0 {
		t.Fatalf("real child SDK lifecycle failed: %+v %v calls=%d", result, err, calls)
	}
}

func TestRunnerRejectsWorkerFaultsAndBoundsResources(t *testing.T) {
	for _, test := range []struct{ scenario, code string }{
		{"wrong_identity", "identity_mismatch"},
		{"wrong_version", "handshake_mismatch"},
		{"wrong_api", "handshake_mismatch"},
		{"wrong_capabilities", "handshake_mismatch"},
		{"host_before_handshake", "invalid_host_call"},
		{"cross_instance_host", "identity_mismatch"},
		{"unknown_rpc_field", "invalid_frame"},
		{"invalid_json", "invalid_frame"},
		{"oversized", "output_limit"},
		{"many_host_calls", "host_call_limit"},
		{"crash", "worker_io_failed"},
		{"stop_failed", "plugin_operation_failed"},
		{"stderr_flood", "stderr_limit"},
	} {
		t.Run(test.scenario, func(t *testing.T) {
			calls := 0
			result, err := (Runner{MaxFrameBytes: 4096, MaxOutputBytes: 8192, MaxStderrBytes: 512, MaxHostCalls: 2, Timeout: 5 * time.Second}).Run(context.Background(), fixtureCommand(t, test.scenario), fixtureManifest(), "Fixture", "invoke", json.RawMessage(`{"password":"synthetic-invocation-secret"}`), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				calls++
				return json.RawMessage(`null`), nil
			})
			if codeOf(err) != test.code || len(result.Result) != 0 || (test.scenario != "many_host_calls" && calls != 0) {
				t.Fatalf("fault escaped isolation: result=%+v err=%v host_calls=%d", result, err, calls)
			}
			if err != nil && (strings.Contains(err.Error(), "synthetic") || strings.Contains(err.Error(), "password")) {
				t.Fatal("worker controlled input escaped in error")
			}
		})
	}
}

func TestRunnerCancellationKillsBlockedChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Runner{}).Run(ctx, fixtureCommand(t, "blocked"), fixtureManifest(), "Fixture", "invoke", nil, nil); codeOf(err) != "worker_start_failed" {
		t.Fatal("canceled child was started", err)
	}
	start := time.Now()
	_, err := (Runner{Timeout: 200 * time.Millisecond}).Run(context.Background(), fixtureCommand(t, "blocked"), fixtureManifest(), "Fixture", "invoke", nil, nil)
	if codeOf(err) != "execution_canceled" || time.Since(start) > 3*time.Second {
		t.Fatal("blocked child was not bounded", err, time.Since(start))
	}
}

func TestRunnerHostErrorsAreSanitized(t *testing.T) {
	_, err := (Runner{Timeout: 5 * time.Second}).Run(context.Background(), fixtureCommand(t, "sdk"), fixtureManifest(), "Fixture", "invoke", json.RawMessage(`{}`), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("synthetic-private-provider-password")
	})
	if codeOf(err) != "plugin_operation_failed" || strings.Contains(err.Error(), "private") {
		t.Fatal("host error was exposed or accepted", err)
	}
}

func TestManifestRejectsUnsafeOrIncompatibleEntries(t *testing.T) {
	for _, path := range []string{"/tmp/a", "../a", "a/../b", "a\\b", ".", "..", "a\n"} {
		manifest := fixtureManifest()
		manifest.Entry = path
		if manifest.Validate() == nil {
			t.Errorf("unsafe entry accepted: %q", path)
		}
	}
	manifest := fixtureManifest()
	manifest.HostAPIMin, manifest.HostAPIMax = 2, 2
	if codeOf(manifest.Validate()) != "unsupported_api" {
		t.Fatal("incompatible package accepted")
	}
	manifest = fixtureManifest()
	manifest.Methods = []string{"invoke", "invoke", "stop"}
	if manifest.Validate() == nil {
		t.Fatal("duplicate methods accepted")
	}
}

func TestManifestConfigDefaultsContract(t *testing.T) {
	for _, declaration := range []string{"", ConfigDefaultsPure, ConfigDefaultsTransactional} {
		manifest := fixtureManifest()
		manifest.ConfigDefaults = declaration
		if err := manifest.Validate(); err != nil {
			t.Fatalf("valid defaults declaration %q rejected: %v", declaration, err)
		}
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(`"config_defaults"`)) != (declaration != "") {
			t.Fatal("optional defaults declaration serialization changed", string(raw))
		}
		var restored Manifest
		if json.Unmarshal(raw, &restored) != nil || restored.ConfigDefaults != declaration {
			t.Fatal("defaults declaration did not round trip")
		}
	}
	for _, declaration := range []string{"pure", "pure/v2", "native/v1", " pure/v1", "PURE/V1", "transactional", "transactional/v2"} {
		manifest := fixtureManifest()
		manifest.ConfigDefaults = declaration
		if codeOf(manifest.Validate()) != "invalid_manifest" {
			t.Fatalf("unknown defaults declaration accepted: %q", declaration)
		}
	}
}

func TestManifestConfigFormProjectionContract(t *testing.T) {
	for _, declaration := range []string{"", ConfigFormProjectionTransactional} {
		for _, defaults := range []string{"", ConfigDefaultsPure, ConfigDefaultsTransactional} {
			manifest := fixtureManifest()
			manifest.ConfigFormProjection, manifest.ConfigDefaults = declaration, defaults
			if err := manifest.Validate(); err != nil {
				t.Fatalf("independent form/defaults declarations rejected: %q, %q: %v", declaration, defaults, err)
			}
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(`"config_form_projection"`)) != (declaration != "") {
				t.Fatal("optional form declaration serialization changed", string(raw))
			}
			var restored Manifest
			if json.Unmarshal(raw, &restored) != nil || restored.ConfigFormProjection != declaration || restored.ConfigDefaults != defaults {
				t.Fatal("independent declarations did not round trip")
			}
		}
	}
	for _, declaration := range []string{"pure/v1", "transactional", "transactional/v2", " transactional/v1", "TRANSACTIONAL/V1", "transactional/v1 "} {
		manifest := fixtureManifest()
		manifest.ConfigFormProjection = declaration
		if codeOf(manifest.Validate()) != "invalid_manifest" {
			t.Fatalf("unknown form projection declaration accepted: %q", declaration)
		}
	}
}

func TestWireIsJSONRPCAndRejectsAmbiguousMessages(t *testing.T) {
	identity := Identity{PluginID: "Fixture", InstanceID: "Fixture", Version: "1.2.3"}
	frames := []Frame{
		{Protocol: 1, ID: "abc", Type: "call", Identity: identity, Method: "invoke", Payload: json.RawMessage(`{"action":"init"}`)},
		{Protocol: 1, ID: "abc", Type: "host_call", Identity: identity, Method: "data.read", Payload: json.RawMessage(`{"key":"history"}`)},
		{Protocol: 1, ID: "abc", Type: "result", Identity: identity, Result: json.RawMessage(`null`)},
		{Protocol: 1, ID: "abc", Type: "host_result", Identity: identity, Error: &ReplyError{Code: "HOST_DENIED"}},
	}
	for _, frame := range frames {
		raw, err := json.Marshal(frame)
		if err != nil || !bytes.Contains(raw, []byte(`"jsonrpc":"2.0"`)) || bytes.Contains(raw, []byte(`"type"`)) {
			t.Fatal("wire is not JSON-RPC 2.0", string(raw), err)
		}
		var roundtrip Frame
		if json.Unmarshal(raw, &roundtrip) != nil || roundtrip.Protocol != frame.Protocol || roundtrip.Type != frame.Type || roundtrip.Identity != identity || roundtrip.ID != frame.ID || roundtrip.Method != frame.Method || !bytes.Equal(roundtrip.Payload, frame.Payload) || !bytes.Equal(roundtrip.Result, frame.Result) || (roundtrip.Error == nil) != (frame.Error == nil) {
			t.Fatal("wire roundtrip changed message", string(raw), roundtrip)
		}
	}
	for _, raw := range []string{
		`{"jsonrpc":"2.0","id":"x","method":"plugin.invoke","params":{},"result":null}`,
		`{"jsonrpc":"2.0","id":"x","result":null,"error":{}}`,
		`{"jsonrpc":"1.0","id":"x","result":{}}`,
		`{"jsonrpc":"2.0","method":"plugin.invoke","params":{}}`,
		`{"jsonrpc":"2.0","id":"x","result":{},"extra":"unknown"}`,
	} {
		var frame Frame
		if json.Unmarshal([]byte(raw), &frame) == nil {
			t.Fatal("ambiguous or incompatible JSON-RPC accepted", raw)
		}
	}
}

// This function is selected in a real child process. It never reads secrets
// from env/argv and exits before the testing package can write PASS to stdout.
func TestWorkerFixture(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return
	}
	scenario := os.Args[separator+1]
	if scenario == "sdk" {
		err := Serve(context.Background(), os.Stdin, os.Stdout, fixtureManifest(), func(ctx context.Context, method string, payload json.RawMessage, host HostCaller) (json.RawMessage, error) {
			if method == "stop" {
				return json.RawMessage(`null`), nil
			}
			raw, err := host(ctx, "data.read", json.RawMessage(`{"key":"history"}`))
			if err != nil {
				return nil, err
			}
			var data struct{ Value string }
			_ = json.Unmarshal(raw, &data)
			return json.Marshal(map[string]any{"host": data.Value, "inherited_secret": os.Getenv("PROVIDER_SECRET") != ""})
		})
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	var request Frame
	if decoder.Decode(&request) != nil {
		os.Exit(2)
	}
	if scenario == "blocked" {
		time.Sleep(20 * time.Second)
		os.Exit(0)
	}
	if scenario == "crash" {
		fmt.Fprintln(os.Stderr, "synthetic-private-stderr")
		os.Exit(19)
	}
	if scenario == "stderr_flood" {
		_, _ = io.WriteString(os.Stderr, strings.Repeat("synthetic-stderr-", 1000))
		time.Sleep(20 * time.Second)
		os.Exit(0)
	}
	if scenario == "oversized" {
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 10000))
		os.Exit(0)
	}
	if scenario == "invalid_json" {
		fmt.Fprintln(os.Stdout, "not json synthetic-private-output")
		os.Exit(0)
	}
	if scenario == "unknown_rpc_field" {
		fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":"x","result":null,"private":"synthetic"}`)
		os.Exit(0)
	}
	hello := Handshake{Protocol: 1, PluginID: "Fixture", Version: "1.2.3", HostAPI: 1, Methods: fixtureManifest().Methods, Capabilities: fixtureManifest().Capabilities}
	switch scenario {
	case "wrong_version":
		hello.Version = "9.9.9"
	case "wrong_api":
		hello.HostAPI = 2
	case "wrong_capabilities":
		hello.Capabilities = []string{"shell.execute"}
	}
	raw, _ := json.Marshal(hello)
	reply := Frame{Protocol: 1, ID: request.ID, Type: "result", Identity: request.Identity, Result: raw}
	if scenario == "wrong_identity" {
		reply.InstanceID = "Other"
	}
	if scenario == "host_before_handshake" {
		reply.Type, reply.Method, reply.Result = "host_call", "config.read", nil
	}
	_ = encoder.Encode(reply)
	if scenario == "wrong_identity" || strings.HasPrefix(scenario, "wrong_") || scenario == "host_before_handshake" {
		// A rejected handshake must never receive the invocation/configuration.
		if decoder.Decode(&request) == nil {
			fmt.Fprintln(os.Stderr, "unexpected invocation after rejected handshake")
			os.Exit(3)
		}
		os.Exit(0)
	}
	if decoder.Decode(&request) != nil {
		os.Exit(2)
	}
	if scenario == "cross_instance_host" || scenario == "many_host_calls" {
		for i := 0; i < 3; i++ {
			requestHost := Frame{Protocol: 1, ID: fmt.Sprint("host-", i), Type: "host_call", Identity: request.Identity, Method: "data.read", Payload: json.RawMessage(`{}`)}
			if scenario == "cross_instance_host" {
				requestHost.InstanceID = "Other"
			}
			_ = encoder.Encode(requestHost)
			var hostReply Frame
			if decoder.Decode(&hostReply) != nil {
				os.Exit(0)
			}
		}
	}
	_ = encoder.Encode(Frame{Protocol: 1, ID: request.ID, Type: "result", Identity: request.Identity, Result: json.RawMessage(`{"done":true}`)})
	if decoder.Decode(&request) != nil {
		os.Exit(2)
	}
	_ = encoder.Encode(Frame{Protocol: 1, ID: request.ID, Type: "result", Identity: request.Identity, Error: &ReplyError{Code: "STOP_FAILED"}})
	os.Exit(0)
}
