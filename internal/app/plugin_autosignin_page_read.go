package app

import (
	"context"
	"database/sql"
	"light-media/internal/plugins/autosignin/durable"
	"strconv"
)

// autoSignInPageStateTx authenticates historical data without granting execution.
// Unlike scheduler admission, a current paused configuration may differ from the
// last committed source snapshot; its identity/config are independently anchored.
func (p *PluginStore) autoSignInPageStateTx(ctx context.Context, tx *sql.Tx, anchor pluginFormAnchor) (durable.State, error) {
	var empty durable.State
	i := anchor.installation
	var sealed []byte
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT generation,sealed FROM plugin_signin_owners WHERE instance_id=?`, i.InstanceID).Scan(&generation, &sealed); err != nil {
		return empty, err
	}
	raw, err := p.unseal("signin_owner", i.InstanceID, "current", sealed)
	if err != nil {
		return empty, err
	}
	var binding durable.Binding
	if cleanupJournalStrict(raw, &binding) != nil || binding.Instance != i.InstanceID || binding.Generation != uint64(i.Generation) || generation != i.Generation || binding.Implementation != autoSignInImplementation || binding.Epoch == "" {
		return empty, durable.ErrStale
	}
	reader := &pluginAutoSignInOwner{a: &App{Store: p.store, pluginStore: p}, binding: binding}
	loaded, err := reader.readTx(ctx, tx)
	if err != nil {
		return empty, err
	}
	state := loaded.State
	if state.Scope != binding.Scope || state.Epoch != binding.Epoch || state.RuntimeEpoch == "" || len(state.Runtime) == 0 {
		return empty, durable.ErrStale
	}
	// Derive the historical tail from decrypted authenticated receipts, never an
	// untrusted SQL MAX. The existing verifier below checks every receipt's source
	// run/lane/digest and the exact final source config as well.
	rows, err := tx.QueryContext(ctx, `SELECT to_revision,sealed FROM plugin_signin_config_receipts WHERE instance_id=? AND generation=? ORDER BY to_revision`, i.InstanceID, i.Generation)
	if err != nil {
		return empty, err
	}
	last := binding.Revision
	count := 0
	for rows.Next() {
		var to uint64
		var cell []byte
		if err = rows.Scan(&to, &cell); err != nil {
			break
		}
		count++
		if count > pluginAutoSignInRowLimit {
			err = durable.ErrCapacity
			break
		}
		var value []byte
		value, err = p.unseal("signin_config", i.InstanceID, reader.aad("config", strconv.FormatUint(to, 10), 0), cell)
		if err != nil {
			break
		}
		var receipt pluginAutoSignInConfigReceipt
		if cleanupJournalStrict(value, &receipt) != nil || receipt.Scope != binding.Scope || receipt.From != last || receipt.To != to || to != last+1 {
			err = durable.ErrStale
			break
		}
		last = to
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return empty, err
	}
	if anchor.durable.Revision < 1 || last > uint64(anchor.durable.Revision) {
		return empty, durable.ErrStale
	}
	if _, err = reader.configChainTx(ctx, tx, last, state.Config, state); err != nil {
		return empty, err
	}
	return state, nil
}

// A trusted Cold marker is necessary but not sufficient to synthesize the
// original empty page: no current-generation authenticated business data may
// be hidden. No schema creation, owner claim or source Init is performed.
func (p *PluginStore) autoSignInColdPageEmptyTx(ctx context.Context, tx *sql.Tx, i PluginInstallation) error {
	for _, table := range []string{"plugin_signin_owners", "plugin_signin_meta", "plugin_signin_rows", "plugin_signin_config_receipts", "plugin_signin_source_acceptances", "plugin_signin_commands", "plugin_signin_archive_meta", "plugin_signin_archive_rows", "plugin_signin_archive_config"} {
		var exists, count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			continue
		}
		// table is from this fixed internal list, never worker or user input.
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE instance_id=? AND generation=?`, i.InstanceID, i.Generation).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return durable.ErrStale
		}
	}
	var data int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind<>'config'`, i.InstanceID).Scan(&data); err != nil {
		return err
	}
	if data != 0 {
		return durable.ErrStale
	}
	return nil
}
