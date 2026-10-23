package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// This is the existing central scheduler's bound callback, authenticated from
// its real occurrence and run. It is not a private native scheduler receipt.
func (a *App) originalServiceInvocation(ctx context.Context, run PluginRun) (*originalPluginInstance, pluginScheduleDefinition, string, bool, error) {
	return a.pluginStore.originalServiceInvocationQuery(ctx, a.Store.DB, run, "running", "running")
}
func originalServiceOccurrence(run PluginRun) string {
	var value struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if json.Unmarshal(run.Input, &value) != nil {
		return ""
	}
	var id string
	if raw, ok := value.Input["occurrence_id"]; ok {
		_ = json.Unmarshal(raw, &id)
	}
	if id == "" {
		if raw, ok := value.Input["event_id"]; ok {
			_ = json.Unmarshal(raw, &id)
		}
	}
	return id
}
func (p *PluginStore) originalServiceInvocationQuery(ctx context.Context, q pluginOrganizationReader, run PluginRun, occurrenceState, runState string) (*originalPluginInstance, pluginScheduleDefinition, string, bool, error) {
	var definition pluginScheduleDefinition
	if run.Action != "service" {
		return nil, definition, "", false, nil
	}
	occurrence := originalServiceOccurrence(run)
	if !pluginStorageID.MatchString(occurrence) {
		return nil, definition, "", false, nil
	}
	var service, group, digest, actualOccurrence, actualRun, plugin, version string
	var sealed, persisted []byte
	var generation, revision, currentGeneration int64
	var active bool
	err := q.QueryRowContext(ctx, `SELECT s.service_id,s.group_key,s.definition,s.definition_digest,s.active,o.state,o.generation,o.config_revision,r.state,r.input,i.plugin_id,i.version,i.generation FROM plugin_schedule_occurrences o JOIN plugin_schedules s ON s.instance_id=o.instance_id AND s.service_id=o.service_id JOIN plugin_runs r ON r.id=o.run_id JOIN plugin_installations i ON i.instance_id=o.instance_id WHERE o.id=? AND o.run_id=? AND o.instance_id=? AND s.generation=o.generation AND s.config_revision=o.config_revision`, occurrence, run.ID, run.InstanceID).Scan(&service, &group, &sealed, &digest, &active, &actualOccurrence, &generation, &revision, &actualRun, &persisted, &plugin, &version, &currentGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, definition, occurrence, false, nil
	}
	if err != nil {
		return nil, definition, occurrence, true, err
	}
	if group != "original_services" {
		return nil, definition, occurrence, false, nil
	}
	denied := bad("PLUGIN_SCHEDULE_SCOPE_DENIED", "原插件定时调用绑定已失效")
	if !active || actualOccurrence != occurrenceState || actualRun != runState || generation != run.Generation || revision != run.ConfigRevision || currentGeneration != generation || version != run.Version {
		return nil, definition, occurrence, true, denied
	}
	actual, err := p.unseal("run", run.InstanceID, run.ID, persisted)
	if err != nil || !bytes.Equal(actual, run.Input) {
		return nil, definition, occurrence, true, denied
	}
	raw, err := p.unseal("schedule", run.InstanceID, service, sealed)
	if err != nil || !pluginStrictJSON(raw, &definition) || definition.ID != service || digest != tokenHash(string(raw)) {
		return nil, definition, occurrence, true, denied
	}
	v, ok := p.originalScheduledObjects.Load(originalServiceKey{run.InstanceID, service})
	if !ok {
		return nil, definition, occurrence, true, denied
	}
	binding := v.(originalServiceBinding)
	if !originalObjectStillLoaded(binding.object) || binding.digest != digest || binding.object.installation.PluginID != plugin || binding.object.installation.Generation != generation || binding.object.installation.Version != version {
		return nil, definition, occurrence, true, denied
	}
	return binding.object, definition, occurrence, true, nil
}
