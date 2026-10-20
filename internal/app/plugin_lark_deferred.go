package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
	lark "light-media/internal/plugins/larkmessager"
	larkhost "light-media/internal/plugins/larkmessager/host"
)

func larkDeferredSpec(run PluginRun) (string, json.RawMessage, error) {
	var in plugins.Invocation
	if cleanupJournalStrict(run.Input, &in) != nil || in.Action != run.Action {
		return "", nil, larkEvidenceError()
	}
	switch run.Action {
	case "init", "reload":
		return lark.ConfigureCapability, json.RawMessage(`{}`), nil
	case "page":
		return lark.PageCapability, json.RawMessage(`{}`), nil
	case "module", "event":
		key, method := "call_id", lark.ModuleCapability
		if run.Action == "event" {
			key, method = "event_id", lark.EventCapability
		}
		var v map[string]string
		if cleanupJournalStrict(in.Input, &v) != nil || len(v) != 1 || !pluginStorageID.MatchString(v[key]) {
			return "", nil, lark.ErrDenied
		}
		return method, mustCleanSeedJSON(v), nil
	case "api":
		var r plugins.HTTPRequest
		if cleanupJournalStrict(in.Input, &r) != nil || !pluginStorageID.MatchString(r.RequestID) {
			return "", nil, lark.ErrDenied
		}
		return lark.APICapability, mustCleanSeedJSON(map[string]string{"request_id": r.RequestID}), nil
	}
	return "", nil, lark.ErrDenied
}

// Source identity is derived from an actual native producer, never supplied by
// the SDK. Notice calls additionally re-open their authenticated queue fact.
func (a *App) larkSourceTx(ctx context.Context, tx *sql.Tx, run PluginRun, method string) (string, *pluginLarkSourceCall, error) {
	want, _, e := larkDeferredSpec(run)
	if e != nil || want != method {
		return "", nil, lark.ErrDenied
	}
	if run.Action == "module" {
		var inv plugins.Invocation
		var ref struct {
			ID string `json:"call_id"`
		}
		if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &ref) != nil {
			return "", nil, lark.ErrDenied
		}
		root, e := a.pluginStore.larkRootRead(ctx, tx, run.InstanceID, run.Generation)
		if e != nil {
			return "", nil, e
		}
		call, ok := root.Calls[ref.ID]
		if !ok || call.Run != run.ID || call.Generation != run.Generation || call.Revision != run.ConfigRevision || call.InputSHA != deferredInputSHA(run.Input) || call.ConfigSHA != tokenHash(string(inv.Config)) {
			return "", nil, larkEvidenceError()
		}
		if call.Kind == "notice_queue" {
			q, e := a.pluginStore.readPluginMessage(ctx, tx, call.Origin)
			if e != nil {
				return "", nil, e
			}
			n, e := larkNotificationFromSource(q.Call.Kwargs["message"])
			if e != nil || q.State != "intent" || len(q.Call.Args) != 1 || q.Call.Args[0] != "post_message" || call.SourceID != "notice-queue/"+q.ID || !larkSameRaw(mustCleanSeedJSON(call.Module), mustCleanSeedJSON(pluginLarkModuleInput{Name: "post_message", Notification: n})) {
				return "", nil, larkEvidenceError()
			}
		} else if call.Kind == "core_message" {
			core, ok := ctx.Value(pluginLarkCoreKey{}).(*pluginLarkCoreExecution)
			if !ok || core == nil || core.inbox.ID != call.Origin {
				return "", nil, lark.ErrDenied
			}
			_, f, e := a.larkCoreScopeTx(ctx, tx, core)
			if e != nil {
				return "", nil, e
			}
			steps, e := a.pluginStore.larkInboxStepsTx(ctx, tx, f)
			if e != nil {
				return "", nil, e
			}
			if call.Ordinal < 0 || call.Ordinal >= len(steps) {
				return "", nil, larkEvidenceError()
			}
			step := steps[call.Ordinal]
			if step.Call != call.ID || step.Run != run.ID || call.SourceID != f.SourceID+"/module/"+notificationChannelRevisionKey(int64(call.Ordinal)) || !larkSameRaw(mustCleanSeedJSON(step.Module), mustCleanSeedJSON(call.Module)) {
				return "", nil, larkEvidenceError()
			}
		} else {
			return "", nil, lark.ErrDenied
		}
		return call.SourceID, &call, nil
	}
	if run.Action == "api" {
		r, e := a.validatePluginHTTPRunTx(ctx, tx, run)
		if e != nil {
			return "", nil, e
		}
		return "http/" + r.RequestID, nil, nil
	}
	if run.Action == "event" {
		return "", nil, lark.ErrDependency
	}
	return "run/" + run.ID, nil, nil
}

