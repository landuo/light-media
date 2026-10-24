// Command owned-instance-log-fixture is an owned SDK verification worker, not a
// business plugin. It imports only the standard SDK and emits synthetic text.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"light-media/internal/plugins"
)

var fixtureDenied = "false"

func fixtureManifest(denied bool) plugins.Manifest {
	id, entry := "OwnedInstanceLog", "owned-instance-log-sdk-fixture"
	capabilities := []string{plugins.LogWriteCapability}
	if denied {
		id, entry = "OwnedInstanceLogDenied", "owned-instance-log-sdk-fixture-no-cap"
		capabilities = []string{}
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: id, Name: "Owned instance log SDK fixture", Version: "1.0", Runtime: "go-executable", Entry: entry,
		HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "write", "resource", "probe", "fail", "close", "stop_service", "stop"}, Capabilities: capabilities}
}

func main() {
	if fixtureDenied != "false" && fixtureDenied != "true" {
		os.Exit(80)
	}
	denied := fixtureDenied == "true"
	manifest := fixtureManifest(denied)
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		if json.NewEncoder(os.Stdout).Encode(manifest) != nil {
			os.Exit(81)
		}
		return
	}
	if len(os.Args) != 1 {
		os.Exit(80)
	}
	// Retention is deliberately adversarial and exists only in this fixture.
	var previous plugins.HostCaller
	var startupDenied bool
	handle := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		logger := plugins.Logger{Host: host}
		prior := previous
		previous = host
		switch method {
		case "init":
			err := logger.Log(ctx, plugins.LogInfo, "owned startup")
			startupDenied = err != nil
			if denied {
				return json.RawMessage(`null`), nil
			}
			return json.RawMessage(`null`), err
		case "get_state":
			return json.RawMessage(`false`), nil
		case "get_service", "get_command", "get_api":
			return json.RawMessage(`[]`), nil
		case "get_render_mode":
			return json.RawMessage(`["vuetify",""]`), nil
		case "resource":
			return host(ctx, plugins.LogWriteCapability, raw)
		case "write":
			var entry plugins.LogEntryV1
			if err := json.Unmarshal(raw, &entry); err != nil {
				return nil, err
			}
			return json.RawMessage(`null`), logger.Log(ctx, entry.Level, entry.Message)
		case "fail":
			if err := logger.Log(ctx, plugins.LogError, "owned callback error"); err != nil {
				return nil, err
			}
			return nil, errors.New("owned failure not automatically copied to log")
		case "probe":
			staleDenied := false
			if prior != nil {
				value, err := prior(ctx, plugins.LogWriteCapability, json.RawMessage(`{"level":"INFO","message":"owned stale must not appear"}`))
				staleDenied = err != nil && len(value) == 0
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			cancelDenied := logger.Log(canceled, plugins.LogInfo, "owned canceled must not appear") != nil
			_, unknownErr := host(ctx, "plugin.log.unknown", json.RawMessage(`{}`))
			writeErr := logger.Log(ctx, plugins.LogInfo, "owned callback")
			return json.Marshal(map[string]any{"pid": os.Getpid(), "stale_denied": staleDenied, "cancel_denied": cancelDenied, "unknown_denied": unknownErr != nil, "write_denied": writeErr != nil, "startup_denied": startupDenied})
		case "close", "stop_service", "stop":
			err := logger.Log(ctx, plugins.LogInfo, "owned "+method)
			if denied {
				return json.RawMessage(`null`), nil
			}
			return json.RawMessage(`null`), err
		}
		return nil, errors.New("unsupported owned instance log method")
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, handle) != nil {
		fmt.Fprintln(os.Stderr, "owned instance log SDK stopped")
		os.Exit(82)
	}
}
