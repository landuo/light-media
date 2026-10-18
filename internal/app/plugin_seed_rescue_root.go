package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

const seedRescueRootSchema = `CREATE TABLE IF NOT EXISTS plugin_seed_rescue_roots(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));`

type seedRescueRoot struct {
	Version                                       int
	Purpose, Instance, Event, ParentSHA, StateSHA string
	Generation, ParentGeneration, Sequence        int64
}
type seedRescueParent struct {
	Generation int64
	SHA        string
}
type seedRescueAdmissionKey struct{}
type seedRescueAdmission struct {
	Run, Instance, Version, Action, InputSHA, Method string
	Generation, Revision                             int64
}

func seedRescueErrorCode(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func seedRescueRootKey(g int64) string { return "seed-rescue-root:v1:" + strconv.FormatInt(g, 10) }
func seedRescueEvidenceError() error {
	return bad("SEED_RESCUE_EVIDENCE_MISSING", "SeedRescue稀缺保种执行证据缺失或不一致，请恢复可信稀缺保种；未重新初始化或重复执行")
}
func (a *App) seedRescueAuthorizedAdmission(ctx context.Context, run PluginRun, method string) (context.Context, error) {
	if err := a.requireDeferredExecution(ctx, &run, method); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, seedRescueAdmissionKey{}, seedRescueAdmission{run.ID, run.InstanceID, run.Version, run.Action, deferredInputSHA(run.Input), method, run.Generation, run.ConfigRevision}), nil
}