func (d *pluginDeferredCall) larkStartedTx(ctx context.Context, tx *sql.Tx) error {
	lease, ok := ctx.Value(pluginLarkLeaseKey{}).(*pluginLarkLease)
	if !ok || lease == nil || lease.call != d || lease.owner.a != d.a {
		return lark.ErrDenied
	}
	source, _, e := d.a.larkSourceTx(ctx, tx, d.run, d.record.Method)
	if e != nil {
		return e
	}
	root, e := d.a.pluginStore.larkRootRead(ctx, tx, d.run.InstanceID, d.run.Generation)
	if e != nil {
		return e
	}
	if _, ok = root.Admissions[d.run.ID]; ok {
		return larkEvidenceError()
	}
	var inv plugins.Invocation
	if cleanupJournalStrict(d.run.Input, &inv) != nil {
		return larkEvidenceError()
	}
	r := pluginLarkAdmission{Version: 1, Instance: d.run.InstanceID, Run: d.run.ID, Generation: d.run.Generation, Revision: d.run.ConfigRevision, Action: d.run.Action, Method: d.record.Method, InputSHA: deferredInputSHA(d.run.Input), ConfigSHA: tokenHash(string(inv.Config)), SourceID: source, Phase: "accepted"}
	return d.a.pluginStore.larkSaveAdmissionTx(ctx, tx, root, r)
}

func (o *pluginLarkOwner) guardTx(ctx context.Context, tx *sql.Tx, scope larkhost.Scope) error {
	lease, ok := ctx.Value(pluginLarkLeaseKey{}).(*pluginLarkLease)
	if !ok || lease == nil || lease.owner != o || lease.call.a != o.a || scope.Owner != o.instance || scope.Generation != o.generation {
		return lark.ErrDenied
	}
	run := lease.call.run
	if scope.InvocationID != run.ID || scope.Revision != run.ConfigRevision || scope.Action != lease.call.record.Method || scope.InputSHA != deferredInputSHA(run.Input) {
		return lark.ErrDenied
	}
	d, _, e := o.a.pluginStore.readDeferred(ctx, tx, run)
	if e != nil {
		return e
	}
	if !deferredSameRun(d, lease.call.installation, run) || d.Phase != "started" || d.Method != scope.Action {
		return lark.ErrDenied
	}
	if e = o.a.pluginStore.validateDeferredRunTx(ctx, tx, d, run); e != nil {
		return e
	}
	root, e := o.a.pluginStore.larkRootRead(ctx, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	ad, ok := root.Admissions[run.ID]
	if !ok || ad.Phase != "accepted" || !larkAdmissionSame(ad, run, scope.Action) || scope.ConfigSHA != ad.ConfigSHA || scope.SourceID != ad.SourceID {
		return larkEvidenceError()
	}
	source, _, e := o.a.larkSourceTx(ctx, tx, run, scope.Action)
	if e != nil {
		return e
	}
	if source != scope.SourceID {
		return larkEvidenceError()
	}
	return nil
}

func (o *pluginLarkOwner) saveOutcome(ctx context.Context, run PluginRun, method string, result json.RawMessage, code string) error {
	tx, e := o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	root, e := o.a.pluginStore.larkRootRead(ctx, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	ad, ok := root.Admissions[run.ID]
	if !ok || !larkAdmissionSame(ad, run, method) || ad.Phase != "accepted" {
		return larkEvidenceError()
	}
	proofs, e := larkhost.AuditTx(ctx, o.a.Store.DB, tx, o.a.Store.key, larkhost.Group{Owner: o.instance, Generation: o.generation}, pluginLarkAuthority{o.a.pluginStore, o.instance, o.generation})
	if e != nil {
		return e
	}
	found := false
	for _, p := range proofs {
		if p.Scope.InvocationID == run.ID {
			found = true
			if p.Uncertain {
				return larkUnknownError()
			}
		}
	}
	if !found {
		return larkEvidenceError()
	}
	if !json.Valid(result) {
		return larkEvidenceError()
	}
	ad.Phase, ad.Result, ad.ErrorCode = "confirmed", append(json.RawMessage(nil), result...), code
	if code != "" {
		ad.Phase = "failed"
	}
	if e = o.a.pluginStore.larkSaveAdmissionTx(ctx, tx, root, ad); e != nil {
		return e
	}
	return tx.Commit()
}
func (p *PluginStore) larkOutcome(ctx context.Context, run PluginRun, method string) (json.RawMessage, string, bool, error) {
	tx, e := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, "", false, e
	}
	defer tx.Rollback()
	root, e := p.larkRootRead(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return nil, "", false, e
	}
	ad, ok := root.Admissions[run.ID]
	if !ok {
		return nil, "", false, nil
	}
	if !larkAdmissionSame(ad, run, method) {
		return nil, "", false, larkEvidenceError()
	}
	proofs, e := larkhost.AuditTx(ctx, p.store.DB, tx, p.store.key, larkhost.Group{Owner: run.InstanceID, Generation: run.Generation}, pluginLarkAuthority{p, run.InstanceID, run.Generation})
	if e != nil {
		return nil, "", false, e
	}
	found := false
	for _, proof := range proofs {
		if proof.Scope.InvocationID == run.ID {
			found = true
			if proof.Uncertain {
				return nil, "", false, nil
			}
		}
	}
	if ad.Phase == "accepted" {
		return nil, "", false, nil
	}
	if !found || !json.Valid(ad.Result) {
		return nil, "", false, larkEvidenceError()
	}
	if e = tx.Commit(); e != nil {
		return nil, "", false, e
	}
	return append(json.RawMessage(nil), ad.Result...), ad.ErrorCode, true, nil
}
func larkRecoveryErrorCode(e error) string {
	if e == nil {
		return ""
	}
	if errors.Is(e, larkhost.ErrIntegrity) || errors.Is(e, larkhost.ErrReplay) || pluginErrorCodeInternal(e) == "LARK_EVIDENCE_MISSING" {
		return "LARK_EVIDENCE_MISSING"
	}
	return ""
}
