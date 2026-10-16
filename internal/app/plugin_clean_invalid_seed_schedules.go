package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
	"light-media/internal/plugins/schedule"
)

// Called only by the existing scheduler's enqueue transaction. The caller
// inserts its real occurrence row and advances next_due in that same tx.
func (p *PluginStore) enqueueCleanInvalidSeedScheduleTx(ctx context.Context, tx *sql.Tx, instance string, definition pluginScheduleDefinition, at time.Time, occurrenceID string, manual bool) (PluginRun, bool, error) {
	if definition.Handler != "clean_invalid_seed" {
		return PluginRun{}, false, nil
	}
	var generation, revision int64
	var plugin, version string
	var installed, enabled bool
	err := tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, instance).Scan(&plugin, &version, &generation, &installed, &enabled, &revision)
	if err != nil {
		return PluginRun{}, true, err
	}
	if plugin != "CleanInvalidSeed" || version != "2.1" || !installed || !enabled || !pluginStorageID.MatchString(occurrenceID) {
		return PluginRun{}, true, seedhost.ErrUnsafe
	}
	var input struct {
		ID string `json:"source_plan_id"`
	}
	if cleanupJournalStrict(definition.Input, &input) != nil || input.ID == "" || definition.ID != cleanSeedServiceID(input.ID) || definition.MisfireGraceMS != 1000 {
		return PluginRun{}, true, seedhost.ErrUnsafe
	}
	j := &pluginCleanInvalidSeedJournal{p: p, instance: instance, generation: generation}
	var run PluginRun
	err = j.updateTx(ctx, tx, func(s *seedhost.State, m *pluginCleanInvalidSeedMeta) error {
		if m.Transition != nil && m.Transition.Phase != "applied" {
			return seedhost.ErrUnknown
		}
		binding := seedhost.Binding{InstanceID: instance, Generation: generation, Revision: revision}
		plan, ok := s.Triggers[input.ID]
		if !ok || plan.Binding != binding || plan.Phase != "ready" || (plan.Kind != "once" && plan.Kind != "service") {
			return seedhost.ErrUnsafe
		}
		if plan.Kind == "once" {
			if (!manual && !plan.Due.Equal(at)) || definition.Trigger.Kind != "date" || definition.Trigger.RunAt != schedule.FormatTime(plan.Due) {
				return seedhost.ErrUnsafe
			}
		} else {
			if plan.Spec == nil || string(mustCleanSeedJSON(*plan.Spec)) != string(mustCleanSeedJSON(definition.Trigger)) || (!manual && at.Before(plan.Due)) {
				return seedhost.ErrUnsafe
			}
			trigger, err := schedule.New(*plan.Spec, plan.Due)
			if err != nil || (!manual && !trigger.First(at).Equal(at)) {
				return seedhost.ErrUnsafe
			}
		}
		eventID := id("cse_")
		raw, _ := json.Marshal(map[string]string{"event_id": eventID})
		var err error
		run, err = p.enqueueRunTx(ctx, tx, instance, "event", raw)
		if err != nil {
			return err
		}
		inv, err := cleanSeedInvocation(run)
		if err != nil {
			return err
		}
		kind, action := plan.Kind, plan.Action
		if manual {
			kind, action = "manual_service", "clean_invalid_seed"
		}
		s.Triggers[eventID] = seedhost.Trigger{ID: eventID, Kind: kind, Action: action, Phase: "queued", Binding: binding, Due: at, QueuedRunID: run.ID, Queued: &inv, OccurrenceID: occurrenceID}
		return nil
	})
	return run, true, err
}

