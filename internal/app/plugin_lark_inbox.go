package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"light-media/internal/plugins"
	lark "light-media/internal/plugins/larkmessager"
)

type pluginLarkInbox struct {
	Version                                                                                    int
	ID, Instance, SourceID, OriginRun, InputSHA, ConfigSHA, Job, JobInputSHA, State, ErrorCode string
	Generation, Revision                                                                       int64
	Payload                                                                                    lark.Object
	Count                                                                                      int
	LastSHA                                                                                    string
}
type pluginLarkInboxStep struct {
	Version                       int
	Inbox, Call, Run, PreviousSHA string
	Ordinal                       int
	Module                        pluginLarkModuleInput
	Kind                          string          `json:",omitempty"`
	Operation                     string          `json:",omitempty"`
	InputSHA                      string          `json:",omitempty"`
	Phase                         string          `json:",omitempty"`
	ErrorCode                     string          `json:",omitempty"`
	Result                        json.RawMessage `json:",omitempty"`
}
type pluginLarkCoreExecution struct {
	a       *App
	inbox   pluginLarkInbox
	job     Job
	ordinal int
	release func()
}
type pluginLarkCoreKey struct{}

func larkInboxSHA(f pluginLarkInbox) string         { return tokenHash(string(mustCleanSeedJSON(f))) }
func larkInboxStepSHA(f pluginLarkInboxStep) string { return tokenHash(string(mustCleanSeedJSON(f))) }
func (p *PluginStore) larkSaveInboxTx(ctx context.Context, tx *sql.Tx, root pluginLarkRootView, f pluginLarkInbox) error {
	if f.Instance != root.Root.Instance || f.Generation != root.Root.Generation {
		return larkEvidenceError()
	}
	sealed, e := p.seal("lark_inbox", f.Instance, larkRootKey(f.Generation)+"/inbox/"+f.ID, mustCleanSeedJSON(f))
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_lark_inbox VALUES(?,?,?,?,?) ON CONFLICT(instance_id,generation,inbox_id) DO UPDATE SET sealed=excluded.sealed`, f.Instance, f.Generation, f.ID, f.SourceID, sealed); e != nil {
		return e
	}
	next := root.Root
	next.Sequence++
	return p.larkAppendRootTx(ctx, tx, root, next, "inbox", f.ID, larkInboxSHA(f))
}
func (p *PluginStore) larkInboxStepsTx(ctx context.Context, q pluginRemoveLinkJournalReader, f pluginLarkInbox) ([]pluginLarkInboxStep, error) {
	rows, e := q.QueryContext(ctx, `SELECT ordinal,sealed FROM plugin_lark_inbox_steps WHERE inbox_id=? ORDER BY ordinal`, f.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	steps := []pluginLarkInboxStep{}
	previous := ""
	for rows.Next() {
		var n int
		var sealed []byte
		var step pluginLarkInboxStep
		if e = rows.Scan(&n, &sealed); e != nil {
			return nil, e
		}
		raw, e := p.unseal("lark_inbox_step", f.Instance, f.ID+"/"+notificationChannelRevisionKey(int64(n)), sealed)
		if e != nil || cleanupJournalStrict(raw, &step) != nil || step.Version != 1 || step.Inbox != f.ID || step.Ordinal != len(steps) || n != step.Ordinal || step.PreviousSHA != previous {
			return nil, larkEvidenceError()
		}
		if step.Kind == "" || step.Kind == "module" {
			if !pluginStorageID.MatchString(step.Call) || !pluginStorageID.MatchString(step.Run) || step.Operation != "" || step.InputSHA != "" || step.Phase != "" || len(step.Result) != 0 || step.ErrorCode != "" {
				return nil, larkEvidenceError()
			}
		} else if !larkLocalStepValid(step) {
			return nil, larkEvidenceError()
		}
		steps = append(steps, step)
		previous = larkInboxStepSHA(step)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(steps) != f.Count || previous != f.LastSHA {
		return nil, larkEvidenceError()
	}
	return steps, nil
}
func (p *PluginStore) larkInboxBackingTx(ctx context.Context, tx *sql.Tx, f pluginLarkInbox) error {
	if f.State != "queued" && f.State != "started" && f.State != "done" && f.State != "unknown" && f.State != "failed" {
		return larkEvidenceError()
	}
	run, e := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, f.OriginRun))
	if e != nil {
		return larkEvidenceError()
	}
	var inv plugins.Invocation
	var request plugins.HTTPRequest
	if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &request) != nil || run.InstanceID != f.Instance || run.Generation != f.Generation || run.ConfigRevision != f.Revision || run.Action != "api" || request.Key != "webhook" || f.InputSHA != deferredInputSHA(run.Input) || f.ConfigSHA != tokenHash(string(inv.Config)) {
		return larkEvidenceError()
	}
	var sealed []byte
	if e = tx.QueryRowContext(ctx, `SELECT scope FROM plugin_api_calls WHERE run_id=? AND instance_id=? AND generation=? AND config_revision=?`, run.ID, f.Instance, f.Generation, f.Revision).Scan(&sealed); e != nil {
		return larkEvidenceError()
	}
	scopeRaw, e := p.unseal("api_scope", f.Instance, run.ID, sealed)
	var scope pluginAPIScope
	if e != nil || cleanupJournalStrict(scopeRaw, &scope) != nil || scope.Key != request.Key || scope.Method != request.Method || scope.RequestID != request.RequestID || scope.Authority.Kind != "anonymous" || scope.Authority.Proof != "" {
		return larkEvidenceError()
	}
	authorized := false
	for _, api := range lark.Manifest().APIs {
		if api.Key == scope.Key && api.Auth == "anonymous" && slices.Contains(api.Methods, scope.Method) {
			params, matched := api.Match(request.Path)
			authorized = matched && maps.Equal(params, request.Params)
		}
	}
	if !authorized {
		return larkEvidenceError()
	}
	var job Job
	job, e = scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, f.Job))
	if e != nil {
		return larkEvidenceError()
	}
	var payload struct {
		ID string `json:"id"`
	}
	if job.Kind != "lark_message" || json.Unmarshal(job.Payload, &payload) != nil || payload.ID != f.ID || tokenHash(string(job.Payload)) != f.JobInputSHA {
		return larkEvidenceError()
	}
	_, e = p.larkInboxStepsTx(ctx, tx, f)
	return e
}
func (o *pluginLarkOwner) enqueueIncoming(ctx context.Context, payload lark.Object) error {
	x, e := o.execution(ctx)
	if e != nil || x.scope.Action != lark.APICapability {
		return lark.ErrDenied
	}
	lease := ctx.Value(pluginLarkLeaseKey{}).(*pluginLarkLease)
	tx, e := o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = o.guardTx(ctx, tx, x.scope); e != nil {
		return e
	}
	r, e := o.a.validatePluginHTTPRunTx(ctx, tx, lease.call.run)
	if e != nil || r.Key != "webhook" {
		return lark.ErrDenied
	}
	c := o.plugin.ConfigTrusted()
	decoded, e := lark.DecodeSourceWebhook(r.Body, r.Headers, plugins.LastQuery(r.Query, "token"), c.EncryptKey, c.VerificationToken)
	if e != nil || !decoded.Accepted {
		return lark.ErrDenied
	}
	var expected lark.Object
	switch decoded.EventType {
	case "im.message.receive_v1":
		expected, e = lark.ReceivePayload(decoded.Body)
	case "card.action.trigger":
		var test bool
		expected, test, e = lark.CardActionPayload(decoded.Body)
		if test {
			return lark.ErrDenied
		}
	default:
		return lark.ErrDenied
	}
	if e != nil || !larkSameRaw(mustCleanSeedJSON(expected), mustCleanSeedJSON(payload)) {
		return lark.ErrDenied
	}
	source := "incoming/http/" + r.RequestID
	if header, ok := decoded.Body["header"].(map[string]any); ok {
		if event := larkSourceText(header["event_id"]); event != "" {
			source = "incoming/event/" + tokenHash(event)
		}
	}
	var oldGen int64
	var oldID string
	e = tx.QueryRowContext(ctx, `SELECT generation,inbox_id FROM plugin_lark_inbox WHERE instance_id=? AND source_id=?`, o.instance, source).Scan(&oldGen, &oldID)
	if e == nil {
		root, e := o.a.pluginStore.larkRootRead(ctx, tx, o.instance, oldGen)
		if e != nil {
			return e
		}
		old, ok := root.Inboxes[oldID]
		if !ok || old.SourceID != source || !larkSameRaw(mustCleanSeedJSON(old.Payload), mustCleanSeedJSON(payload)) {
			return larkEvidenceError()
		}
		if e = o.a.pluginStore.larkInboxBackingTx(ctx, tx, old); e != nil {
			return e
		}
		return tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	root, e := o.a.pluginStore.larkRootRead(ctx, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	id := "li_" + o.a.pluginStore.noticeID("lark-inbox", o.instance, source)
	job, e := enqueueTx(ctx, tx, "lark_message", map[string]string{"id": id}, id)
	if e != nil {
		return e
	}
	f := pluginLarkInbox{Version: 1, ID: id, Instance: o.instance, Generation: o.generation, Revision: lease.call.run.ConfigRevision, SourceID: source, OriginRun: lease.call.run.ID, InputSHA: deferredInputSHA(lease.call.run.Input), ConfigSHA: x.scope.ConfigSHA, Job: job.ID, JobInputSHA: tokenHash(string(job.Payload)), State: "queued", Payload: payload}
	if e = o.a.pluginStore.larkSaveInboxTx(ctx, tx, root, f); e != nil {
		return e
	}
	return tx.Commit()
}
