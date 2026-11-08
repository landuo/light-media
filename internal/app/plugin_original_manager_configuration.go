package app

import (
	"bytes"
	"context"
	"encoding/json"
)

// MoviePilot PluginManager.get_plugin_config, not _PluginBase.get_config:
// no imported class or any falsey stored value produces {}, without storing it.
func (a *App) originalManagerConfiguration(ctx context.Context, instance string) (json.RawMessage, error) {
	if _, ok := a.pluginOriginalClasses.Load(instance); !ok {
		return json.RawMessage(`{}`), nil
	}
	return a.originalManagerStoredConfiguration(ctx, instance)
}

func (a *App) originalManagerStoredConfiguration(ctx context.Context, instance string) (json.RawMessage, error) {
	raw, _, err := a.readOriginalBaseConfigForInit(ctx, instance)
	if err != nil {
		return nil, err
	}
	if !originalBaseJSONTruthy(raw) {
		return json.RawMessage(`{}`), nil
	}
	var value map[string]json.RawMessage
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	// Keep original key code points in the raw JSON. encoding/json's map
	// decoder normalizes unpaired surrogate keys to U+FFFD, which would
	// incorrectly turn a later source HTTP serialization failure into success.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token() // already validated above as an object
	var output bytes.Buffer
	output.WriteByte('{')
	for decoder.More() {
		start := decoder.InputOffset()
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key := token.(string)
		keyRaw := bytes.TrimSpace(raw[start:decoder.InputOffset()])
		keyRaw = bytes.TrimSpace(bytes.TrimPrefix(keyRaw, []byte(",")))
		var item json.RawMessage
		if err := decoder.Decode(&item); err != nil {
			return nil, err
		}
		if key == "" {
			continue
		}
		if output.Len() > 1 {
			output.WriteByte(',')
		}
		output.Write(keyRaw)
		output.WriteByte(':')
		output.Write(item)
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}
