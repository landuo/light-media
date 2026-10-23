package app

import (
	"context"
	"database/sql"
	"errors"

	"light-media/internal/plugins/uninstall"
)

// This proves historical identity continuity only. It neither checks a live
// installation nor permits executing an old operation. Terminal results remain
// recoverable after reset/uninstall; prepared and uncertain phase policy stays
// in recoverDeferredRuns and must not be relaxed by this proof.
func (p *PluginStore) validateDeferredHistory(ctx context.Context, receipt pluginDeferredRecord, run PluginRun) error {
	if receipt.Schema != 1 || receipt.Run != run.ID || receipt.Instance != run.InstanceID || receipt.Version != run.Version || receipt.Generation != run.Generation || receipt.Action != run.Action {
		return bad("PLUGIN_DEFERRED_INVALID", "插件操作恢复身份不匹配")
	}
	inputMatches := receipt.InputSHA == deferredInputSHA(run.Input)
	if receipt.ConfigSHA == "" {
		// Unanchored historical receipts retain the pre-existing same-input result
		// repair rule. They never acquire active revision continuation authority.
		if inputMatches {
			return nil
		}
		return bad("PLUGIN_DEFERRED_INVALID", "插件旧操作恢复输入不匹配")
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var successors int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=? AND from_revision>=?`, run.ID, run.ConfigRevision).Scan(&successors); err != nil {
		return err
	}
	if successors != 0 {
		return bad("PLUGIN_RUN_PROOF_INVALID", "插件历史执行修订发生回退")
	}
	_, chainErr := p.verifyRunConfigChainTx(ctx, tx, receipt.Plugin, run, receipt.Revision, receipt.InputSHA, receipt.ConfigSHA)
	if chainErr == nil {
		return nil
	}
	if pluginErrorCodeInternal(chainErr) != "PLUGIN_RUN_PROOF_INVALID" {
		return chainErr
	}
	// Self-uninstall has its own already-sealed Initial/FinalRevision journal.
	// It predates the generic chain and must not be forged into generic edges.
	if receipt.Plugin == "PluginUnInstall" && receipt.Version == "2.2" && receipt.Method == uninstall.ApplyCapability && inputMatches && run.ConfigRevision > receipt.Revision {
		var genericEdges int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=? AND from_revision>=? AND from_revision<?`, run.ID, receipt.Revision, run.ConfigRevision).Scan(&genericEdges); err != nil {
			return err
		}
		if genericEdges != 0 {
			return chainErr
		}
		source, _, sourceErr := p.readUninstallSource(ctx, tx, run)
		if sourceErr != nil && !errors.Is(sourceErr, sql.ErrNoRows) && pluginErrorCodeInternal(sourceErr) != "PLUGIN_UNINSTALL_EVIDENCE_INVALID" {
			return sourceErr
		}
		if sourceErr == nil && source.InitialRevision == receipt.Revision && source.FinalRevision == run.ConfigRevision {
			return nil
		}
		return bad("PLUGIN_UNINSTALL_EVIDENCE_INVALID", "卸载历史修订证明缺失或不匹配")
	}
	return chainErr
}
