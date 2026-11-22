package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"

	"light-media/internal/media"
)

// backfillAssetSlots gives every asset that already describes its episodes the
// asset_slots rows the subscription coverage reads. Assets imported before the
// import path wrote those rows were invisible to the library count, so a
// subscription showed fewer downloaded episodes than the library actually held.
func backfillAssetSlots(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT payload FROM library_asset WHERE record_kind='asset'`)
	if err != nil {
		return err
	}
	var payloads [][]byte
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		payloads = append(payloads, raw)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, raw := range payloads {
		var asset Asset
		if json.Unmarshal(raw, &asset) != nil || asset.ID == "" {
			continue
		}
		// Assets imported before the episode fields were persisted carry no
		// number, so their slots could never be derived. Read it from the file
		// name the way the scanner does.
		if len(asset.Episodes) == 0 && len(asset.EpisodeScopes) == 0 && asset.Path != "" {
			if parsed := media.Parse(filepath.Base(asset.Path)); parsed.Original != nil && len(parsed.Original.Episodes) > 0 {
				asset.Episodes = append([]int(nil), parsed.Original.Episodes...)
				if parsed.Original.Season != nil && *parsed.Original.Season > 0 {
					asset.Season = *parsed.Original.Season
				}
			}
		}
		for _, slot := range asset.inventorySlots() {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO asset_slots VALUES(?,?,?,?)`,
				mediaKey(asset.Media), slot.Season, slot.Episode, asset.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// dropBlurayStreamObservations removes observations that still point at a member
// of BDMV/STREAM. MoviePilot promotes such an event to the disc root before it
// is dispatched (monitorBlurayDiscRoot), so an observation keyed by the m2ts file
// is stale by construction: rescanning now records the disc instead.
func dropBlurayStreamObservations(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT uid FROM directory_file WHERE record_kind='directory_file' AND lower(path) LIKE '%/bdmv/stream/%'`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	dropped := 0
	for _, id := range ids {
		res, execErr := tx.ExecContext(ctx, `DELETE FROM directory_file WHERE uid=? AND record_kind='directory_file'`, id)
		if execErr != nil {
			return dropped, execErr
		}
		if n, _ := res.RowsAffected(); n > 0 {
			dropped++
		}
	}
	return dropped, tx.Commit()
}
