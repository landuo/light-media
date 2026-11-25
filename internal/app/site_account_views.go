package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"light-media/internal/plugins/statistic"
)

// The imported archive is immutable. This read model overlays later native
// observations once, so the plugin, site cards and account history agree.
type siteAccountView struct {
	Binding *sourceAccountBinding
	Rows    []accountObservation
	Sites   map[string]SiteConfig
	Rank    map[int64]int
}

type siteAccountViewScope struct {
	latestOnly bool
	day        string
}

func (a *App) recordSiteAccountObservationTx(ctx context.Context, tx *sql.Tx, site SiteConfig, snapshot siteSnapshot) error {
	return a.recordSiteAccountObservationTxMode(ctx, tx, site, snapshot, false)
}

func (a *App) recordSiteAccountObservationTxMode(ctx context.Context, tx *sql.Tx, site SiteConfig, snapshot siteSnapshot, source bool) error {
	if snapshot.SiteID != site.ID || snapshot.ConfigKey != a.managedSiteKey(site) {
		return invalidSiteAccountSnapshot()
	}
	if snapshot.SourceOutcome == "none" {
		// A swallowed module exception/unsupported parser has no DTO and cannot
		// create a source daily row. Preserve its native connection diagnostic.
		return a.Store.putSiteAccountSnapshotTx(ctx, tx, "site_connection", site.ID, snapshot)
	}
	data, err := statisticData(snapshot, site)
	if err != nil {
		return err
	}
	if err = a.Store.putSiteAccountSnapshotTx(ctx, tx, "site_connection", site.ID, snapshot); err != nil {
		return err
	}
	view, err := a.readSiteAccountViewTx(ctx, tx, []SiteConfig{site}, siteAccountViewScope{day: data.UpdatedDay})
	if err != nil {
		return err
	}
	exists := false
	for _, o := range view.Rows {
		if o.SiteID == site.ID && o.Header.Day == data.UpdatedDay {
			exists = true
			break
		}
	}
	if source && !exists && snapshot.SourceData != nil {
		// Source current return/message DTO remains separate from the stored
		// INSERT model's scalar defaults. Existing-row UPDATE preserves null.
		cloned, err := snapshot.SourceData.SourceAccountInsertClone()
		if err != nil {
			return err
		}
		snapshot.SourceData = cloned
	}
	if data.Error == "" || !exists {
		if snapshot.SourceData != nil {
			if err := snapshot.SourceData.ValidateSourceAccountBinding(); err != nil {
				return err
			}
		}
		if err = a.Store.putSiteAccountSnapshotTx(ctx, tx, "site_stats_history", site.ID+":"+data.UpdatedDay, snapshot); err != nil {
			return err
		}
	}
	if data.Error == "" {
		return a.Store.putSiteAccountSnapshotTx(ctx, tx, "site_stats_latest", site.ID, snapshot)
	}
	return nil
}

func (a *App) siteAccountViewTx(ctx context.Context, tx *sql.Tx, sites []SiteConfig) (siteAccountView, error) {
	return a.readSiteAccountViewTx(ctx, tx, sites, siteAccountViewScope{})
}

