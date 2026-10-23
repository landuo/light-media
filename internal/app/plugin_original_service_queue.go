package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var errOriginalServiceStopped = errors.New("original service object stopped")

type originalServiceEnqueueKey struct{}

func originalServiceEnqueueMatches(ctx context.Context, instance, plugin, version string, generation int64, action string) bool {
	object, ok := ctx.Value(originalServiceEnqueueKey{}).(*originalPluginInstance)
	return ok && action == "service" && originalObjectStillLoaded(object) && object.installation.InstanceID == instance && object.installation.PluginID == plugin && object.installation.Version == version && object.installation.Generation == generation
}

// Public callbacks are bound by get_service registration, not by a private
// scheduler's source plan. The sealed declaration and the live object must both
// still match before the ordinary queue can construct the real run.
func (p *PluginStore) enqueueOriginalServiceTx(ctx context.Context, tx *sql.Tx, instance string, definition pluginScheduleDefinition, occurrence string) (PluginRun, bool, error) {
	var group, digest, version, plugin string
	var sealed []byte
	var generation int64
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT s.group_key,s.definition,s.definition_digest,s.generation,s.active,i.plugin_id,i.version FROM plugin_schedules s JOIN plugin_installations i ON i.instance_id=s.instance_id AND i.generation=s.generation WHERE s.instance_id=? AND s.service_id=?`, instance, definition.ID).Scan(&group, &sealed, &digest, &generation, &active, &plugin, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return PluginRun{}, false, nil
	}
	if err != nil {
		return PluginRun{}, true, err
	}
	if group != "original_services" {
		return PluginRun{}, false, nil
	}
	denied := bad("PLUGIN_SCHEDULE_SCOPE_DENIED", "原插件定时回调已失效")
	value, ok := p.originalScheduledObjects.Load(originalServiceKey{instance, definition.ID})
	if !ok || !active {
		return PluginRun{}, true, denied
	}
	binding, ok := value.(originalServiceBinding)
	if ok && !originalObjectStillLoaded(binding.object) {
		return PluginRun{}, true, errOriginalServiceStopped
	}
	if !ok || !originalObjectStillLoaded(binding.object) || binding.digest != digest || binding.object.installation.Generation != generation || binding.object.installation.PluginID != plugin || binding.object.installation.Version != version {
		return PluginRun{}, true, denied
	}
	raw, err := p.unseal("schedule", instance, definition.ID, sealed)
	expected, marshalErr := json.Marshal(definition)
	if err != nil || marshalErr != nil || tokenHash(string(raw)) != digest || tokenHash(string(expected)) != digest {
		return PluginRun{}, true, denied
	}
	key := "occurrence_id"
	if plugin == "CleanInvalidSeed" {
		key = "event_id"
	}
	input, err := json.Marshal(map[string]string{key: occurrence})
	if err != nil {
		return PluginRun{}, true, err
	}
	run, err := p.enqueueRunTx(context.WithValue(ctx, originalServiceEnqueueKey{}, binding.object), tx, instance, "service", input)
	return run, true, err
}

func (p *PluginStore) claimOriginalServiceTx(ctx context.Context, tx *sql.Tx, run PluginRun, at time.Time) (string, error) {
	_, _, occurrence, handled, err := p.originalServiceInvocationQuery(ctx, tx, run, "queued", "pending")
	if err != nil {
		return "", err
	}
	if !handled {
		return "PLUGIN_SCHEDULE_SCOPE_DENIED", nil
	}
	var scheduled string
	var grace int64
	var manual bool
	if err := tx.QueryRowContext(ctx, `SELECT scheduled_at,misfire_grace_ms,manual FROM plugin_schedule_occurrences WHERE id=? AND run_id=? AND state='queued'`, occurrence, run.ID).Scan(&scheduled, &grace, &manual); err != nil {
		return "", err
	}
	due, err := time.Parse(time.RFC3339Nano, scheduled)
	if err != nil || grace < -1 {
		return "PLUGIN_SCHEDULE_CORRUPT", nil
	}
	state, code := "running", ""
	if !manual && grace != -1 && at.Sub(due) > time.Duration(grace)*time.Millisecond {
		state, code = "missed", "PLUGIN_SCHEDULE_MISFIRE"
	}
	_, err = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state=? WHERE id=? AND state='queued'`, state, occurrence)
	return code, err
}
