package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const SharedSystemConfigGet = "system.config.get"
const SharedSystemConfigSet = "system.config.set"
const SystemConfigCustomIdentifiers = "CustomIdentifiers"

// SharedSystemConfig accesses only the host's explicitly granted ordinary
// global keys. Empty/unknown keys never mean all settings. The first vocabulary
// contains CustomIdentifiers; credentials and plugin-private settings are not
// part of it. Each call uses the current invocation, never a saved HostCaller.
type SharedSystemConfig struct{ Host HostCaller }
type SharedSystemConfigGetV1 struct {
	Key string `json:"key"`
}
type SharedSystemConfigSetV1 struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// Get returns the internal value, retaining JSON-escaped lone UTF-16 code points.
// Keep RawMessage elements unchanged or use Decode/EncodeSystemConfigJSONString;
// ordinary Go string decoding is lossy for those source strings. UTF-8 HTTP
// rendering is a separate caller boundary, not a prerequisite for DAO access.
// The separately documented nonfinite-number boundary is unchanged here.
func (s SharedSystemConfig) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if s.Host == nil {
		return nil, errors.New("system config requires an active host caller")
	}
	raw, e := json.Marshal(SharedSystemConfigGetV1{Key: key})
	if e != nil {
		return nil, e
	}
	return s.Host(ctx, SharedSystemConfigGet, raw)
}

// Set preserves the selected synchronous cache-before-DB semantics and nullable
// bool result. The validated concurrent producer contract is RemoteIdentifiers
// plus native words editing, not arbitrary whole-SystemConfig-table DAO parity.
// In particular, cross-key row-ID reuse after concurrent delete/recreate and
// historical source row identities are outside this contract. Supply json.RawMessage when an
// object's insertion order matters; an ordinary Go map has no caller order.
// A successful Set does not promise every stored JSON value is usable as rules.
func (s SharedSystemConfig) Set(ctx context.Context, key string, value any) (*bool, error) {
	if s.Host == nil {
		return nil, errors.New("system config requires an active host caller")
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	input, e := json.Marshal(SharedSystemConfigSetV1{Key: key, Value: raw})
	if e != nil {
		return nil, e
	}
	output, e := s.Host(ctx, SharedSystemConfigSet, input)
	if e != nil {
		return nil, e
	}
	var result *bool
	e = json.Unmarshal(output, &result)
	return result, e
}
