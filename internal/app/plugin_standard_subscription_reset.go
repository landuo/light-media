package app

import (
	"context"
	"encoding/json"
)

// Pinned reset_subscribes resets six source runtime fields. Reuse this host's
// revision/job invalidation and transaction, retaining accepted downloads and
// every unrelated subscription editor setting.
func (a *App) resetSubscription(ctx context.Context, key string) (Subscription, error) {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Subscription{}, err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, key).Scan(&raw); err != nil {
		return Subscription{}, err
	}
	var s Subscription
	if err = json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	if s.State == "cancelled" {
		return s, &APIError{"SUBSCRIPTION_NOT_FOUND", "订阅不存在", 404}
	}
	before, snapshotErr := a.subscriptionSourceProjectionReferences(ctx, tx, s, true)
	s.SourceNote = []int{}
	s.CurrentPriority = nil
	s.EpisodePriority = map[string]int{}
	s.ManualTotalEpisode = false
	s.SourceState = "R"
	s.State = "active"
	total := s.TotalEpisodes
	s.LackEpisode = &total
	if s.Media.MediaType == "movie" {
		s.LackEpisode = nil
	}
	s.Revision++
	s.NextCheck = ""
	s.LastReason = "订阅已重置"
	if s.Schedule.Mode != "manual" {
		s.NextCheck = now()
	}
	if err = txPut(ctx, tx, "subscription", key, s); err != nil {
		return s, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE kind='subscription' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, now(), key); err != nil {
		return s, err
	}
	var event json.RawMessage
	if snapshotErr == nil {
		var after json.RawMessage
		after, snapshotErr = a.subscriptionSourceProjectionReferences(ctx, tx, s, true)
		if snapshotErr == nil {
			event, snapshotErr = standardSubscriptionModifiedPayload(key, before, after, "reset")
		}
	}
	if err = tx.Commit(); err != nil {
		return s, err
	}
	if snapshotErr == nil {
		a.emitStandardBusinessEvent(ctx, "subscribe.modified", event)
	} else {
		a.log("ERROR", "plugins", "subscription_event_snapshot_failed", "订阅已重置，但插件事件快照无法核对", "", "")
	}
	return s, nil
}
