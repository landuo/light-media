package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	iyuu "light-media/internal/plugins/iyuuautoseed"
)

type iyuuAdmissionKey struct{}
type iyuuAdmission struct {
	Run, Instance, Version, Action, InputSHA, Method string
	Generation, Revision                             int64
}
type iyuuRunFact struct {
	Run, Version, Action, InputSHA, Method, Phase string
	Generation, Revision, ResultRevision          int64
	Result                                        json.RawMessage `json:",omitempty"`
}

func iyuuAdmissionFromRun(run PluginRun, method string) iyuuAdmission {
	return iyuuAdmission{run.ID, run.InstanceID, run.Version, run.Action, deferredInputSHA(run.Input), method, run.Generation, run.ConfigRevision}
}
func iyuuAdmissionMatches(ctx context.Context, run PluginRun, d pluginDeferredRecord) bool {
	a, ok := ctx.Value(iyuuAdmissionKey{}).(iyuuAdmission)
	return ok && a.Run == run.ID && a.Instance == run.InstanceID && a.Version == run.Version && a.Action == run.Action && a.InputSHA == d.InputSHA && a.Method == d.Method && a.Generation == run.Generation && a.Revision == d.Revision
}

// The independent root proves the exact accepted run set. The ordinary Root
// ledger and sealed deferred receipt must agree in both directions; missing
// evidence cannot be replaced with an empty set or a new admission.
func (p *PluginStore) iyuuValidateDeferredTx(ctx context.Context, tx *sql.Tx, root iyuuOwnerRoot) error {
	rows, e := tx.QueryContext(ctx, "SELECT id FROM plugin_runs WHERE instance_id=? AND generation=? ORDER BY id", root.Instance, root.Generation)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	unknown := false
	for _, id := range ids {
		run, e := p.scanPluginRun(tx.QueryRowContext(ctx, "SELECT "+pluginRunColumns+" FROM plugin_runs WHERE id=?", id))
		if e != nil {
			return iyuu.ErrJournalIntegrity
		}
		fact, exists := root.Runs[id]
		d, _, e := p.readDeferred(ctx, tx, run)
		if errors.Is(e, sql.ErrNoRows) {
			if exists || run.State == "succeeded" || run.State == "reconcile_pending" {
				return iyuu.ErrJournalIntegrity
			}
			continue
		}
		if e != nil {
			return iyuu.ErrJournalIntegrity
		}
		method, payload, e := iyuuDeferredSpec(run)
		if e != nil || d.Plugin != "IYUUAutoSeed" || d.Version != "2.19" || d.Run != run.ID || d.Instance != root.Instance || d.Generation != root.Generation || d.Version != run.Version || d.Action != run.Action || d.InputSHA != deferredInputSHA(run.Input) || d.Method != method || !deferredPayloadEqual(d.Raw, payload) {
			return iyuu.ErrJournalIntegrity
		}
		if !exists {
			if d.Phase == "prepared" && run.State != "succeeded" {
				continue
			}
			if d.Phase == "started" && run.State == "running" && iyuuAdmissionMatches(ctx, run, d) {
				continue
			}
			return iyuu.ErrJournalIntegrity
		}
		seen[id] = true
		if fact.Run != id || fact.Version != d.Version || fact.Generation != d.Generation || fact.Action != d.Action || fact.InputSHA != d.InputSHA || fact.Method != d.Method || fact.Revision != d.Revision {
			return iyuu.ErrJournalIntegrity
		}
		if fact.Phase == "accepted" {
			if fact.ResultRevision != 0 || len(fact.Result) != 0 || run.ConfigRevision != fact.Revision {
				return iyuu.ErrJournalIntegrity
			}
			if (d.Phase == "started" || d.Phase == "uncertain") && iyuuAdmissionMatches(ctx, run, d) && run.State == "running" {
				continue
			}
			if d.Phase == "started" || d.Phase == "uncertain" {
				unknown = true
				continue // Validate every later run and missing root fact first.
			}
			return iyuu.ErrJournalIntegrity
		}
		revisionOK := run.ConfigRevision == fact.ResultRevision
		if !revisionOK && fact.ResultRevision == run.ConfigRevision+1 && (run.Action == "init" || run.Action == "reload") && d.Phase == "started" && d.Method == iyuu.ConfigureCapability && iyuuAdmissionMatches(ctx, run, d) {
			revisionOK = true
		}
		if fact.Phase != "confirmed" || !json.Valid(fact.Result) || fact.ResultRevision < fact.Revision || !revisionOK {
			return iyuu.ErrJournalIntegrity
		}
		switch d.Phase {
		case "started", "uncertain":
		case "confirmed":
			if !bytes.Equal(d.Result, fact.Result) || d.ErrorCode != "" {
				return iyuu.ErrJournalIntegrity
			}
		default:
			return iyuu.ErrJournalIntegrity
		}
	}
	for id := range root.Runs {
		if !seen[id] {
			return iyuu.ErrJournalIntegrity
		}
	}
	if unknown {
		return iyuu.ErrUnconfirmed
	}
	return nil
}
func (p *PluginStore) iyuuSaveRootTx(ctx context.Context, tx *sql.Tx, root iyuuOwnerRoot, prior []byte) error {
	raw, e := json.Marshal(root)
	if e != nil {
		return e
	}
	sealed, e := p.seal("run_result", root.Instance, iyuuRootKey(root.Generation), raw)
	if e != nil {
		return e
	}
	changed, e := tx.ExecContext(ctx, "UPDATE plugin_iyuu_roots SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?", sealed, root.Instance, root.Generation, prior)
	if e != nil {
		return e
	}
	n, e := changed.RowsAffected()
	if e != nil || n != 1 {
		return iyuu.ErrJournalIntegrity
	}
	return nil
}
func (p *PluginStore) iyuuAcceptRun(ctx context.Context, run PluginRun) error {
	tx, e := p.store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = p.iyuuAcceptRunTx(ctx, tx, run); e != nil {
		return e
	}
	return tx.Commit()
}

