package app

import (
	"context"
)

// getState calls the already-applied object's original state method. Adapters
// not yet ported fail explicitly rather than guessing enabled from the DB.
func (p *originalPluginInstance) getState(ctx context.Context) (bool, error) {
	p.methodMu.Lock()
	defer p.methodMu.Unlock()
	var state any
	err := p.readSession(ctx, "get_state", &state)
	return sourceSubscriptionTruth(state), err
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
