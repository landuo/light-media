package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/plugins"
)

// DownloadHistoryOper.get_by_hash observes only completed Add calls. Both
// native downloads and download intents publish the same sealed accepted fact.
func (a *App) originalSubscribeGroupDownloadHistory(ctx context.Context, hash string) (plugins.DownloadHistoryResultV1, error) {
	out := plugins.DownloadHistoryResultV1{SchemaVersion: 1}
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT kind,id FROM records WHERE kind IN ('intent','native_download') AND json_extract(payload,'$.hash')=?`, hash)
	if err != nil {
		return out, err
	}
	var eventIDs []string
	for rows.Next() {
		var kind, id string
		if err = rows.Scan(&kind, &id); err != nil {
			rows.Close()
			return out, err
		}
		if kind == "native_download" {
			id = kind + ":" + id
		}
		eventIDs = append(eventIDs, domainEventID("download.accepted", id))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	var latest DomainEvent
	var latestDate time.Time
	for _, id := range eventIDs {
		var raw json.RawMessage
		var seq int64
		err = tx.QueryRowContext(ctx, `SELECT seq,payload FROM domain_events WHERE event_id=?`, id).Scan(&seq, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return out, err
		}
		var event DomainEvent
		if json.Unmarshal(raw, &event) != nil || event.EventID != id || event.Type != "download.accepted" {
			return out, bad("INVALID_DOWNLOAD_EVENT", "下载历史事件无法核对")
		}
		date, err := time.Parse(time.RFC3339Nano, event.OccurredAt)
		if err != nil {
			return out, bad("INVALID_DOWNLOAD_EVENT", "下载历史日期无效")
		}
		// The source history stores its local time to second precision and
		// sorts on that field. Use insertion order to resolve equal dates.
		date = date.Truncate(time.Second)
		if latest.EventID == "" || date.After(latestDate) || date.Equal(latestDate) && seq < latest.Sequence {
			event.Sequence = seq
			latest, latestDate = event, date
		}
	}
	if latest.EventID == "" {
		return out, nil
	}
	fact, err := a.pluginStore.downloadAddedSnapshotFrom(ctx, tx, latest)
	if err != nil {
		return out, err
	}
	if !sameHash(fact.Hash, hash) {
		return out, bad("INVALID_DOWNLOAD_EVENT", "下载历史Hash与事件不符")
	}
	row := &plugins.DownloadHistoryRowV1{ID: fact.DownloadID, Hash: fact.Hash, Type: fact.MediaType, TMDBID: fact.TMDBID, SiteID: fact.SiteID, CreatedAt: latest.OccurredAt}
	switch row.Type {
	case "tv":
		row.Type = "电视剧"
	case "movie":
		row.Type = "电影"
	}
	if fact.Seasons != "" {
		row.Seasons = &fact.Seasons
	}
	if source := fact.Context; source != nil && source.Original != nil && len(source.Original.Meta) > 0 {
		var meta struct {
			Season json.RawMessage `json:"season"`
		}
		if err = json.Unmarshal(source.Original.Meta, &meta); err != nil {
			return out, bad("INVALID_DOWNLOAD_CONTEXT", "下载历史季号无法读取")
		}
		if len(meta.Season) != 0 && json.Unmarshal(meta.Season, &row.Seasons) != nil {
			return out, bad("INVALID_DOWNLOAD_CONTEXT", "下载历史季号无法读取")
		}
	}
	var raw json.RawMessage
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, fact.DownloadKind, fact.DownloadID).Scan(&raw); err != nil {
		return out, err
	}
	if fact.DownloadKind == "native_download" {
		var native NativeDownload
		if err = json.Unmarshal(raw, &native); err != nil {
			return out, err
		}
		row.Title = native.ResourceTitle
		if native.Media != nil {
			row.Title = native.Media.Title
		}
		row.DownloaderID, row.SavePath, row.State = native.DownloaderID, native.DownloadPath, native.State
	} else {
		var intent Intent
		if err = json.Unmarshal(raw, &intent); err != nil {
			return out, err
		}
		row.Title, row.Season, row.Episodes = intent.Media.Title, intent.Season, intent.Episodes
		row.DownloaderID, row.SavePath, row.State = intent.DownloaderID, intent.DownloadPath, intent.State
		if intent.SubscriptionAcquisition != "" {
			// The accepted child has already committed. Allow its parent to
			// finish using the sealed selection before changing its revision.
			// Never wait with a database transaction or mutation lock held.
			if err := tx.Rollback(); err != nil {
				return out, err
			}
			if err := a.awaitOriginalSubscribeGroupAcquisition(ctx, intent.SubscriptionAcquisition); err != nil {
				return out, err
			}
		}
	}
	out.Item = row
	return out, nil
}

func (a *App) awaitOriginalSubscribeGroupAcquisition(ctx context.Context, id string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var kind, state string
		err := a.Store.DB.QueryRowContext(ctx, `SELECT kind,state FROM jobs WHERE id=?`, id).Scan(&kind, &state)
		if err != nil {
			return err
		}
		if kind != "subscription" {
			return acquisitionEvidence()
		}
		switch state {
		case "queued", "running", "retry_wait", "waiting_paused":
		default:
			return nil
		}
		runtime := &a.pluginOriginalBackground
		runtime.mu.Lock()
		closing := runtime.sourceEventsClosing || runtime.closed
		runtime.mu.Unlock()
		if closing {
			return bad("PLUGIN_CALL_CLOSED", "插件事件已停止")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
