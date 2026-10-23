package app

import (
	"context"
	"fmt"
	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

// originalWatcherState only looks up an already retained owner. In particular,
// invoking the production owner constructor here would perform recovery I/O.
// The registry key is instance ID; generation is a separate owner invariant.
func originalWatcherState(ctx context.Context, p *originalPluginInstance) (bool, bool, error) {
	if p == nil {
		return false, false, nil
	}
	switch p.installation.PluginID {
	case "RemoveLink":
		if err := ctx.Err(); err != nil {
			return false, true, err
		}
		if p.app == nil {
			return false, true, fmt.Errorf("loaded RemoveLink application missing")
		}
		r := &p.app.pluginRemoveLink
		r.mu.Lock()
		closing := r.closing
		o := r.owners[p.installation.InstanceID]
		r.mu.Unlock()
		if closing {
			return false, true, cleanuphost.ErrClosed
		}
		if o == nil {
			return false, true, fmt.Errorf("loaded RemoveLink owner missing")
		}
		select {
		case <-o.ready:
		case <-ctx.Done():
			return false, true, ctx.Err()
		}
		if o.err != nil {
			return false, true, o.err
		}
		if err := o.lock(ctx); err != nil {
			return false, true, err
		}
		defer o.unlock()
		// Transitions can retire this owner while the caller waits for its gate.
		r.mu.Lock()
		closing = r.closing
		current := r.owners[p.installation.InstanceID] == o
		r.mu.Unlock()
		if closing {
			return false, true, cleanuphost.ErrClosed
		}
		if !current || o.instance != p.installation.InstanceID || o.generation != p.installation.Generation {
			return false, true, cleanuphost.ErrUncertain
		}
		if o.h == nil {
			if o.originalMachine == nil {
				return false, true, fmt.Errorf("loaded RemoveLink live machine missing (inert owner has no retained machine)")
			}
			return removelink.Truth(o.originalMachine.StateValue()), true, nil
		}
		state, err := o.h.OriginalState(ctx)
		return state, true, err
	case "ScrapeFileClean":
		if err := ctx.Err(); err != nil {
			return false, true, err
		}
		if p.app == nil {
			return false, true, fmt.Errorf("loaded ScrapeFileClean application missing")
		}
		r := &p.app.pluginScrapeClean
		r.mu.Lock()
		closing := r.closing
		o := r.owners[p.installation.InstanceID]
		r.mu.Unlock()
		if closing {
			return false, true, scrapehost.ErrClosed
		}
		if o == nil {
			return false, true, fmt.Errorf("loaded ScrapeFileClean owner missing")
		}
		select {
		case <-o.ready:
		case <-ctx.Done():
			return false, true, ctx.Err()
		}
		if o.err != nil {
			return false, true, o.err
		}
		if err := o.lock(ctx); err != nil {
			return false, true, err
		}
		defer o.unlock()
		// Transitions can retire this owner while the caller waits for its gate.
		r.mu.Lock()
		closing = r.closing
		current := r.owners[p.installation.InstanceID] == o
		r.mu.Unlock()
		if closing {
			return false, true, scrapehost.ErrClosed
		}
		if !current || o.instance != p.installation.InstanceID || o.generation != p.installation.Generation {
			return false, true, scrapehost.ErrUncertain
		}
		if o.h == nil {
			if o.originalMachine == nil {
				return false, true, fmt.Errorf("loaded ScrapeFileClean live machine missing (inert owner has no retained machine)")
			}
			return o.originalMachine.GetState(), true, nil
		}
		state, err := o.h.OriginalState(ctx)
		return state, true, err
	}
	return false, false, nil
}

// Both source classes inherit _PluginBase.get_service, whose body is pass.
func originalWatcherServices(ctx context.Context, p *originalPluginInstance) ([]pluginScheduleDefinition, bool, error) {
	if p == nil {
		return nil, false, nil
	}
	switch p.installation.PluginID {
	case "RemoveLink", "ScrapeFileClean":
		return nil, true, ctx.Err()
	}
	return nil, false, nil
}

func originalWatcherCommands(ctx context.Context, p *originalPluginInstance) ([]map[string]any, bool, error) {
	if p == nil {
		return nil, false, nil
	}
	switch p.installation.PluginID {
	case "RemoveLink": // source.py:558-559, get_command: pass (None).
		return nil, true, ctx.Err()
	case "ScrapeFileClean": // source.py:519-521, get_command: return [].
		return []map[string]any{}, true, ctx.Err()
	}
	return nil, false, nil
}
