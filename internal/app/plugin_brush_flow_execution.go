package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	bf "light-media/internal/plugins/brushflow"
)

type brushFlowLeaseKey struct{}
type brushFlowLease struct {
	d *pluginDeferredCall
	o *pluginBrushFlowOwner
}

func (d *pluginDeferredCall) lockBrushFlow(ctx context.Context) (context.Context, func(), error) {
	if hook := d.a.pluginBrushFlow.beforeLease; hook != nil {
		hook(ctx, d.run)
	}
	o, e := d.a.brushFlowOwner(ctx, d.installation)
	if e != nil {
		return ctx, nil, e
	}
	exclusive := d.run.Action == "init" || d.run.Action == "reload"
	if e = o.gate.acquire(ctx, exclusive); e != nil {
		return ctx, nil, e
	}
	owned := &brushFlowLiveLease{d: d}
	apiHeld := false
	if d.run.Action == "api" {
		select {
		case o.apiGate <- struct{}{}:
			apiHeld = true
		case <-ctx.Done():
			o.gate.release(exclusive)
			return ctx, nil, ctx.Err()
		}
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			o.mu.Lock()
			if o.live[d.run.ID] == owned {
				delete(o.live, d.run.ID)
			}
			o.mu.Unlock()
			if apiHeld {
				<-o.apiGate
			}
			o.gate.release(exclusive)
		})
	}

	if e = o.reconcileAdds(ctx); e != nil {
		release()
		return ctx, nil, e
	}
	if d.run.Action == "api" {
		busy, err := d.a.pluginStore.brushFlowRequestBusy(ctx, d.run)
		if err != nil {
			release()
			return ctx, nil, err
		}
		if busy {
			release()
			return ctx, nil, errBrushFlowBusy
		}
	}
	o.mu.Lock()
	if o.live[d.run.ID] != nil {
		o.mu.Unlock()
		release()
		return ctx, nil, brushFlowEvidenceError()
	}
	o.live[d.run.ID] = owned
	o.mu.Unlock()
	return context.WithValue(ctx, brushFlowLeaseKey{}, brushFlowLease{d, o}), release, nil
}

var errBrushFlowBusy = errors.New("private BrushFlow busy decision")

