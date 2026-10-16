package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"
)

type pluginRefreshWait struct{ Until time.Time }

func (*pluginRefreshWait) Error() string {
	return "等待媒体库刷新插件处理当前整理批次后核对入库"
}

// The installed refresh plugin owns scan timing; core still proves visibility.
// An accepted scan receipt is independent of SDK exit success and is reusable
// only for this import event, asset identity/path and configured server.
func (a *App) pluginRefreshGate(ctx context.Context, asset Asset) (bool, string, error) {
	var eventID string
	var sequence int64
	err := a.Store.DB.QueryRowContext(ctx, `SELECT seq,event_id FROM domain_events WHERE json_extract(payload,'$.asset_id')=? AND json_extract(payload,'$.event_type')='import.completed' ORDER BY seq DESC LIMIT 1`, asset.ID).Scan(&sequence, &eventID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, "", err
	}
	installations, err := a.pluginStore.Installations(ctx)
	if err != nil {
		return false, "", err
	}
	owned, waiting := false, false
	until := time.Now().Add(5 * time.Second)
	for _, installation := range installations {
		if installation.PluginID != "MediaServerRefresh" || !installation.DesiredEnabled {
			continue
		}
		owned = true
		config, err := a.mediaRefreshConfig(ctx, installation.InstanceID)
		if err != nil {
			return true, "", err
		}
		if !config.Enabled || !slices.Contains(config.MediaServers, "fnos") || eventID == "" {
			continue
		}
		var batchState string
		var runState, operationState sql.NullString
		var due int64
		err = a.Store.DB.QueryRowContext(ctx, `SELECT b.state,b.due_at,r.state,o.state FROM plugin_event_deliveries d JOIN plugin_event_batches b ON b.id=d.batch_id LEFT JOIN plugin_runs r ON r.id=b.run_id LEFT JOIN plugin_media_refresh_operations o ON o.run_id=r.id WHERE d.instance_id=? AND d.handler_key='refresh' AND d.event_id=?`, installation.InstanceID, eventID).Scan(&batchState, &due, &runState, &operationState)
		if errors.Is(err, sql.ErrNoRows) {
			var active bool
			var cursor int64
			err = a.Store.DB.QueryRowContext(ctx, `SELECT active,cursor FROM plugin_event_subscriptions WHERE instance_id=? AND handler_key='refresh'`, installation.InstanceID).Scan(&active, &cursor)
			if err == nil && cursor >= sequence {
				continue
			} // Before enable: no history replay.
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return true, "", err
			}
			var pending int
			if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND state IN ('pending','running') AND action IN ('init','reload')`, installation.InstanceID).Scan(&pending); err != nil {
				return true, "", err
			}
			if active || pending > 0 {
				waiting = true
				continue
			}
			return true, "", bad("REVIEW_PLUGIN_REFRESH_INIT", "媒体库刷新插件尚未成功注册事件，请检查插件执行记录")
		}
		if err != nil {
			return true, "", err
		}
		if operationState.String == "confirmed" {
			continue
		} // Validate its receipt below.
		if batchState == "canceled" {
			continue
		}
		if batchState == "collecting" || runState.String == "pending" || runState.String == "running" {
			waiting = true
			if time.UnixMilli(due).After(until) {
				until = time.UnixMilli(due).Add(time.Second)
			}
			continue
		}
		if runState.String == "failed" || runState.String == "reconcile_pending" {
			// A failed/uncertain scan must not be resent by core. Read-only
			// visibility proof may still succeed after the server's own scan;
			// that never changes the failed plugin execution into success.
			continue
		}
	}
	if eventID != "" {
		var runID, instanceID, confirmedAt string
		var sealed []byte
		err = a.Store.DB.QueryRowContext(ctx, `SELECT o.run_id,o.instance_id,o.updated_at,i.digest FROM plugin_media_refresh_operations o JOIN plugin_media_refresh_items i ON i.run_id=o.run_id JOIN plugin_event_batches b ON b.run_id=o.run_id JOIN plugin_event_deliveries d ON d.batch_id=b.id WHERE i.asset_id=? AND d.event_id=? AND o.state='confirmed' AND o.service_fingerprint=? ORDER BY o.updated_at DESC LIMIT 1`, asset.ID, eventID, mediaRefreshFingerprint(a.Settings().FNOS)).Scan(&runID, &instanceID, &confirmedAt, &sealed)
		if err == nil {
			digest, err := a.pluginStore.unseal("media_refresh_item", instanceID, runID+":"+asset.ID, sealed)
			if err != nil {
				return owned, "", err
			}
			when, _ := time.Parse(time.RFC3339Nano, confirmedAt)
			if bytes.Equal(digest, mediaRefreshAssetDigest(asset)) && (owned || (!when.IsZero() && time.Since(when) < 2*time.Minute)) {
				return true, confirmedAt, nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return owned, "", err
		}
	}
	if waiting {
		return true, "", &pluginRefreshWait{Until: until}
	}
	return owned, "", nil
}

func (a *App) wakeMediaVerification(ctx context.Context) {
	_, _ = a.Store.DB.ExecContext(ctx, `UPDATE jobs SET run_at=MIN(run_at,?) WHERE kind='fnos_sync' AND state='retry_wait' AND error_code='PLUGIN_REFRESH_PENDING'`, time.Now().Unix())
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
}
