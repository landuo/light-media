package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins"
)

// Site cards, plugin site selectors and brush inventory share the same durable
// inputs. Read their change counter within the same snapshot as the data.
func siteDisplayVersionSchema() string {
	var sql strings.Builder
	sql.WriteString("CREATE TABLE IF NOT EXISTS site_display_version(id INTEGER PRIMARY KEY CHECK(id=1),version INTEGER NOT NULL); INSERT OR IGNORE INTO site_display_version VALUES(1,0);")
	for _, table := range []string{"systemconfig", "siteicon", "site_state", "sitestatistic", "site_statistic_event", "source_site_account_rows", "native_site_identities", "native_site_import_refs", "native_site_source_snapshots", "native_site_migration_receipts", "plugin_record", "plugin_migration_receipts", "plugin_migration_runs"} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			condition := ""
			if table == "plugin_record" {
				switch event {
				case "INSERT":
					condition = " WHEN NEW.kind='original_site_account_history'"
				case "DELETE":
					condition = " WHEN OLD.kind='original_site_account_history'"
				default:
					condition = " WHEN NEW.kind='original_site_account_history' OR OLD.kind='original_site_account_history'"
				}
			}
			fmt.Fprintf(&sql, "CREATE TRIGGER IF NOT EXISTS site_display_%s_%s AFTER %s ON %s%s BEGIN UPDATE site_display_version SET version=version+1 WHERE id=1; END;", table, event, event, table, condition)
		}
	}
	return sql.String() + nativeAccountStampSchema()
}

func siteDisplayVersion(ctx context.Context, tx *sql.Tx) (int64, error) {
	var version int64
	err := tx.QueryRowContext(ctx, "SELECT version FROM site_display_version WHERE id=1").Scan(&version)
	return version, err
}

type managedSiteDisplay struct {
	version  int64
	settings *Settings
	data     map[string]any
}
type brushSiteDisplay struct {
	version  int64
	settings *Settings
	data     plugins.BrushInventoryV1
}

type sourceSiteDisplay struct {
	version  int64
	settings *Settings
	data     plugins.SiteHostResultV1
}

type sourceAccountDisplay struct {
	version  int64
	settings *Settings
	data     json.RawMessage
}

// The widget compares each site's current observation with the preceding days.
type datedAccountDisplay struct {
	mu       sync.Mutex
	version  int64
	settings *Settings
	rows     map[string]json.RawMessage
	bytes    int
}

func (c *datedAccountDisplay) get(day string, version int64, settings *Settings) json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.version != version || c.settings != settings {
		return nil
	}
	return append(json.RawMessage(nil), c.rows[day]...)
}
func (c *datedAccountDisplay) put(day string, version int64, settings *Settings, raw json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.settings != settings || c.version != version || c.bytes+len(raw) > 512<<10 || len(c.rows) >= 8 {
		c.version = version
		c.settings = settings
		c.rows = make(map[string]json.RawMessage)
		c.bytes = 0
	}
	c.bytes += len(raw) - len(c.rows[day])
	c.rows[day] = append(json.RawMessage(nil), raw...)
}

// Read only local display inputs. No plugin code or external provider is called.
func (a *App) warmNavigationData(ctx context.Context) {
	if sub, err := a.loadSubscriptionDisplay(ctx); err == nil {
		a.warmSubscriptionImages(ctx, sub.subscriptions)
	}
	_, _ = a.jobQueue(ctx)
	_, _, _ = a.transferHistoryMPPage(ctx, 1, 50)
	if ctx.Err() != nil || len(a.Settings().Sites) == 0 {
		return
	}
	_, _ = a.managedSites(ctx)
	var brush, statistic bool
	if err := a.Store.reader().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_installations WHERE plugin_id='BrushFlowLanduo' AND installed=1), EXISTS(SELECT 1 FROM plugin_installations WHERE plugin_id='SiteStatistic' AND installed=1)`).Scan(&brush, &statistic); err != nil {
		return
	}
	if brush {
		_, _ = a.originalSourceConfiguredRows(ctx, plugins.SiteHostRequestV1{SourceProjection: true})
		_, _ = a.originalBrushInventory(ctx)
	}
	if statistic {
		latest, err := a.siteAccountProjection(ctx, plugins.SiteAccountHostRequestV1{Latest: true}, true)
		if err != nil {
			return
		}
		// The widget first compares each site's last update with its previous day.
		var data plugins.SiteAccountHostResultV1
		if json.Unmarshal(latest, &data) != nil {
			return
		}
		seen := map[string]bool{}
		for _, row := range data.Items {
			var fields struct {
				Day string `json:"updated_day"`
			}
			if json.Unmarshal(row.Fields, &fields) != nil {
				continue
			}
			day, err := time.Parse("2006-01-02", fields.Day)
			if err != nil {
				continue
			}
			prior := day.AddDate(0, 0, -1).Format("2006-01-02")
			if !seen[prior] && len(seen) < 4 && ctx.Err() == nil {
				seen[prior] = true
				_, _ = a.siteAccountProjection(ctx, plugins.SiteAccountHostRequestV1{Date: prior}, true)
			}
		}
	}
}

// Package verification reads local files and shares the same stamp checks as a
// cold descriptor request. Warm at most two loaded full-page plugin frontends.
func (a *App) warmNavigationFrontends(ctx context.Context) {
	count := 0
	for _, object := range a.originalLoadedPluginSnapshot() {
		f := object.manifest.Frontend
		if !object.external || f == nil || !slices.Contains(f.Exposes, "AppPage") {
			continue
		}
		if ctx.Err() != nil || count == 2 {
			return
		}
		a.pluginImplementationMu.RLock()
		_, _, _ = a.originalFrontendLocked(ctx, object.installation.InstanceID)
		a.pluginImplementationMu.RUnlock()
		count++
	}
}

type accountArchiveDisplay struct {
	version int64
	proof   *sourceAccountProof
	rank    map[int64]int
}

func (a *App) siteArchiveInSnapshot(ctx context.Context, tx *sql.Tx) (*accountArchiveDisplay, error) {
	version, err := siteDisplayVersion(ctx, tx)
	if err != nil {
		return nil, err
	}
	if cached := a.siteArchive.Load(); cached != nil && cached.version == version {
		return cached, nil
	}
	proof, err := a.pluginStore.sourceAccountProofTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	view := &accountArchiveDisplay{version: version, proof: proof, rank: map[int64]int{}}
	size := 0
	if proof != nil {
		b := proof.Binding
		if err = sourceAccountHeadersTx(ctx, tx, b); err != nil {
			return nil, err
		}
		ids, err := sourceAccountLatestIDsTx(ctx, tx, b.SourceFingerprint)
		if err != nil {
			return nil, err
		}
		for i, id := range ids {
			view.rank[id] = i
		}
		for _, r := range b.Rows {
			size += len(r.SHA) + len(r.Domain) + len(r.Day) + len(r.Time) + 128
		}
		for _, v := range b.Sites {
			size += len(v.SiteID) + len(v.Origin) + len(v.Domain) + len(v.URL) + 128
		}
		size += len(ids) * 32
	}
	if size <= 512<<10 {
		a.siteArchive.Store(view)
	}
	return view, nil
}
