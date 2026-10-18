package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

const brushFlowRootSchema = `CREATE TABLE IF NOT EXISTS plugin_brush_flow_roots(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));`

type brushFlowRoot struct {
	Version                                       int
	Purpose, Instance, Event, ParentSHA, StateSHA string
	Generation, ParentGeneration, Sequence        int64
}
type brushFlowParent struct {
	Generation int64
	SHA        string
}
type brushFlowAdmissionKey struct{}
type brushFlowAdmission struct {
	Run, Instance, Version, Action, InputSHA, Method string
	Generation, Revision                             int64
}

func brushFlowErrorCode(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func brushFlowRootKey(g int64) string { return "brush-flow-root:v1:" + strconv.FormatInt(g, 10) }
func brushFlowEvidenceError() error {
	return bad("BRUSH_FLOW_EVIDENCE_MISSING", "BrushFlow站点刷流执行证据缺失或不一致，请恢复可信站点刷流；未重新初始化或重复执行")
}
func (a *App) brushFlowAuthorizedAdmission(ctx context.Context, run PluginRun, method string) (context.Context, error) {
	if err := a.requireDeferredExecution(ctx, &run, method); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, brushFlowAdmissionKey{}, brushFlowAdmission{run.ID, run.InstanceID, run.Version, run.Action, deferredInputSHA(run.Input), method, run.Generation, run.ConfigRevision}), nil
}

func (p *PluginStore) brushFlowRootRead(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, g int64) (brushFlowRoot, []byte, error) {
	var out brushFlowRoot
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_brush_flow_roots WHERE instance_id=? AND generation=?`, instance, g).Scan(&sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil, brushFlowEvidenceError()
		}
		return out, nil, err
	}
	raw, err := p.unseal("run_result", instance, brushFlowRootKey(g), sealed)
	if err != nil || cleanupJournalStrict(raw, &out) != nil || out.Version != 1 || out.Purpose != "brush-flow-owner-root/v1" || out.Instance != instance || out.Generation != g || out.Sequence < 0 || !pluginStorageDigest.MatchString(out.StateSHA) {
		return out, nil, brushFlowEvidenceError()
	}
	if out.Event != "install" && out.Event != "reset" && out.Event != "uninstall" && out.Event != "bulk_uninstall" {
		return out, nil, brushFlowEvidenceError()
	}
	if (g == 1 && (out.ParentGeneration != 0 || out.ParentSHA != "" || out.Event != "install")) || (g > 1 && (out.ParentGeneration != g-1 || !pluginStorageDigest.MatchString(out.ParentSHA))) {
		return out, nil, brushFlowEvidenceError()
	}
	return out, sealed, nil
}
func (p *PluginStore) brushFlowValidateRootChain(ctx context.Context, q pluginRemoveLinkJournalReader, s brushFlowState, raw json.RawMessage) error {
	instance, g := s.Instance, s.Generation
	expected := ""
	for g > 0 {
		root, _, err := p.brushFlowRootRead(ctx, q, instance, g)
		if err != nil {
			return err
		}
		if expected != "" && expected != tokenHash(string(mustCleanSeedJSON(root))) {
			return brushFlowEvidenceError()
		}
		stateRaw := raw
		if g != s.Generation {
			var sealed []byte
			if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_brush_flow_state WHERE instance_id=? AND generation=?`, instance, g).Scan(&sealed); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return brushFlowEvidenceError()
				}
				return err
			}
			stateRaw, err = p.unseal("run_result", instance, brushFlowKey(g), sealed)
			if err != nil {
				return brushFlowEvidenceError()
			}
		}
		if root.StateSHA != tokenHash(string(stateRaw)) {
			return brushFlowEvidenceError()
		}
		if g != s.Generation {
			var parent brushFlowState
			if cleanupJournalStrict(stateRaw, &parent) != nil || parent.Version != 1 || parent.Instance != instance || parent.Generation != g || parent.Runs == nil {
				return brushFlowEvidenceError()
			}
			if err = p.brushFlowValidateDeferred(ctx, q, parent); err != nil {
				return err
			}
		}

		expected, g = root.ParentSHA, root.ParentGeneration
	}
	return nil
}

