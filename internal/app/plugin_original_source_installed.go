package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sync"
)

// The ordinary source SystemConfigOper cache is process-local. A failed SQL
// write retains the new value. Observe the real encrypted row separately so a
// different existing native writer invalidates this finite cached setting.
type originalInstalledCache struct {
	mu       sync.Mutex
	writeMu  sync.Mutex
	epoch    uint64
	valid    bool
	observed [sha256.Size]byte
	ids      []string
}

func (p *PluginStore) readOriginalInstalledObservation(ctx context.Context, db originalInstalledOrderReader) ([]string, [sha256.Size]byte, error) {
	var sealed []byte
	err := db.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id='main'`, originalInstalledOrderKind).Scan(&sealed)
	if err == nil {
		raw, e := p.unseal(originalInstalledOrderKind, "admin", "main", sealed)
		if e != nil {
			return nil, [sha256.Size]byte{}, e
		}
		var ids []string
		if json.Unmarshal(raw, &ids) != nil || ids == nil {
			return nil, [sha256.Size]byte{}, bad("PLUGIN_INSTALLED_ORDER_INVALID", "插件安装清单无法读取")
		}
		return ids, sha256.Sum256(append([]byte{1}, sealed...)), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, [sha256.Size]byte{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT instance_id FROM plugin_installations WHERE installed=1 ORDER BY created_at,rowid`)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, [sha256.Size]byte{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	raw, _ := json.Marshal(ids)
	return ids, sha256.Sum256(append([]byte{0}, raw...)), nil
}
func (p *PluginStore) sourceSetOriginalInstalled(ctx context.Context, raw json.RawMessage) (bool, error) {
	var ids []string
	if json.Unmarshal(raw, &ids) != nil || ids == nil {
		return false, bad("INVALID_PLUGIN_VALUE", "安装清单必须为实例ID数组")
	}
	for _, id := range ids {
		if !pluginStorageID.MatchString(id) {
			return false, bad("INVALID_PLUGIN_ID", "插件实例ID无效")
		}
	}
	state := &p.originalInstalledCache
	state.writeMu.Lock()
	defer state.writeMu.Unlock()
	previous, observed, err := p.readOriginalInstalledObservation(ctx, p.store.DB)
	if err != nil {
		return false, err
	}
	var rowExists bool
	if err = p.store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE kind=? AND id='main')`, originalInstalledOrderKind).Scan(&rowExists); err != nil {
		return false, err
	}
	state.mu.Lock()
	if state.valid && state.observed == observed {
		previous = state.ids
	}
	state.epoch++
	epoch := state.epoch
	state.valid, state.observed, state.ids = true, observed, append([]string{}, ids...) // source always deep-copies before SQL
	state.mu.Unlock()
	if rowExists && slices.Equal(previous, ids) {
		return false, nil
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if rowExists && len(ids) == 0 {
		_, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind=? AND id='main'`, originalInstalledOrderKind)
	} else {
		var sealed []byte
		sealed, err = p.seal(originalInstalledOrderKind, "admin", "main", raw)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, originalInstalledOrderKind, sealed, now())
		}
	}
	if err != nil {
		return false, err
	}
	// Native membership is a read-model projection of the same real source list;
	// class/running maps, plugin config/data and generation are deliberately separate.
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET installed=0`); err != nil {
		return false, err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET installed=1 WHERE instance_id=?`, id); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	_, nextObserved, err := p.readOriginalInstalledObservation(ctx, p.store.DB)
	if err == nil {
		state.mu.Lock()
		if state.epoch == epoch {
			state.observed = nextObserved
			state.valid = true
		}
		state.mu.Unlock()
	}
	return true, err
}