func (a *App) readSiteAccountViewTx(ctx context.Context, tx *sql.Tx, sites []SiteConfig, scope siteAccountViewScope) (siteAccountView, error) {
	v := siteAccountView{Sites: map[string]SiteConfig{}, Rank: map[int64]int{}}
	legacy := map[string]string{}
	for _, site := range sites {
		v.Sites[site.ID] = site
	}
	if a.pluginStore != nil {
		archive, err := a.siteArchiveInSnapshot(ctx, tx)
		if err != nil {
			return v, err
		}
		if archive.proof != nil {
			b := &archive.proof.Binding
			v.Binding = b
			v.Rank = archive.rank
			for _, bind := range b.Sites {
				site, exists := v.Sites[bind.SiteID]
				if !exists {
					continue
				}
				identity, err := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", site.ID)
				if err != nil {
					return v, err
				}
				origin, oe := nativeConfiguredSiteOrigin(site)
				if oe == nil && identity.State == "active" && identity.SourceID == bind.NativeID && identity.CanonicalOrigin == bind.Origin && origin == bind.Origin {
					legacy[bind.Domain] = site.ID
				}
			}
			for _, h := range b.Rows {
				if scope.day != "" && h.Day != scope.day {
					continue
				}
				v.Rows = append(v.Rows, accountObservation{Header: h, SiteID: legacy[h.Domain]})
			}

		}
	}
	first := map[string]int{}
	latestGoodDay := map[string]string{}
	for i, o := range v.Rows {
		if o.SiteID == "" {
			continue
		}
		if scope.latestOnly && o.Header.Good {
			latestGoodDay[o.SiteID] = max(latestGoodDay[o.SiteID], o.Header.Day)
		}
		key := o.SiteID + ":" + o.Header.Day
		if _, exists := first[key]; !exists {
			first[key] = i
		}
	}
	for _, site := range sites {
		prefix := site.ID + ":"
		configKey := a.managedSiteKey(site)
		appendNative := func(s *siteSnapshot, data statistic.Data, order int64) {
			key := prefix + data.UpdatedDay
			if i, exists := first[key]; exists {
				// A failure cannot replace an existing observation for the day.
				if data.Error == "" && data.UpdatedTime >= v.Rows[i].Header.Time {
					v.Rows[i].Native = s
					v.Rows[i].Header.Time, v.Rows[i].Header.Good = data.UpdatedTime, true
				}
			} else {
				first[key] = len(v.Rows)
				v.Rows = append(v.Rows, accountObservation{SiteID: site.ID, Header: sourceAccountHeader{Domain: data.Domain, Day: data.UpdatedDay, Time: data.UpdatedTime, Good: data.Error == ""}, Native: s, Order: order})
			}
		}
		query := `SELECT id,rowid,payload FROM site_statistic_event WHERE id>=? AND id<? ORDER BY id`
		args := []any{prefix, site.ID + ";"}
		if scope.day != "" {
			query = `SELECT id,rowid,payload FROM site_statistic_event WHERE id=?`
			args = []any{prefix + scope.day}
		} else if scope.latestOnly {
			// The key ends with a YYYY-MM-DD day. Read newest first so the most
			// recent good observation bounds how much history we must decrypt.
			query += " DESC"
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return v, err
		}
		oldestKey := prefix + latestGoodDay[site.ID]
		for rows.Next() {
			var key string
			var order int64
			var raw sql.RawBytes
			var s siteSnapshot
			if err = rows.Scan(&key, &order, &raw); err != nil {
				break
			}
			if scope.latestOnly && key < oldestKey {
				break
			}
			if err = a.Store.openSiteAccountSnapshot("site_stats_history", key, raw, &s); err != nil {
				break
			}
			if s.ConfigKey != configKey {
				continue
			}
			data, e := statisticData(s, site)
			if e != nil {
				err = e
				break
			}
			if s.SiteID != "" && s.SiteID != site.ID || key != prefix+data.UpdatedDay {
				err = invalidSiteAccountSnapshot()
				break
			}
			appendNative(&s, data, order)
			if scope.latestOnly && v.Rows[first[key]].Header.Good {
				oldestKey = key
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return v, err
		}
		var raw []byte
		var order int64
		err = tx.QueryRowContext(ctx, `SELECT rowid,payload FROM sitestatistic WHERE uid=?`, site.ID).Scan(&order, &raw)
		if err == nil {
			var s siteSnapshot
			if err = a.Store.openSiteAccountSnapshot("site_stats_latest", site.ID, raw, &s); err != nil {
				return v, err
			}
			if s.ConfigKey == configKey {
				if s.SiteID != "" && s.SiteID != site.ID {
					return v, invalidSiteAccountSnapshot()
				}
				data, e := statisticData(s, site)
				if e != nil {
					return v, e
				}
				if scope.day == "" || data.UpdatedDay == scope.day {
					appendNative(&s, data, order)
				}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return v, err
		}
	}
	// Date queries in the source use database row order before their name-based
	// lookup. Preserve imported IDs and then native history insertion order,
	// rather than accidentally changing that lookup with site-card sorting.
	sort.SliceStable(v.Rows, func(i, j int) bool {
		a, b := v.Rows[i], v.Rows[j]
		if a.Header.ID > 0 || b.Header.ID > 0 {
			if a.Header.ID == 0 {
				return false
			}
			if b.Header.ID == 0 {
				return true
			}
			return a.Header.ID < b.Header.ID
		}
		return a.Order < b.Order
	})
	return v, nil
}

func (a *App) siteAccountStatisticTx(ctx context.Context, tx *sql.Tx, v siteAccountView, o accountObservation) (statistic.Data, error) {
	if o.Native != nil {
		return statisticData(*o.Native, v.Sites[o.SiteID])
	}
	r, err := a.pluginStore.sourceAccountRowTx(ctx, tx, *v.Binding, o.Header)
	if err != nil {
		return statistic.Data{}, err
	}
	domain := o.Header.Domain
	if site, exists := v.Sites[o.SiteID]; exists {
		domain = siteStatisticDomain(site)
	}
	return r.statistic(o.SiteID, domain), nil
}

func (v siteAccountView) latest(siteID string) []accountObservation {
	day := ""
	for _, o := range v.Rows {
		if o.SiteID == siteID && o.Header.Good && o.Header.Day > day {
			day = o.Header.Day
		}
	}
	var out []accountObservation
	if day == "" {
		return out
	}
	for _, o := range v.Rows {
		if o.SiteID == siteID && o.Header.Day == day {
			out = append(out, o)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return accountObservationLess(out[i], out[j], v.Rank) })
	return out
}

type managedSiteAccount struct {
	SiteID     string            `json:"site_id"`
	Name       string            `json:"name,omitempty"`
	Domain     string            `json:"domain,omitempty"`
	Day        string            `json:"updated_day"`
	Connection managedConnection `json:"connection"`
	Stats      any               `json:"stats"`
}

func (a *App) managedSiteAccountTx(ctx context.Context, tx *sql.Tx, v siteAccountView, o accountObservation) (managedSiteAccount, error) {
	if o.Native != nil {
		s := o.Native
		stats := any(s.Stats)
		if s.Stats == nil {
			stats = map[string]any{"captured_at": s.Connection.CheckedAt}
		}
		return managedSiteAccount{SiteID: o.SiteID, Name: s.Name, Domain: s.Domain, Day: o.Header.Day, Connection: s.Connection, Stats: stats}, nil
	}
	r, err := a.pluginStore.sourceAccountRowTx(ctx, tx, *v.Binding, o.Header)
	if err != nil {
		return managedSiteAccount{}, err
	}
	at, _ := time.ParseInLocation("2006-01-02 15:04:05", o.Header.Day+" "+o.Header.Time, shanghai())
	connection := managedConnection{Status: "ok", CheckedAt: at.UTC().Format(time.RFC3339)}
	if !o.Header.Good {
		connection.Status, connection.Error = "failed", accountString(r.Error)
	}
	// Original byte totals are floats, not int64. Project without truncation or
	// treating SQL NULL as an observed zero. Nested JSON stays structured JSON.
	var seedingInfo, messages json.RawMessage
	if r.SeedingInfo != nil {
		seedingInfo = json.RawMessage(*r.SeedingInfo)
	}
	if r.MessageUnreadContents != nil {
		messages = json.RawMessage(*r.MessageUnreadContents)
	}
	stats := map[string]any{"user_id": r.UserID, "username": r.Username, "level": r.UserLevel, "joined_at": r.JoinAt, "uploaded": r.Upload, "downloaded": r.Download, "ratio": r.Ratio, "seeding": r.Seeding, "seeding_bytes": r.SeedingSize, "leeching": r.Leeching, "leeching_bytes": r.LeechingSize, "bonus": r.Bonus, "seeding_info": seedingInfo, "message_unread": r.MessageUnread, "message_unread_contents": messages, "captured_at": connection.CheckedAt}
	return managedSiteAccount{SiteID: o.SiteID, Name: accountString(r.Name), Domain: o.Header.Domain, Day: o.Header.Day, Connection: connection, Stats: stats}, nil
}
