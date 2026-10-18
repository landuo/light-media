package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/plugins"
)

func (p *PluginStore) enqueueCloudflareScheduleTx(ctx context.Context, tx *sql.Tx, instance string, def pluginScheduleDefinition, at time.Time, occurrence string, manual bool) (PluginRun, bool, error) {
	if def.Handler != "cloudflare" {
		return PluginRun{}, false, nil
	}
	var plugin, version string
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id,version FROM plugin_installations WHERE instance_id=? AND installed=1 AND desired_enabled=1`, instance).Scan(&plugin, &version); err != nil {
		return PluginRun{}, true, err
	}
	if plugin != "CloudflareSpeedTest" || version != "1.5.1" || (def.ID != "CloudflareSpeedTest" && def.ID != "CloudflareSpeedTest.once") || def.MisfireGraceMS != 1000 {
		return PluginRun{}, true, bad("CLOUDFLARE_SCOPE_DENIED", "优选调度不匹配")
	}
	action := "service"
	if def.Trigger.Kind == "date" {
		action = "once"
	}
	input, _ := json.Marshal(map[string]string{"event_id": occurrence})
	run, err := p.enqueueRunTx(ctx, tx, instance, action, input)
	return run, true, err
}
func (p *PluginStore) claimCloudflareScheduleTx(ctx context.Context, tx *sql.Tx, run PluginRun, at time.Time) (string, error) {
	var inv plugins.Invocation
	var token struct {
		ID string `json:"event_id"`
	}
	if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &token) != nil || !pluginStorageID.MatchString(token.ID) {
		return "PLUGIN_SCHEDULE_SCOPE_DENIED", nil
	}
	var due string
	var grace int64
	var manual bool
	err := tx.QueryRowContext(ctx, `SELECT o.scheduled_at,o.misfire_grace_ms,o.manual FROM plugin_schedule_occurrences o JOIN plugin_schedules s ON s.instance_id=o.instance_id AND s.service_id=o.service_id WHERE o.id=? AND o.run_id=? AND o.instance_id=? AND o.generation=? AND o.config_revision=? AND o.state='queued' AND s.active=1 AND s.generation=o.generation AND s.config_revision=o.config_revision`, token.ID, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&due, &grace, &manual)
	if errors.Is(err, sql.ErrNoRows) {
		return "PLUGIN_SCHEDULE_SCOPE_DENIED", nil
	}
	if err != nil {
		return "", err
	}
	t, err := time.Parse(time.RFC3339Nano, due)
	if err != nil {
		return "PLUGIN_SCHEDULE_CORRUPT", nil
	}
	if !manual && grace != -1 && at.Sub(t) > time.Duration(grace)*time.Millisecond {
		return "PLUGIN_SCHEDULE_MISFIRE", nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state='running' WHERE id=? AND state='queued'`, token.ID)
	return "", err
}
func (p *PluginStore) validateCloudflareOccurrence(ctx context.Context, run PluginRun) error {
	var inv plugins.Invocation
	var token struct {
		ID string `json:"event_id"`
	}
	if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &token) != nil {
		return bad("CLOUDFLARE_SCOPE_DENIED", "优选调度输入无效")
	}
	var count int
	err := p.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_schedule_occurrences o JOIN plugin_schedules s ON s.instance_id=o.instance_id AND s.service_id=o.service_id JOIN plugin_installations i ON i.instance_id=o.instance_id WHERE o.id=? AND o.run_id=? AND o.instance_id=? AND o.generation=? AND o.config_revision=? AND o.state='running' AND s.active=1 AND s.generation=o.generation AND s.config_revision=o.config_revision AND i.plugin_id='CloudflareSpeedTest' AND i.version='1.5.1'`, token.ID, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return bad("CLOUDFLARE_SCOPE_DENIED", "优选调度未授权")
	}
	return nil
}
