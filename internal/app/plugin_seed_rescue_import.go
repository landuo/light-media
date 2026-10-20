package app

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Import history and its owner proof in the same transaction as the receipt.
// Reading the empty installed owner first also prevents a missing/corrupt root
// from being silently replaced. Import does not apply configuration or run jobs.
func (p *PluginStore) importSeedRescueDataTx(ctx context.Context, tx *sql.Tx, instance string, data map[string]json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&generation); err != nil {
		return err
	}
	state, previous, err := p.seedRescueRead(ctx, tx, instance, generation)
	if err != nil {
		return err
	}
	for key, raw := range data {
		value, err := p.saveValueTx(ctx, tx, instance, "data", key, 0, raw)
		if err != nil {
			return err
		}
		state.Values[key] = seedRescueValue{Revision: value.Revision, SHA: tokenHash(string(value.Value))}
	}
	return p.seedRescueSave(ctx, tx, state, previous)
}
