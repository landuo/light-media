package app

// UserInstalledPlugins is a SystemConfig value, not the set of loaded plugin
// classes/packages. An interrupted uninstall can remove a package while this
// independent value still contains its ID. Its warm cache is set before the
// separate database commit; a cold process reads the durable value again.

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sync"
)

const pluginSourceInstalledSchema = `CREATE TABLE IF NOT EXISTS plugin_source_installed(
 id INTEGER PRIMARY KEY CHECK(id=1),revision INTEGER NOT NULL CHECK(revision>0),sealed BLOB NOT NULL);`

type pluginSourceInstalledValue struct {
	IDs    []string `json:"ids"`
	Exists bool     `json:"exists"`
}

type pluginSourceInstalledCache struct {
	mu       sync.Mutex
	revision int64
	ids      []string
}

func (p *PluginStore) initializeSourceInstalledTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT i.instance_id FROM plugin_installations i LEFT JOIN plugin_install_sequence s USING(instance_id) WHERE i.installed=1 ORDER BY s.seq,i.rowid`)
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
	raw, err := pluginJSON(pluginSourceInstalledValue{IDs: ids, Exists: len(ids) > 0}, true)
	if err != nil {
		return err
	}
	sealed, err := p.seal("uninstall_result", "PluginUnInstall", "installed-global:v1", raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_source_installed VALUES(1,1,?)`, sealed)
	return err
}

func (p *PluginStore) sourceInstalledState(ctx context.Context, q pluginRemoveLinkJournalReader) (pluginSourceInstalledValue, int64, error) {
	var value pluginSourceInstalledValue
	var revision int64
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_source_installed WHERE id=1`).Scan(&revision, &sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return value, 0, bad("PLUGIN_INVENTORY_ORDER_INVALID", "插件源安装列表缺失")
		}
		return value, 0, err
	}
	raw, err := p.unseal("uninstall_result", "PluginUnInstall", "installed-global:v1", sealed)
	if err != nil || revision < 1 || cleanupJournalStrict(raw, &value) != nil || value.IDs == nil || (!value.Exists && len(value.IDs) != 0) {
		return value, 0, bad("PLUGIN_INVENTORY_ORDER_INVALID", "插件源安装列表完整性无效")
	}
	seen := map[string]bool{}
	for _, id := range value.IDs {
		if !pluginStorageID.MatchString(id) || seen[id] {
			return value, 0, bad("PLUGIN_INVENTORY_ORDER_INVALID", "插件源安装列表身份无效")
		}
		seen[id] = true
	}
	return value, revision, nil
}

func (p *PluginStore) sourceInstalled(ctx context.Context, q pluginRemoveLinkJournalReader, warm bool) ([]string, int64, error) {
	value, revision, err := p.sourceInstalledState(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	ids := value.IDs
	if warm {
		p.installedSource.mu.Lock()
		if p.installedSource.revision == revision {
			ids = append([]string{}, p.installedSource.ids...)
		}
		p.installedSource.mu.Unlock()
	}
	return ids, revision, nil
}

func (p *PluginStore) cacheSourceInstalled(revision int64, ids []string) {
	p.installedSource.mu.Lock()
	defer p.installedSource.mu.Unlock()
	p.installedSource.revision = revision
	p.installedSource.ids = append([]string{}, ids...)
}

func (p *PluginStore) writeSourceInstalledTx(ctx context.Context, tx *sql.Tx, revision int64, ids []string, exists bool) error {
	raw, err := pluginJSON(pluginSourceInstalledValue{IDs: ids, Exists: exists}, true)
	if err != nil {
		return err
	}
	sealed, err := p.seal("uninstall_result", "PluginUnInstall", "installed-global:v1", raw)
	if err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE plugin_source_installed SET revision=revision+1,sealed=? WHERE id=1 AND revision=?`, sealed, revision)
	if err == nil {
		if n, _ := changed.RowsAffected(); n != 1 {
			err = bad("PLUGIN_INVENTORY_ORDER_INVALID", "插件源安装列表已变化")
		}
	}
	return err
}

func (p *PluginStore) updateSourceInstalledMembershipTx(ctx context.Context, tx *sql.Tx, instance string, install bool) error {
	ids, revision, err := p.sourceInstalled(ctx, tx, true)
	if err != nil {
		return err
	}
	index := slices.Index(ids, instance)
	if install {
		if index >= 0 {
			return nil
		}
		ids = append(ids, instance)
	} else {
		if index < 0 {
			return nil
		}
		ids = slices.Delete(ids, index, index+1)
	}
	return p.writeSourceInstalledTx(ctx, tx, revision, ids, len(ids) > 0)
}

// Resolve every ID against its current native identity, including a removed
// class's tombstone. New generations are captured here, never supplied by a
// worker or replayed from an earlier run's journal.
func (p *PluginStore) uninstallSourceInventory(ctx context.Context) ([]PluginInstallation, []string, []string, int64, error) {
	ids, revision, err := p.sourceInstalled(ctx, p.store.DB, true)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	durable, dr, err := p.sourceInstalled(ctx, p.store.DB, false)
	if err != nil || dr != revision {
		if err == nil {
			err = bad("PLUGIN_INVENTORY_ORDER_INVALID", "插件源安装列表已变化")
		}
		return nil, nil, nil, 0, err
	}
	out := make([]PluginInstallation, 0, len(ids))
	for _, id := range ids {
		in, e := p.Installation(ctx, id)
		if e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return nil, nil, nil, 0, bad("PLUGIN_INVENTORY_ORDER_INVALID", "插件源安装列表缺少本地身份")
			}
			return nil, nil, nil, 0, e
		}
		var sequence int64
		if e = p.store.DB.QueryRowContext(ctx, `SELECT seq FROM plugin_install_sequence WHERE instance_id=?`, id).Scan(&sequence); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return nil, nil, nil, 0, bad("PLUGIN_INVENTORY_ORDER_INVALID", "本地插件安装顺序记录缺失")
			}
			return nil, nil, nil, 0, e
		}
		if sequence < 1 {
			return nil, nil, nil, 0, bad("PLUGIN_INVENTORY_ORDER_INVALID", "本地插件安装顺序记录缺失")
		}
		out = append(out, in)
	}
	return out, ids, durable, revision, nil
}
