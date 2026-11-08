package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// getState calls the already-applied object's original state method. Adapters
// not yet ported fail explicitly rather than guessing enabled from the DB.
func (p *originalPluginInstance) getState(ctx context.Context) (bool, error) {
	unlock := p.lockOriginalMethod()
	defer unlock()
	var raw json.RawMessage
	if err := p.readSession(ctx, "get_state", &raw); err != nil {
		return false, err
	}
	return originalJSONStateTruth(raw)
}

// get_state may return any original Python value. Preserve its JSON number
// token until this truth boundary: Python floats overflow to infinities and
// underflow to zero, whereas Python integers have arbitrary precision. A
// nonempty container is true regardless of numbers nested inside it.
func originalJSONStateTruth(raw json.RawMessage) (bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var state any
	if err := decoder.Decode(&state); err != nil {
		return false, err
	}
	if number, ok := state.(json.Number); ok {
		text := string(number)
		if strings.ContainsAny(text, ".eE") {
			value, err := strconv.ParseFloat(text, 64)
			if err != nil && !errors.Is(err, strconv.ErrRange) {
				return false, err
			}
			return value != 0, nil
		}
		return strings.Trim(text, "-0") != "", nil
	}
	return sourceSubscriptionTruth(state), nil
}

func (a *App) updateOriginalPluginEventState(ctx context.Context, p *originalPluginInstance) error {
	enabled, err := p.getState(ctx)
	if err != nil {
		return err
	}
	p.configMu.Lock()
	p.eventsEnabled = enabled
	p.configMu.Unlock()
	return nil
}

// A direct init_plugin call mutates the existing object's members. In
// particular, empty configuration is not permission to construct a new object.
