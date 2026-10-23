package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
	durable "light-media/internal/plugins/autosignin/durable"
	"light-media/internal/plugins/schedule"
)

const autoSignInScheduleGroup = "autosignin"
const autoSignInScheduleHandler = "autosignin.sign_in"

type autoSignInScheduleProof struct {
	OriginRun  string `json:"origin_run"`
	JobKey     string `json:"job_key,omitempty"`
	SourceHash string `json:"source_hash"`
}

// This reader verifies sealed owner state but deliberately does not acquire an
// execution capability. It is safe inside the existing dispatcher transaction.
func (p *PluginStore) autoSignInScheduleStateTx(ctx context.Context, tx *sql.Tx, instance string, generation, admission int64) (bool, durable.State, error) {
	var empty durable.State
	var plugin, version string
	var gen, revision int64
	var installed, enabled bool
	var configSealed []byte
	err := tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision,v.sealed FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, instance).Scan(&plugin, &version, &gen, &installed, &enabled, &revision, &configSealed)
	if err != nil {
		return false, empty, err
	}
	if plugin != "AutoSignIn" {
		return false, empty, nil
	}
	if version != autosignin.Version || !installed || !enabled || gen != generation || admission < 1 {
		return true, empty, durable.ErrStale
	}
	var sealed []byte
	var ownerGen int64
	if err = tx.QueryRowContext(ctx, `SELECT generation,sealed FROM plugin_signin_owners WHERE instance_id=?`, instance).Scan(&ownerGen, &sealed); err != nil {
		return true, empty, err
	}
	raw, err := p.unseal("signin_owner", instance, "current", sealed)
	if err != nil {
		return true, empty, err
	}
	var binding durable.Binding
	if cleanupJournalStrict(raw, &binding) != nil || binding.Instance != instance || binding.Generation != uint64(gen) || ownerGen != gen || binding.Epoch == "" || binding.Implementation == "" {
		return true, empty, durable.ErrStale
	}
	reader := &pluginAutoSignInOwner{a: &App{Store: p.store, pluginStore: p}, binding: binding}
	loaded, err := reader.readTx(ctx, tx)
	if err != nil {
		return true, empty, err
	}
	if loaded.State.Scope != binding.Scope || loaded.State.Epoch != binding.Epoch {
		return true, empty, durable.ErrStale
	}
	config, err := p.unseal("config", instance, "main", configSealed)
	if err != nil {
		return true, empty, err
	}
	admissions, err := reader.configChainTx(ctx, tx, uint64(revision), config, loaded.State)
	if err != nil {
		return true, empty, err
	}
	if !admissions[uint64(admission)] {
		return true, empty, durable.ErrStale
	}
	return true, loaded.State, nil
}

func (p *PluginStore) autoSignInScheduleAdmissionTx(ctx context.Context, tx *sql.Tx, instance string, generation, revision int64) (bool, error) {
	handled, _, err := p.autoSignInScheduleStateTx(ctx, tx, instance, generation, revision)
	return handled, err
}

