// Command owned-brush-inventory-fixture is an owned verification worker, not a
// shipped Brush business plugin. It uses the actual standard SDK and no App or
// restored-plugin packages. Build two variants with -X main.fixtureDenied=true
// or false; filenames and process basenames never determine authority.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"light-media/internal/plugins"
)

// Set only by the documented linker flag. A non-boolean spelling is rejected.
var fixtureDenied = "false"

func fixtureManifest(denied bool) plugins.Manifest {
	id, entry := "OwnedBrushInventory", "owned-brush-inventory-sdk-fixture"
	capabilities := []string{plugins.BrushInventoryReadCapability}
	if denied {
		id, entry = "OwnedBrushInventoryDenied", "owned-brush-inventory-sdk-fixture-no-cap"
		capabilities = []string{}
	}
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: id, Name: "Owned inventory SDK fixture", Version: "1.0", Runtime: "go-executable", Entry: entry,
		HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "snapshot", "resource", "stop"},
		Capabilities: capabilities,
	}
}

type fixtureSnapshot struct {
	Inventory   plugins.BrushInventoryV1 `json:"inventory"`
	PID         int                      `json:"pid"`
	HadPrevious bool                     `json:"had_previous"`
	StaleDenied bool                     `json:"stale_denied"`
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
	// Deliberately retain the PREVIOUS callback only in this owned adversarial
	// fixture to prove its revocation. Production workers must never do this.
	var previous plugins.HostCaller
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
			return host(ctx, plugins.BrushInventoryReadCapability, raw)
		case "snapshot":
			hadPrevious, denied := previous != nil, true
			if previous != nil {
				value, err := previous(ctx, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`))
				denied = err != nil && len(value) == 0
			}
			value, err := host(ctx, plugins.BrushInventoryReadCapability, json.RawMessage(`{}`))
			if err != nil {
				return nil, err
			}
			var inventory plugins.BrushInventoryV1
			decoder := json.NewDecoder(bytes.NewReader(value))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&inventory); err != nil {
				return nil, err
			}
			if inventory.SchemaVersion != 1 || inventory.Sites == nil || inventory.Downloaders == nil {
				return nil, errors.New("invalid owned inventory result")
			}
			previous = host
			return json.Marshal(fixtureSnapshot{Inventory: inventory, PID: os.Getpid(), HadPrevious: hadPrevious, StaleDenied: denied})
		}
		return nil, errors.New("unsupported owned inventory method")
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, handle) != nil {
		fmt.Fprintln(os.Stderr, "owned inventory SDK stopped with an error")
		os.Exit(82)
	}
}
