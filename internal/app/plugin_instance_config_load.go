package app

import (
	"context"
	"time"
)

// An explicit declared configuration operation may load the same authenticated
// instance before continuing its original API. GET never calls this function.
// No second instance state or worker/business implementation is introduced.
func (a *App) loadPluginForConfiguration(ctx context.Context, expected PluginInstallation, authority pluginAPIAuthority) (PluginInstallation, PluginRun, error) {
	p := a.pluginStore
	if err := p.checkPluginFormWrite(ctx, expected.InstanceID, pluginFormWriteLifecycle); err != nil {
		return PluginInstallation{}, PluginRun{}, err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginInstallation{}, PluginRun{}, err
	}
	defer tx.Rollback()
	if err = a.validatePluginHTTPAuthorityTx(ctx, tx, authority); err != nil {
		return PluginInstallation{}, PluginRun{}, err
	}
	current, err := p.pluginLifecycleInstallation(ctx, tx, expected.InstanceID)
	if err != nil {
		return PluginInstallation{}, PluginRun{}, err
	}
	if !current.Installed || current.PluginID != expected.PluginID || current.Version != expected.Version || current.Generation != expected.Generation {
		return PluginInstallation{}, PluginRun{}, errPluginRunAttemptStale
	}
	var run PluginRun
	if current.DesiredEnabled {
		// Another concurrent first configuration request may already have queued
		// the common initialization. Observe it rather than creating a second load.
		run, err = p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE instance_id=? AND generation=? AND action IN ('init','reload') ORDER BY created_at DESC,id DESC LIMIT 1`, current.InstanceID, current.Generation))
	} else {
		err = p.preparePluginReloadTx(ctx, tx, current.InstanceID)
		if err == nil {
			run, err = p.enqueueRunTx(ctx, tx, current.InstanceID, "reload", nil)
		}
	}
	if err != nil {
		return PluginInstallation{}, PluginRun{}, err
	}
	if err = tx.Commit(); err != nil {
		return PluginInstallation{}, PluginRun{}, err
	}
	a.wakePlugins()
	wait, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	completed, err := a.waitPluginConfigurationApplied(wait, current, run, authority)
	if err != nil {
		if completed.ID != "" {
			run = completed
		}
		return PluginInstallation{}, run, err
	}
	loaded, err := p.Installation(wait, current.InstanceID)
	if err == nil && (!loaded.Installed || !loaded.DesiredEnabled || loaded.Generation != current.Generation || loaded.Version != current.Version) {
		err = errPluginRunAttemptStale
	}
	return loaded, completed, err
}
