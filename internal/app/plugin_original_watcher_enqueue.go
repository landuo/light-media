package app

import (
	"context"
	"errors"
)

// enqueueOriginalWatcherInit reserves the existing owner transition BEFORE the
// run is visible. The real run and its transition are published in one SQL
// transaction, exactly as the authorized watcher lifecycle HTTP route does.
// It creates no receipt, bypass, new lifecycle state or synthetic running row.
func (a *App) enqueueOriginalWatcherInit(ctx context.Context, instance string) (PluginRun, bool, error) {
	installation, err := a.pluginStore.Installation(ctx, instance)
	if err != nil {
		return PluginRun{}, false, nil
	}
	if installation.PluginID != "RemoveLink" && installation.PluginID != "ScrapeFileClean" {
		return PluginRun{}, false, nil
	}
	if !originalInitializationMatches(ctx, instance, installation.PluginID, installation.Version, installation.Generation, "init") {
		return PluginRun{}, true, bad("PLUGIN_IDENTITY_MISMATCH", "原插件初始化对象不匹配")
	}
	if err = a.pluginStore.checkPluginFormWrite(ctx, instance, pluginFormWriteLifecycle); err != nil {
		return PluginRun{}, true, err
	}
	call := ctx.Value(originalInitializingObjectKey{}).(*originalInitializationCall)
	if installation.PluginID == "RemoveLink" {
		prepared, err := a.prepareRemoveLinkTransition(ctx, installation, "init")
		if err != nil {
			return PluginRun{}, true, err
		}
		if prepared == nil {
			return PluginRun{}, true, bad("PLUGIN_SNAPSHOT_STALE", "插件原宿主未保留")
		}
		defer prepared.release()
		var run PluginRun
		committed := false
		err = func() error {
			a.pluginLifecycleMu.Lock()
			defer a.pluginLifecycleMu.Unlock()
			a.pluginMu.Lock()
			defer a.pluginMu.Unlock()
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			run, err = a.pluginStore.enqueueRunTx(ctx, tx, instance, "init", nil)
			if err != nil {
				return err
			}
			// Reinitialization uses the already-reserved exact generation/revision.
			// Config changes must use the existing explicit lifecycle publication path.
			to := prepared.from
			if run.Generation != to.Generation || run.ConfigRevision != to.Revision {
				return bad("PLUGIN_SNAPSHOT_STALE", "插件配置已变化")
			}
			if err = prepared.publishTx(ctx, tx, to, run.ID); err != nil {
				return err
			}
			a.pluginOriginalInitializingRuns.Store(run.ID, call)
			a.pluginStore.originalInitializingRuns.Store(run.ID, call)
			err = tx.Commit()
			if err != nil {
				a.pluginOriginalInitializingRuns.Delete(run.ID)
				a.pluginStore.originalInitializingRuns.Delete(run.ID)
				return err
			}
			committed = true
			return nil
		}()
		resolveErr := resolveRemoveLinkPrepared(prepared, committed)
		return run, true, errors.Join(err, resolveErr)
	}
	if installation.PluginID == "ScrapeFileClean" {
		prepared, err := a.prepareScrapeFileCleanTransition(ctx, installation, "init")
		if err != nil {
			return PluginRun{}, true, err
		}
		if prepared == nil {
			return PluginRun{}, true, bad("PLUGIN_SNAPSHOT_STALE", "插件原宿主未保留")
		}
		defer prepared.release()
		var run PluginRun
		committed := false
		err = func() error {
			a.pluginLifecycleMu.Lock()
			defer a.pluginLifecycleMu.Unlock()
			a.pluginMu.Lock()
			defer a.pluginMu.Unlock()
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			run, err = a.pluginStore.enqueueRunTx(ctx, tx, instance, "init", nil)
			if err != nil {
				return err
			}
			// Reinitialization uses the already-reserved exact generation/revision.
			// Config changes must use the existing explicit lifecycle publication path.
			to := prepared.from
			if run.Generation != to.Generation || run.ConfigRevision != to.Revision {
				return bad("PLUGIN_SNAPSHOT_STALE", "插件配置已变化")
			}
			if err = prepared.publishTx(ctx, tx, to, run.ID); err != nil {
				return err
			}
			a.pluginOriginalInitializingRuns.Store(run.ID, call)
			a.pluginStore.originalInitializingRuns.Store(run.ID, call)
			err = tx.Commit()
			if err != nil {
				a.pluginOriginalInitializingRuns.Delete(run.ID)
				a.pluginStore.originalInitializingRuns.Delete(run.ID)
				return err
			}
			committed = true
			return nil
		}()
		resolveErr := resolveScrapeFileCleanPrepared(prepared, committed)
		return run, true, errors.Join(err, resolveErr)
	}
	return PluginRun{}, false, nil
}
