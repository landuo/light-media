package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

const cloudflareRootSchema = `CREATE TABLE IF NOT EXISTS plugin_cloudflare_roots(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));`

type cloudflareRoot struct {
	Version                                       int
	Purpose, Instance, Event, ParentSHA, StateSHA string
	Generation, ParentGeneration, Sequence        int64
}
type cloudflareParent struct {
	Generation int64
	SHA        string
}
type cloudflareAdmissionKey struct{}
type cloudflareAdmission struct {
	Run, Instance, Version, Action, InputSHA, Method string
	Generation, Revision                             int64
}

func cloudflareErrorCode(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func cloudflareRootKey(g int64) string { return "cloudflare-root:v1:" + strconv.FormatInt(g, 10) }
func cloudflareEvidenceError() error {
	return bad("CLOUDFLARE_EVIDENCE_MISSING", "Cloudflare优选执行证据缺失或不一致，请恢复可信优选；未重新初始化或重复执行")
}
func (a *App) cloudflareAuthorizedAdmission(ctx context.Context, run PluginRun, method string) (context.Context, error) {
	if err := a.requireDeferredExecution(ctx, &run, method); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, cloudflareAdmissionKey{}, cloudflareAdmission{run.ID, run.InstanceID, run.Version, run.Action, deferredInputSHA(run.Input), method, run.Generation, run.ConfigRevision}), nil
}

func (p *PluginStore) cloudflareRootRead(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, g int64) (cloudflareRoot, []byte, error) {
	var out cloudflareRoot
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_cloudflare_roots WHERE instance_id=? AND generation=?`, instance, g).Scan(&sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil, cloudflareEvidenceError()
		}
		return out, nil, err
	}
	raw, err := p.unseal("run_result", instance, cloudflareRootKey(g), sealed)
	if err != nil || cleanupJournalStrict(raw, &out) != nil || out.Version != 1 || out.Purpose != "cloudflare-owner-root/v1" || out.Instance != instance || out.Generation != g || out.Sequence < 0 || !pluginStorageDigest.MatchString(out.StateSHA) {
		return out, nil, cloudflareEvidenceError()
	}
	if out.Event != "install" && out.Event != "reset" && out.Event != "uninstall" && out.Event != "bulk_uninstall" {
		return out, nil, cloudflareEvidenceError()
	}
	if (g == 1 && (out.ParentGeneration != 0 || out.ParentSHA != "" || out.Event != "install")) || (g > 1 && (out.ParentGeneration != g-1 || !pluginStorageDigest.MatchString(out.ParentSHA))) {
		return out, nil, cloudflareEvidenceError()
	}
	return out, sealed, nil
}
func (p *PluginStore) cloudflareValidateRootChain(ctx context.Context, q pluginRemoveLinkJournalReader, s cloudflareState, raw json.RawMessage) error {
	instance, g := s.Instance, s.Generation
	expected := ""
	for g > 0 {
		root, _, err := p.cloudflareRootRead(ctx, q, instance, g)
		if err != nil {
			return err
		}
		if expected != "" && expected != tokenHash(string(mustCleanSeedJSON(root))) {
			return cloudflareEvidenceError()
		}
		stateRaw := raw
		if g != s.Generation {
			var sealed []byte
			if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_cloudflare_state WHERE instance_id=? AND generation=?`, instance, g).Scan(&sealed); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return cloudflareEvidenceError()
				}
				return err
			}
			stateRaw, err = p.unseal("run_result", instance, cloudflareKey(g), sealed)
			if err != nil {
				return cloudflareEvidenceError()
			}
		}
		if root.StateSHA != tokenHash(string(stateRaw)) {
			return cloudflareEvidenceError()
		}
		if g != s.Generation {
			var parent cloudflareState
			if cleanupJournalStrict(stateRaw, &parent) != nil || parent.Version != 1 || parent.Instance != instance || parent.Generation != g || parent.Runs == nil {
				return cloudflareEvidenceError()
			}
			if err = p.cloudflareValidateDeferred(ctx, q, parent); err != nil {
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
func (p *PluginStore) cloudflareValidateDeferred(ctx context.Context, q pluginRemoveLinkJournalReader, s cloudflareState) error {
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
				return cloudflareEvidenceError()
			}
			continue // Pending or a rejected pre-Host admission has no native effect.
		}
		if err != nil {
			if cloudflareErrorCode(err) == "PLUGIN_DATA_CORRUPT" || cloudflareErrorCode(err) == "PLUGIN_DEFERRED_INVALID" {
				return cloudflareEvidenceError()
			}
			return err
		}
		method, payload, specErr := cloudflareDeferredSpec(run)
		if specErr != nil || d.Plugin != "CloudflareSpeedTest" || d.Version != "1.5.1" || d.Version != run.Version || d.Generation != s.Generation || d.Revision != run.ConfigRevision || d.Action != run.Action || d.InputSHA != deferredInputSHA(run.Input) || d.Method != method || !deferredPayloadEqual(d.Raw, payload) {
			return cloudflareEvidenceError()
		}
		action, exists := s.Runs[id]
		if d.Phase == "prepared" && !exists {
			if run.State == "succeeded" {
				return cloudflareEvidenceError()
			}
			continue
		}
		if !exists {
			allowed, ok := ctx.Value(cloudflareAdmissionKey{}).(cloudflareAdmission)
			if ok && d.Phase == "started" && run.State == "running" && allowed.Run == id && allowed.Instance == s.Instance && allowed.Version == run.Version && allowed.Action == run.Action && allowed.InputSHA == d.InputSHA && allowed.Method == d.Method && allowed.Generation == s.Generation && allowed.Revision == run.ConfigRevision {
				continue
			}
			return cloudflareEvidenceError()
		}
		seen[id] = true
		if action.RunID != run.ID || action.Action != run.Action || action.Revision != run.ConfigRevision || action.InputSHA != d.InputSHA || action.Method != d.Method {
			return cloudflareEvidenceError()
		}
		switch d.Phase {
		case "started", "uncertain":
			if action.Phase != "started" && action.Phase != "confirmed" && action.Phase != "failed" {
				return cloudflareEvidenceError()
			}
		case "confirmed":
			if action.Phase != "confirmed" || action.ErrorCode != "" || !bytes.Equal(action.Result, d.Result) {
				return cloudflareEvidenceError()
			}
		case "terminal_failed":
			if action.Phase != "failed" || action.ErrorCode != d.ErrorCode || !bytes.Equal(action.Result, d.Result) {
				return cloudflareEvidenceError()
			}
		case "abandoned_unknown":
			if action.Phase != "abandoned_unknown" || !validCloudflareResolution(action, s.Instance, s.Generation) || !validCloudflareDeferredAbandon(d) || !bytes.Equal(mustCleanSeedJSON(action.Resolution), d.Result) {
				return cloudflareEvidenceError()
			}
		default:
			return cloudflareEvidenceError()
		}
	}
	for id := range s.Runs {
		if !seen[id] {
			return cloudflareEvidenceError()
		}
	}
	return nil
}

