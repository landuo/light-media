// Command owned-history-fixture is a small owned standard SDK worker,
// not a shipped history/Brush implementation. Credentials remain in App.
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
	id, entry := "OwnedHistory", "owned-history-sdk-fixture"
	capabilities := []string{plugins.TransferHistoryBySourceCapability, plugins.TransferHistoryByDestinationCapability}
	if denied {
		id, entry = "OwnedHistoryDenied", "owned-history-sdk-fixture-no-cap"
		capabilities = []string{}
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: id, Name: "Owned history query SDK fixture", Version: "1.0", Runtime: "go-executable", Entry: entry, HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "snapshot", "resource", "stop"}, Capabilities: capabilities}
}

type fixtureSnapshot struct {
	Result      *plugins.TransferHistoryRecord `json:"result"`
	PID         int                            `json:"pid"`
	HadPrevious bool                           `json:"had_previous"`
	StaleDenied bool                           `json:"stale_denied"`
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
			var request struct {
				Method  string          `json:"method"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			return host(ctx, request.Method, request.Payload)
		case "snapshot":
			var request struct {
				Source           *string `json:"src"`
				Destination      *string `json:"dest"`
				Storage          *string `json:"storage"`
				ByDestination    bool    `json:"by_destination"`
				SourceBytes      []byte  `json:"source_bytes_for_sdk_probe"`
				DestinationBytes []byte  `json:"dest_bytes_for_sdk_probe"`
				StorageBytes     []byte  `json:"storage_bytes_for_sdk_probe"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			// Test-only byte inputs construct Go strings inside the actual worker,
			// avoiding JSON's earlier Unicode replacement at the test transport.
			if request.SourceBytes != nil {
				value := string(request.SourceBytes)
				request.Source = &value
			}
			if request.DestinationBytes != nil {
				value := string(request.DestinationBytes)
				request.Destination = &value
			}
			if request.StorageBytes != nil {
				value := string(request.StorageBytes)
				request.Storage = &value
			}
			mu.Lock()
			old := previous
			mu.Unlock()
			denied := true
			if old != nil {
				value, err := old(ctx, plugins.TransferHistoryBySourceCapability, json.RawMessage(`{"src":null}`))
				denied = err != nil && len(value) == 0
			}
			var result *plugins.TransferHistoryRecord
			var err error
			sdk := plugins.Histories{Host: host}
			if request.ByDestination {
				result, err = sdk.TransferByDestination(ctx, request.Destination, request.Storage)
			} else {
				result, err = sdk.TransferBySource(ctx, request.Source, request.Storage)
			}
			if err != nil {
				return nil, err
			}
			mu.Lock()
			previous = host
			mu.Unlock()
			return json.Marshal(fixtureSnapshot{Result: result, PID: os.Getpid(), HadPrevious: old != nil, StaleDenied: denied})
		}
		return nil, errors.New("unsupported owned history method")
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, handle) != nil {
		fmt.Fprintln(os.Stderr, "owned history SDK stopped with an error")
		os.Exit(82)
	}
}
