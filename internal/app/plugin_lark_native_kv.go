package app

import (
	"context"
	"database/sql"
	"errors"

	lark "light-media/internal/plugins/larkmessager"
)

// The source's two mutable display cells are mirrored into the existing
// PluginStore KV in the same transaction. The owner root authenticates the
// exact cell set; deleting the cell cannot silently turn it into a first read.
type pluginLarkNativeKV struct{ o *pluginLarkOwner }

func (k pluginLarkNativeKV) open(ctx context.Context, key string, write bool) (*sql.Tx, pluginLarkRootView, error) {
	if key != "last_test_result" && key != "last_chats" {
		return nil, pluginLarkRootView{}, lark.ErrDenied
	}
	x, e := k.o.execution(ctx)
	if e != nil {
		return nil, pluginLarkRootView{}, e
	}
	tx, e := k.o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: !write})
	if e != nil {
		return nil, pluginLarkRootView{}, e
	}
	if e = k.o.guardTx(ctx, tx, x.scope); e != nil {
		tx.Rollback()
		return nil, pluginLarkRootView{}, e
	}
	root, e := k.o.a.pluginStore.larkRootRead(ctx, tx, k.o.instance, k.o.generation)
	if e != nil {
		tx.Rollback()
		return nil, root, e
	}
	return tx, root, nil
}
func (k pluginLarkNativeKV) Get(ctx context.Context, key string) (lark.Object, error) {
	tx, root, e := k.open(ctx, key, false)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if root.Root.Values[key] == "" {
		return nil, tx.Commit()
	}
	var sealed []byte
	if e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_lark_kv_values WHERE instance_id=? AND generation=? AND item_key=?`, k.o.instance, k.o.generation, key).Scan(&sealed); e != nil {
		return nil, larkEvidenceError()
	}
	raw, e := k.o.a.pluginStore.unseal("lark_kv", k.o.instance, larkRootKey(k.o.generation)+"/kv/"+key, sealed)
	if e != nil {
		return nil, e
	}
	v, e := lark.DecodeSourceObject(raw)
	if e != nil {
		return nil, e
	}
	return v, tx.Commit()
}
func (k pluginLarkNativeKV) Put(ctx context.Context, key string, value lark.Object) error {
	raw, e := pluginJSONLimit(value, false, pluginDataValueLimit)
	if e != nil {
		return e
	}
	if _, e = lark.DecodeSourceObject(raw); e != nil {
		return e
	}
	tx, root, e := k.open(ctx, key, true)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var revision int64
	e = tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, k.o.instance, key).Scan(&revision)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if _, e = k.o.a.pluginStore.saveValueTx(ctx, tx, k.o.instance, "data", key, revision, raw); e != nil {
		return e
	}
	sealed, e := k.o.a.pluginStore.seal("lark_kv", k.o.instance, larkRootKey(k.o.generation)+"/kv/"+key, raw)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_lark_kv_values VALUES(?,?,?,?) ON CONFLICT(instance_id,generation,item_key) DO UPDATE SET sealed=excluded.sealed`, k.o.instance, k.o.generation, key, sealed); e != nil {
		return e
	}
	next := root.Root
	next.Values = map[string]string{}
	for key, digest := range root.Root.Values {
		next.Values[key] = digest
	}
	next.Values[key] = tokenHash(string(sealed))
	next.Sequence++
	if e = k.o.a.pluginStore.larkAppendRootTx(ctx, tx, root, next, "kv", key, next.Values[key]); e != nil {
		return e
	}
	return tx.Commit()
}
func (k pluginLarkNativeKV) Delete(ctx context.Context, key string) error {
	tx, root, e := k.open(ctx, key, true)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, k.o.instance, key); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM plugin_lark_kv_values WHERE instance_id=? AND generation=? AND item_key=?`, k.o.instance, k.o.generation, key); e != nil {
		return e
	}
	next := root.Root
	next.Values = map[string]string{}
	for k, v := range root.Root.Values {
		if k != key {
			next.Values[k] = v
		}
	}
	next.Sequence++
	if e = k.o.a.pluginStore.larkAppendRootTx(ctx, tx, root, next, "kv", key, tokenHash("deleted")); e != nil {
		return e
	}
	return tx.Commit()
}