// Only the installation/lifecycle SQL transaction can create a generation.
// Missing pre-existing roots/journals never authorize a genesis or legacy lift.
func (p *PluginStore) cloudflareTransitionParentTx(ctx context.Context, tx *sql.Tx, instance string) (cloudflareParent, error) {
	var id, version string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&id, &version, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_cloudflare_roots WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_cloudflare_state WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?)`, instance, instance, instance).Scan(&count)
		if err != nil {
			return cloudflareParent{}, err
		}
		if count != 0 {
			return cloudflareParent{}, cloudflareEvidenceError()
		}
		return cloudflareParent{}, nil
	}
	if err != nil {
		return cloudflareParent{}, err
	}
	if id != "CloudflareSpeedTest" || version != "1.5.1" {
		return cloudflareParent{}, cloudflareEvidenceError()
	}
	state, _, err := p.cloudflareRead(ctx, tx, instance, generation)
	if err != nil {
		return cloudflareParent{}, err
	}
	if err = cloudflareNoUnknown(state); err != nil {
		return cloudflareParent{}, err
	}
	root, _, err := p.cloudflareRootRead(ctx, tx, instance, generation)
	if err != nil {
		return cloudflareParent{}, err
	}
	return cloudflareParent{generation, tokenHash(string(mustCleanSeedJSON(root)))}, nil
}
func (p *PluginStore) cloudflareGenesisTx(ctx context.Context, tx *sql.Tx, instance, event string, parent cloudflareParent) error {
	var plugin, version string
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &version, &generation); err != nil {
		return err
	}
	if plugin != "CloudflareSpeedTest" || version != "1.5.1" || generation != parent.Generation+1 {
		return cloudflareEvidenceError()
	}
	state := cloudflareState{Version: 1, Instance: instance, Generation: generation, Runs: map[string]cloudflareAction{}}
	raw := mustCleanSeedJSON(state)
	sealed, err := p.seal("run_result", instance, cloudflareKey(generation), raw)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_cloudflare_state VALUES(?,?,?)`, instance, generation, sealed); err != nil {
		return err
	}
	root := cloudflareRoot{Version: 1, Purpose: "cloudflare-owner-root/v1", Instance: instance, Event: event, Generation: generation, ParentGeneration: parent.Generation, ParentSHA: parent.SHA, StateSHA: tokenHash(string(raw))}
	sealed, err = p.seal("run_result", instance, cloudflareRootKey(generation), mustCleanSeedJSON(root))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_cloudflare_roots VALUES(?,?,?)`, instance, generation, sealed)
	return err
}
func (p *PluginStore) cloudflareLifecycleParentTx(ctx context.Context, tx *sql.Tx, instance string) (cloudflareParent, bool, error) {
	var plugin string
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin); err != nil {
		return cloudflareParent{}, false, err
	}
	if plugin != "CloudflareSpeedTest" {
		return cloudflareParent{}, false, nil
	}
	parent, err := p.cloudflareTransitionParentTx(ctx, tx, instance)
	return parent, true, err
}
