package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/plugins/builtin"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	transfer "light-media/internal/plugins/torrenttransfer"
)

// getState calls the already-applied object's original state method. Adapters
// not yet ported fail explicitly rather than guessing enabled from the DB.
func (p *originalPluginInstance) getState(ctx context.Context) (bool, error) {
	p.configMu.RLock()
	raw := append(json.RawMessage(nil), p.appliedConfig...)
	nativeState := p.nativeState
	p.configMu.RUnlock()
	switch p.installation.PluginID {
	case "SystemProcess":
		var cfg builtin.SystemProcessConfiguration
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return false, err
		}
		return cfg.State(), nil
	case "CustomHosts":
		var cfg builtin.CustomHostsConfiguration
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return false, err
		}
		return cfg.Enabled, nil
	case "PluginUnInstall":
		return false, nil
	case "TorrentTransfer", "TorrentTransfermusic":
		if cfg, ok := nativeState.(transfer.Config); ok {
			return cfg.State(), nil
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return false, err
		}
		cfg, err := transfer.RestoreConfig(value)
		if err != nil {
			return false, err
		}
		return cfg.State(), nil
	case "IYUUAutoSeed":
		if cfg, ok := nativeState.(iyuu.Config); ok {
			return cfg.State(), nil
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return false, err
		}
		cfg, err := iyuu.RestoreConfig(value)
		if err != nil {
			return false, err
		}
		return cfg.State(), nil
	case "MPServerStatus":
		runtime := &p.app.pluginServerStatus
		runtime.mu.Lock()
		slot := runtime.slots[p.installation.InstanceID]
		runtime.mu.Unlock()
		if slot == nil {
			return false, fmt.Errorf("loaded server status object missing")
		}
		slot.mu.Lock()
		defer slot.mu.Unlock()
		if slot.machine == nil {
			return false, fmt.Errorf("loaded server status machine missing")
		}
		return slot.machine.State(), nil
	default:
		return false, fmt.Errorf("original get_state adapter not yet ported: %s", p.installation.PluginID)
	}
}

func (a *App) updateOriginalPluginEventState(ctx context.Context, p *originalPluginInstance) error {
	enabled, err := p.getState(ctx)
	if err != nil {
		return err
	}
	active := 0
	if enabled {
		active = 1
	}
	if _, err = a.Store.DB.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET active=? WHERE instance_id=?`, active, p.installation.InstanceID); err != nil {
		return err
	}
	p.configMu.Lock()
	p.eventsEnabled = enabled
	p.configMu.Unlock()
	return nil
}
