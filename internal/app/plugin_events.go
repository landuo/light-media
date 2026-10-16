package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// Each delivery is tied to a committed v1 fact. Cursors, deduplication and
// batch creation commit together; a queue error cannot consume an event.
type pluginEventSubscription struct {
	InstanceID     string
	Handler        string
	Generation     int64
	ConfigRevision int64
	Types          []string
	Delay          time.Duration
}

func (p *PluginStore) configureEventSubscription(ctx context.Context, run PluginRun, handler string, types []string, delay time.Duration, active bool) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = p.configureEventSubscriptionTx(ctx, tx, run, handler, types, delay, active); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *PluginStore) configureEventSubscriptionTx(ctx context.Context, tx *sql.Tx, run PluginRun, handler string, types []string, delay time.Duration, active bool) error {
	if !pluginStorageID.MatchString(handler) || len(types) == 0 || len(types) > 8 || delay < 0 {
		return bad("INVALID_PLUGIN_SUBSCRIPTION", "插件事件订阅参数无效")
	}
	seen := map[string]bool{}
	for _, eventType := range types {
		if !slices.Contains([]string{"download.accepted", "download.completed", "downloadfile.deleted", "import.completed", "scrape.completed", "library.visible", "job.failed", "site.refreshed", "subscription.added"}, eventType) || seen[eventType] {
			return bad("INVALID_PLUGIN_SUBSCRIPTION", "插件事件类型无效或重复")
		}
		seen[eventType] = true
	}
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.installed=1 AND i.desired_enabled=1 AND i.generation=? AND i.version=? AND v.revision=? AND EXISTS(SELECT 1 FROM plugin_runs r WHERE r.id=? AND r.instance_id=i.instance_id AND r.generation=i.generation AND r.config_revision=v.revision AND r.state='running')`, run.InstanceID, run.Generation, run.Version, run.ConfigRevision, run.ID).Scan(&current); err != nil {
		return err
	}
	if current != 1 {
		return bad("PLUGIN_SNAPSHOT_STALE", "插件配置已变化，未注册旧事件订阅")
	}
	var highWater int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM domain_events`).Scan(&highWater); err != nil {
		return err
	}
	var generation, revision, cursor int64
	var wasActive bool
	err := tx.QueryRowContext(ctx, `SELECT generation,config_revision,active,cursor FROM plugin_event_subscriptions WHERE instance_id=? AND handler_key=?`, run.InstanceID, handler).Scan(&generation, &revision, &wasActive, &cursor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err != nil || generation != run.Generation || revision != run.ConfigRevision || !wasActive {
		cursor = highWater // First enable/re-enable never silently replays history.
		var invocation struct {
			Input *struct {
				EventAfter int64 `json:"event_after"`
			} `json:"input"`
		}
		if (run.Action == "init" || run.Action == "reload") && len(run.Input) != 0 {
			if json.Unmarshal(run.Input, &invocation) != nil {
				return bad("PLUGIN_SNAPSHOT_CORRUPT", "插件事件启用边界无法读取")
			}
			if invocation.Input != nil {
				if invocation.Input.EventAfter < 0 || invocation.Input.EventAfter > highWater {
					return bad("PLUGIN_SNAPSHOT_CORRUPT", "插件事件启用边界无效")
				}
				cursor = invocation.Input.EventAfter
			}
		}
	}
	raw, _ := json.Marshal(types)
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_event_subscriptions VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,handler_key) DO UPDATE SET generation=excluded.generation,config_revision=excluded.config_revision,event_types=excluded.event_types,delay_ms=excluded.delay_ms,active=excluded.active,cursor=excluded.cursor`, run.InstanceID, handler, run.Generation, run.ConfigRevision, string(raw), delay.Milliseconds(), active, cursor)
	if err != nil {
		return err
	}
	return nil
}

func (p *PluginStore) activeEventSubscriptions(ctx context.Context) ([]pluginEventSubscription, error) {
	rows, err := p.store.DB.QueryContext(ctx, `SELECT s.instance_id,s.handler_key,s.generation,s.config_revision,s.event_types,s.delay_ms FROM plugin_event_subscriptions s JOIN plugin_installations i ON i.instance_id=s.instance_id JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE s.active=1 AND i.installed=1 AND i.desired_enabled=1 AND i.generation=s.generation AND v.revision=s.config_revision ORDER BY s.instance_id,s.handler_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []pluginEventSubscription{}
	for rows.Next() {
		var item pluginEventSubscription
		var types string
		var delay int64
		if err := rows.Scan(&item.InstanceID, &item.Handler, &item.Generation, &item.ConfigRevision, &types, &delay); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(types), &item.Types) != nil || delay < 0 || delay > (time.Duration(1<<63-1)).Milliseconds() {
			return nil, bad("PLUGIN_SUBSCRIPTION_CORRUPT", "插件事件订阅无法读取")
		}
		item.Delay = time.Duration(delay) * time.Millisecond
		result = append(result, item)
	}
	return result, rows.Err()
}

