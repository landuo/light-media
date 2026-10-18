package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

const autoBackupRootSchema = `CREATE TABLE IF NOT EXISTS plugin_auto_backup_roots(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));`

type autoBackupRoot struct {
	Version                                       int
	Purpose, Instance, Event, ParentSHA, StateSHA string
	Generation, ParentGeneration, Sequence        int64
}
type autoBackupParent struct {
	Generation int64
	SHA        string
}
type autoBackupAdmissionKey struct{}
type autoBackupAdmission struct {
	Run, Instance, Version, Action, InputSHA, Method string
	Generation, Revision                             int64
}

func autoBackupErrorCode(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func autoBackupRootKey(g int64) string { return "auto-backup-root:v1:" + strconv.FormatInt(g, 10) }
func autoBackupEvidenceError() error {
	return bad("AUTO_BACKUP_EVIDENCE_MISSING", "自动备份执行证据缺失或不一致，请恢复可信备份；未重新初始化或重复执行")
}
func (a *App) autoBackupAuthorizedAdmission(ctx context.Context, run PluginRun, method string) (context.Context, error) {
	if err := a.requireDeferredExecution(ctx, &run, method); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, autoBackupAdmissionKey{}, autoBackupAdmission{run.ID, run.InstanceID, run.Version, run.Action, deferredInputSHA(run.Input), method, run.Generation, run.ConfigRevision}), nil
}