// Started and the accepted original input are one durable admission. A crash
// cannot leave a legal started row outside the independently sealed run set.
func (p *PluginStore) iyuuAcceptRunTx(ctx context.Context, tx *sql.Tx, run PluginRun) error {
	root, prior, e := p.iyuuRootReadTx(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return e
	}
	if e = p.iyuuValidateDeferredTx(ctx, tx, root); e != nil {
		return e
	}
	current, e := p.scanPluginRun(tx.QueryRowContext(ctx, "SELECT "+pluginRunColumns+" FROM plugin_runs WHERE id=?", run.ID))
	if e != nil {
		return e
	}
	d, _, e := p.readDeferred(ctx, tx, current)
	if e != nil || d.Phase != "started" || !iyuuAdmissionMatches(ctx, current, d) {
		return iyuu.ErrJournalIntegrity
	}
	if _, ok := root.Runs[run.ID]; ok {
		return nil
	}
	root.Runs[run.ID] = iyuuRunFact{Run: run.ID, Version: run.Version, Action: run.Action, InputSHA: d.InputSHA, Method: d.Method, Phase: "accepted", Generation: run.Generation, Revision: d.Revision}
	root.Sequence++
	if e = p.iyuuSaveRootTx(ctx, tx, root, prior); e != nil {
		return e
	}
	return nil
}
func (p *PluginStore) iyuuCompleteRunTx(ctx context.Context, tx *sql.Tx, run PluginRun, result json.RawMessage, revision int64) error {
	root, prior, e := p.iyuuRootReadTx(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return e
	}
	fact, ok := root.Runs[run.ID]
	if !ok || fact.InputSHA != deferredInputSHA(run.Input) || fact.Action != run.Action || revision < fact.Revision || !json.Valid(result) {
		return iyuu.ErrJournalIntegrity
	}
	if fact.Phase == "confirmed" {
		if !bytes.Equal(fact.Result, result) || fact.ResultRevision != revision {
			return iyuu.ErrJournalIntegrity
		}
		return nil
	}
	if fact.Phase != "accepted" {
		return iyuu.ErrJournalIntegrity
	}
	fact.Phase = "confirmed"
	fact.Result = append(json.RawMessage{}, result...)
	fact.ResultRevision = revision
	root.Runs[run.ID] = fact
	root.Sequence++
	return p.iyuuSaveRootTx(ctx, tx, root, prior)
}
func iyuuSourceResult(run PluginRun, state iyuu.RuntimeCheckpoint) (json.RawMessage, error) {
	if run.Action == "init" || run.Action == "reload" {
		return json.RawMessage(`{"configured":true}`), nil
	}
	if run.Action == "event" {
		return json.RawMessage(`{"completed":true}`), nil
	}
	return json.Marshal(map[string]any{"completed": true, "counts": state.Core.Counters})
}