func (p *PluginStore) collectPluginEvents(ctx context.Context, subscription pluginEventSubscription, at time.Time) error {
	return p.collectPluginEventsUntil(ctx, subscription, at, 1<<63-1)
}

func (p *PluginStore) collectPluginEventsUntil(ctx context.Context, subscription pluginEventSubscription, at time.Time, highWater int64) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cursor int64
	err = tx.QueryRowContext(ctx, `SELECT s.cursor FROM plugin_event_subscriptions s JOIN plugin_installations i ON i.instance_id=s.instance_id JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE s.instance_id=? AND s.handler_key=? AND s.active=1 AND s.generation=? AND s.config_revision=? AND i.installed=1 AND i.desired_enabled=1 AND i.generation=s.generation AND v.revision=s.config_revision`, subscription.InstanceID, subscription.Handler, subscription.Generation, subscription.ConfigRevision).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // A user edit won the race with this dispatcher snapshot.
	}
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq,event_id,payload FROM domain_events WHERE seq>? AND seq<=? ORDER BY seq LIMIT 200`, cursor, highWater)
	if err != nil {
		return err
	}
	events := []DomainEvent{}
	for rows.Next() {
		var sequence int64
		var eventID string
		var raw []byte
		var event DomainEvent
		if err := rows.Scan(&sequence, &eventID, &raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal(raw, &event) != nil || event.EventID != eventID || event.SchemaVersion != 1 || event.OccurredAt == "" {
			rows.Close()
			return bad("DOMAIN_EVENT_CORRUPT", "域事件无法读取，插件游标未推进")
		}
		event, err = validateDomainEvent(event)
		if err != nil {
			rows.Close()
			return err
		}
		event.Sequence = sequence
		events = append(events, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, event := range events {
		cursor = event.Sequence
		if !slices.Contains(subscription.Types, event.Type) {
			continue
		}
		var duplicate int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_event_deliveries WHERE instance_id=? AND handler_key=? AND event_id=?`, subscription.InstanceID, subscription.Handler, event.EventID).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate != 0 {
			continue
		}
		var batchID string
		// Zero delay means one invocation per event, as in the original.
		if subscription.Delay > 0 {
			err = tx.QueryRowContext(ctx, `SELECT b.id FROM plugin_event_batches b WHERE b.instance_id=? AND b.handler_key=? AND b.generation=? AND b.config_revision=? AND b.run_id IS NULL AND b.state='collecting' ORDER BY b.created_at DESC,b.id DESC LIMIT 1`, subscription.InstanceID, subscription.Handler, subscription.Generation, subscription.ConfigRevision).Scan(&batchID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if batchID == "" {
			var pending int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_event_batches WHERE run_id IS NULL AND state='collecting'`).Scan(&pending); err != nil {
				return err
			}
			if pending >= 1000 {
				return bad("PLUGIN_QUEUE_FULL", "插件事件队列已满，未丢弃待投递事件")
			}
			batchID = id("pb_")
			if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_event_batches(id,instance_id,handler_key,generation,config_revision,due_at,created_at) VALUES(?,?,?,?,?,?,?)`, batchID, subscription.InstanceID, subscription.Handler, subscription.Generation, subscription.ConfigRevision, at.Add(subscription.Delay).UnixMilli(), now()); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE plugin_event_batches SET due_at=? WHERE id=?`, at.Add(subscription.Delay).UnixMilli(), batchID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_event_deliveries VALUES(?,?,?,?,?)`, subscription.InstanceID, subscription.Handler, event.EventID, event.Sequence, batchID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_event_batches SET item_count=item_count+1 WHERE id=?`, batchID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET cursor=? WHERE instance_id=? AND handler_key=?`, cursor, subscription.InstanceID, subscription.Handler); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *PluginStore) enqueueDuePluginEvents(ctx context.Context, at time.Time) error {
	return p.enqueueDuePluginEventsFor(ctx, at, "")
}

