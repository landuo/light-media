package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

func pluginDataVersionSchema() string {
	// Keep the read cursor on a compact index. A change to a different plugin
	// or task must not evict hundreds of kilobytes of unchanged torrent data.
	var ddl strings.Builder
	ddl.WriteString(`DROP TRIGGER IF EXISTS plugin_display_value_insert;
 DROP TRIGGER IF EXISTS plugin_display_value_update;
 DROP TRIGGER IF EXISTS plugin_display_value_delete;
 CREATE TABLE IF NOT EXISTS plugin_display_keys(instance TEXT, item_key TEXT, stamp TEXT NOT NULL, revision INTEGER NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(instance,item_key)) WITHOUT ROWID;`)
	eligible := func(prefix string) string {
		k := prefix + "item_key"
		return prefix + "kind='data' AND (" + k + " GLOB 'task.*.torrents' OR " + k + " GLOB 'task.*.runs' OR " + k + " GLOB 'task.*.archived' OR " + k + " GLOB 'task.*.statistic')"
	}
	ddl.WriteString("INSERT OR IGNORE INTO plugin_display_keys SELECT instance_id,item_key,hex(randomblob(16)),revision,updated_at FROM plugin_values WHERE " + eligible("") + ";")
	save := "INSERT INTO plugin_display_keys SELECT NEW.instance_id,NEW.item_key,hex(randomblob(16)),NEW.revision,NEW.updated_at WHERE " + eligible("NEW.") + " ON CONFLICT(instance,item_key) DO UPDATE SET stamp=excluded.stamp,revision=excluded.revision,updated_at=excluded.updated_at;"
	remove := "DELETE FROM plugin_display_keys WHERE instance=OLD.instance_id AND item_key=OLD.item_key AND OLD.kind='data';"
	fmt.Fprintf(&ddl, "CREATE TRIGGER IF NOT EXISTS plugin_display_key_insert AFTER INSERT ON plugin_values WHEN %s BEGIN %s END;", eligible("NEW."), save)
	fmt.Fprintf(&ddl, "CREATE TRIGGER IF NOT EXISTS plugin_display_key_update AFTER UPDATE ON plugin_values WHEN (%s) OR (%s) BEGIN %s %s END;", eligible("OLD."), eligible("NEW."), remove, save)
	fmt.Fprintf(&ddl, "CREATE TRIGGER IF NOT EXISTS plugin_display_key_delete AFTER DELETE ON plugin_values WHEN %s BEGIN %s END;", eligible("OLD."), remove)
	return ddl.String()
}

type pluginDataKey struct{ instance, key string }
type pluginDataEntry struct {
	stamp string
	raw   json.RawMessage
}
type pluginDataCache struct {
	mu     sync.Mutex
	bytes  int
	values map[pluginDataKey]pluginDataEntry
	order  []pluginDataKey
}

func (p *PluginStore) readDisplayData(ctx context.Context, instance, key string) (PluginStoredValue, error) {
	c := &p.displayData
	k := pluginDataKey{instance, key}
	c.mu.Lock()
	cached := c.values[k]
	c.mu.Unlock()
	var stamp string
	var sealed []byte
	var v PluginStoredValue
	err := p.store.reader().QueryRowContext(ctx, `SELECT k.stamp,k.revision,k.updated_at,
 CASE WHEN k.stamp!=? THEN (SELECT sealed FROM plugin_values WHERE instance_id=k.instance AND kind='data' AND item_key=k.item_key) END
 FROM plugin_display_keys k WHERE k.instance=? AND k.item_key=?`, cached.stamp, instance, key).Scan(&stamp, &v.Revision, &v.UpdatedAt, &sealed)
	if err != nil {
		return v, err
	}
	if stamp == cached.stamp {
		v.Value = append(json.RawMessage(nil), cached.raw...)
		return v, nil
	}
	v.Value, err = p.unseal("data", instance, key, sealed)
	if err != nil || len(v.Value) > 512<<10 {
		return v, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values = make(map[pluginDataKey]pluginDataEntry)
	}
	if existing, ok := c.values[k]; ok {
		if existing.stamp == stamp {
			return v, nil
		}
		// Another caller has read this key since our initial lookup. Its data
		// may be newer. Future readers always check the committed stamp.
		if existing.stamp != cached.stamp {
			return v, nil
		}
		c.bytes -= len(existing.raw)
		delete(c.values, k)
		for i, id := range c.order {
			if id == k {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	}
	for len(c.order) > 0 && (c.bytes+len(v.Value) > 1536<<10 || len(c.order) >= 64) {
		first := c.order[0]
		c.order = c.order[1:]
		c.bytes -= len(c.values[first].raw)
		delete(c.values, first)
	}
	c.values[k] = pluginDataEntry{stamp: stamp, raw: append(json.RawMessage(nil), v.Value...)}
	c.order = append(c.order, k)
	c.bytes += len(v.Value)
	return v, nil
}
