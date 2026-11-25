package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sync"
)

// Resource searches and provider pages share cache_entry. Keep version checks
// on a small metadata-only table, so unrelated torrent writes leave pages hot.
const catalogPageStampSchema = `
DROP TRIGGER IF EXISTS nav_list_cache_entry_INSERT;
DROP TRIGGER IF EXISTS nav_list_cache_entry_UPDATE;
DROP TRIGGER IF EXISTS nav_list_cache_entry_DELETE;
CREATE TABLE IF NOT EXISTS catalog_page_stamps(id TEXT PRIMARY KEY, stamp TEXT NOT NULL) WITHOUT ROWID;
INSERT OR IGNORE INTO catalog_page_stamps SELECT id,hex(randomblob(16)) FROM cache_entry
 WHERE record_kind='cache' AND NOT EXISTS(SELECT 1 FROM catalog_page_stamps LIMIT 1);
DROP TRIGGER IF EXISTS catalog_page_insert;
CREATE TRIGGER catalog_page_insert AFTER INSERT ON cache_entry BEGIN
 DELETE FROM catalog_page_stamps WHERE id=NEW.id AND NEW.record_kind IS NOT 'cache';
 INSERT INTO catalog_page_stamps SELECT NEW.id,hex(randomblob(16)) WHERE NEW.record_kind='cache' ON CONFLICT(id) DO UPDATE SET stamp=excluded.stamp;
END;
DROP TRIGGER IF EXISTS catalog_page_change;
CREATE TRIGGER catalog_page_change AFTER UPDATE OF id,payload,record_kind ON cache_entry
 WHEN OLD.record_kind='cache' OR NEW.record_kind='cache' BEGIN
 DELETE FROM catalog_page_stamps WHERE id=OLD.id;
 INSERT INTO catalog_page_stamps SELECT NEW.id,hex(randomblob(16)) WHERE NEW.record_kind='cache' ON CONFLICT(id) DO UPDATE SET stamp=excluded.stamp;
END;
CREATE TRIGGER IF NOT EXISTS catalog_page_remove AFTER DELETE ON cache_entry WHEN OLD.record_kind='cache' BEGIN
 DELETE FROM catalog_page_stamps WHERE id=OLD.id;
END;`

type catalogPageEntry struct {
	stamp string
	raw   []byte
}
type catalogPageCache struct {
	mu    sync.Mutex
	pages map[string]catalogPageEntry
	order []string
	bytes int
}

func (c *catalogPageCache) get(stamp, key string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.pages[key]
	if entry.stamp != stamp {
		return nil
	}
	return entry.raw
}
func (c *catalogPageCache) put(stamp, key string, raw []byte) {
	const budget = 2 << 20
	if len(raw) > budget/2 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pages == nil {
		c.pages = make(map[string]catalogPageEntry)
	}
	if old, ok := c.pages[key]; ok {
		if old.stamp == stamp {
			return
		}
		c.bytes -= len(old.raw)
		i := slices.Index(c.order, key)
		c.order = slices.Delete(c.order, i, i+1)
	}
	for len(c.order) > 0 && (c.bytes+len(raw) > budget || len(c.order) >= 32) {
		key := c.order[0]
		c.order = c.order[1:]
		c.bytes -= len(c.pages[key].raw)
		delete(c.pages, key)
	}
	c.pages[key] = catalogPageEntry{stamp: stamp, raw: raw}
	c.order = append(c.order, key)
	c.bytes += len(raw)
}
func (s *Store) readCatalogCache(ctx context.Context, key string, dst any) error {
	tx, err := s.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stamp string
	err = tx.QueryRowContext(ctx, "SELECT stamp FROM catalog_page_stamps WHERE id=?", key).Scan(&stamp)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var raw []byte
	if stamp != "" {
		raw = s.catalogPageCache.get(stamp, key)
	}
	if raw == nil {
		if err = tx.QueryRowContext(ctx, "SELECT payload FROM cache_entry WHERE id=?", key).Scan(&raw); err != nil {
			return err
		}
		if stamp != "" {
			s.catalogPageCache.put(stamp, key, raw)
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}
