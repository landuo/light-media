// Package uninstall ports the installed PluginUnInstall 2.2 operation order.
// It has no filesystem/database authority: the host supplies operations bound
// to its current initialization run and independently verifies every target.
package pluginuninstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var ErrConfiguration = errors.New("invalid uninstall configuration")

// Raw values preserve the source's missing/null flags and Python truthiness.
// A successful execution replaces its own config with precisely these fields.
type Configuration struct {
	PluginIDs   json.RawMessage `json:"plugin_ids"`
	ClearConfig json.RawMessage `json:"clear_config"`
	ClearData   json.RawMessage `json:"clear_data"`
}

func decode(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrConfiguration
	}
	return nil
}

func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		number, err := v.Float64()
		return err != nil || number != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

func value(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out any
	err := decode(raw, &out)
	return out, err
}

// ParseConfiguration accepts the same JSON-shaped falsey/no-op inputs as init.
// Unknown object fields are discarded only by the source's eventual reset.
func ParseConfiguration(raw json.RawMessage) (Configuration, error) {
	var config Configuration
	input, err := value(raw)
	if err != nil || !truthy(input) {
		return config, err
	}
	if _, ok := input.(map[string]any); !ok {
		return config, ErrConfiguration // A truthy source config must support get.
	}
	if err = decode(raw, &config); err != nil {
		return config, err
	}
	return config, nil
}

func (c Configuration) Selected() (bool, error) {
	ids, err := value(c.PluginIDs)
	return truthy(ids), err
}

func contains(selection any, id string) (bool, error) {
	switch selected := selection.(type) {
	case []any:
		for _, entry := range selected {
			if text, ok := entry.(string); ok && text == id {
				return true, nil
			}
		}
		return false, nil
	case string:
		return strings.Contains(selected, id), nil
	case map[string]any:
		_, found := selected[id]
		return found, nil
	default:
		return false, ErrConfiguration // Python membership rejects truthy scalars.
	}
}

func (c Configuration) reset() json.RawMessage {
	c.PluginIDs = json.RawMessage(`[]`)
	raw, _ := json.Marshal(c)
	return raw
}

// Operations names the source manager/storage boundaries. The standard worker
// adapter supplies finite authenticated host calls; paths never come from a
// worker-chosen filesystem root. This package owns the source business sequence.
type Operations interface {
	Installed(context.Context) ([]string, error)
	RemoveJobs(context.Context, string) error
	RemovePlugin(context.Context, string) error
	PackageExists(context.Context, string) (bool, error)
	RemovePackage(context.Context, string) error
	DeleteConfig(context.Context, string, bool) (bool, error)
	DeleteData(context.Context, string, bool) (bool, error)
	LogRemoved(context.Context, string) error
	SaveInstalled(context.Context, []string) error
	SaveOwnConfig(context.Context, json.RawMessage) error
}

type Result struct {
	Ran                  bool     `json:"ran"`
	Removed              []string `json:"removed"`
	Remaining            []string `json:"remaining"`
	ConfigClearRequested bool     `json:"config_clear_requested"`
	DataClearRequested   bool     `json:"data_clear_requested"`
	ConfigCleared        []string `json:"config_cleared"`
	DataCleared          []string `json:"data_cleared"`
	OwnConfigReset       bool     `json:"own_config_reset"`
}

// Run follows the source's selected-vs-installed membership, installed order,
// duplicate handling and two independent cleanup requests. The installed
// manager removes its class before delete(force=false), making those requests
// no-ops; the host adapter must preserve that guard and report applied honestly.
// The host suppresses ordinary filesystem failures inside its rmtree boundary.
// Any returned error is an unexpected source error or an authority/IPC failure
// and must stop this initializer before its next side effect.
func Run(ctx context.Context, raw json.RawMessage, host Operations) (Result, error) {
	out := Result{Removed: []string{}, Remaining: []string{}, ConfigCleared: []string{}, DataCleared: []string{}}
	config, err := ParseConfiguration(raw)
	if err != nil {
		return out, err
	}
	selection, err := value(config.PluginIDs)
	if err != nil || !truthy(selection) {
		return out, err
	}
	if host == nil {
		return out, errors.New("missing uninstall host")
	}
	clearConfig, _ := value(config.ClearConfig)
	clearData, _ := value(config.ClearData)
	out.ConfigClearRequested, out.DataClearRequested = truthy(clearConfig), truthy(clearData)
	installed, err := host.Installed(ctx)
	if err != nil {
		return out, err
	}
	out.Ran = true
	for _, id := range installed {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		selected, matchErr := contains(selection, id)
		if matchErr != nil {
			return out, matchErr
		}
		if !selected {
			out.Remaining = append(out.Remaining, id)
			continue
		}
		if err = host.RemoveJobs(ctx, id); err != nil {
			return out, err
		}
		if err = host.RemovePlugin(ctx, id); err != nil {
			return out, err
		}
		exists, existsErr := host.PackageExists(ctx, id)
		if existsErr != nil {
			return out, existsErr
		}
		if exists {
			if err = host.RemovePackage(ctx, id); err != nil {
				return out, err
			}
		}
		if out.ConfigClearRequested {
			applied, deleteErr := host.DeleteConfig(ctx, id, false)
			if deleteErr != nil {
				return out, deleteErr
			}
			if applied {
				out.ConfigCleared = append(out.ConfigCleared, id)
			}
		}
		if out.DataClearRequested {
			applied, deleteErr := host.DeleteData(ctx, id, false)
			if deleteErr != nil {
				return out, deleteErr
			}
			if applied {
				out.DataCleared = append(out.DataCleared, id)
			}
		}
		if err = host.LogRemoved(ctx, id); err != nil {
			return out, err
		}
		out.Removed = append(out.Removed, id)
	}
	if err = host.SaveInstalled(ctx, out.Remaining); err != nil {
		return out, err
	}
	if err = host.SaveOwnConfig(ctx, config.reset()); err != nil {
		return out, err
	}
	out.OwnConfigReset = true
	return out, nil
}
