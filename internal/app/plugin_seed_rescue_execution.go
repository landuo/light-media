package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/schedule"
	sr "light-media/internal/plugins/seedrescue"
)

type seedRescueLeaseKey struct{}
type seedRescueLease struct {
	d *pluginDeferredCall
	o *pluginSeedRescueOwner
}

func (d *pluginDeferredCall) lockSeedRescue(ctx context.Context) (context.Context, func(), error) {
	o, e := d.a.seedRescueOwner(ctx, d.installation)
	if e != nil {
		return ctx, nil, e
	}
	if d.run.Action == "api" && !o.tryLock() {
		return ctx, nil, errSeedRescueBusy
	}
	if d.run.Action != "api" {
		if e = o.lock(ctx); e != nil {
			return ctx, nil, e
		}
	}
	if e = o.reconcileAdds(ctx); e != nil {
		o.unlock()
		return ctx, nil, e
	}
	if d.run.Action == "api" {
		tx, e := d.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			o.unlock()
			return ctx, nil, e
		}
		state, _, e := d.a.pluginStore.seedRescueRead(ctx, tx, o.instance, o.generation)
		var pending bool
		if e == nil {
			pending, e = seedRescueHasPendingSource(ctx, tx, state, d.run.ID)
		}
		tx.Rollback()
		if e != nil {
			o.unlock()
			return ctx, nil, e
		}
		if pending {
			o.unlock()
			return ctx, nil, errSeedRescueBusy
		}
	}
	return context.WithValue(ctx, seedRescueLeaseKey{}, seedRescueLease{d, o}), o.unlock, nil
}

var errSeedRescueBusy = errors.New("private SeedRescue busy decision")

