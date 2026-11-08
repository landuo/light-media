package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const originalInstalledOrderKind = "plugin-original-installed-order-v1"

type originalInstalledOrderReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// The original /installed endpoint reads UserInstalledPlugins, not the loaded
// class registry or get_state. Failed, stopped and disabled installed objects
// remain visible, and independent clones retain their own identifiers.
func (a *App) originalInstalledPluginIDs(ctx context.Context) ([]string, error) {
	return a.pluginStore.originalInstalledPluginIDs(ctx, a.Store.DB)
}

func (p *PluginStore) originalInstalledPluginIDs(ctx context.Context, db originalInstalledOrderReader) ([]string, error) {
	state := &p.originalInstalledCache
	state.mu.Lock()
	epoch := state.epoch
	state.mu.Unlock()
	// Never hold the cache mutex while waiting for Store's single connection.
	// An existing native transaction reads this same setting before committing.
	ids, observed, err := p.readOriginalInstalledObservation(ctx, db)
	if err != nil {
		return nil, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.valid && state.observed == observed {
		return append([]string{}, state.ids...), nil
	}
	// A snapshot begun before a new source set cannot invalidate its newer cache.
	if state.epoch == epoch {
		state.valid = false
	}
	return ids, nil
}

// This is the persistent UserInstalledPlugins list, separate from the loaded
// registry and UI order. Updating an installed plugin retains its position;
// a new installation or a reinstall appends after all surviving instances.
func (p *PluginStore) updateOriginalInstalledOrderTx(ctx context.Context, tx *sql.Tx, instance string, installed, previouslyInstalled bool) error {
	ids, err := p.originalInstalledPluginIDs(ctx, tx)
	if err != nil {
		return err
	}
	updated := make([]string, 0, len(ids)+1)
	retained := false
	for _, id := range ids {
		if id != instance {
			updated = append(updated, id)
		} else if installed && previouslyInstalled && !retained {
			updated = append(updated, id)
			retained = true
		}
	}
	if installed && !retained {
		updated = append(updated, instance)
	}
	raw, err := json.Marshal(updated)
	if err != nil {
		return err
	}
	sealed, err := p.seal(originalInstalledOrderKind, "admin", "main", raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, originalInstalledOrderKind, sealed, now())
	return err
}

func (a *App) setOriginalPluginInstalled(ctx context.Context, instance string, installed bool) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous bool
	err = tx.QueryRowContext(ctx, `SELECT installed FROM plugin_installations WHERE instance_id=?`, instance).Scan(&previous)
	if err != nil && (!errors.Is(err, sql.ErrNoRows) || installed) {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET installed=? WHERE instance_id=?`, installed, instance); err != nil {
		return err
	}
	if err = a.pluginStore.updateOriginalInstalledOrderTx(ctx, tx, instance, installed, previous); err != nil {
		return err
	}
	return tx.Commit()
}