// A started deferred operation may precede its first owner row update only in
// the current exact trusted Host call. Public reads and lifecycle methods never
// receive this admission capability. Prepared means no native effect started.
func (p *PluginStore) brushFlowValidateDeferred(ctx context.Context, q pluginRemoveLinkJournalReader, s brushFlowState) error {
	rows, err := q.QueryContext(ctx, `SELECT id FROM plugin_runs WHERE instance_id=? AND generation=? ORDER BY id`, s.Instance, s.Generation)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		run, err := p.scanPluginRun(q.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, id))
		if err != nil {
			return err
		}
		if brushFlowReadRun(run) {
			if _, exists := s.Runs[id]; exists {
				return brushFlowEvidenceError()
			}
			continue
		}
		d, _, err := p.readDeferred(ctx, q, run)
		if errors.Is(err, sql.ErrNoRows) {
			if _, exists := s.Runs[id]; exists || run.State == "succeeded" || (run.State == "reconcile_pending" && !brushFlowColdNoDeferred(ctx, p, q, run)) {
				return brushFlowEvidenceError()
			}
			continue // Pending or a rejected pre-Host admission has no native effect.
		}
		if err != nil {
			if brushFlowErrorCode(err) == "PLUGIN_DATA_CORRUPT" || brushFlowErrorCode(err) == "PLUGIN_DEFERRED_INVALID" {
				return brushFlowEvidenceError()
			}
			return err
		}
		method, payload, specErr := brushFlowDeferredSpec(run)
		if specErr != nil || d.Plugin != "BrushFlowLanduo" || d.Version != "5.2.15" || d.Version != run.Version || d.Generation != s.Generation || d.Revision != run.ConfigRevision || d.Action != run.Action || d.InputSHA != deferredInputSHA(run.Input) || d.Method != method || !deferredPayloadEqual(d.Raw, payload) {
			return brushFlowEvidenceError()
		}
		action, exists := s.Runs[id]
		if d.Phase == "prepared" && !exists {
			if run.State == "succeeded" {
				return brushFlowEvidenceError()
			}
			continue
		}
		if !exists {
			return brushFlowEvidenceError()
		}

		seen[id] = true
		if action.RunID != run.ID || action.Action != run.Action || action.Revision != run.ConfigRevision || action.InputSHA != d.InputSHA || action.Method != d.Method {
			return brushFlowEvidenceError()
		}
		switch d.Phase {
		case "started", "uncertain":
			if action.Phase != "started" && action.Phase != "confirmed" && action.Phase != "failed" {
				return brushFlowEvidenceError()
			}
		case "confirmed":
			if action.Phase != "confirmed" || action.ErrorCode != "" || !bytes.Equal(action.Result, d.Result) {
				return brushFlowEvidenceError()
			}
		case "terminal_failed":
			if action.Phase != "failed" || action.ErrorCode != d.ErrorCode || !bytes.Equal(action.Result, d.Result) {
				return brushFlowEvidenceError()
			}

		default:
			return brushFlowEvidenceError()
		}
	}
	for id := range s.Runs {
		if !seen[id] {
			return brushFlowEvidenceError()
		}
	}
	return nil
}

// Only the installation/lifecycle SQL transaction can create a generation.
// Missing pre-existing roots/journals never authorize a genesis or legacy lift.
func (p *PluginStore) brushFlowTransitionParentTx(ctx context.Context, tx *sql.Tx, instance string) (brushFlowParent, error) {
	var id, version string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&id, &version, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_brush_flow_roots WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_brush_flow_state WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?)`, instance, instance, instance).Scan(&count)
		if err != nil {
			return brushFlowParent{}, err
		}
		if count != 0 {
			return brushFlowParent{}, brushFlowEvidenceError()
		}
		return brushFlowParent{}, nil
	}
	if err != nil {
		return brushFlowParent{}, err
	}
	if id != "BrushFlowLanduo" || version != "5.2.15" {
		return brushFlowParent{}, brushFlowEvidenceError()
	}
	state, _, err := p.brushFlowRead(ctx, tx, instance, generation)
	if err != nil {
		return brushFlowParent{}, err
	}
	if err = brushFlowNoUnknown(state); err != nil {
		return brushFlowParent{}, err
	}
	root, _, err := p.brushFlowRootRead(ctx, tx, instance, generation)
	if err != nil {
		return brushFlowParent{}, err
	}
	return brushFlowParent{generation, tokenHash(string(mustCleanSeedJSON(root)))}, nil
}
func (p *PluginStore) brushFlowGenesisTx(ctx context.Context, tx *sql.Tx, instance, event string, parent brushFlowParent) error {
	var plugin, version string
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &version, &generation); err != nil {
		return err
	}
	if plugin != "BrushFlowLanduo" || version != "5.2.15" || generation != parent.Generation+1 {
		return brushFlowEvidenceError()
	}
	state := brushFlowState{Version: 1, Instance: instance, Generation: generation, Runs: map[string]brushFlowAction{}, Values: map[string]brushFlowValue{}, Sources: map[string]brushFlowScanSource{}}
	rows, err := tx.QueryContext(ctx, `SELECT item_key,revision,sealed FROM plugin_values WHERE instance_id=? AND kind='data'`, instance)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		var rev int64
		var cell []byte
		if err = rows.Scan(&key, &rev, &cell); err != nil {
			rows.Close()
			return err
		}
		value, e := p.unseal("data", instance, key, cell)
		if e != nil {
			rows.Close()
			return e
		}
		state.Values[key] = brushFlowValue{Revision: rev, SHA: tokenHash(string(value))}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	raw := mustCleanSeedJSON(state)
	sealed, err := p.seal("run_result", instance, brushFlowKey(generation), raw)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_brush_flow_state VALUES(?,?,?)`, instance, generation, sealed); err != nil {
		return err
	}
	root := brushFlowRoot{Version: 1, Purpose: "brush-flow-owner-root/v1", Instance: instance, Event: event, Generation: generation, ParentGeneration: parent.Generation, ParentSHA: parent.SHA, StateSHA: tokenHash(string(raw))}
	sealed, err = p.seal("run_result", instance, brushFlowRootKey(generation), mustCleanSeedJSON(root))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_brush_flow_roots VALUES(?,?,?)`, instance, generation, sealed)
	return err
}
func (p *PluginStore) brushFlowLifecycleParentTx(ctx context.Context, tx *sql.Tx, instance string) (brushFlowParent, bool, error) {
	var plugin string
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin); err != nil {
		return brushFlowParent{}, false, err
	}
	if plugin != "BrushFlowLanduo" {
		return brushFlowParent{}, false, nil
	}
	parent, err := p.brushFlowTransitionParentTx(ctx, tx, instance)
	return parent, true, err
}
