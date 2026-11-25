package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Only native account writes can change these snapshots. An unrelated site
// refresh must not force hundreds of historical encrypted rows to be reread.
func nativeAccountStampSchema() string {
	var ddl strings.Builder
	ddl.WriteString("CREATE TABLE IF NOT EXISTS native_account_stamps(site TEXT PRIMARY KEY, stamp TEXT NOT NULL) WITHOUT ROWID;")
	for _, table := range []string{"sitestatistic", "site_statistic_event"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			name := "native_account_stamp_" + table + "_" + op
			fmt.Fprintf(&ddl, "DROP TRIGGER IF EXISTS %s; CREATE TRIGGER %s AFTER %s ON %s BEGIN ", name, name, op, table)
			for _, side := range []string{"OLD", "NEW"} {
				if side == "NEW" && op == "DELETE" || side == "OLD" && op == "INSERT" {
					continue
				}
				key := side + ".uid"
				if table == "site_statistic_event" {
					key = "substr(" + side + ".id,1,instr(" + side + ".id,':')-1)"
				}
				fmt.Fprintf(&ddl, "INSERT INTO native_account_stamps VALUES(%s,hex(randomblob(16))) ON CONFLICT(site) DO UPDATE SET stamp=excluded.stamp;", key)
			}
			ddl.WriteString("END;")
		}
	}
	return ddl.String()
}

type nativeAccountEntry struct {
	stamp string
	rows  []accountObservation
	size  int
}
type nativeAccountCache struct {
	mu       sync.Mutex
	settings *Settings
	entries  map[string]nativeAccountEntry
	bytes    int
}

func (a *App) nativeLatestCandidates(ctx context.Context, tx *sql.Tx, settings *Settings, site SiteConfig) ([]accountObservation, error) {
	var stamp string
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT stamp FROM native_account_stamps WHERE site=?),'')", site.ID).Scan(&stamp); err != nil {
		return nil, err
	}
	c := &a.nativeAccountCache
	c.mu.Lock()
	cached, ok := c.entries[site.ID]
	hit := ok && c.settings == settings && cached.stamp == stamp
	c.mu.Unlock()
	if hit {
		return cached.rows, nil
	}
	view, err := a.readSiteAccountViewTx(ctx, tx, []SiteConfig{site}, siteAccountViewScope{})
	if err != nil {
		return nil, err
	}
	// Another site on the same domain may supply a newer good day. Keep all
	// rows that could match it, including failures on or after our last success.
	days := map[string]string{}
	for _, r := range view.Rows {
		if r.Header.Good {
			days[r.Header.Domain] = max(days[r.Header.Domain], r.Header.Day)
		}
	}
	var rows []accountObservation
	for _, r := range view.Rows {
		if r.Header.Day >= days[r.Header.Domain] {
			rows = append(rows, r)
		}
	}
	encoded, err := json.Marshal(rows)
	if err != nil || len(encoded) > 64<<10 {
		return rows, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previous, exists := c.entries[site.ID]
	if c.settings != settings || c.bytes+len(encoded)-previous.size > 512<<10 || len(c.entries) >= 64 && !exists {
		c.settings = settings
		c.entries = make(map[string]nativeAccountEntry)
		c.bytes = 0
	}
	c.bytes += len(encoded) - c.entries[site.ID].size
	c.entries[site.ID] = nativeAccountEntry{stamp: stamp, rows: rows, size: len(encoded)}
	return rows, nil
}

func (a *App) sourceAccountDisplayView(ctx context.Context, tx *sql.Tx, settings *Settings, sites []SiteConfig, latest bool, day string) (siteAccountView, error) {
	if latest && a.pluginStore != nil {
		archive, err := a.siteArchiveInSnapshot(ctx, tx)
		if err != nil {
			return siteAccountView{}, err
		}
		native := archive.proof == nil
		for _, site := range sites {
			if strings.Contains(site.ID, ":") {
				native = false
				break
			}
		}
		if native {
			view := siteAccountView{Sites: map[string]SiteConfig{}}
			for _, s := range sites {
				view.Sites[s.ID] = s
				rows, err := a.nativeLatestCandidates(ctx, tx, settings, s)
				if err != nil {
					return view, err
				}
				view.Rows = append(view.Rows, rows...)
			}
			sort.SliceStable(view.Rows, func(i, j int) bool { return view.Rows[i].Order < view.Rows[j].Order })
			return view, nil
		}
	}
	return a.readSiteAccountViewTx(ctx, tx, sites, siteAccountViewScope{day: day})
}