// Only authenticated source descriptors are accepted, never worker arguments.
func autoSignInServiceDefinition(raw json.RawMessage, zone string) (pluginScheduleDefinition, error) {
	var d struct {
		ID      string          `json:"id"`
		Name    string          `json:"name"`
		Trigger json.RawMessage `json:"trigger"`
		Func    struct {
			Callable string `json:"callable"`
		} `json:"func"`
		Kwargs map[string]json.RawMessage `json:"kwargs"`
	}
	if cleanupJournalStrict(raw, &d) != nil || !pluginServiceID.MatchString(d.ID) || d.Func.Callable != "sign_in" || d.Name == "" {
		return pluginScheduleDefinition{}, durable.ErrStore
	}
	out := pluginScheduleDefinition{ID: d.ID, Name: d.Name, Handler: autoSignInScheduleHandler, MisfireGraceMS: 1000, Trigger: schedule.Spec{Timezone: zone}}
	var kind string
	if json.Unmarshal(d.Trigger, &kind) == nil {
		switch kind {
		case "interval":
			var hours float64
			if len(d.Kwargs) != 1 || json.Unmarshal(d.Kwargs["hours"], &hours) != nil {
				return out, autosignin.ErrScheduleNotReady
			}
			ms := hours * 3600000
			if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 0 || ms > float64((time.Duration(1<<63-1)).Milliseconds()) || math.Trunc(ms) != ms {
				return out, autosignin.ErrScheduleNotReady
			}
			out.Trigger.Kind = "interval"
			out.Trigger.IntervalMS = int64(ms)
		case "cron":
			var hour, minute int
			if len(d.Kwargs) != 2 || json.Unmarshal(d.Kwargs["hour"], &hour) != nil || json.Unmarshal(d.Kwargs["minute"], &minute) != nil {
				return out, durable.ErrStore
			}
			out.Trigger.Kind = "cron"
			out.Trigger.Fields = &schedule.CronFields{Hour: strconv.Itoa(hour), Minute: strconv.Itoa(minute)}
		default:
			return out, durable.ErrStore
		}
	} else {
		var cron struct {
			Trigger  string              `json:"trigger"`
			Fields   schedule.CronFields `json:"fields"`
			Timezone string              `json:"timezone"`
		}
		if cleanupJournalStrict(d.Trigger, &cron) != nil || cron.Trigger != "CronTrigger" || cron.Timezone != zone || len(d.Kwargs) != 0 {
			return out, durable.ErrStore
		}
		out.Trigger.Kind = "cron"
		out.Trigger.Fields = &cron.Fields
	}
	return out, nil
}

func autoSignInOnceDefinition(key string, raw json.RawMessage, zone string) (pluginScheduleDefinition, error) {
	var job struct {
		Op   string `json:"op"`
		Func struct {
			Callable string `json:"callable"`
		} `json:"func"`
		Trigger string `json:"trigger"`
		RunDate string `json:"run_date"`
		Name    string `json:"name"`
	}
	if cleanupJournalStrict(raw, &job) != nil || job.Op != "scheduler_job" || job.Func.Callable != "sign_in" || job.Trigger != "date" || job.Name == "" {
		return pluginScheduleDefinition{}, durable.ErrStore
	}
	return pluginScheduleDefinition{ID: "AutoSignIn.once." + tokenHash(key), Name: job.Name, Handler: autoSignInScheduleHandler, MisfireGraceMS: 1000, Trigger: schedule.Spec{Kind: "date", Timezone: zone, RunAt: job.RunDate}}, nil
}

func signinAppliedKind(run *durable.Run, kind string) bool {
	for _, entries := range run.Lanes {
		for _, e := range entries {
			if e.Kind == kind && e.Phase == "applied" {
				return true
			}
		}
	}
	return false
}