// Every configuration writer shares this transaction boundary, including peer
// site-switch/follow updates that do not use the IYUU HTTP wrapper. No owner
// wait or journal lock is taken while shared application locks are held.
func (p *PluginStore) iyuuConfigWriteTx(ctx context.Context, tx *sql.Tx, instance string, revision int64, raw json.RawMessage) error {
	var plugin string
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin); e != nil {
		return e
	}
	if plugin != "IYUUAutoSeed" {
		return nil
	}
	var in PluginInstallation
	in.InstanceID = instance
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?", instance).Scan(&in.PluginID, &in.Version, &in.Generation); e != nil {
		return e
	}
	protocol, e := p.readSiteReferenceProtocol(ctx, tx, in)
	if e != nil {
		grant, ok := ctx.Value(iyuuOriginalImportKey{}).(iyuuOriginalImportAdmission)
		if !ok || grant.Instance != instance || grant.ConfigSHA != tokenHash(string(raw)) || revision != 0 || protocol.Protocol != originalUnclaimedSiteReferenceProtocol || protocol.Source != "moviepilot-import/v1:"+grant.Migration || pluginErrorCodeInternal(e) != "PLUGIN_SITE_PROTOCOL_UNCLAIMED" {
			return e
		}
		var count int
		if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM plugin_migration_runs WHERE id=? AND instance_id=? AND state='planned'", grant.Migration, instance).Scan(&count); e != nil {
			return e
		}
		if count != 1 {
			return iyuu.ErrJournalIntegrity
		}
	}
	_, e = p.iyuuTransitionParentTx(ctx, tx, instance)
	return e
}

// The original update_config retains these runtime-owned caches. They are not
// visible form fields. A user save cannot silently clear them or replace them
// with arbitrary submitted hashes; clearcache remains a source Init action.
func (p *PluginStore) iyuuPreserveCachesTx(ctx context.Context, tx *sql.Tx, instance string, revision int64, raw json.RawMessage) (json.RawMessage, error) {
	var plugin string
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin); e != nil {
		return nil, e
	}
	if plugin != "IYUUAutoSeed" {
		return raw, nil
	}
	var sealed []byte
	var current int64
	if e := tx.QueryRowContext(ctx, "SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'", instance).Scan(&current, &sealed); e != nil {
		if errors.Is(e, sql.ErrNoRows) && revision == 0 {
			return raw, nil
		}
		return nil, e
	}
	if current != revision {
		return nil, bad("PLUGIN_REVISION_CONFLICT", "插件数据已变化，请重新读取后保存")
	}
	prior, e := p.unseal("config", instance, "main", sealed)
	if e != nil {
		return nil, e
	}
	var old, next map[string]json.RawMessage
	if json.Unmarshal(prior, &old) != nil || json.Unmarshal(raw, &next) != nil || old == nil || next == nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "IYUU配置必须为对象")
	}
	for _, key := range []string{"success_caches", "error_caches", "permanent_error_caches"} {
		if v, ok := old[key]; ok {
			next[key] = v
		} else {
			delete(next, key)
		}
	}
	return pluginJSON(next, true)
}

// Minted only by the trusted original import transaction, bound to its exact
// initial config bytes. It permits staging data, never activating its protocol.
type iyuuOriginalImportKey struct{}
type iyuuOriginalImportAdmission struct{ Instance, Migration, ConfigSHA string }

// Queue admission precedes a run/deferred identity. A rejected new request must
// not create a started record that never entered its independent owner chain.
func (p *PluginStore) iyuuQueueAdmissionTx(ctx context.Context, tx *sql.Tx, instance, plugin string) error {
	if plugin != "IYUUAutoSeed" {
		return nil
	}
	_, e := p.iyuuTransitionParentTx(ctx, tx, instance)
	if errors.Is(e, iyuu.ErrUnconfirmed) {
		return fmt.Errorf("%w: %w", bad("IYUU_UNCONFIRMED", "IYUU存在待核对执行，原证据已保留，暂不能开始新任务"), e)
	}
	if errors.Is(e, iyuu.ErrJournalIntegrity) || errors.Is(e, iyuu.ErrReplayDiverged) {
		return fmt.Errorf("%w: %w", bad("IYUU_EVIDENCE_MISSING", "IYUU执行证据缺失或无法校验，暂不能开始新任务"), e)
	}
	return e
}

// Integrity/storage errors are not a safe scheduler rejection. They must be
// surfaced; only a validated existing unknown may be skipped for this tick.
func iyuuUnconfirmedQueueAdmission(err error) bool {
	return pluginErrorCodeInternal(err) == "IYUU_UNCONFIRMED" && errors.Is(err, iyuu.ErrUnconfirmed) && !errors.Is(err, iyuu.ErrJournalIntegrity) && !errors.Is(err, iyuu.ErrReplayDiverged)
}
