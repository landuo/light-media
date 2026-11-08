// This test-only worker proves that Cloudflare reaches a separate standard
// CustomHosts object. It is never registered in a production artifact index.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/plugins"
)

const (
	instance        = "CustomHosts"
	fixtureBasename = "owned-cloudflare-hosts"
	fixturePrefix   = "# Cloudflare owned fixture\n127.0.0.1 localhost\n"
	fixtureKey      = "cloudflare_owned_fixture"
	reloadKey       = "cloudflare_owned_reloads"
)

func main() {
	manifest := plugins.Manifest{
		SchemaVersion: 1, PluginID: instance, Name: "Owned CustomHosts peer", Version: "1.2.2",
		Runtime: "go-executable", Entry: "custom-hosts-peer", HostAPIMin: 1, HostAPIMax: 1,
		Methods:           []string{"init", "get_state", "get_command", "get_api", "get_service", "get_sidebar_nav", "reload", "stop"},
		Capabilities:      []string{plugins.BaseGetConfig, plugins.BaseGetData, plugins.BaseSaveData},
		BroadcastHandlers: []plugins.BroadcastHandler{{Event: "plugin.reload", Method: "reload"}},
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		if err := json.NewEncoder(os.Stdout).Encode(manifest); err != nil {
			os.Exit(2)
		}
		return
	}
	if err := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, manifest, handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func handle(ctx context.Context, method string, raw json.RawMessage, caller plugins.HostCaller) (json.RawMessage, error) {
	base := plugins.Base{Host: caller}
	switch method {
	case "init":
		var config struct {
			Fixture string `json:"fixture_path"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, err
		}
		if config.Fixture == "" {
			return nil, nil
		}
		if _, err := readOwnedFixture(config.Fixture); err != nil {
			return nil, err
		}
		return nil, base.SaveData(ctx, fixtureKey, config.Fixture)
	case "get_state":
		return json.RawMessage("true"), nil
	case "get_command", "get_api", "get_service", "get_sidebar_nav":
		return json.RawMessage("[]"), nil
	case "stop":
		return nil, nil
	case "reload":
		var event plugins.MutableEvent
		var fields struct {
			PluginID string `json:"plugin_id"`
		}
		if json.Unmarshal(raw, &event) != nil || json.Unmarshal(event.Data, &fields) != nil {
			return nil, errors.New("invalid CustomHosts reload event")
		}
		if event.Type != "plugin.reload" || fields.PluginID != instance {
			return nil, nil
		}
		return nil, applyOwnedFixture(ctx, base)
	default:
		return nil, errors.New("unknown fixture method")
	}
}

func readOwnedFixture(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != fixtureBasename {
		return nil, errors.New("fixture path rejected")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("fixture file rejected")
	}
	content, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(content, []byte(fixturePrefix)) {
		return nil, errors.New("fixture prefix rejected")
	}
	return content, nil
}

func applyOwnedFixture(ctx context.Context, base plugins.Base) error {
	key := fixtureKey
	raw, err := base.GetData(ctx, &key)
	if err != nil {
		return err
	}
	var path string
	if json.Unmarshal(raw, &path) != nil {
		return errors.New("missing owned fixture path")
	}
	if _, err := readOwnedFixture(path); err != nil {
		return err
	}
	raw, err = base.GetConfig(ctx)
	if err != nil {
		return err
	}
	var config struct {
		Enabled bool   `json:"enabled"`
		Hosts   string `json:"hosts"`
	}
	if json.Unmarshal(raw, &config) != nil || !config.Enabled || config.Hosts == "" {
		return errors.New("missing sealed CustomHosts values")
	}
	var output strings.Builder
	output.WriteString(fixturePrefix + "# CustomHostsPlugin\n")
	for _, line := range strings.Split(strings.TrimSpace(config.Hosts), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasSuffix(fields[1], ".invalid") {
			return errors.New("fixture hosts row rejected")
		}
		if _, err := netip.ParseAddr(fields[0]); err != nil {
			return err
		}
		output.WriteString(fields[0] + "\t" + fields[1] + "\n")
	}
	if err := os.WriteFile(path, []byte(output.String()), 0600); err != nil {
		return err
	}
	key = reloadKey
	raw, err = base.GetData(ctx, &key)
	if err != nil {
		return err
	}
	var reloads int
	if err := json.Unmarshal(raw, &reloads); err != nil {
		return err
	}
	return base.SaveData(ctx, reloadKey, reloads+1)
}
