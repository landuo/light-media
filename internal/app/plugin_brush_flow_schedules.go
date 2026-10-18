package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/schedule"
	"time"
)

func (c *brushFlowCall) writeSchedulesTx(ctx context.Context, tx *sql.Tx, revision int64, config bf.Row) error {
	if len(c.services) > 1000 {
		return bad("INVALID_PLUGIN_SCHEDULE", "刷流任务定时服务超出上限")
	}
	zone, e := autoBackupHostTimezone()
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	at := c.o.Now()
	for _, service := range c.services {
		if !pluginServiceID.MatchString(service.ID) || seen[service.ID] {
			return brushFlowEvidenceError()
		}
		seen[service.ID] = true
		spec := schedule.Spec{Kind: "interval", Timezone: zone, IntervalMS: int64(service.Minutes) * 60000}
		if service.Cron != "" {
			spec = schedule.Spec{Kind: "cron", Cron: service.Cron, Timezone: zone}
		}
		trigger, e := schedule.New(spec, at)
		if e != nil {
			return e
		}
		definition := pluginScheduleDefinition{ID: service.ID, Name: service.Name, Handler: "brushflow", Trigger: trigger.Spec(), Input: mustCleanSeedJSON(map[string]string{"task_id": service.TaskID, "operation": service.Operation}), MisfireGraceMS: 1000}
		raw := mustCleanSeedJSON(definition)
		digest := tokenHash(string(raw))
		sealed, e := c.o.a.pluginStore.seal("schedule", c.o.instance, service.ID, raw)
		if e != nil {
			return e
		}
		var oldGroup, oldDigest string
		var oldGeneration, oldRevision int64
		e = tx.QueryRowContext(ctx, `SELECT group_key,generation,config_revision,definition_digest FROM plugin_schedules WHERE instance_id=? AND service_id=?`, c.o.instance, service.ID).Scan(&oldGroup, &oldGeneration, &oldRevision, &oldDigest)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil && oldGroup != "brushflow" {
			return brushFlowEvidenceError()
		}
		if e == nil && oldGeneration == c.o.generation && oldRevision == revision && oldDigest == digest {
			if _, e = tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=1,updated_at=? WHERE instance_id=? AND service_id=?`, now(), c.o.instance, service.ID); e != nil {
				return e
			}
			continue
		}
		var due any
		if first := trigger.First(at); !first.IsZero() {
			due = schedule.FormatTime(first)
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_schedules VALUES(?,?,?,?,?,?,?,1,?,?) ON CONFLICT(instance_id,service_id) DO UPDATE SET group_key=excluded.group_key,generation=excluded.generation,config_revision=excluded.config_revision,definition=excluded.definition,definition_digest=excluded.definition_digest,active=1,next_due=excluded.next_due,updated_at=excluded.updated_at`, c.o.instance, service.ID, "brushflow", c.o.generation, revision, sealed, digest, due, now()); e != nil {
			return e
		}
	}
	rows, e := tx.QueryContext(ctx, `SELECT service_id FROM plugin_schedules WHERE instance_id=? AND group_key='brushflow' AND active=1`, c.o.instance)
	if e != nil {
		return e
	}
	obsolete := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		if !seen[id] {
			obsolete = append(obsolete, id)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range obsolete {
		if _, e = tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0,updated_at=? WHERE instance_id=? AND service_id=?`, now(), c.o.instance, id); e != nil {
			return e
		}
	}
	var total int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_schedules WHERE active=1`).Scan(&total); e != nil {
		return e
	}
	if total > 10000 {
		return bad("PLUGIN_SCHEDULE_CAPACITY", "插件定时服务数量已达上限")
	}
	return nil
}
func (p *PluginStore) enqueueBrushFlowScheduleTx(ctx context.Context, tx *sql.Tx, instance string, def pluginScheduleDefinition, at time.Time, occurrence string, manual bool) (PluginRun, bool, error) {
	if def.Handler != "brushflow" {
		return PluginRun{}, false, nil
	}
	var plugin, version string
	var gen int64
	if e := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=? AND installed=1 AND desired_enabled=1`, instance).Scan(&plugin, &version, &gen); e != nil {
		return PluginRun{}, true, e
	}
	if plugin != bf.PluginID || version != bf.Version || def.MisfireGraceMS != 1000 {
		return PluginRun{}, true, brushFlowEvidenceError()
	}
	s, _, e := p.brushFlowRead(ctx, tx, instance, gen)
	if e != nil {
		return PluginRun{}, true, e
	}
	if e = brushFlowNoUnknown(s); e != nil {
		return PluginRun{}, true, e
	}
	if _, e = brushFlowDefinitionOperation(s.Config, def); e != nil {
		return PluginRun{}, true, e
	}
	run, e := p.enqueueRunTx(ctx, tx, instance, "service", mustCleanSeedJSON(map[string]string{"event_id": occurrence}))
	return run, true, e
}
func brushFlowDefinitionOperation(config json.RawMessage, def pluginScheduleDefinition) (brushFlowQueued, error) {
	var token struct {
		Task      string `json:"task_id"`
		Operation string `json:"operation"`
	}
	if def.Handler != "brushflow" || cleanupJournalStrict(def.Input, &token) != nil {
		return brushFlowQueued{}, brushFlowEvidenceError()
	}
	var cfg bf.Row
	if json.Unmarshal(config, &cfg) != nil || cfg["enabled"] != true {
		return brushFlowQueued{}, brushFlowEvidenceError()
	}
	for _, t := range brushFlowTasks(cfg) {
		if t.ID() != token.Task || !t.B("enabled") {
			continue
		}
		suffix := ""
		switch token.Operation {
		case "brush":
			suffix = "Brush"
		case "check":
			suffix = "Check"
		case "promotion_expiry":
			if !t.B("del_no_free") {
				return brushFlowQueued{}, brushFlowEvidenceError()
			}
			suffix = "PromotionExpiry"
		default:
			return brushFlowQueued{}, brushFlowEvidenceError()
		}
		if def.ID != "Task_"+t.ID()+"_"+suffix {
			return brushFlowQueued{}, brushFlowEvidenceError()
		}
		return brushFlowQueued{t.ID(), token.Operation, nil}, nil
	}
	return brushFlowQueued{}, brushFlowEvidenceError()
}
func (c *brushFlowCall) readScheduleOperation(ctx context.Context) (brushFlowQueued, error) {
	tx, e := c.o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return brushFlowQueued{}, e
	}
	defer tx.Rollback()
	var inv plugins.Invocation
	var token struct {
		ID string `json:"event_id"`
	}
	if cleanupJournalStrict(c.run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &token) != nil {
		return brushFlowQueued{}, brushFlowEvidenceError()
	}
	var raw []byte
	var service string
	e = tx.QueryRowContext(ctx, `SELECT s.service_id,s.definition FROM plugin_schedule_occurrences o JOIN plugin_schedules s ON s.instance_id=o.instance_id AND s.service_id=o.service_id WHERE o.id=? AND o.run_id=? AND o.instance_id=? AND o.generation=? AND o.config_revision=? AND o.state='running' AND s.active=1 AND s.generation=o.generation AND s.config_revision=o.config_revision AND s.group_key='brushflow'`, token.ID, c.run.ID, c.o.instance, c.o.generation, c.run.ConfigRevision).Scan(&service, &raw)
	if e != nil {
		return brushFlowQueued{}, e
	}
	clear, e := c.o.a.pluginStore.unseal("schedule", c.o.instance, service, raw)
	if e != nil {
		return brushFlowQueued{}, e
	}
	var def pluginScheduleDefinition
	if cleanupJournalStrict(clear, &def) != nil {
		return brushFlowQueued{}, brushFlowEvidenceError()
	}
	return brushFlowDefinitionOperation(mustCleanSeedJSON(c.config), def)
}
func (p *PluginStore) claimBrushFlowScheduleTx(ctx context.Context, tx *sql.Tx, run PluginRun, at time.Time) (string, error) {
	s, _, e := p.brushFlowRead(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return "", e
	}
	if e = brushFlowNoUnknown(s); e != nil {
		return "BRUSH_FLOW_BLOCKED_BY_UNKNOWN", nil
	}
	if run.Action == "task" {
		for _, source := range s.Sources {
			if source.Run == run.ID && source.Revision == run.ConfigRevision {
				return "", nil
			}
		}
		return "PLUGIN_SCHEDULE_SCOPE_DENIED", nil
	}
	var inv plugins.Invocation
	var token struct {
		ID string `json:"event_id"`
	}
	if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &token) != nil {
		return "PLUGIN_SCHEDULE_SCOPE_DENIED", nil
	}
	var due string
	var grace int64
	var manual bool
	e = tx.QueryRowContext(ctx, `SELECT o.scheduled_at,o.misfire_grace_ms,o.manual FROM plugin_schedule_occurrences o JOIN plugin_schedules s ON s.instance_id=o.instance_id AND s.service_id=o.service_id WHERE o.id=? AND o.run_id=? AND o.instance_id=? AND o.generation=? AND o.config_revision=? AND o.state='queued' AND s.active=1 AND s.generation=o.generation AND s.config_revision=o.config_revision AND s.group_key='brushflow'`, token.ID, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&due, &grace, &manual)
	if errors.Is(e, sql.ErrNoRows) {
		return "PLUGIN_SCHEDULE_SCOPE_DENIED", nil
	}
	if e != nil {
		return "", e
	}
	t, e := time.Parse(time.RFC3339Nano, due)
	if e != nil {
		return "PLUGIN_SCHEDULE_CORRUPT", nil
	}
	if !manual && grace != -1 && at.Sub(t) > time.Duration(grace)*time.Millisecond {
		return "PLUGIN_SCHEDULE_MISFIRE", nil
	}
	_, e = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state='running' WHERE id=? AND state='queued'`, token.ID)
	return "", e
}
