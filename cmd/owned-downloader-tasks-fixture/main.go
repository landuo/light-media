// Command owned-downloader-tasks-fixture is a small owned standard SDK worker,
// not a shipped downloader/Brush implementation. Credentials remain in App.
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

var fixtureDenied = "false"

func fixtureManifest(denied bool) plugins.Manifest {
	id, entry := "OwnedDownloaderTasks", "owned-downloader-tasks-sdk-fixture"
	capabilities := []string{plugins.DownloaderTasksReadCapability}
	if denied {
		id, entry = "OwnedDownloaderTasksDenied", "owned-downloader-tasks-sdk-fixture-no-cap"
		capabilities = []string{}
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: id, Name: "Owned downloader tasks SDK fixture", Version: "1.0", Runtime: "go-executable", Entry: entry, HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "snapshot", "resource", "stop"}, Capabilities: capabilities}
}

type fixtureSnapshot struct {
	Result      plugins.DownloaderTasksV1 `json:"result"`
	PID         int                       `json:"pid"`
	HadPrevious bool                      `json:"had_previous"`
	StaleDenied bool                      `json:"stale_denied"`
}

func main() {
	if fixtureDenied != "false" && fixtureDenied != "true" {
		fmt.Fprintln(os.Stderr, "invalid owned fixture linker mode")
		os.Exit(80)
	}
	manifest := fixtureManifest(fixtureDenied == "true")
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		if json.NewEncoder(os.Stdout).Encode(manifest) != nil {
			os.Exit(81)
		}
		return
	}
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "unsupported owned fixture argument")
		os.Exit(80)
	}
	// Adversarial fixture only: retain a previous callback to prove revocation.
	var previous plugins.HostCaller
	var mu sync.Mutex
	handle := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		switch method {
		case "init", "stop":
			return json.RawMessage(`null`), nil
		case "get_state":
			return json.RawMessage(`false`), nil
		case "get_service", "get_command", "get_api":
			return json.RawMessage(`[]`), nil
		case "get_render_mode":
			return json.RawMessage(`["vuetify",""]`), nil
		case "resource":
			return host(ctx, plugins.DownloaderTasksReadCapability, raw)
		case "snapshot":
			var request plugins.DownloaderTasksRequestV1
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			mu.Lock()
			old := previous
			mu.Unlock()
			denied := true
			if old != nil {
				value, err := old(ctx, plugins.DownloaderTasksReadCapability, raw)
				denied = err != nil && len(value) == 0
			}
			result, err := (plugins.Downloaders{Host: host}).Tasks(ctx, request)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			previous = host
			mu.Unlock()
			return json.Marshal(fixtureSnapshot{Result: result, PID: os.Getpid(), HadPrevious: old != nil, StaleDenied: denied})
		}
		return nil, errors.New("unsupported owned downloader method")
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, handle) != nil {
		fmt.Fprintln(os.Stderr, "owned downloader SDK stopped with an error")
		os.Exit(82)
	}
}
