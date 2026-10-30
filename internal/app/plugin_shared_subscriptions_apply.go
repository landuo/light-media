package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"light-media/internal/torrentfilter"
)

func (a *App) applySharedSubscriptionColumns(ctx context.Context, tx *sql.Tx, s *Subscription, row map[string]json.RawMessage, patch map[string]json.RawMessage, nonfinite map[string]string) error {
	if s.SharedSourceFields == nil {
		s.SharedSourceFields = sharedSubscriptionFields{}
	} else {
		s.SharedSourceFields = cloneSharedSubscriptionFields(s.SharedSourceFields)
	}
	for key := range patch {
		v := row[key]
		text := sharedSubscriptionString(v)
		switch key {
		case "name":
			s.Media.Title = text
		case "year":
			s.Media.Year, _ = strconv.Atoi(text)
		case "type":
			s.Media.MediaType = sharedSubscriptionType(v)
		case "keyword":
			s.Keyword = text
		case "tmdbid":
			s.Media.ID = int64(sharedSubscriptionInt(v))
		case "imdbid":
			s.Media.IMDbID = text
		case "season":
			s.Season = sharedSubscriptionInt(v)
		case "poster":
			s.Media.PosterPath = text
		case "backdrop":
			s.Media.BackdropPath = text
		case "vote":
			s.Media.VoteAverage = 0
			_ = json.Unmarshal(v, &s.Media.VoteAverage)
		case "description":
			s.Media.Overview = text
		case "include", "exclude", "quality", "resolution", "effect":
			if s.Policy.FilterParams == nil {
				s.Policy.FilterParams = &torrentfilter.Parameters{}
			}
			switch key {
			case "include":
				s.Policy.Include = text
				s.Policy.FilterParams.Include = &text
			case "exclude":
				s.Policy.Exclude = text
				s.Policy.FilterParams.Exclude = &text
			case "quality":
				s.Policy.Source = text
				s.Policy.FilterParams.Quality = &text
			case "resolution":
				s.Policy.Resolution = text
				s.Policy.FilterParams.Resolution = &text
			case "effect":
				s.Policy.HDR = text
				s.Policy.FilterParams.Effect = &text
			}
		case "total_episode":
			s.TotalEpisodes = sharedSubscriptionInt(v)
		case "start_episode":
			s.StartEpisode = sharedSubscriptionInt(v)
		case "lack_episode":
			s.LackEpisode = sharedSubscriptionPtr(v)
		case "current_priority":
			s.CurrentPriority = sharedSubscriptionPtr(v)
		case "note":
			s.SourceNote = nil
			if json.Unmarshal(v, &s.SourceNote) != nil {
				s.SourceNote = nil
			}
		case "episode_priority":
			s.EpisodePriority = nil
			if json.Unmarshal(v, &s.EpisodePriority) != nil {
				s.EpisodePriority = nil
			}
		case "state":
			s.SourceState = text
			s.State = "paused"
			if text == "N" || text == "R" || text == "P" {
				s.State = "active"
			}
		case "last_update", "date":
			native := ""
			if instant, e := time.ParseInLocation("2006-01-02 15:04:05", text, shanghai()); e == nil {
				native = instant.UTC().Format(time.RFC3339Nano)
			}
			if key == "date" {
				s.CreatedAt = native
			} else {
				s.LastCheck = native
			}
		case "sites":
			s.SiteIDs = []string{}
			var values []json.RawMessage
			if json.Unmarshal(v, &values) != nil {
				s.SiteIDs = []string{"source-unrepresentable:sites"}
				break
			}
			for _, raw := range values {
				var n json.Number
				if json.Unmarshal(raw, &n) != nil {
					s.SiteIDs = append(s.SiteIDs, "source-unrepresentable:"+tokenHash(string(raw)))
					continue
				}
				id, e := n.Int64()
				if e != nil {
					s.SiteIDs = append(s.SiteIDs, "source-unrepresentable:"+tokenHash(string(raw)))
					continue
				}
				identity, e := a.Store.readNativeSiteIdentity(ctx, tx, "source_id", id)
				if errors.Is(e, sql.ErrNoRows) {
					s.SiteIDs = append(s.SiteIDs, "source-site:"+strconv.FormatInt(id, 10))
					continue
				}
				if e != nil {
					return e
				}
				s.SiteIDs = append(s.SiteIDs, identity.SiteID)
			}
		case "downloader":
			s.DownloaderID = ""
			if text != "" {
				s.DownloaderID = "source-downloader:" + text
				for _, d := range a.Settings().Downloaders {
					if d.Name == text {
						s.DownloaderID = d.ID
						break
					}
				}
			}
		case "best_version":
			s.BestVersion = sharedSubscriptionFlag(v)
		case "best_version_full":
			s.BestVersionFull = sharedSubscriptionFlag(v)
		case "save_path":
			s.DownloadPath = text
		case "search_imdbid":
			s.SourceSearchIMDb = sharedSubscriptionFlag(v)
		case "manual_total_episode":
			s.ManualTotalEpisode = sharedSubscriptionFlag(v)
		case "filter_groups":
			s.Policy.FilterGroups = nil
			if json.Unmarshal(v, &s.Policy.FilterGroups) != nil {
				s.Policy.FilterGroups = []string{"source-unrepresentable:filter_groups"}
			}
		case "custom_words":
			if e := a.writeSharedSubscriptionWordsTx(ctx, tx, s.ID, text); e != nil {
				return e
			}
		}
	}
	// Bind after all columns, including related fields such as type/season and
	// filter params, have reached the same native record.
	for _, column := range sharedSubscriptionColumns {
		key := column.Name
		v := row[key]
		if key == "custom_words" && sharedSubscriptionString(v) != "" {
			delete(s.SharedSourceFields, key)
			continue
		}
		s.SharedSourceFields[key] = sharedSubscriptionField{Value: copyOriginalBaseJSON(v), Native: sharedSubscriptionNative(*s, key), Nonfinite: nonfinite[key]}
	}
	return nil
}
func (a *App) writeSharedSubscriptionWordsTx(ctx context.Context, tx *sql.Tx, id, text string) error {
	if text == "" {
		_, e := tx.ExecContext(ctx, `DELETE FROM subscription_words WHERE subscription_id=?`, id)
		return e
	}
	sealed, e := a.pluginStore.seal("subscription_words", "system", id, mustCleanSeedJSON(text))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO subscription_words(subscription_id,sealed,updated_at) VALUES(?,?,?) ON CONFLICT(subscription_id) DO UPDATE SET sealed=excluded.sealed,updated_at=excluded.updated_at`, id, sealed, now())
	return e
}
func clearSharedSubscriptionWordsTx(ctx context.Context, tx *sql.Tx, id string) error {
	var raw []byte
	e := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var sub Subscription
	if e = json.Unmarshal(raw, &sub); e != nil {
		return e
	}
	if _, ok := sub.SharedSourceFields["custom_words"]; !ok {
		return nil
	}
	delete(sub.SharedSourceFields, "custom_words")
	return txPut(ctx, tx, "subscription", id, sub)
}