func (p *PluginStore) seedRescueRootRead(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, g int64) (seedRescueRoot, []byte, error) {
	var out seedRescueRoot
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_seed_rescue_roots WHERE instance_id=? AND generation=?`, instance, g).Scan(&sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil, seedRescueEvidenceError()
		}
		return out, nil, err
	}
	raw, err := p.unseal("run_result", instance, seedRescueRootKey(g), sealed)
	if err != nil || cleanupJournalStrict(raw, &out) != nil || out.Version != 1 || out.Purpose != "seed-rescue-owner-root/v1" || out.Instance != instance || out.Generation != g || out.Sequence < 0 || !pluginStorageDigest.MatchString(out.StateSHA) {
		return out, nil, seedRescueEvidenceError()
	}
	if out.Event != "install" && out.Event != "reset" && out.Event != "uninstall" && out.Event != "bulk_uninstall" {
		return out, nil, seedRescueEvidenceError()
	}
	if (g == 1 && (out.ParentGeneration != 0 || out.ParentSHA != "" || out.Event != "install")) || (g > 1 && (out.ParentGeneration != g-1 || !pluginStorageDigest.MatchString(out.ParentSHA))) {
		return out, nil, seedRescueEvidenceError()
	}
	return out, sealed, nil
}
func (p *PluginStore) seedRescueValidateRootChain(ctx context.Context, q pluginRemoveLinkJournalReader, s seedRescueState, raw json.RawMessage) error {
	instance, g := s.Instance, s.Generation
	expected := ""
	for g > 0 {
		root, _, err := p.seedRescueRootRead(ctx, q, instance, g)
		if err != nil {
			return err
		}
		if expected != "" && expected != tokenHash(string(mustCleanSeedJSON(root))) {
			return seedRescueEvidenceError()
		}
		stateRaw := raw
		if g != s.Generation {
			var sealed []byte
			if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_seed_rescue_state WHERE instance_id=? AND generation=?`, instance, g).Scan(&sealed); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return seedRescueEvidenceError()
				}
				return err
			}
			stateRaw, err = p.unseal("run_result", instance, seedRescueKey(g), sealed)
			if err != nil {
				return seedRescueEvidenceError()
			}
		}
		if root.StateSHA != tokenHash(string(stateRaw)) {
			return seedRescueEvidenceError()
		}
		if g != s.Generation {
			var parent seedRescueState
			if cleanupJournalStrict(stateRaw, &parent) != nil || parent.Version != 1 || parent.Instance != instance || parent.Generation != g || parent.Runs == nil {
				return seedRescueEvidenceError()
			}
			if err = p.seedRescueValidateDeferred(ctx, q, parent); err != nil {
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
func (p *PluginStore) seedRescueValidateDeferred(ctx context.Context, q pluginRemoveLinkJournalReader, s seedRescueState) error {
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
		if seedRescueReadRun(run) {
			if _, exists := s.Runs[id]; exists {
				return seedRescueEvidenceError()
			}
			continue
		}
		d, _, err := p.readDeferred(ctx, q, run)
		if errors.Is(err, sql.ErrNoRows) {
			if _, exists := s.Runs[id]; exists || run.State == "succeeded" || run.State == "reconcile_pending" {
				return seedRescueEvidenceError()
			}
			continue // Pending or a rejected pre-Host admission has no native effect.
		}
		if err != nil {
			if seedRescueErrorCode(err) == "PLUGIN_DATA_CORRUPT" || seedRescueErrorCode(err) == "PLUGIN_DEFERRED_INVALID" {
				return seedRescueEvidenceError()
			}
			return err
		}
		method, payload, specErr := seedRescueDeferredSpec(run)
		if specErr != nil || d.Plugin != "SeedRescue" || d.Version != "1.6.3" || d.Version != run.Version || d.Generation != s.Generation || d.Revision != run.ConfigRevision || d.Action != run.Action || d.InputSHA != deferredInputSHA(run.Input) || d.Method != method || !deferredPayloadEqual(d.Raw, payload) {
			return seedRescueEvidenceError()
		}
		action, exists := s.Runs[id]
		if d.Phase == "prepared" && !exists {
			if run.State == "succeeded" {
				return seedRescueEvidenceError()
			}
			continue
		}
		if !exists {
			return seedRescueEvidenceError()
		}

		seen[id] = true
		if action.RunID != run.ID || action.Action != run.Action || action.Revision != run.ConfigRevision || action.InputSHA != d.InputSHA || action.Method != d.Method {
			return seedRescueEvidenceError()
		}
		switch d.Phase {
		case "started", "uncertain":
			if action.Phase != "started" && action.Phase != "confirmed" && action.Phase != "failed" {
				return seedRescueEvidenceError()
			}
		case "confirmed":
			if action.Phase != "confirmed" || action.ErrorCode != "" || !bytes.Equal(action.Result, d.Result) {
				return seedRescueEvidenceError()
			}
		case "terminal_failed":
			if action.Phase != "failed" || action.ErrorCode != d.ErrorCode || !bytes.Equal(action.Result, d.Result) {
				return seedRescueEvidenceError()
			}

		default:
			return seedRescueEvidenceError()
		}
	}
	for id := range s.Runs {
		if !seen[id] {
			return seedRescueEvidenceError()
		}
	}
	return nil
}

// Only the installation/lifecycle SQL transaction can create a generation.
// Missing pre-existing roots/journals never authorize a genesis or legacy lift.
func (p *PluginStore) seedRescueTransitionParentTx(ctx context.Context, tx *sql.Tx, instance string) (seedRescueParent, error) {
	var id, version string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&id, &version, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_seed_rescue_roots WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_seed_rescue_state WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?)`, instance, instance, instance).Scan(&count)
		if err != nil {
			return seedRescueParent{}, err
		}
		if count != 0 {
			return seedRescueParent{}, seedRescueEvidenceError()
		}
		return seedRescueParent{}, nil
	}
	if err != nil {
		return seedRescueParent{}, err
	}
	if id != "SeedRescue" || version != "1.6.3" {
		return seedRescueParent{}, seedRescueEvidenceError()
	}
	state, _, err := p.seedRescueRead(ctx, tx, instance, generation)
	if err != nil {
		return seedRescueParent{}, err
	}
	if err = seedRescueNoUnknown(state); err != nil {
		return seedRescueParent{}, err
	}
	root, _, err := p.seedRescueRootRead(ctx, tx, instance, generation)
	if err != nil {
		return seedRescueParent{}, err
	}
	return seedRescueParent{generation, tokenHash(string(mustCleanSeedJSON(root)))}, nil
}
func (p *PluginStore) seedRescueGenesisTx(ctx context.Context, tx *sql.Tx, instance, event string, parent seedRescueParent) error {
	var plugin, version string
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &version, &generation); err != nil {
		return err
	}
	if plugin != "SeedRescue" || version != "1.6.3" || generation != parent.Generation+1 {
		return seedRescueEvidenceError()
	}
	state := seedRescueState{Version: 1, Instance: instance, Generation: generation, Runs: map[string]seedRescueAction{}, Values: map[string]seedRescueValue{}, Sources: map[string]seedRescueScanSource{}}
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
		state.Values[key] = seedRescueValue{Revision: rev, SHA: tokenHash(string(value))}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	raw := mustCleanSeedJSON(state)
	sealed, err := p.seal("run_result", instance, seedRescueKey(generation), raw)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_seed_rescue_state VALUES(?,?,?)`, instance, generation, sealed); err != nil {
		return err
	}
	root := seedRescueRoot{Version: 1, Purpose: "seed-rescue-owner-root/v1", Instance: instance, Event: event, Generation: generation, ParentGeneration: parent.Generation, ParentSHA: parent.SHA, StateSHA: tokenHash(string(raw))}
	sealed, err = p.seal("run_result", instance, seedRescueRootKey(generation), mustCleanSeedJSON(root))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_seed_rescue_roots VALUES(?,?,?)`, instance, generation, sealed)
	return err
}
func (p *PluginStore) seedRescueLifecycleParentTx(ctx context.Context, tx *sql.Tx, instance string) (seedRescueParent, bool, error) {
	var plugin string
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin); err != nil {
		return seedRescueParent{}, false, err
	}
	if plugin != "SeedRescue" {
		return seedRescueParent{}, false, nil
	}
	parent, err := p.seedRescueTransitionParentTx(ctx, tx, instance)
	return parent, true, err
}