func (d *pluginDeferredCall) seedRescueStartedTx(ctx context.Context, tx *sql.Tx) error {
	lease, ok := ctx.Value(seedRescueLeaseKey{}).(seedRescueLease)
	if !ok || lease.d != d || lease.o == nil || lease.o.a != d.a || lease.o.instance != d.run.InstanceID || lease.o.generation != d.run.Generation {
		return seedRescueEvidenceError()
	}
	s, b, e := d.a.pluginStore.seedRescueRead(ctx, tx, d.run.InstanceID, d.run.Generation)
	if e != nil {
		return e
	}
	if _, exists := s.Runs[d.run.ID]; exists {
		return bad("SEED_RESCUE_REPLAY_DENIED", "稀缺保种执行身份已经登记")
	}
	r := seedRescueAction{RunID: d.run.ID, Action: d.run.Action, Revision: d.run.ConfigRevision, EffectiveRevision: d.run.ConfigRevision, InputSHA: deferredInputSHA(d.run.Input), Method: d.record.Method, Phase: "started"}
	if seedRescueNoUnknown(s) != nil {
		r.Phase = "failed"
		r.ErrorCode = seedRescueBlockedByUnknown
		r.Result = seedRescueBlockedResult()
	}
	s.Runs[r.RunID] = r
	return d.a.pluginStore.seedRescueSave(ctx, tx, s, b)
}
func (d *pluginDeferredCall) seedRescueBusy(ctx context.Context) (json.RawMessage, error) {
	tx, e := d.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	r, blob, e := d.a.pluginStore.readDeferred(ctx, tx, d.run)
	if e != nil {
		return nil, e
	}
	if r.Phase != "prepared" || !deferredSameRun(r, d.installation, d.run) || !bytes.Equal(r.Raw, d.record.Raw) || !bytes.Equal(r.Ack, d.record.Ack) {
		return nil, seedRescueEvidenceError()
	}
	if e = d.a.pluginStore.validateDeferredRunTx(ctx, tx, r, d.run); e != nil {
		return nil, e
	}
	if _, e = d.a.validatePluginHTTPRunTx(ctx, tx, d.run); e != nil {
		return nil, e
	}
	s, b, e := d.a.pluginStore.seedRescueRead(ctx, tx, d.run.InstanceID, d.run.Generation)
	if e != nil {
		return nil, e
	}
	if _, ok := s.Runs[d.run.ID]; ok {
		return nil, seedRescueEvidenceError()
	}
	msg := "插件正在执行其他操作，请稍后重试"
	if req, _ := seedRescueRequest(d.run); req.Key == "scan" {
		msg = "稀缺保种任务正在运行"
	}
	out := seedRescueHTTPResponse(sr.Response{Message: &msg})
	s.Runs[d.run.ID] = seedRescueAction{RunID: d.run.ID, Action: d.run.Action, Revision: d.run.ConfigRevision, EffectiveRevision: d.run.ConfigRevision, InputSHA: deferredInputSHA(d.run.Input), Method: r.Method, Phase: "confirmed", Decision: "busy", Result: out}
	if e = d.a.pluginStore.seedRescueSave(ctx, tx, s, b); e != nil {
		return nil, e
	}
	r.Phase = "confirmed"
	r.Result = out
	if e = d.a.pluginStore.writeDeferred(ctx, tx, r, blob); e != nil {
		return nil, e
	}
	return out, tx.Commit()
}
func (a *App) shouldInitializeSeedRescue(ctx context.Context, in PluginInstallation, cfg PluginStoredValue) (bool, error) {
	s, _, e := a.pluginStore.seedRescueRead(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if e != nil {
		return false, e
	}
	if e = seedRescueNoUnknown(s); e != nil {
		return false, e
	}
	return s.AppliedRevision != cfg.Revision, nil
}
func (a *App) pluginSeedRescueHost(ctx context.Context, in PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if run == nil {
		return nil, seedRescueEvidenceError()
	}
	if method == sr.ReadCapability {
		return a.seedRescueReadAPI(ctx, in, *run, raw)
	}
	want, payload, e := seedRescueDeferredSpec(*run)
	if e != nil || want != method || !deferredPayloadEqual(raw, payload) {
		return nil, seedRescueEvidenceError()
	}
	if e = a.requireDeferredExecution(ctx, run, method); e != nil {
		return nil, e
	}
	d, _ := ctx.Value(pluginDeferredExecutionKey{}).(*pluginDeferredCall)
	lease, ok := ctx.Value(seedRescueLeaseKey{}).(seedRescueLease)
	if !ok || lease.d != d || lease.o == nil || lease.o.a != a || lease.o.instance != in.InstanceID || lease.o.generation != in.Generation {
		return nil, seedRescueEvidenceError()
	}
	cfg, e := a.pluginStore.Config(ctx, in.InstanceID)
	if e != nil {
		return nil, e
	}
	value, e := seedRescueConfig(cfg.Value)
	if e != nil {
		return nil, e
	}
	c := &seedRescueCall{o: lease.o, run: *run, method: method, config: value, revision: cfg.Revision}
	lease.o.active = c
	defer func() { lease.o.active = nil }()
	e = c.o.update(ctx, func(s *seedRescueState) error {
		r, exists := s.Runs[run.ID]
		if !exists {
			return seedRescueEvidenceError()
		}
		if r.Phase == "failed" && r.ErrorCode == seedRescueBlockedByUnknown && !r.Entered && len(r.Effects) == 0 && bytes.Equal(r.Result, seedRescueBlockedResult()) {
			return bad(seedRescueBlockedByUnknown, "已有稀缺保种操作待核对，此次没有开始外部操作")
		}
		if r.Phase != "started" || r.Entered || r.RunID != run.ID || r.InputSHA != deferredInputSHA(run.Input) || r.Revision != run.ConfigRevision || r.EffectiveRevision != cfg.Revision || r.Method != method {
			return seedRescueEvidenceError()
		}
		r.Entered = true
		s.Runs[run.ID] = r
		return nil
	})
	if e != nil {
		return nil, e
	}
	var result json.RawMessage
	result, e = c.execute(ctx)
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if c.uncertain.Load() {
		return nil, bad("SEED_RESCUE_UNCERTAIN", "下载或远端写操作结果待核对，未重新派发")
	}
	sourceErr := e
	e = c.o.update(finish, func(s *seedRescueState) error {
		r := s.Runs[run.ID]
		if r.Phase == "confirmed" {
			return nil
		}
		if r.Phase != "started" {
			return seedRescueEvidenceError()
		}
		for _, effect := range r.Effects {
			if effect.Phase != "confirmed" {
				return bad("SEED_RESCUE_UNCERTAIN", "稀缺保种外部操作尚未确认")
			}
		}
		r.Phase = "confirmed"
		r.Result = result
		if sourceErr != nil {
			r.Phase = "failed"
			r.ErrorCode = "SEED_RESCUE_SOURCE_FAILED"
			r.Result = mustCleanSeedJSON(map[string]any{"source_failed": true, "error_type": seedRescueSourceErrorClass(sourceErr)})
		}
		s.Runs[run.ID] = r
		return nil
	})
	if e != nil {
		return nil, e
	}
	return result, sourceErr
}
func (c *seedRescueCall) execute(ctx context.Context) (json.RawMessage, error) {
	if e := c.Guard(ctx); e != nil {
		return nil, e
	}
	if c.run.Action != "init" && c.run.Action != "reload" {
		s, _, e := c.o.a.pluginStore.seedRescueRead(ctx, c.o.a.Store.DB, c.o.instance, c.o.generation)
		if e != nil {
			return nil, e
		}
		if s.AppliedRevision != c.revision || s.AppliedConfigSHA != tokenHash(string(mustCleanSeedJSON(c.config))) {
			return nil, bad("SEED_RESCUE_CONFIG_PENDING", "稀缺保种配置尚未初始化完成")
		}
	}
	if c.run.Action == "api" {
		req, e := seedRescueRequest(c.run)
		if e != nil {
			return nil, e
		}
		if req.Key == "download" {
			if _, e = sr.ParseSelection(req.Body); e != nil {
				return seedRescueValidationResponse("body", "candidate_id"), nil
			}
		}
	}
	m := c.o.machine
	if m == nil {
		var e error
		m, e = sr.New(ctx, c.o)
		if e != nil {
			return nil, e
		}
		s, _, e := c.o.a.pluginStore.seedRescueRead(ctx, c.o.a.Store.DB, c.o.instance, c.o.generation)
		if e != nil {
			return nil, e
		}
		if len(s.Config) > 0 && string(s.Config) != "null" {
			value, e := seedRescueConfig(s.Config)
			if e != nil {
				return nil, e
			}
			value["onlyonce"] = false
			c.warming = true
			e = m.Init(ctx, value)
			c.warming = false
			if e != nil {
				return nil, e
			}
		}
		c.o.machine = m
	}
	if c.run.Action == "init" || c.run.Action == "reload" {
		if e := m.Init(ctx, c.config); e != nil {
			return nil, e
		}
		services, e := m.Services(ctx)
		if e != nil {
			return nil, e
		}
		defs := []pluginScheduleDefinition{}
		zone, e := autoBackupHostTimezone()
		if e != nil {
			return nil, e
		}
		for _, s := range services {
			spec := schedule.Spec{Kind: s.Trigger, Cron: s.Cron, Timezone: zone}
			if s.Trigger == "date" {
				spec.RunAt = schedule.FormatTime(s.RunAt)
			}
			defs = append(defs, pluginScheduleDefinition{ID: s.ID, Name: s.Name, Handler: "seedrescue", Trigger: spec, MisfireGraceMS: 1000})
		}
		updated := c.run
		var committedRevision int64
		e = c.o.a.pluginStore.configurePluginSchedulesWithEffect(ctx, &updated, "seedrescue", defs, c.o.Now(), nil, func(ctx context.Context, tx *sql.Tx, _ PluginRun) error {
			if e := c.guardTx(ctx, tx); e != nil {
				return e
			}
			s, b, e := c.o.a.pluginStore.seedRescueRead(ctx, tx, c.o.instance, c.o.generation)
			if e != nil {
				return e
			}
			revision := c.revision
			if c.staged != nil {
				v, e := c.o.a.pluginStore.saveValueTx(ctx, tx, c.o.instance, "config", "main", revision, mustCleanSeedJSON(c.staged))
				if e != nil {
					return e
				}
				revision = v.Revision
				if _, e = tx.ExecContext(ctx, `UPDATE plugin_schedules SET config_revision=? WHERE instance_id=? AND generation=? AND group_key='seedrescue'`, revision, c.o.instance, c.o.generation); e != nil {
					return e
				}
			}
			s.AppliedRevision = revision
			s.Config = mustCleanSeedJSON(m.Config())
			var stored []byte
			if e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, c.o.instance).Scan(&stored); e != nil {
				return e
			}
			plain, e := c.o.a.pluginStore.unseal("config", c.o.instance, "main", stored)
			if e != nil {
				return e
			}
			s.AppliedConfigSHA = seedRescueCanonicalSHA(plain)
			r := s.Runs[c.run.ID]
			r.EffectiveRevision = revision
			r.Phase = "confirmed"
			r.Result = json.RawMessage(`{"configured":true}`)
			s.Runs[c.run.ID] = r
			committedRevision = revision
			return c.o.a.pluginStore.seedRescueSave(ctx, tx, s, b)
		})
		if e != nil {
			return nil, e
		}
		c.revision = committedRevision
		return json.RawMessage(`{"configured":true}`), nil
	}
	if c.run.Action == "api" {
		req, e := seedRescueRequest(c.run)
		if e != nil {
			return nil, e
		}
		hash := req.Params["target_hash"]
		if len(hash) > 128 {
			return nil, bad("INVALID_PLUGIN_PARAMETER", "种子Hash参数无效")
		}
		var out sr.Response
		switch req.Key {
		case "scan":
			return c.queueScan(ctx, m)
		case "download":
			selection, e := sr.ParseSelection(req.Body)
			if e != nil {
				return nil, e
			}
			out, e = m.DownloadCandidate(ctx, hash, selection)
			if e != nil {
				return nil, e
			}
		case "retry":
			out, e = m.RetryJob(ctx, hash)
			if e != nil {
				return nil, e
			}
		case "ignore":
			out, e = m.IgnoreJob(ctx, hash)
			if e != nil {
				return nil, e
			}
		default:
			return nil, bad("SEED_RESCUE_SCOPE_DENIED", "稀缺保种API路由未绑定")
		}
		return seedRescueHTTPResponse(out), nil
	}
	if c.run.Action == "scan" {
		if e := c.validateScanSource(ctx); e != nil {
			return nil, e
		}
	}
	if c.run.Action == "once" || c.run.Action == "service" {
		if e := c.o.a.pluginStore.validateSeedRescueOccurrence(ctx, c.run); e != nil {
			return nil, e
		}
	}
	if e := m.Scan(ctx); e != nil {
		return nil, e
	}
	return json.RawMessage(`{"scanned":true}`), nil
}
func (p *PluginStore) seedRescueOutcome(ctx context.Context, run PluginRun, method string) (json.RawMessage, string, bool, error) {
	s, _, e := p.seedRescueRead(ctx, p.store.DB, run.InstanceID, run.Generation)
	if e != nil {
		return nil, "", false, e
	}
	r, ok := s.Runs[run.ID]
	if !ok {
		return nil, "", false, nil
	}
	if r.Action != run.Action || r.Revision != run.ConfigRevision || r.InputSHA != deferredInputSHA(run.Input) || r.Method != method {
		return nil, "", false, seedRescueEvidenceError()
	}
	if r.Phase != "confirmed" && r.Phase != "failed" {
		return nil, "", false, nil
	}
	for _, effect := range r.Effects {
		if effect.Phase != "confirmed" {
			return nil, "", false, nil
		}
	}
	return r.Result, r.ErrorCode, true, nil
}

func seedRescueSourceErrorClass(e error) string {
	if class := sr.ErrorClass(e); class != "" {
		return class
	}
	if code := seedRescueErrorCode(e); code != "" {
		return code
	}
	return "HostError"
}

func seedRescueValidationResponse(location, field string) json.RawMessage {
	return mustCleanSeedJSON(plugins.HTTPResponse{Status: 422, Body: mustCleanSeedJSON(map[string]any{"detail": []any{map[string]any{"type": "value_error", "loc": []string{location, field}, "msg": "Invalid input"}}})})
}
