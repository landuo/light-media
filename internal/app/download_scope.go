package app

import (
	"context"
	"strings"

	"light-media/internal/download"
)

// MoviePilot limits its download page to its own torrent tag. Our tag is
// deliberately distinct: operating this independent app must not adopt the
// old MoviePilot instance's torrents. Durable records also recognize an
// explicitly selected duplicate that the daemon already had without our tag.
type downloaderTaskOwnership map[string][]string

func (a *App) downloaderTaskOwnership(ctx context.Context, downloaderID string) (downloaderTaskOwnership, error) {
	rows, err := a.Store.reader().QueryContext(ctx, `SELECT json_extract(payload,'$.hash') FROM records WHERE kind IN ('intent','native_download') AND json_extract(payload,'$.downloader_id')=? AND COALESCE(json_extract(payload,'$.state'),'')!='cancelled' AND length(json_extract(payload,'$.hash')) IN (40,64)`, downloaderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owned := downloaderTaskOwnership{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		hash = strings.ToLower(hash)
		owned[hash[:40]] = append(owned[hash[:40]], hash)
	}
	return owned, rows.Err()
}

func (owned downloaderTaskOwnership) contains(task download.Task) bool {
	for _, tag := range task.Tags {
		if strings.TrimSpace(tag) == "light-media" {
			return true
		}
	}
	hash := strings.ToLower(task.Hash)
	if len(hash) < 40 {
		return false
	}
	for _, previous := range owned[hash[:40]] {
		if sameHash(previous, hash) {
			return true
		}
	}
	return false
}
