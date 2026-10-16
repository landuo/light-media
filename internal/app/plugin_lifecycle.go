package app

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The caller holds pluginLifecycleMu. Original MediaServerRefresh.stop_service
// ends its delay immediately; drain captured work using the old config before
// committing a replacement config/uninstall. No network call holds a DB tx.
func (a *App) drainMediaRefresh(parent context.Context, installation PluginInstallation) (returnErr error) {
	if installation.PluginID != "MediaServerRefresh" || !installation.DesiredEnabled {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	a.pluginMu.Lock()
	consumers, err := a.pluginStore.activeEventSubscriptions(ctx)
	if err != nil {
		a.pluginMu.Unlock()
		return err
	}
	selected := []pluginEventSubscription{}
	for _, consumer := range consumers {
		if consumer.InstanceID == installation.InstanceID {
			selected = append(selected, consumer)
		}
	}
	var highWater int64
	err = a.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM domain_events`).Scan(&highWater)
	a.pluginMu.Unlock()
	if err != nil {
		return err
	}
	defer func() {
		if returnErr == nil {
			return
		}
		restore, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		for _, consumer := range selected {
			_, _ = a.Store.DB.ExecContext(restore, `UPDATE plugin_event_subscriptions SET active=1 WHERE instance_id=? AND handler_key=? AND generation=? AND config_revision=?`, consumer.InstanceID, consumer.Handler, consumer.Generation, consumer.ConfigRevision)
		}
	}()
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return bad("PLUGIN_DRAIN_TIMEOUT", "插件待刷新内容尚未收尾，配置未切换，请重试")
		}
		a.pluginMu.Lock()
		caughtUp := true
		var collectErr error
		for _, consumer := range selected {
			var cursor int64
			if err := a.Store.DB.QueryRowContext(ctx, `SELECT cursor FROM plugin_event_subscriptions WHERE instance_id=? AND handler_key=?`, consumer.InstanceID, consumer.Handler).Scan(&cursor); err != nil {
				collectErr = err
				break
			}
			if cursor < highWater {
				if err := a.pluginStore.collectPluginEventsUntil(ctx, consumer, time.Now(), highWater); err != nil {
					collectErr = err
				}
				caughtUp = false
			}
		}
		if caughtUp && collectErr == nil {
			_, collectErr = a.Store.DB.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id=?`, installation.InstanceID)
		}
		if collectErr == nil || isPluginQueueFull(collectErr) {
			_, err = a.Store.DB.ExecContext(ctx, `UPDATE plugin_event_batches SET due_at=0 WHERE instance_id=? AND generation=? AND run_id IS NULL AND state='collecting'`, installation.InstanceID, installation.Generation)
			if err == nil {
				err = a.pluginStore.enqueueDuePluginEventsFor(ctx, time.Now(), installation.InstanceID)
			}
		} else {
			err = collectErr
		}
		a.pluginMu.Unlock()
		if err != nil && !isPluginQueueFull(err) {
			return err
		}
		run, claimErr := a.pluginStore.claimRunForInstance(ctx, installation.InstanceID)
		if claimErr == nil {
			a.executePluginRun(ctx, run)
			continue
		}
		if !errors.Is(claimErr, sql.ErrNoRows) {
			return claimErr
		}
		var pending int
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND state IN ('pending','running'))+(SELECT COUNT(*) FROM plugin_event_batches WHERE instance_id=? AND generation=? AND run_id IS NULL AND state='collecting')`, installation.InstanceID, installation.InstanceID, installation.Generation).Scan(&pending); err != nil {
			return err
		}
		if caughtUp && pending == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	}
}

func isPluginQueueFull(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Code == "PLUGIN_QUEUE_FULL"
}

func (a *App) stopMediaRefreshPlugins() {
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.pluginStore.recoverMediaRefreshRuns(ctx); err != nil {
		return
	}
	installations, err := a.pluginStore.Installations(ctx)
	if err != nil {
		return
	}
	for _, installation := range installations {
		if err := a.drainMediaRefresh(ctx, installation); err != nil {
			a.log("ERROR", "plugins", "shutdown_refresh_pending", "媒体库刷新收尾尚未完成，状态已保留供重启核对", "", "")
		}
	}
}
