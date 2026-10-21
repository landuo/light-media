package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"light-media/internal/plugins/schedule"
)

var pluginServiceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.|:-]{0,159}$`)

// Registration is private host state. Native adapters must derive/validate it
// from the actual installed config before calling configurePluginSchedules.
type pluginScheduleDefinition struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Handler        string          `json:"handler"`
	Trigger        schedule.Spec   `json:"trigger"`
	Input          json.RawMessage `json:"input,omitempty"`
	MisfireGraceMS int64           `json:"misfire_grace_ms,omitempty"`
}

func (p *PluginStore) configurePluginSchedules(ctx context.Context, run PluginRun, group string, definitions []pluginScheduleDefinition, at time.Time) error {
	return p.configurePluginSchedulesAndPatch(ctx, &run, group, definitions, at, nil)
}

func (p *PluginStore) configurePluginSchedulesAndPatch(ctx context.Context, run *PluginRun, group string, definitions []pluginScheduleDefinition, at time.Time, patch map[string]json.RawMessage) error {
	return p.configurePluginSchedulesWithEffect(ctx, run, group, definitions, at, patch, nil)
}

func (p *PluginStore) configurePluginSchedulesWithEffect(ctx context.Context, run *PluginRun, group string, definitions []pluginScheduleDefinition, at time.Time, patch map[string]json.RawMessage, effect func(context.Context, *sql.Tx, PluginRun) error) error {
	return p.configurePluginSchedulesWithReplacement(ctx, run, group, definitions, at, patch, effect, nil)
}
func (p *PluginStore) configureIdentifiersOnce(ctx context.Context, run *PluginRun, definitions []pluginScheduleDefinition, at time.Time, replacement json.RawMessage) error {
	return p.configurePluginSchedulesWithReplacement(ctx, run, "once", definitions, at, nil, nil, replacement)
}
func (p *PluginStore) configurePluginSchedulesWithReplacement(ctx context.Context, run *PluginRun, group string, definitions []pluginScheduleDefinition, at time.Time, patch map[string]json.RawMessage, effect func(context.Context, *sql.Tx, PluginRun) error, replacement json.RawMessage) error {
	if !pluginStorageID.MatchString(group) || len(definitions) > 1000 || at.IsZero() {
		return bad("INVALID_PLUGIN_SCHEDULE", "插件定时服务声明无效")
	}
	type prepared struct {
		definition pluginScheduleDefinition
		sealed     []byte
		digest     string
		due        any
	}
	items := make([]prepared, 0, len(definitions))
	seen := map[string]bool{}
	totalBytes := 0
	for _, definition := range definitions {
		if !pluginServiceID.MatchString(definition.ID) || seen[definition.ID] || definition.Name == "" || len(definition.Name) > 512 || !pluginStorageID.MatchString(definition.Handler) || definition.MisfireGraceMS < -1 || definition.MisfireGraceMS > (time.Duration(1<<63-1)).Milliseconds() || (len(definition.Input) != 0 && !json.Valid(definition.Input)) {
			return bad("INVALID_PLUGIN_SCHEDULE", "插件定时服务标识、处理器或参数无效")
		}
		seen[definition.ID] = true
		if definition.MisfireGraceMS == 0 {
			definition.MisfireGraceMS = 1000 // APScheduler's default, not unlimited.
		}
		declaration, err := pluginJSON(definition, true)
		if err != nil {
			return err
		}
		totalBytes += len(declaration)
		if totalBytes > pluginValueLimit {
			return bad("INVALID_PLUGIN_SCHEDULE", "插件定时服务声明总量超出协议上限")
		}
		digest := tokenHash(string(declaration))
		trigger, err := schedule.New(definition.Trigger, at)
		if err != nil {
			return bad("INVALID_PLUGIN_SCHEDULE", "插件定时表达式或时区无效")
		}
		definition.Trigger = trigger.Spec()
		raw, err := pluginJSON(definition, true)
		if err != nil {
			return err
		}
		sealed, err := p.seal("schedule", run.InstanceID, definition.ID, raw)
		if err != nil {
			return err
		}
		var due any
		if first := trigger.First(at); !first.IsZero() {
			due = schedule.FormatTime(first)
		}
		items = append(items, prepared{definition, sealed, digest, due})
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' JOIN plugin_runs r ON r.instance_id=i.instance_id WHERE i.instance_id=? AND i.installed=1 AND i.desired_enabled=1 AND i.generation=? AND i.version=? AND v.revision=? AND r.id=? AND r.state='running' AND r.generation=i.generation AND r.config_revision=v.revision`, run.InstanceID, run.Generation, run.Version, run.ConfigRevision, run.ID).Scan(&current); err != nil {
		return err
	}
	if current != 1 || (run.Action != "init" && run.Action != "reload") {
		return bad("PLUGIN_SNAPSHOT_STALE", "插件已变化，未注册旧定时服务")
	}
	for _, item := range items {
		var oldGroup, digest string
		var generation, revision int64
		var due sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT group_key,generation,config_revision,definition_digest,next_due FROM plugin_schedules WHERE instance_id=? AND service_id=?`, run.InstanceID, item.definition.ID).Scan(&oldGroup, &generation, &revision, &digest, &due)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && oldGroup != group {
			return bad("PLUGIN_SCHEDULE_SCOPE_CONFLICT", "定时服务已由另一注册分组管理")
		}
		if err == nil && generation == run.Generation && revision == run.ConfigRevision && digest == item.digest {
			// Same config restart must preserve both anchors and exhausted date
			// jobs. Never reset a previously consumed one-shot to a new date.
			if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=1,updated_at=? WHERE instance_id=? AND service_id=?`, now(), run.InstanceID, item.definition.ID); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_schedules VALUES(?,?,?,?,?,?,?,1,?,?) ON CONFLICT(instance_id,service_id) DO UPDATE SET group_key=excluded.group_key,generation=excluded.generation,config_revision=excluded.config_revision,definition=excluded.definition,definition_digest=excluded.definition_digest,active=1,next_due=excluded.next_due,updated_at=excluded.updated_at`, run.InstanceID, item.definition.ID, group, run.Generation, run.ConfigRevision, item.sealed, item.digest, item.due, now()); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT service_id FROM plugin_schedules WHERE instance_id=? AND group_key=? AND active=1`, run.InstanceID, group)
	if err != nil {
		return err
	}
	obsolete := []string{}
	for rows.Next() {
		var serviceID string
		if err := rows.Scan(&serviceID); err != nil {
			rows.Close()
			return err
		}
		if !seen[serviceID] {
			obsolete = append(obsolete, serviceID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, serviceID := range obsolete {
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0,updated_at=? WHERE instance_id=? AND service_id=?`, now(), run.InstanceID, serviceID); err != nil {
			return err
		}
	}
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_schedules WHERE active=1`).Scan(&total); err != nil {
		return err
	}
	if total > 10000 {
		exempt, err := p.brushFlowExemptScheduleCountTx(ctx, tx)
		if err != nil {
			return err
		}
		if total-exempt > 10000 {
			return bad("PLUGIN_SCHEDULE_CAPACITY", "插件定时服务数量已达上限，注册未提交")
		}
	}
	updated := *run
	if effect != nil {
		if err := effect(ctx, tx, updated); err != nil {
			return err
		}
	}
	if len(replacement) > 0 {
		if err := p.replaceRunConfigTx(ctx, tx, &updated, replacement); err != nil {
			return err
		}
	}
	if len(patch) > 0 {
		if err := p.patchRunConfigTx(ctx, tx, &updated, patch); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	*run = updated
	return nil
}

// Coalesce long outages without iterating every missed second/minute. First is
// monotonic in real time, including the original cron gap behavior. Retain the
// already persisted due instant even when its wall time was virtual in a gap.
func latestPluginScheduleDue(trigger *schedule.Trigger, due, at time.Time) (time.Time, time.Time) {
	next := trigger.Next(due)
	if next.IsZero() || next.After(at) {
		return due, next
	}
	best, low, high := next, next, at
	for !low.After(high) {
		seconds := high.Unix() - low.Unix()
		middle := time.Unix(low.Unix()+seconds/2, (int64(low.Nanosecond())+int64(high.Nanosecond()))/2+(seconds%2)*int64(time.Second)/2).UTC()
		candidate := trigger.First(middle)
		if !candidate.IsZero() && !candidate.After(at) {
			if candidate.After(best) {
				best = candidate
			}
			low = candidate.Add(time.Nanosecond)
		} else {
			high = middle.Add(-time.Nanosecond)
		}
	}
	return best, trigger.Next(best)
}

func (p *PluginStore) enqueueDuePluginSchedules(ctx context.Context, at time.Time) (int, error) {
	queued := 0
	// Only this dispatch skips a positively identified unknown owner. Its due
	// instant is untouched and the next dispatch checks the owner again.
	blocked := make([]any, 0)
	for count := 0; count < 200; count++ {
		tx, err := p.store.DB.BeginTx(ctx, nil)
		if err != nil {
			return queued, err
		}
		var instanceID, serviceID, dueText, pluginID string
		var generation, revision int64
		var sealed []byte
		query := `SELECT s.instance_id,s.service_id,s.generation,s.config_revision,s.definition,s.next_due,i.plugin_id FROM plugin_schedules s JOIN plugin_installations i ON i.instance_id=s.instance_id JOIN plugin_values v ON v.instance_id=s.instance_id AND v.kind='config' AND v.item_key='main' WHERE s.active=1 AND s.next_due IS NOT NULL AND s.next_due<=? AND i.installed=1 AND i.desired_enabled=1 AND i.generation=s.generation AND (v.revision=s.config_revision OR i.plugin_id='AutoSignIn')`
		args := []any{schedule.FormatTime(at)}
		if len(blocked) > 0 {
			// At most 200 instances are considered in one bounded dispatch. IDs
			// stay SQL parameters, including all services of a blocked instance.
			query += ` AND s.instance_id NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(blocked)), ",") + `)`
			args = append(args, blocked...)
		}
		query += ` ORDER BY s.next_due,s.instance_id,s.service_id LIMIT 1`
		err = tx.QueryRowContext(ctx, query, args...).Scan(&instanceID, &serviceID, &generation, &revision, &sealed, &dueText, &pluginID)
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			return queued, nil
		}
		if err != nil {
			_ = tx.Rollback()
			return queued, err
		}
		enqueued := false
		applyErr := func() error {
			if pluginID == "AutoSignIn" {
				handled, e := p.autoSignInScheduleAdmissionTx(ctx, tx, instanceID, generation, revision)
				if e != nil {
					return e
				}
				if !handled {
					return bad("SIGNIN_SCHEDULE_DENIED", "签到调度身份未匹配")
				}
			}
			raw, err := p.unseal("schedule", instanceID, serviceID, sealed)
			if err != nil {
				return err
			}
			var definition pluginScheduleDefinition
			if !pluginStrictJSON(raw, &definition) || definition.ID != serviceID {
				return bad("PLUGIN_SCHEDULE_CORRUPT", "插件定时服务无法读取")
			}
			due, err := time.Parse(time.RFC3339Nano, dueText)
			if err != nil {
				return bad("PLUGIN_SCHEDULE_CORRUPT", "插件定时服务时间无效")
			}
			trigger, err := schedule.New(definition.Trigger, due)
			if err != nil {
				return bad("PLUGIN_SCHEDULE_CORRUPT", "插件定时触发器无法读取")
			}
			// Check before misfire/busy handling too: neither can consume a due
			// instant belonging to an unresolved authenticated IYUU timeline.
			if err := p.pluginNativeQueueAdmissionTx(ctx, tx, instanceID, pluginID); err != nil {
				return err
			}
			latest, next := latestPluginScheduleDue(trigger, due, at)
			occurrenceID := id("ps_")
			state := "queued"
			var runID any
			if definition.MisfireGraceMS != -1 && at.Sub(latest) > time.Duration(definition.MisfireGraceMS)*time.Millisecond {
				state = "missed"
			}
			var busy int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_schedule_occurrences o JOIN plugin_runs r ON r.id=o.run_id WHERE o.instance_id=? AND o.service_id=? AND r.state IN ('pending','running','reconcile_pending')`, instanceID, serviceID).Scan(&busy); err != nil {
				return err
			}
			if busy != 0 && state == "queued" {
				state = "skipped_busy"
			}
			if state == "queued" {
				run, handled, err := p.enqueueCleanInvalidSeedScheduleTx(ctx, tx, instanceID, definition, latest, occurrenceID, false)
				if !handled && err == nil {
					run, handled, err = p.enqueueCloudflareScheduleTx(ctx, tx, instanceID, definition, latest, occurrenceID, false)
					if !handled && err == nil {
						run, handled, err = p.enqueueAutoBackupScheduleTx(ctx, tx, instanceID, definition, latest, occurrenceID, false)
					}
					if !handled && err == nil {
						run, handled, err = p.enqueueBrushFlowScheduleTx(ctx, tx, instanceID, definition, latest, occurrenceID, false)
						if err == nil && !handled {
							run, handled, err = p.enqueueSeedRescueScheduleTx(ctx, tx, instanceID, definition, latest, occurrenceID, false)
						}
					}
				}
				if !handled && err == nil {
					input, _ := json.Marshal(map[string]string{"occurrence_id": occurrenceID})
					run, err = p.enqueueRunTx(ctx, tx, instanceID, "service", input)
				}
				if err != nil {
					return err // Queue capacity cannot consume this due instant.
				}
				runID = run.ID
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_schedule_occurrences VALUES(?,?,?,?,?,?,?,?,?,0,?)`, occurrenceID, instanceID, serviceID, generation, revision, schedule.FormatTime(latest), state, runID, definition.MisfireGraceMS, now()); err != nil {
				return err
			}
			var nextDue any
			if !next.IsZero() {
				nextDue = schedule.FormatTime(next)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedules SET next_due=?,updated_at=? WHERE instance_id=? AND service_id=? AND generation=? AND config_revision=?`, nextDue, now(), instanceID, serviceID, generation, revision); err != nil {
				return err
			}
			if state == "queued" {
				enqueued = true
			}
			return nil
		}()
		if applyErr != nil {
			_ = tx.Rollback()
			if pluginNativeUnconfirmedQueueAdmission(applyErr) {
				blocked = append(blocked, instanceID)
				continue
			}
			return queued, applyErr
		}
		if err := tx.Commit(); err != nil {
			return queued, err
		}
		if enqueued {
			queued++
		}
	}
	return queued, nil
}

func (p *PluginStore) claimPluginScheduleTx(ctx context.Context, tx *sql.Tx, run PluginRun, at time.Time) (string, error) {
	if handled, code, err := p.claimAutoSignInScheduleTx(ctx, tx, run, at); handled || err != nil {
		return code, err
	}
	var invocation struct {
		Input struct {
			OccurrenceID string `json:"occurrence_id"`
		} `json:"input"`
	}
	if json.Unmarshal(run.Input, &invocation) != nil || !pluginStorageID.MatchString(invocation.Input.OccurrenceID) {
		return "PLUGIN_SCHEDULE_SCOPE_DENIED", nil
	}
	var scheduledAt string
	var grace int64
	var active, manual bool
	err := tx.QueryRowContext(ctx, `SELECT o.scheduled_at,o.misfire_grace_ms,o.manual,s.active FROM plugin_schedule_occurrences o JOIN plugin_schedules s ON s.instance_id=o.instance_id AND s.service_id=o.service_id WHERE o.id=? AND o.run_id=? AND o.instance_id=? AND o.generation=? AND o.config_revision=? AND o.state='queued' AND s.generation=o.generation AND s.config_revision=o.config_revision`, invocation.Input.OccurrenceID, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&scheduledAt, &grace, &manual, &active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !active) {
		return "PLUGIN_SCHEDULE_SCOPE_DENIED", nil
	}
	if err != nil {
		return "", err
	}
	due, err := time.Parse(time.RFC3339Nano, scheduledAt)
	if err != nil || grace < -1 {
		return "PLUGIN_SCHEDULE_CORRUPT", nil
	}
	state, errorCode := "running", ""
	if !manual && grace != -1 && at.Sub(due) > time.Duration(grace)*time.Millisecond {
		state, errorCode = "missed", "PLUGIN_SCHEDULE_MISFIRE"
	}
	_, err = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state=? WHERE id=? AND state='queued'`, state, invocation.Input.OccurrenceID)
	return errorCode, err
}

func (p *PluginStore) pluginScheduledInvocation(ctx context.Context, run PluginRun, occurrenceID string) (pluginScheduleDefinition, error) {
	var out pluginScheduleDefinition
	if run.Action != "service" || !pluginStorageID.MatchString(occurrenceID) {
		return out, bad("PLUGIN_SCHEDULE_SCOPE_DENIED", "只能读取当前运行的定时服务参数")
	}
	var invocation struct {
		Input struct {
			OccurrenceID string `json:"occurrence_id"`
		} `json:"input"`
	}
	if json.Unmarshal(run.Input, &invocation) != nil || invocation.Input.OccurrenceID != occurrenceID {
		return out, bad("PLUGIN_SCHEDULE_SCOPE_DENIED", "不能读取其他定时服务执行参数")
	}
	var serviceID string
	var sealed []byte
	err := p.store.DB.QueryRowContext(ctx, `SELECT s.service_id,s.definition FROM plugin_schedule_occurrences o JOIN plugin_schedules s ON s.instance_id=o.instance_id AND s.service_id=o.service_id JOIN plugin_runs r ON r.id=o.run_id JOIN plugin_installations i ON i.instance_id=s.instance_id JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE o.id=? AND o.run_id=? AND o.instance_id=? AND o.generation=? AND o.config_revision=? AND o.state='running' AND r.state='running' AND s.active=1 AND s.generation=o.generation AND s.config_revision=o.config_revision AND i.installed=1 AND i.desired_enabled=1 AND i.generation=o.generation AND v.revision=o.config_revision`, occurrenceID, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&serviceID, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return out, bad("PLUGIN_SCHEDULE_SCOPE_DENIED", "定时服务已停止或执行范围已变化")
	}
	if err != nil {
		return out, err
	}
	raw, err := p.unseal("schedule", run.InstanceID, serviceID, sealed)
	if err != nil || !pluginStrictJSON(raw, &out) || out.ID != serviceID {
		return out, bad("PLUGIN_SCHEDULE_CORRUPT", "定时服务参数无法读取")
	}
	return out, nil
}

func (p *PluginStore) enqueueManualPluginService(ctx context.Context, instanceID, serviceID string, at time.Time) (PluginRun, error) {
	if !pluginStorageID.MatchString(instanceID) || !pluginServiceID.MatchString(serviceID) || at.IsZero() {
		return PluginRun{}, bad("INVALID_PLUGIN_SCHEDULE", "定时服务标识无效")
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginRun{}, err
	}
	defer tx.Rollback()
	var generation, revision int64
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT s.generation,s.config_revision,s.definition FROM plugin_schedules s JOIN plugin_installations i ON i.instance_id=s.instance_id JOIN plugin_values v ON v.instance_id=s.instance_id AND v.kind='config' AND v.item_key='main' WHERE s.instance_id=? AND s.service_id=? AND s.active=1 AND s.next_due IS NOT NULL AND i.installed=1 AND i.desired_enabled=1 AND i.generation=s.generation AND v.revision=s.config_revision`, instanceID, serviceID).Scan(&generation, &revision, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return PluginRun{}, bad("PLUGIN_SERVICE_NOT_FOUND", "定时服务不存在或已停用")
	}
	if err != nil {
		return PluginRun{}, err
	}
	var busy int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_schedule_occurrences o JOIN plugin_runs r ON r.id=o.run_id WHERE o.instance_id=? AND o.service_id=? AND r.state IN ('pending','running','reconcile_pending')`, instanceID, serviceID).Scan(&busy); err != nil {
		return PluginRun{}, err
	}
	if busy != 0 {
		return PluginRun{}, bad("PLUGIN_SERVICE_BUSY", "该定时服务正在等待或执行中")
	}
	raw, err := p.unseal("schedule", instanceID, serviceID, sealed)
	var definition pluginScheduleDefinition
	if err != nil || !pluginStrictJSON(raw, &definition) || definition.ID != serviceID {
		return PluginRun{}, bad("PLUGIN_SCHEDULE_CORRUPT", "定时服务参数无法读取")
	}
	occurrenceID := id("ps_")
	run, handled, err := p.enqueueCleanInvalidSeedScheduleTx(ctx, tx, instanceID, definition, at, occurrenceID, true)
	if !handled && err == nil {
		run, handled, err = p.enqueueCloudflareScheduleTx(ctx, tx, instanceID, definition, at, occurrenceID, true)
		if !handled && err == nil {
			run, handled, err = p.enqueueAutoBackupScheduleTx(ctx, tx, instanceID, definition, at, occurrenceID, true)
		}
		if !handled && err == nil {
			run, handled, err = p.enqueueBrushFlowScheduleTx(ctx, tx, instanceID, definition, at, occurrenceID, true)
			if err == nil && !handled {
				run, handled, err = p.enqueueSeedRescueScheduleTx(ctx, tx, instanceID, definition, at, occurrenceID, true)
			}
		}
	}
	if !handled && err == nil {
		input, _ := json.Marshal(map[string]string{"occurrence_id": occurrenceID})
		run, err = p.enqueueRunTx(ctx, tx, instanceID, "service", input)
	}
	if err != nil {
		return PluginRun{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_schedule_occurrences VALUES(?,?,?,?,?,?,'queued',?,?,1,?)`, occurrenceID, instanceID, serviceID, generation, revision, schedule.FormatTime(at), run.ID, definition.MisfireGraceMS, now()); err != nil {
		return PluginRun{}, err
	}
	return run, tx.Commit()
}

func (a *App) pluginScheduleWorker(ctx context.Context) {
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if a.paused() {
				continue
			}
			queued, err := a.pluginStore.enqueueDuePluginSchedules(ctx, time.Now())
			if err != nil && ctx.Err() == nil {
				a.log("ERROR", "plugins", "schedule_dispatch_failed", "插件定时服务投递失败，已保留未提交的到期时间", "", "")
			}
			if queued > 0 {
				a.wakePlugins()
			}
		}
	}
}
