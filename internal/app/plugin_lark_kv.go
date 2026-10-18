package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
	lark "light-media/internal/plugins/larkmessager"
)

// pluginLarkKV is a private native adapter. The original Page marks results as
// displayed and Status removes them, so neither surface can use a read-only
// worker view. No SDK request may choose this adapter's instance or generation.
// Actual owner/deferred admission is supplied by the enclosing native call;
// this adapter additionally rejects a retired installation inside each SQL tx.
type pluginLarkKV struct {
	store      *PluginStore
	instance   string
	generation int64
}

var _ lark.KV = pluginLarkKV{}

func (k pluginLarkKV) guard(ctx context.Context, tx *sql.Tx, key string) error {
	if k.store == nil || !pluginStorageID.MatchString(k.instance) || k.generation < 1 || (key != "last_test_result" && key != "last_chats") {
		return lark.ErrDenied
	}
	var plugin, version string
	var installed bool
	var generation int64
	var sealed []byte
	err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,installed,generation,manifest FROM plugin_installations WHERE instance_id=?`, k.instance).Scan(&plugin, &version, &installed, &generation, &sealed)
	if err != nil {
		return err
	}
	if !installed || generation != k.generation || plugin != "LarkMessager" || version != "0.9.1" {
		return lark.ErrDenied
	}
	raw, err := k.store.unseal("manifest", k.instance, "main", sealed)
	if err != nil {
		return err
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != plugin || manifest.Version != version || manifest.Runtime != "go-executable" {
		return lark.ErrDenied
	}
	return nil
}

func (k pluginLarkKV) Get(ctx context.Context, key string) (lark.Object, error) {
	if k.store == nil {
		return nil, lark.ErrDenied
	}
	tx, err := k.store.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = k.guard(ctx, tx, key); err != nil {
		return nil, err
	}
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, k.instance, key).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	raw, err := k.store.unseal("data", k.instance, key, sealed)
	if err != nil {
		return nil, err
	}
	value, err := lark.DecodeSourceObject(raw)
	if err != nil {
		return nil, err
	}
	return value, tx.Commit()
}

func (k pluginLarkKV) Put(ctx context.Context, key string, value lark.Object) error {
	if k.store == nil {
		return lark.ErrDenied
	}
	raw, err := pluginJSONLimit(value, false, pluginDataValueLimit)
	if err != nil {
		return err
	}
	// Decode through the original-compatible model before retaining the value.
	if _, err = lark.DecodeSourceObject(raw); err != nil {
		return err
	}
	tx, err := k.store.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = k.guard(ctx, tx, key); err != nil {
		return err
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, k.instance, key).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = k.store.saveValueTx(ctx, tx, k.instance, "data", key, revision, raw); err != nil {
		return err
	}
	return tx.Commit()
}

func (k pluginLarkKV) Delete(ctx context.Context, key string) error {
	if k.store == nil {
		return lark.ErrDenied
	}
	tx, err := k.store.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = k.guard(ctx, tx, key); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, k.instance, key); err != nil {
		return err
	}
	return tx.Commit()
}
