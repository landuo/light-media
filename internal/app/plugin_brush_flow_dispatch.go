package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	bf "light-media/internal/plugins/brushflow"
)

func (c *brushFlowCall) execute(ctx context.Context) (json.RawMessage, error) {
	if e := c.Guard(ctx); e != nil {
		return nil, e
	}
	if c.run.Action != "init" && c.run.Action != "reload" {
		s, _, e := c.o.a.pluginStore.brushFlowRead(ctx, c.o.a.Store.DB, c.o.instance, c.o.generation)
		if e != nil {
			return nil, e
		}
		if s.AppliedRevision != c.revision || s.AppliedConfigSHA != brushFlowCanonicalSHA(mustCleanSeedJSON(c.config)) {
			return nil, bad("BRUSH_FLOW_CONFIG_PENDING", "站点刷流配置尚未初始化完成")
		}
	}
	var core *bf.Core
	var e error
	if c.run.Action == "init" || c.run.Action == "reload" {
		c.o.subscriptionMu.Lock()
		c.o.subscriptionNames = map[string][]string{}
		c.o.subscriptionMu.Unlock()
		core, e = bf.New(ctx, c.o, c.config)
		if e != nil {
			return nil, e
		}
		c.services = core.Services(ctx)
		c.o.setMachine(core)
		return json.RawMessage(`{"configured":true}`), nil
	}
	if c.run.Action == "api" {
		cfg, err := bf.LoadApplied(c.config)
		if err != nil {
			return nil, err
		}
		// Original API changes publish config only after their SQL CAS commits.
		// Active source tasks retain shared locks/runtime on the live machine.
		core = bf.NewReadProjection(c.o, cfg)
	} else {
		core, e = c.o.runtimeMachine(c.config)
		if e != nil {
			return nil, e
		}
	}

	if e = c.projectRuntime(ctx, core); e != nil {
		return nil, e
	}
	if c.run.Action == "api" {
		q, e := brushFlowRequest(c.run)
		if e != nil {
			return nil, e
		}
		var response bf.Response
		switch q.Key {
		case "settings", "create", "update", "state":
			raw, e := brushFlowBody(q.Body)
			if e != nil {
				return brushFlowValidation("body"), nil
			}
			switch q.Key {
			case "settings":
				response, e = core.UpdateSettings(ctx, raw)
			case "create":
				response, e = core.CreateTask(ctx, raw)
			case "update":
				response, e = core.UpdateTask(ctx, q.Params["task_id"], raw)
			case "state":
				enabled, x := bf.ValidateTaskState(raw)
				if x != nil {
					e = x
				} else {
					response, e = core.StateTask(ctx, q.Params["task_id"], enabled)
				}
			}
			if e != nil {
				var validation *bf.ValidationError
				if errors.As(e, &validation) {
					return brushFlowValidation(validation.Field), nil
				}
				return nil, e
			}
		case "delete":
			response, e = core.DeleteTask(ctx, q.Params["task_id"])
		case "clear":
			response, e = core.ClearTask(ctx, q.Params["task_id"])
		case "run":
			response, e = core.Submit(ctx, q.Params["task_id"], "brush")
		case "check":
			response, e = core.Submit(ctx, q.Params["task_id"], "check")
		default:
			return nil, brushFlowEvidenceError()
		}
		if e != nil {
			return nil, e
		}
		return brushFlowHTTPResponse(response), nil
	}
	if c.run.Action == "run" {
		return json.RawMessage(`null`), nil
	}
	var op brushFlowQueued
	if c.run.Action == "task" {
		op, e = c.sourceOperation(ctx)
	} else if c.run.Action == "service" {
		op, e = c.scheduleOperation(ctx)
	} else {
		return nil, brushFlowEvidenceError()
	}
	if e != nil {
		return nil, e
	}
	c.taskID, c.operation = op.TaskID, op.Operation
	switch op.Operation {
	case "brush":
		e = core.Brush(ctx, op.TaskID)
	case "check":
		e = core.Check(ctx, op.TaskID, false)
	case "promotion_expiry":
		e = core.CheckPromotion(ctx, op.TaskID)
	case "cleanup_tags":
		e = core.CleanupTags(ctx)
	case "cleanup_task_tag":
		var row bf.Row
		if json.Unmarshal(op.Snapshot, &row) != nil {
			return nil, brushFlowEvidenceError()
		}
		e = core.CleanupTaskTag(ctx, bf.Task{Row: row}, nil)
	default:
		e = brushFlowEvidenceError()
	}
	if e != nil {
		return nil, e
	}
	return json.RawMessage(`null`), nil
}
func (c *brushFlowCall) projectRuntime(ctx context.Context, core *bf.Core) error {
	tx, e := c.o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	s, _, e := c.o.a.pluginStore.brushFlowRead(ctx, tx, c.o.instance, c.o.generation)
	if e != nil {
		return e
	}
	for _, task := range core.Config().Tasks {
		core.SetRuntime(task.ID(), bf.Row{"state": "idle", "operation": nil})
	}
	for _, source := range s.Sources {
		if source.Run == c.run.ID || source.Operation != "brush" && source.Operation != "check" {
			continue
		}
		var state string
		if e = tx.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, source.Run).Scan(&state); e != nil {
			return e
		}
		if state == "pending" || state == "running" {
			core.SetRuntime(source.TaskID, bf.Row{"state": map[bool]string{true: "running", false: "queued"}[state == "running"], "operation": source.Operation, "last_error": nil})
		}
	}
	return nil
}
func brushFlowSourceErrorClass(e error) string {
	if errors.Is(e, context.Canceled) {
		return "CancelledError"
	}
	var v *bf.ValidationError
	if errors.As(e, &v) {
		return "ValidationError"
	}
	return "SourceError"
}
func (c *brushFlowCall) commitResult(ctx context.Context, result json.RawMessage, sourceErr error) error {
	tx, e := c.o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	s, b, e := c.o.a.pluginStore.brushFlowRead(ctx, tx, c.o.instance, c.o.generation)
	if e != nil {
		return e
	}
	ctx = context.WithValue(ctx, brushFlowLocalCommitKey{}, &brushFlowLocalTransaction{c, tx, &s})
	r := s.Runs[c.run.ID]
	if r.Phase != "started" {
		return brushFlowEvidenceError()
	}
	for _, effect := range r.Effects {
		if effect.Phase != "confirmed" {
			return bad("BRUSH_FLOW_UNCERTAIN", "站点刷流外部操作尚未确认")
		}
	}
	guardPassed := true
	if guard := c.guardTx(ctx, tx); guard != nil {
		guardPassed = false
		sourceErr = guard
		c.staged = nil
		c.writes = map[string]json.RawMessage{}
		c.queued = nil
	}
	var nextRevision int64 = c.revision
	if c.staged != nil {
		value, e := c.o.a.pluginStore.saveValueTx(ctx, tx, c.o.instance, "config", "main", c.revision, mustCleanSeedJSON(c.staged))
		if e != nil {
			return e
		}
		nextRevision = value.Revision
		r.Effects = append(r.Effects, brushFlowEffect{Kind: "own_config", Phase: "confirmed", Args: mustCleanSeedJSON(map[string]any{"from_revision": c.revision, "to_revision": nextRevision, "config_sha": tokenHash(string(value.Value))}), Result: json.RawMessage(`null`)})
		s.Config = value.Value
		s.AppliedRevision = nextRevision
		s.AppliedConfigSHA = brushFlowCanonicalSHA(value.Value)
		r.EffectiveRevision = nextRevision
		for id, active := range s.Runs {
			if id != c.run.ID && active.Phase == "started" && c.o.hasLive(s, active) {
				active.EffectiveRevision = nextRevision
				s.Runs[id] = active
			}
		}
		if e = c.writeSchedulesTx(ctx, tx, nextRevision, c.staged); e != nil {
			return e
		}
	} else if guardPassed && (c.run.Action == "init" || c.run.Action == "reload") {
		s.Config = mustCleanSeedJSON(c.config)
		s.AppliedRevision = nextRevision
		s.AppliedConfigSHA = brushFlowCanonicalSHA(s.Config)
		if e = c.writeSchedulesTx(ctx, tx, nextRevision, c.config); e != nil {
			return e
		}
	}
	for key, raw := range c.writes {
		if e = c.writeValueTx(ctx, tx, &s, key, raw); e != nil {
			return e
		}
	}
	r.Phase = "confirmed"
	r.Result = result
	if len(r.Result) == 0 {
		r.Result = json.RawMessage(`null`)
	}
	if sourceErr != nil {
		r.Phase = "failed"
		r.ErrorCode = "BRUSH_FLOW_SOURCE_FAILED"
		r.Result = mustCleanSeedJSON(map[string]any{"source_failed": true, "error_type": brushFlowSourceErrorClass(sourceErr)})
	}
	s.Runs[c.run.ID] = r
	if e = c.publishQueuedTx(ctx, tx, &s); e != nil {
		return e
	}
	if e = c.o.a.pluginStore.brushFlowSave(ctx, tx, s, b); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		c.uncertain.Store(true)
		return e
	}
	c.revision = nextRevision
	if c.staged != nil {
		c.config = c.staged
		cfg, err := bf.LoadApplied(c.staged)
		if err != nil {
			return err
		}
		if live := c.o.machineSnapshot(); live != nil {
			live.ApplyConfig(cfg)
		}
	}
	if len(c.queued) > 0 {
		c.o.a.wakePlugins()
	}
	return nil
}
func (c *brushFlowCall) scheduleOperation(ctx context.Context) (brushFlowQueued, error) {
	return c.readScheduleOperation(ctx)
}