func (p *PluginStore) enqueueCleanInvalidSeedCommand(ctx context.Context, instance string, event map[string]any) (PluginRun, error) {
	action, _ := event["action"].(string)
	if !map[string]bool{"detect_invalid_torrents": true, "delete_invalid_torrents": true, "detect_invalid_files": true, "delete_invalid_files": true, "toggle_notify_all": true}[action] {
		return PluginRun{}, seedhost.ErrUnsafe
	}
	for k := range event {
		if k != "action" && k != "channel" && k != "user" {
			return PluginRun{}, seedhost.ErrUnsafe
		}
	}
	if raw, err := pluginJSON(event, true); err != nil || len(raw) > pluginValueLimit {
		return PluginRun{}, seedhost.ErrUnsafe
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginRun{}, err
	}
	defer tx.Rollback()
	var generation, revision int64
	var plugin, version string
	var installed, enabled bool
	err = tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, instance).Scan(&plugin, &version, &generation, &installed, &enabled, &revision)
	if err != nil {
		return PluginRun{}, err
	}
	if plugin != "CleanInvalidSeed" || version != "2.1" || !installed || !enabled {
		return PluginRun{}, seedhost.ErrUnsafe
	}
	j := &pluginCleanInvalidSeedJournal{p: p, instance: instance, generation: generation}
	var run PluginRun
	err = j.updateTx(ctx, tx, func(s *seedhost.State, m *pluginCleanInvalidSeedMeta) error {
		binding := seedhost.Binding{InstanceID: instance, Generation: generation, Revision: revision}
		if s.Binding != binding || m.Transition != nil && m.Transition.Phase != "applied" {
			return seedhost.ErrUnknown
		}
		eventID := id("csc_")
		input, _ := json.Marshal(map[string]string{"event_id": eventID})
		var err error
		run, err = p.enqueueRunTx(ctx, tx, instance, "event", input)
		if err != nil {
			return err
		}
		inv, err := cleanSeedInvocation(run)
		if err != nil {
			return err
		}
		// Copy via the exact typed JSON codec: callers cannot later mutate the
		// trusted event map that became this immutable source command.
		var copied map[string]any
		if cleanupJournalStrict(mustCleanSeedJSON(event), &copied) != nil {
			return seedhost.ErrUnsafe
		}
		s.Triggers[eventID] = seedhost.Trigger{ID: eventID, Kind: "command", Phase: "queued", Binding: binding, QueuedRunID: run.ID, Queued: &inv, Command: copied}
		return nil
	})
	if err != nil {
		return PluginRun{}, err
	}
	return run, tx.Commit()
}

func cleanSeedClaimOccurrenceTx(ctx context.Context, tx *sql.Tx, s seedhost.State, inv seedhost.Invocation) error {
	if inv.EventID == "" {
		return nil
	}
	t, ok := s.Triggers[inv.EventID]
	if !ok || t.Phase != "queued" || t.Binding != inv.Binding || t.QueuedRunID != inv.RunID || t.Queued == nil || *t.Queued != inv {
		return seedhost.ErrUnsafe
	}
	if t.Kind == "command" {
		if t.OccurrenceID != "" {
			return seedhost.ErrUnsafe
		}
		return nil
	}
	if t.Kind != "once" && t.Kind != "service" && t.Kind != "manual_service" || !pluginStorageID.MatchString(t.OccurrenceID) {
		return seedhost.ErrUnsafe
	}
	var due, state, service string
	var grace int64
	var manual bool
	err := tx.QueryRowContext(ctx, `SELECT o.scheduled_at,o.state,o.misfire_grace_ms,o.service_id,o.manual FROM plugin_schedule_occurrences o JOIN plugin_schedules d ON d.instance_id=o.instance_id AND d.service_id=o.service_id WHERE o.id=? AND o.run_id=? AND o.instance_id=? AND o.generation=? AND o.config_revision=? AND d.group_key='clean_seed' AND d.active=1 AND d.generation=o.generation AND d.config_revision=o.config_revision`, t.OccurrenceID, inv.RunID, inv.Binding.InstanceID, inv.Binding.Generation, inv.Binding.Revision).Scan(&due, &state, &grace, &service, &manual)
	if err != nil {
		return err
	}
	if due != schedule.FormatTime(t.Due) || state != "queued" || grace != 1000 {
		return seedhost.ErrUnsafe
	}
	if manual != (t.Kind == "manual_service") || manual && t.Action != "clean_invalid_seed" {
		return seedhost.ErrUnsafe
	}
	_, err = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state='running' WHERE id=? AND state='queued'`, t.OccurrenceID)
	return err
}