func (p *PluginStore) autoBackupRootRead(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, g int64) (autoBackupRoot, []byte, error) {
	var out autoBackupRoot
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_auto_backup_roots WHERE instance_id=? AND generation=?`, instance, g).Scan(&sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil, autoBackupEvidenceError()
		}
		return out, nil, err
	}
	raw, err := p.unseal("run_result", instance, autoBackupRootKey(g), sealed)
	if err != nil || cleanupJournalStrict(raw, &out) != nil || out.Version != 1 || out.Purpose != "auto-backup-owner-root/v1" || out.Instance != instance || out.Generation != g || out.Sequence < 0 || !pluginStorageDigest.MatchString(out.StateSHA) {
		return out, nil, autoBackupEvidenceError()
	}
	if out.Event != "install" && out.Event != "reset" && out.Event != "uninstall" && out.Event != "bulk_uninstall" {
		return out, nil, autoBackupEvidenceError()
	}
	if (g == 1 && (out.ParentGeneration != 0 || out.ParentSHA != "" || out.Event != "install")) || (g > 1 && (out.ParentGeneration != g-1 || !pluginStorageDigest.MatchString(out.ParentSHA))) {
		return out, nil, autoBackupEvidenceError()
	}
	return out, sealed, nil
}
func (p *PluginStore) autoBackupValidateRootChain(ctx context.Context, q pluginRemoveLinkJournalReader, s autoBackupState, raw json.RawMessage) error {
	instance, g := s.Instance, s.Generation
	expected := ""
	for g > 0 {
		root, _, err := p.autoBackupRootRead(ctx, q, instance, g)
		if err != nil {
			return err
		}
		if expected != "" && expected != tokenHash(string(mustCleanSeedJSON(root))) {
			return autoBackupEvidenceError()
		}
		stateRaw := raw
		if g != s.Generation {
			var sealed []byte
			if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_auto_backup_state WHERE instance_id=? AND generation=?`, instance, g).Scan(&sealed); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return autoBackupEvidenceError()
				}
				return err
			}
			stateRaw, err = p.unseal("run_result", instance, autoBackupKey(g), sealed)
			if err != nil {
				return autoBackupEvidenceError()
			}
		}
		if root.StateSHA != tokenHash(string(stateRaw)) {
			return autoBackupEvidenceError()
		}
		if g != s.Generation {
			var parent autoBackupState
			if cleanupJournalStrict(stateRaw, &parent) != nil || parent.Version != 1 || parent.Instance != instance || parent.Generation != g || parent.Runs == nil {
				return autoBackupEvidenceError()
			}
			if err = p.autoBackupValidateDeferred(ctx, q, parent); err != nil {
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
func (p *PluginStore) autoBackupValidateDeferred(ctx context.Context, q pluginRemoveLinkJournalReader, s autoBackupState) error {
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
		d, _, err := p.readDeferred(ctx, q, run)
		if errors.Is(err, sql.ErrNoRows) {
			if _, exists := s.Runs[id]; exists || run.State == "succeeded" || run.State == "reconcile_pending" {
				return autoBackupEvidenceError()
			}
			continue // Pending or a rejected pre-Host admission has no native effect.
		}
		if err != nil {
			if autoBackupErrorCode(err) == "PLUGIN_DATA_CORRUPT" || autoBackupErrorCode(err) == "PLUGIN_DEFERRED_INVALID" {
				return autoBackupEvidenceError()
			}
			return err
		}
		method, payload, specErr := autoBackupDeferredSpec(run)
		if specErr != nil || d.Plugin != "AutoBackup" || d.Version != "2.1.4" || d.Version != run.Version || d.Generation != s.Generation || d.Revision != run.ConfigRevision || d.Action != run.Action || d.InputSHA != deferredInputSHA(run.Input) || d.Method != method || !deferredPayloadEqual(d.Raw, payload) {
			return autoBackupEvidenceError()
		}
		action, exists := s.Runs[id]
		if d.Phase == "prepared" && !exists {
			if run.State == "succeeded" {
				return autoBackupEvidenceError()
			}
			continue
		}
		if !exists {
			allowed, ok := ctx.Value(autoBackupAdmissionKey{}).(autoBackupAdmission)
			if ok && d.Phase == "started" && run.State == "running" && allowed.Run == id && allowed.Instance == s.Instance && allowed.Version == run.Version && allowed.Action == run.Action && allowed.InputSHA == d.InputSHA && allowed.Method == d.Method && allowed.Generation == s.Generation && allowed.Revision == run.ConfigRevision {
				continue
			}
			return autoBackupEvidenceError()
		}
		seen[id] = true
		if action.RunID != run.ID || action.Action != run.Action || action.Revision != run.ConfigRevision || action.InputSHA != d.InputSHA || action.Method != d.Method {
			return autoBackupEvidenceError()
		}
		switch d.Phase {
		case "started", "uncertain":
			if action.Phase != "started" && action.Phase != "confirmed" && action.Phase != "failed" {
				return autoBackupEvidenceError()
			}
		case "confirmed":
			if action.Phase != "confirmed" || action.ErrorCode != "" || !bytes.Equal(action.Result, d.Result) {
				return autoBackupEvidenceError()
			}
		case "terminal_failed":
			if action.Phase != "failed" || action.ErrorCode != d.ErrorCode || !bytes.Equal(action.Result, d.Result) {
				return autoBackupEvidenceError()
			}
		case "snapshot_cut":
			if action.Phase != "snapshot_cut" || !validAutoBackupCut(action, s.Instance, s.Generation) || !validAutoBackupDeferredCut(d) || !bytes.Equal(action.Result, d.Result) {
				return autoBackupEvidenceError()
			}
		case "abandoned_unknown":
			if action.Phase != "abandoned_unknown" || !validAutoBackupResolution(action, s.Instance, s.Generation) || !validAutoBackupDeferredAbandon(d) || !bytes.Equal(mustCleanSeedJSON(action.Resolution), d.Result) {
				return autoBackupEvidenceError()
			}
		default:
			return autoBackupEvidenceError()
		}
	}
	for id := range s.Runs {
		if !seen[id] {
			return autoBackupEvidenceError()
		}
	}
	return nil
}

// Only the installation/lifecycle SQL transaction can create a generation.
// Missing pre-existing roots/journals never authorize a genesis or legacy lift.
func (p *PluginStore) autoBackupTransitionParentTx(ctx context.Context, tx *sql.Tx, instance string) (autoBackupParent, error) {
	var id, version string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&id, &version, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_auto_backup_roots WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_auto_backup_state WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?)`, instance, instance, instance).Scan(&count)
		if err != nil {
			return autoBackupParent{}, err
		}
		if count != 0 {
			return autoBackupParent{}, autoBackupEvidenceError()
		}
		return autoBackupParent{}, nil
	}
	if err != nil {
		return autoBackupParent{}, err
	}
	if id != "AutoBackup" || version != "2.1.4" {
		return autoBackupParent{}, autoBackupEvidenceError()
	}
	state, _, err := p.autoBackupRead(ctx, tx, instance, generation)
	if err != nil {
		return autoBackupParent{}, err
	}
	if err = autoBackupNoUnknown(state); err != nil {
		return autoBackupParent{}, err
	}
	root, _, err := p.autoBackupRootRead(ctx, tx, instance, generation)
	if err != nil {
		return autoBackupParent{}, err
	}
	return autoBackupParent{generation, tokenHash(string(mustCleanSeedJSON(root)))}, nil
}
func (p *PluginStore) autoBackupGenesisTx(ctx context.Context, tx *sql.Tx, instance, event string, parent autoBackupParent) error {
	var plugin, version string
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &version, &generation); err != nil {
		return err
	}
	if plugin != "AutoBackup" || version != "2.1.4" || generation != parent.Generation+1 {
		return autoBackupEvidenceError()
	}
	state := autoBackupState{Version: 1, Instance: instance, Generation: generation, Runs: map[string]autoBackupAction{}}
	raw := mustCleanSeedJSON(state)
	sealed, err := p.seal("run_result", instance, autoBackupKey(generation), raw)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_auto_backup_state VALUES(?,?,?)`, instance, generation, sealed); err != nil {
		return err
	}
	root := autoBackupRoot{Version: 1, Purpose: "auto-backup-owner-root/v1", Instance: instance, Event: event, Generation: generation, ParentGeneration: parent.Generation, ParentSHA: parent.SHA, StateSHA: tokenHash(string(raw))}
	sealed, err = p.seal("run_result", instance, autoBackupRootKey(generation), mustCleanSeedJSON(root))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_auto_backup_roots VALUES(?,?,?)`, instance, generation, sealed)
	return err
}
func (p *PluginStore) autoBackupLifecycleParentTx(ctx context.Context, tx *sql.Tx, instance string) (autoBackupParent, bool, error) {
	var plugin string
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin); err != nil {
		return autoBackupParent{}, false, err
	}
	if plugin != "AutoBackup" {
		return autoBackupParent{}, false, nil
	}
	parent, err := p.autoBackupTransitionParentTx(ctx, tx, instance)
	return parent, true, err
}
