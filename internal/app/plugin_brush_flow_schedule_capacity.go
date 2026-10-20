package app

import (
	"bytes"
	"context"
	"database/sql"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/schedule"
	"time"
)

type brushFlowScheduleProjectionHost struct {
	bf.Host
	at time.Time
}

func (h brushFlowScheduleProjectionHost) Now() time.Time                    { return h.at }
func (brushFlowScheduleProjectionHost) Log(context.Context, string, string) {}

// The source-sized native Brush service set must not consume another plugin's
// generic quota. Exemption comes from its authenticated current owner/config and
// exact derived definitions, never from a row's self-declared handler or group.
func (p *PluginStore) brushFlowExemptScheduleCountTx(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, e := tx.QueryContext(ctx, `SELECT i.instance_id,i.generation,v.revision,v.sealed FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.installed=1 AND i.desired_enabled=1 AND i.plugin_id='BrushFlowLanduo' AND i.version='5.2.15' AND EXISTS(SELECT 1 FROM plugin_schedules s WHERE s.instance_id=i.instance_id AND s.active=1)`)
	if e != nil {
		return 0, e
	}
	type owner struct {
		instance             string
		generation, revision int64
		configSealed         []byte
	}
	owners := []owner{}
	for rows.Next() {
		var o owner
		if e = rows.Scan(&o.instance, &o.generation, &o.revision, &o.configSealed); e != nil {
			rows.Close()
			return 0, e
		}
		owners = append(owners, o)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	count := 0
	zone, e := autoBackupHostTimezone()
	if e != nil {
		return 0, e
	}
	location, e := time.LoadLocation(zone)
	if e != nil {
		return 0, e
	}
	at := time.Now().In(location)
	for _, o := range owners {
		s, _, err := p.brushFlowReadCurrent(ctx, tx, o.instance, o.generation)
		if err != nil {
			return 0, err
		}
		if s.AppliedRevision != o.revision {
			return 0, brushFlowEvidenceError()
		}
		currentConfig, err := p.unseal("config", o.instance, "main", o.configSealed)
		if err != nil || !bytes.Equal(currentConfig, s.Config) {
			return 0, brushFlowEvidenceError()
		}

		cfg, err := brushFlowConfig(s.Config)
		if err != nil {
			return 0, err
		}
		applied, err := bf.LoadApplied(cfg)
		if err != nil {
			return 0, err
		}
		services := bf.NewReadProjection(brushFlowScheduleProjectionHost{at: at}, applied).Services(ctx)
		expected := map[string]string{}
		for _, service := range services {
			spec := schedule.Spec{Kind: "interval", Timezone: zone, IntervalMS: int64(service.Minutes) * 60000}
			if service.Cron != "" {
				spec = schedule.Spec{Kind: "cron", Timezone: zone, Cron: service.Cron}
			}
			definition := pluginScheduleDefinition{ID: service.ID, Name: service.Name, Handler: "brushflow", Trigger: spec, Input: mustCleanSeedJSON(map[string]string{"task_id": service.TaskID, "operation": service.Operation}), MisfireGraceMS: 1000}
			expected[service.ID] = tokenHash(string(mustCleanSeedJSON(definition)))
		}
		rows, err := tx.QueryContext(ctx, `SELECT service_id,group_key,generation,config_revision,definition,definition_digest FROM plugin_schedules WHERE instance_id=? AND active=1`, o.instance)
		if err != nil {
			return 0, err
		}
		seen := map[string]bool{}
		for rows.Next() {
			var id, group, digest string
			var generation, revision int64
			var sealed []byte
			if err = rows.Scan(&id, &group, &generation, &revision, &sealed, &digest); err != nil {
				rows.Close()
				return 0, err
			}
			raw, err := p.unseal("schedule", o.instance, id, sealed)
			var declaration pluginScheduleDefinition
			if err != nil || group != "brushflow" || generation != o.generation || revision != o.revision || seen[id] || expected[id] == "" || tokenHash(string(raw)) != digest || cleanupJournalStrict(raw, &declaration) != nil || declaration.ID != id {
				rows.Close()
				return 0, brushFlowEvidenceError()
			}
			// StartAt is the original interval registration anchor retained across
			// restarts, not a new time chosen during this capacity audit.
			trigger, err := schedule.New(declaration.Trigger, at)
			if err != nil || !bytes.Equal(mustCleanSeedJSON(trigger.Spec()), mustCleanSeedJSON(declaration.Trigger)) {
				rows.Close()
				return 0, brushFlowEvidenceError()
			}
			if declaration.Trigger.Kind == "interval" {
				declaration.Trigger.StartAt = ""
			}
			if tokenHash(string(mustCleanSeedJSON(declaration))) != expected[id] {
				rows.Close()
				return 0, brushFlowEvidenceError()
			}
			seen[id] = true
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		if len(seen) != len(expected) {
			return 0, brushFlowEvidenceError()
		}
	}
	return count, nil
}
