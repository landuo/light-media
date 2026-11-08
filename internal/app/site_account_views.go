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
	view, err := a.siteAccountViewTx(ctx, tx, []SiteConfig{site})
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
	v := siteAccountView{Sites: map[string]SiteConfig{}, Rank: map[int64]int{}}
	legacy := map[string]string{}
	for _, site := range sites {
		v.Sites[site.ID] = site
	}
	if a.pluginStore != nil {
		proof, err := a.pluginStore.sourceAccountProofTx(ctx, tx)
		if err != nil {
			return v, err
		}
		if proof != nil {
			b := proof.Binding
			if err = sourceAccountHeadersTx(ctx, tx, b); err != nil {
				return v, err
			}
			v.Binding = &b
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
				v.Rows = append(v.Rows, accountObservation{Header: h, SiteID: legacy[h.Domain]})
			}
			ids, err := sourceAccountLatestIDsTx(ctx, tx, b.SourceFingerprint)
			if err != nil {
				return v, err
			}
			for i, id := range ids {
				v.Rank[id] = i
			}
		}
	}
	first := map[string]int{}
	for i, o := range v.Rows {
		if o.SiteID == "" {
			continue
		}
		key := o.SiteID + ":" + o.Header.Day
		if _, exists := first[key]; !exists {
			first[key] = i
		}
	}
	for _, site := range sites {
		rows, err := tx.QueryContext(ctx, `SELECT id,rowid,payload FROM records WHERE kind='site_stats_history' AND substr(id,1,?)=? ORDER BY id`, len(site.ID)+1, site.ID+":")
		if err != nil {
			return v, err
		}
		var history []siteSnapshot
		orders := map[string]int64{}
		for rows.Next() {
			var key string
			var order int64
			var raw []byte
			var s siteSnapshot
			if err = rows.Scan(&key, &order, &raw); err != nil {
				break
			}
			if err = a.Store.openSiteAccountSnapshot("site_stats_history", key, raw, &s); err != nil {
				break
			}
			if s.ConfigKey != a.managedSiteKey(site) {
				continue
			}
			data, e := statisticData(s, site)
			if e != nil {
				err = e
				break
			}
			if s.SiteID != "" && s.SiteID != site.ID || key != site.ID+":"+data.UpdatedDay {
				err = invalidSiteAccountSnapshot()
				break
			}
			history = append(history, s)
			orders[data.UpdatedDay] = order
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
		err = tx.QueryRowContext(ctx, `SELECT rowid,payload FROM records WHERE kind='site_stats_latest' AND id=?`, site.ID).Scan(&order, &raw)
		if err == nil {
			var s siteSnapshot
			if err = a.Store.openSiteAccountSnapshot("site_stats_latest", site.ID, raw, &s); err != nil {
				return v, err
			}
			if s.ConfigKey == a.managedSiteKey(site) {
				if s.SiteID != "" && s.SiteID != site.ID {
					return v, invalidSiteAccountSnapshot()
				}
				history = append(history, s)
				data, e := statisticData(s, site)
				if e != nil {
					return v, e
				}
				if _, exists := orders[data.UpdatedDay]; !exists {
					orders[data.UpdatedDay] = order
				}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return v, err
		}
		for _, s := range history {
			data, err := statisticData(s, site)
			if err != nil {
				return v, err
			}
			copy := s
			key := site.ID + ":" + data.UpdatedDay
			if i, exists := first[key]; exists {
				// Failures never replace an existing day, including its first
				// failed attempt. A successful refresh updates that first row.
				if data.Error == "" && data.UpdatedTime >= v.Rows[i].Header.Time {
					v.Rows[i].Native = &copy
					v.Rows[i].Header.Time, v.Rows[i].Header.Good = data.UpdatedTime, true
				}
			} else {
				first[key] = len(v.Rows)
				v.Rows = append(v.Rows, accountObservation{SiteID: site.ID, Header: sourceAccountHeader{Domain: data.Domain, Day: data.UpdatedDay, Time: data.UpdatedTime, Good: data.Error == ""}, Native: &copy, Order: orders[data.UpdatedDay]})
			}
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