func (p *PluginStore) enqueueDuePluginEventsFor(ctx context.Context, at time.Time, drainingInstance string) error {
	for count := 0; count < 200; count++ {
		tx, err := p.store.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var batchID, instanceID string
		err = tx.QueryRowContext(ctx, `SELECT b.id,b.instance_id FROM plugin_event_batches b JOIN plugin_event_subscriptions s ON s.instance_id=b.instance_id AND s.handler_key=b.handler_key JOIN plugin_installations i ON i.instance_id=b.instance_id JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE b.run_id IS NULL AND b.state='collecting' AND b.due_at<=? AND (s.active=1 OR b.instance_id=?) AND i.installed=1 AND i.desired_enabled=1 AND i.generation=b.generation AND s.generation=b.generation AND v.revision=b.config_revision AND s.config_revision=b.config_revision ORDER BY b.due_at,b.id LIMIT 1`, at.UnixMilli(), drainingInstance).Scan(&batchID, &instanceID)
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			return nil
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		input, _ := json.Marshal(map[string]string{"batch_id": batchID})
		run, err := p.enqueueRunTx(ctx, tx, instanceID, "event", input)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE plugin_event_batches SET run_id=?,state='queued' WHERE id=? AND run_id IS NULL`, run.ID, batchID)
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

type pluginEventPage struct {
	Events     []DomainEvent
	NextCursor int64
	Complete   bool
	Total      int64
}

// Resolve only a batch attached to this exact running invocation. Plugins never
// supply file paths; the business host adapter resolves these committed asset
// references and rechecks ownership/current configuration before any action.
func (p *PluginStore) pluginBatchEvents(ctx context.Context, run PluginRun, batchID string, after int64, limit int) (pluginEventPage, error) {
	out := pluginEventPage{Events: []DomainEvent{}, NextCursor: after}
	if after < 0 || limit < 1 || limit > 200 {
		return out, bad("INVALID_EVENT_CURSOR", "插件批次游标或每页数量无效")
	}
	err := p.store.DB.QueryRowContext(ctx, `SELECT b.item_count FROM plugin_event_batches b JOIN plugin_runs r ON r.id=b.run_id WHERE b.id=? AND b.run_id=? AND b.instance_id=? AND b.generation=? AND b.config_revision=? AND b.state='queued' AND r.state='running' AND r.instance_id=b.instance_id AND r.generation=b.generation AND r.config_revision=b.config_revision`, batchID, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&out.Total)
	if errors.Is(err, sql.ErrNoRows) {
		return out, bad("PLUGIN_BATCH_SCOPE_DENIED", "插件不能读取其他执行或已停止批次的事件")
	}
	if err != nil {
		return out, err
	}
	if out.Total <= 0 {
		return out, bad("PLUGIN_BATCH_CORRUPT", "插件事件批次数量无效")
	}
	rows, err := p.store.DB.QueryContext(ctx, `SELECT d.event_seq,d.event_id,e.payload FROM plugin_event_deliveries d LEFT JOIN domain_events e ON e.seq=d.event_seq AND e.event_id=d.event_id WHERE d.batch_id=? AND d.instance_id=? AND d.event_seq>? ORDER BY d.event_seq LIMIT ?`, batchID, run.InstanceID, after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int64
		var eventID string
		var raw []byte
		var event DomainEvent
		if err := rows.Scan(&sequence, &eventID, &raw); err != nil {
			return out, err
		}
		if json.Unmarshal(raw, &event) != nil || event.EventID != eventID || event.SchemaVersion != 1 || event.OccurredAt == "" {
			return out, bad("DOMAIN_EVENT_CORRUPT", "插件批次引用的域事件无效")
		}
		event, err = validateDomainEvent(event)
		if err != nil {
			return out, err
		}
		event.Sequence = sequence
		out.Events = append(out.Events, event)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Complete = len(out.Events) <= limit
	if !out.Complete {
		out.Events = out.Events[:limit]
	}
	if len(out.Events) > 0 {
		out.NextCursor = out.Events[len(out.Events)-1].Sequence
	}
	return out, nil
}

func (a *App) dispatchPluginEvents(ctx context.Context, at time.Time) error {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	// Transfer only previously committed effects. No filesystem/downloader or
	// external notification operation runs in this short transaction path.
	nextAudit, outboxError := a.auditRemoveLinkHandoffs(ctx, a.pluginCleanupOutboxAudit, 200)
	if outboxError == nil {
		a.pluginCleanupOutboxAudit = nextAudit
		outboxError = a.dispatchRemoveLinkOutboxes(ctx, 200)
	}
	consumers, err := a.pluginStore.activeEventSubscriptions(ctx)
	if err != nil {
		return err
	}
	var collectionError error
	for _, consumer := range consumers {
		if err := a.pluginStore.collectPluginEvents(ctx, consumer, at); err != nil {
			if collectionError == nil {
				collectionError = err
			}
		}
	}
	// Even if collection hits capacity, due batches must still drain. Otherwise
	// a full collection queue can permanently prevent its own space being freed.
	queueError := a.pluginStore.enqueueDuePluginEvents(ctx, at)
	if outboxError != nil {
		return outboxError
	}
	if collectionError != nil {
		return collectionError
	}
	return queueError
}

func (a *App) pluginEventWorker(ctx context.Context) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-timer.C:
			if err := a.dispatchPluginEvents(ctx, at); err != nil && ctx.Err() == nil {
				a.log("ERROR", "plugins", "event_dispatch_failed", "插件事件投递失败，已保留游标和待处理记录", "", "")
			}
			a.wakePlugins()
		}
	}
}
