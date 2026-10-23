package app

import (
	"context"
	"encoding/json"
	"errors"
)

// Result storage is independent of ordinary configuration and invocation
// limits. A worker can return one complete encoded value up to the KV limit.
func pluginResultJSON(value json.RawMessage) (json.RawMessage, error) {
	if len(value) > pluginDataValueLimit {
		return nil, bad("PLUGIN_RESULT_TOO_LARGE", "插件执行结果超过允许大小，未截断保存")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, bad("INVALID_PLUGIN_RESULT", "插件执行结果不是有效JSON")
	}
	if len(raw) > pluginDataValueLimit {
		return nil, bad("PLUGIN_RESULT_TOO_LARGE", "插件执行结果超过允许大小，未截断保存")
	}
	return raw, nil
}

// A returned worker result must not leave a claim running just because its
// result column could not be saved. This fallback records authenticated null, never
// replaces another terminal decision, and cannot replay the completed worker.
func (p *PluginStore) failPluginResult(ctx context.Context, runID, code string) error {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	if code != "PLUGIN_RESULT_TOO_LARGE" && code != "INVALID_PLUGIN_RESULT" {
		code = "PLUGIN_RESULT_SAVE_FAILED"
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if completed, err := p.completedSupervisedRunTx(ctx, tx, runID); err != nil {
		return err
	} else if completed {
		return nil
	}
	lease, err := p.requireSupervisedFinishTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	var instance string
	if err = tx.QueryRowContext(ctx, `SELECT instance_id FROM plugin_runs WHERE id=?`, runID).Scan(&instance); err != nil {
		return err
	}
	sealed, err := p.seal("run_result", instance, runID, []byte(`null`))
	if err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE plugin_runs SET state='failed',result=?,error_code=?,updated_at=? WHERE id=? AND state='running'`, sealed, code, now(), runID)
	if err != nil {
		return err
	}
	if n, _ := changed.RowsAffected(); n == 0 {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, runID).Scan(&state); err != nil {
			return err
		}
		// Cancellation, reset or a concurrent final commit already won. Do not
		// overwrite that state, including when the result itself is unreadable.
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state='failed' WHERE run_id=? AND state='running'`, runID); err != nil {
		return err
	}
	if err = p.recordSupervisedCompletionTx(ctx, tx, lease, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func pluginResultFailureCode(err error) string {
	var api *APIError
	if errors.As(err, &api) && (api.Code == "PLUGIN_RESULT_TOO_LARGE" || api.Code == "INVALID_PLUGIN_RESULT") {
		return api.Code
	}
	return "PLUGIN_RESULT_SAVE_FAILED"
}