func (d *pluginDeferredCall) brushFlowStartedTx(ctx context.Context, tx *sql.Tx) error {
	lease, ok := ctx.Value(brushFlowLeaseKey{}).(brushFlowLease)
	if !ok || lease.d != d || lease.o == nil || lease.o.a != d.a || lease.o.instance != d.run.InstanceID || lease.o.generation != d.run.Generation {
		return brushFlowEvidenceError()
	}
	s, b, e := d.a.pluginStore.brushFlowReadCurrent(ctx, tx, d.run.InstanceID, d.run.Generation)
	if e != nil {
		return e
	}
	if _, exists := s.lookupRun(d.run.ID); exists {
		return bad("BRUSH_FLOW_REPLAY_DENIED", "站点刷流执行身份已经登记")
	}
	r := brushFlowAction{RunID: d.run.ID, Action: d.run.Action, Revision: d.run.ConfigRevision, EffectiveRevision: d.run.ConfigRevision, InputSHA: deferredInputSHA(d.run.Input), Method: d.record.Method, Phase: "started"}
	if brushFlowAdmissionState(ctx, d.a.pluginStore, s) != nil {
		r.Phase = "failed"
		r.ErrorCode = brushFlowBlockedByUnknown
		r.Result = brushFlowBlockedResult()
	}
	s.setRun(r.RunID, r)
	return d.a.pluginStore.brushFlowSave(ctx, tx, s, b)
}
func (d *pluginDeferredCall) brushFlowBusy(ctx context.Context) (json.RawMessage, error) {
	return d.brushFlowZeroEffect(ctx, false)
}
func (d *pluginDeferredCall) brushFlowBlocked(ctx context.Context) (json.RawMessage, error) {
	return d.brushFlowZeroEffect(ctx, true)
}
func (d *pluginDeferredCall) brushFlowZeroEffect(ctx context.Context, blocked bool) (json.RawMessage, error) {
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
		return nil, brushFlowEvidenceError()
	}
	if e = d.a.pluginStore.validateDeferredRunTx(ctx, tx, r, d.run); e != nil {
		return nil, e
	}
	if _, e = d.a.validatePluginHTTPRunTx(ctx, tx, d.run); e != nil {
		return nil, e
	}
	s, b, e := d.a.pluginStore.brushFlowReadCurrent(ctx, tx, d.run.InstanceID, d.run.Generation)
	if e != nil {
		return nil, e
	}
	if _, ok := s.lookupRun(d.run.ID); ok {
		return nil, brushFlowEvidenceError()
	}
	msg := "插件正在执行其他操作，请稍后重试"
	if req, _ := brushFlowRequest(d.run); req.Key == "run" || req.Key == "check" {
		msg = "任务已有操作正在执行"
	}
	out := brushFlowHTTPResponse(bf.Response{Message: msg})
	s.setRun(d.run.ID, brushFlowAction{RunID: d.run.ID, Action: d.run.Action, Revision: d.run.ConfigRevision, EffectiveRevision: d.run.ConfigRevision, InputSHA: deferredInputSHA(d.run.Input), Method: r.Method, Phase: "confirmed", Decision: "busy", Result: out})
	if blocked {
		out = brushFlowBlockedResult()
		value := s.run(d.run.ID)
		value.Phase = "failed"
		value.ErrorCode = brushFlowBlockedByUnknown
		value.Decision = "blocked_by_unknown"
		value.Result = out
		s.setRun(d.run.ID, value)
	}
	if e = d.a.pluginStore.brushFlowSave(ctx, tx, s, b); e != nil {
		return nil, e
	}
	r.Phase = "confirmed"
	if blocked {
		r.Phase = "terminal_failed"
		r.ErrorCode = brushFlowBlockedByUnknown
	}
	r.Result = out
	if e = d.a.pluginStore.writeDeferred(ctx, tx, r, blob); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	if blocked {
		return nil, bad(brushFlowBlockedByUnknown, "刷流已有操作待核对，本次没有执行外部操作")
	}
	return out, nil
}
func (a *App) shouldInitializeBrushFlow(ctx context.Context, in PluginInstallation, cfg PluginStoredValue) (bool, error) {
	s, _, e := a.pluginStore.brushFlowRead(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if e != nil {
		return false, e
	}
	if e = brushFlowNoUnknown(s); e != nil {
		return false, e
	}
	return s.AppliedRevision != cfg.Revision, nil
}
func (a *App) pluginBrushFlowHost(ctx context.Context, in PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if run == nil {
		return nil, brushFlowEvidenceError()
	}
	if method == bf.ReadCapability {
		return a.brushFlowReadAPI(ctx, in, *run, raw)
	}
	want, payload, e := brushFlowDeferredSpec(*run)
	if e != nil || want != method || !deferredPayloadEqual(raw, payload) {
		return nil, brushFlowEvidenceError()
	}
	if hook := a.pluginBrushFlow.beforeHost; hook != nil {
		hook(ctx, *run)
	}
	if e = a.requireDeferredExecution(ctx, run, method); e != nil {
		return nil, e
	}
	d, _ := ctx.Value(pluginDeferredExecutionKey{}).(*pluginDeferredCall)
	lease, ok := ctx.Value(brushFlowLeaseKey{}).(brushFlowLease)
	if !ok || lease.d != d || lease.o == nil || lease.o.a != a || lease.o.instance != in.InstanceID || lease.o.generation != in.Generation {
		return nil, brushFlowEvidenceError()
	}
	cfg, e := a.pluginStore.Config(ctx, in.InstanceID)
	if e != nil {
		return nil, e
	}
	value, e := brushFlowConfig(cfg.Value)
	if e != nil {
		return nil, e
	}
	c := &brushFlowCall{o: lease.o, run: *run, method: method, config: value, revision: cfg.Revision, writes: map[string]json.RawMessage{}, observations: map[string]map[string]bf.DownloadState{}}
	lease.o.mu.Lock()
	live := lease.o.live[run.ID]
	if live == nil || live.d != d || live.c != nil {
		lease.o.mu.Unlock()
		return nil, brushFlowEvidenceError()
	}
	live.c = c
	lease.o.mu.Unlock()
	ctx = context.WithValue(ctx, brushFlowCallKey{}, c)
	e = c.o.update(ctx, func(s *brushFlowState) error {
		r, exists := s.lookupRun(run.ID)
		if !exists {
			return brushFlowEvidenceError()
		}
		if r.Phase == "failed" && r.ErrorCode == brushFlowBlockedByUnknown && !r.Entered && len(r.Effects) == 0 && bytes.Equal(r.Result, brushFlowBlockedResult()) {
			return bad(brushFlowBlockedByUnknown, "已有站点刷流操作待核对，此次没有开始外部操作")
		}
		if r.Phase != "started" || r.Entered || r.RunID != run.ID || r.InputSHA != deferredInputSHA(run.Input) || r.Revision != run.ConfigRevision || r.EffectiveRevision != cfg.Revision || r.Method != method {
			return brushFlowEvidenceError()
		}
		r.Entered = true
		s.setRun(run.ID, r)
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
		return nil, bad("BRUSH_FLOW_UNCERTAIN", "下载或远端写操作结果待核对，未重新派发")
	}
	sourceErr := e
	if commitErr := c.commitResult(finish, result, sourceErr); commitErr != nil {
		c.o.Log(finish, "error", "保存刷流执行结果失败："+commitErr.Error())
		return nil, commitErr
	}
	return result, sourceErr
}

func (p *PluginStore) brushFlowOutcome(ctx context.Context, run PluginRun, method string) (json.RawMessage, string, bool, error) {
	s, _, e := p.brushFlowReadCurrent(ctx, p.store.DB, run.InstanceID, run.Generation)
	if e != nil {
		return nil, "", false, e
	}
	r, ok := s.lookupRun(run.ID)
	if ok {
		tx, e := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return nil, "", false, e
		}
		defer tx.Rollback()
		if e = p.brushFlowValidateAction(ctx, tx, s, r); e != nil {
			return nil, "", false, e
		}
	}
	if !ok {
		return nil, "", false, s.check()
	}
	if r.Action != run.Action || r.Revision != run.ConfigRevision || r.InputSHA != deferredInputSHA(run.Input) || r.Method != method {
		return nil, "", false, brushFlowEvidenceError()
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

func (p *PluginStore) brushFlowRequestBusy(ctx context.Context, run PluginRun) (bool, error) {
	q, e := brushFlowRequest(run)
	if e != nil {
		return false, e
	}
	target := q.Params["task_id"]
	if target == "" || q.Key == "state" {
		return false, nil
	}
	tx, e := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	s, _, e := p.brushFlowReadCurrent(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return false, e
	}
	if owner := p.brushFlowRuntimeOwner(run.InstanceID, run.Generation); owner != nil {
		if machine := owner.machineSnapshot(); machine != nil && machine.TaskBusy(target) {
			return true, nil
		}
	}
	for _, source := range s.pendingSources(ctx, tx) {
		if source.TaskID != target || source.Operation != "brush" && source.Operation != "check" {
			continue
		}
		var state string
		if e = tx.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, source.Run).Scan(&state); e != nil {
			return false, e
		}
		if state == "running" || state == "pending" {
			return true, nil
		}
	}
	for _, r := range s.pendingRuns() {
		if r.Action != "service" || r.Phase != "started" {
			continue
		}
		var raw []byte
		if e = tx.QueryRowContext(ctx, `SELECT s.definition FROM plugin_schedules s JOIN plugin_schedule_occurrences o ON o.instance_id=s.instance_id AND o.service_id=s.service_id WHERE o.run_id=? AND o.generation=? AND o.config_revision=? AND o.state='running'`, r.RunID, s.Generation, r.Revision).Scan(&raw); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			return false, e
		}
		var service string
		if tx.QueryRowContext(ctx, `SELECT service_id FROM plugin_schedule_occurrences WHERE run_id=?`, r.RunID).Scan(&service) != nil {
			return false, brushFlowEvidenceError()
		}
		clear, e := p.unseal("schedule", s.Instance, service, raw)
		if e != nil {
			return false, e
		}
		var def pluginScheduleDefinition
		if cleanupJournalStrict(clear, &def) != nil {
			return false, brushFlowEvidenceError()
		}
		op, e := brushFlowDefinitionOperation(s.Config, def)
		if e != nil {
			return false, e
		}
		if op.TaskID == target {
			return true, nil
		}
	}
	return false, s.check()
}