// Called after projectConfigTx in the same authenticated journal transaction.
// Until Finish commits runtime, no new date or recurring job can be dispatched.
func (b *pluginAutoSignInBackend) projectSchedulesTx(ctx context.Context, tx *sql.Tx, before map[string]pluginAutoSignInCell, state durable.State) error {
	if b.run == nil {
		return nil
	}
	run := state.Runs[b.run.ID]
	if run == nil {
		return nil
	}
	p := b.owner.a.pluginStore
	// Source stop removes only its private once scheduler, not host services.
	if len(state.Jobs) == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0,updated_at=? WHERE instance_id=? AND group_key=? AND service_id LIKE 'AutoSignIn.once.%'`, now(), b.run.InstanceID, autoSignInScheduleGroup); err != nil {
			return err
		}
	}
	if run.Phase != "completed" || (run.Action != "init" && run.Action != "reload" && run.Action != "cold_init") {
		return nil
	}
	var old pluginAutoSignInRunHead
	if cell, ok := before[signinKey("run.head", signinKey(run.ID))]; ok && cleanupJournalStrict(cell.Data, &old) == nil && old.Phase == "completed" {
		return nil
	}
	if !signinAppliedKind(run, "service.definitions") || state.RuntimeEpoch != state.Epoch {
		return durable.ErrStale
	}
	zone := run.Baseline.Timezone
	at := time.Now().UTC()
	defs := []pluginScheduleDefinition{}
	var services []json.RawMessage
	if json.Unmarshal(state.Services, &services) != nil {
		return durable.ErrStore
	}
	for _, raw := range services {
		d, err := autoSignInServiceDefinition(raw, zone)
		if err != nil {
			return err
		}
		d.Input, _ = json.Marshal(autoSignInScheduleProof{OriginRun: run.ID, SourceHash: tokenHash(string(raw))})
		defs = append(defs, d)
	}
	if len(state.Jobs) > 0 && !signinAppliedKind(run, "scheduler_start") {
		return durable.ErrStale
	}
	for key, raw := range state.Jobs {
		d, err := autoSignInOnceDefinition(key, raw, zone)
		if err != nil {
			return err
		}
		d.Input, _ = json.Marshal(autoSignInScheduleProof{OriginRun: run.ID, JobKey: key, SourceHash: tokenHash(string(raw))})
		defs = append(defs, d)
	}
	if len(defs) > 1000 {
		return durable.ErrCapacity
	}
	seen := map[string]bool{}
	for _, d := range defs {
		if seen[d.ID] {
			return durable.ErrStore
		}
		seen[d.ID] = true
		// Digest excludes origin run: recovering the same source job must preserve
		// both its interval anchor and an already exhausted date next_due=NULL.
		semantic := d
		semantic.Input = nil
		raw, _ := json.Marshal(semantic)
		digest := tokenHash(string(raw))
		var oldGroup, oldDigest string
		var gen, oldRevision int64
		err := tx.QueryRowContext(ctx, `SELECT group_key,generation,config_revision,definition_digest FROM plugin_schedules WHERE instance_id=? AND service_id=?`, b.run.InstanceID, d.ID).Scan(&oldGroup, &gen, &oldRevision, &oldDigest)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && oldGroup != autoSignInScheduleGroup {
			return durable.ErrConflict
		}
		if err == nil && gen == b.run.Generation && oldRevision >= int64(state.Scope.Revision) && oldDigest == digest {
			handled, admissionErr := p.autoSignInScheduleAdmissionTx(ctx, tx, b.run.InstanceID, gen, oldRevision)
			if admissionErr != nil {
				return admissionErr
			}
			if !handled {
				return durable.ErrStale
			}
			if _, err = tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=1,updated_at=? WHERE instance_id=? AND service_id=?`, now(), b.run.InstanceID, d.ID); err != nil {
				return err
			}
			continue
		}
		trigger, err := schedule.New(d.Trigger, at)
		if err != nil {
			return err
		}
		d.Trigger = trigger.Spec()
		raw, err = json.Marshal(d)
		if err != nil {
			return err
		}
		sealed, err := p.seal("schedule", b.run.InstanceID, d.ID, raw)
		if err != nil {
			return err
		}
		var due any
		if first := trigger.First(at); !first.IsZero() {
			due = schedule.FormatTime(first)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_schedules VALUES(?,?,?,?,?,?,?,1,?,?) ON CONFLICT(instance_id,service_id) DO UPDATE SET group_key=excluded.group_key,generation=excluded.generation,config_revision=excluded.config_revision,definition=excluded.definition,definition_digest=excluded.definition_digest,active=1,next_due=excluded.next_due,updated_at=excluded.updated_at`, b.run.InstanceID, d.ID, autoSignInScheduleGroup, b.run.Generation, b.run.ConfigRevision, sealed, digest, due, now()); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT service_id FROM plugin_schedules WHERE instance_id=? AND group_key=? AND active=1`, b.run.InstanceID, autoSignInScheduleGroup)
	if err != nil {
		return err
	}
	obsolete := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !seen[id] {
			obsolete = append(obsolete, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range obsolete {
		if _, err = tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0,updated_at=? WHERE instance_id=? AND service_id=?`, now(), b.run.InstanceID, id); err != nil {
			return err
		}
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_schedules WHERE active=1`).Scan(&count); err != nil {
		return err
	}
	if count > 10000 {
		exempt, e := p.brushFlowExemptScheduleCountTx(ctx, tx)
		if e != nil {
			return e
		}
		if count-exempt > 10000 {
			return durable.ErrCapacity
		}
	}
	return nil
}

func (a *App) autoSignInTrustedService(ctx context.Context, run PluginRun, inv plugins.Invocation) (durable.Command, error) {
	denied := durable.ErrStale
	if run.Action != "service" || inv.Action != "service" {
		return durable.Command{}, denied
	}
	var original plugins.Invocation
	if cleanupJournalStrict(run.Input, &original) != nil || original.Action != inv.Action || !bytes.Equal(original.Input, inv.Input) || !bytes.Equal(original.Config, inv.Config) {
		return durable.Command{}, denied
	}
	var input struct {
		OccurrenceID string `json:"occurrence_id"`
	}
	if cleanupJournalStrict(inv.Input, &input) != nil || !pluginStorageID.MatchString(input.OccurrenceID) {
		return durable.Command{}, denied
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return durable.Command{}, err
	}
	defer tx.Rollback()
	handled, state, err := a.pluginStore.autoSignInScheduleStateTx(ctx, tx, run.InstanceID, run.Generation, run.ConfigRevision)
	if err != nil {
		return durable.Command{}, err
	}
	if !handled {
		return durable.Command{}, denied
	}
	var serviceID string
	var sealed []byte
	var scheduleRevision int64
	err = tx.QueryRowContext(ctx, `SELECT s.service_id,s.definition,s.config_revision FROM plugin_schedule_occurrences o JOIN plugin_schedules s ON s.instance_id=o.instance_id AND s.service_id=o.service_id JOIN plugin_runs r ON r.id=o.run_id WHERE o.id=? AND o.run_id=? AND o.instance_id=? AND o.generation=? AND r.config_revision=? AND o.state='running' AND o.manual=0 AND r.state='running' AND r.action='service' AND r.generation=o.generation AND s.config_revision=o.config_revision AND s.active=1 AND s.generation=o.generation AND s.group_key=?`, input.OccurrenceID, run.ID, run.InstanceID, run.Generation, run.ConfigRevision, autoSignInScheduleGroup).Scan(&serviceID, &sealed, &scheduleRevision)
	if err != nil {
		return durable.Command{}, denied
	}
	if _, err = a.pluginStore.autoSignInScheduleAdmissionTx(ctx, tx, run.InstanceID, run.Generation, scheduleRevision); err != nil {
		return durable.Command{}, err
	}
	var persisted []byte
	if err = tx.QueryRowContext(ctx, `SELECT input FROM plugin_runs WHERE id=? AND instance_id=? AND version=?`, run.ID, run.InstanceID, run.Version).Scan(&persisted); err != nil {
		return durable.Command{}, denied
	}
	persisted, err = a.pluginStore.unseal("run", run.InstanceID, run.ID, persisted)
	if err != nil || !bytes.Equal(persisted, run.Input) {
		return durable.Command{}, denied
	}
	raw, err := a.pluginStore.unseal("schedule", run.InstanceID, serviceID, sealed)
	if err != nil {
		return durable.Command{}, err
	}
	var def pluginScheduleDefinition
	var proof autoSignInScheduleProof
	if cleanupJournalStrict(raw, &def) != nil || def.ID != serviceID || def.Handler != autoSignInScheduleHandler || cleanupJournalStrict(def.Input, &proof) != nil {
		return durable.Command{}, denied
	}
	origin := state.Runs[proof.OriginRun]
	if origin == nil || origin.Phase != "completed" || origin.Scope != state.Scope || (origin.Action != "init" && origin.Action != "reload" && origin.Action != "cold_init") {
		return durable.Command{}, denied
	}
	matched := false
	if proof.JobKey != "" {
		value, ok := state.Jobs[proof.JobKey]
		matched = ok && tokenHash(string(value)) == proof.SourceHash && signinAppliedKind(origin, "scheduler_start")
	} else {
		var values []json.RawMessage
		if json.Unmarshal(state.Services, &values) != nil {
			return durable.Command{}, denied
		}
		for _, value := range values {
			if tokenHash(string(value)) == proof.SourceHash {
				matched = true
			}
		}
	}
	if !matched {
		return durable.Command{}, denied
	}
	return durable.Command{Action: "service"}, nil
}

// The run snapshots the effective public config; the occurrence keeps the
// schedule's original admission. Both must belong to the authenticated chain.
func (p *PluginStore) claimAutoSignInScheduleTx(ctx context.Context, tx *sql.Tx, run PluginRun, at time.Time) (bool, string, error) {
	var plugin string
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, run.InstanceID).Scan(&plugin); err != nil {
		return false, "", err
	}
	if plugin != "AutoSignIn" || run.Action != "service" {
		return false, "", nil
	}
	deny := "PLUGIN_SCHEDULE_SCOPE_DENIED"
	handled, _, err := p.autoSignInScheduleStateTx(ctx, tx, run.InstanceID, run.Generation, run.ConfigRevision)
	if err != nil {
		return handled, "", err
	}
	var inv plugins.Invocation
	var input struct {
		OccurrenceID string `json:"occurrence_id"`
	}
	if cleanupJournalStrict(run.Input, &inv) != nil || inv.Action != "service" || cleanupJournalStrict(inv.Input, &input) != nil || !pluginStorageID.MatchString(input.OccurrenceID) {
		return true, deny, nil
	}
	var dueText string
	var grace, admission int64
	err = tx.QueryRowContext(ctx, `SELECT o.scheduled_at,o.misfire_grace_ms,o.config_revision FROM plugin_schedule_occurrences o JOIN plugin_schedules s ON s.instance_id=o.instance_id AND s.service_id=o.service_id JOIN plugin_runs r ON r.id=o.run_id WHERE o.id=? AND o.run_id=? AND o.instance_id=? AND o.generation=? AND r.config_revision=? AND r.generation=o.generation AND r.state='pending' AND r.action='service' AND o.state='queued' AND o.manual=0 AND s.active=1 AND s.generation=o.generation AND s.config_revision=o.config_revision AND s.group_key=?`, input.OccurrenceID, run.ID, run.InstanceID, run.Generation, run.ConfigRevision, autoSignInScheduleGroup).Scan(&dueText, &grace, &admission)
	if errors.Is(err, sql.ErrNoRows) {
		return true, deny, nil
	}
	if err != nil {
		return true, "", err
	}
	if _, err = p.autoSignInScheduleAdmissionTx(ctx, tx, run.InstanceID, run.Generation, admission); err != nil {
		return true, "", err
	}
	due, err := time.Parse(time.RFC3339Nano, dueText)
	if err != nil || grace < -1 {
		return true, "PLUGIN_SCHEDULE_CORRUPT", nil
	}
	state, code := "running", ""
	if grace != -1 && at.Sub(due) > time.Duration(grace)*time.Millisecond {
		state, code = "missed", "PLUGIN_SCHEDULE_MISFIRE"
	}
	_, err = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state=? WHERE id=? AND state='queued'`, state, input.OccurrenceID)
	return true, code, err
}
