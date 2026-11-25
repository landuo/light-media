package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	SettingsReadCapability  = "settings.read"
	SettingsWriteCapability = "settings.write"
)

// Keys are MoviePilot's ordinary setting names. The host explicitly maps each
// supported key to its owning subsystem; there is no arbitrary KV. The ordinary
// BIG_MEMORY_MODE, META_CACHE_EXPIRE (raw hours), TMDB_LOCALE and RECOGNIZE_SOURCE
// startup snapshots are read-only and are not database configuration values.
// RMT_MEDIAEXT is the host's effective, read-only extension list, including
// native configuration and environment precedence; it is not plugin data.
type SettingsHostRequestV1 struct {
	Key string `json:"key"`
}
type SettingsHostWriteV1 struct {
	Key                      string          `json:"key"`
	Value                    json.RawMessage `json:"value"`
	ReplaceRemoteIdentifiers bool            `json:"replace_remote_identifiers,omitempty"`
}
type SettingsHostResultV1 struct {
	SchemaVersion int             `json:"schema_version"`
	Key           string          `json:"key"`
	Value         json.RawMessage `json:"value"`
	Changed       *bool           `json:"changed"`
}
type SystemSettings struct{ Host HostCaller }

func (s SystemSettings) Get(ctx context.Context, key string) (SettingsHostResultV1, error) {
	var out SettingsHostResultV1
	err := hostBusinessCall(ctx, s.Host, SettingsReadCapability, SettingsHostRequestV1{key}, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Key != key || !json.Valid(out.Value)) {
		err = errors.New("invalid system setting response")
	}
	return out, err
}
func (s SystemSettings) Set(ctx context.Context, key string, value any) (SettingsHostResultV1, error) {
	return s.set(ctx, key, value, false)
}

// ReplaceRemoteIdentifiers publishes one fully collected remote suffix. The
// host retains the latest user prefix in the same recognition-store transaction.
func (s SystemSettings) ReplaceRemoteIdentifiers(ctx context.Context, words []string) (SettingsHostResultV1, error) {
	return s.set(ctx, "CustomIdentifiers", words, true)
}

func (s SystemSettings) set(ctx context.Context, key string, value any, replaceRemoteIdentifiers bool) (SettingsHostResultV1, error) {
	var out SettingsHostResultV1
	raw, err := json.Marshal(value)
	if err != nil {
		return out, err
	}
	request := SettingsHostWriteV1{Key: key, Value: raw, ReplaceRemoteIdentifiers: replaceRemoteIdentifiers}
	err = hostBusinessCall(ctx, s.Host, SettingsWriteCapability, request, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Key != key || !json.Valid(out.Value)) {
		err = errors.New("invalid system setting response")
	}
	return out, err
}
