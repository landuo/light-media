package app

import (
	"context"
	"database/sql"
	"encoding/json"
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
		for _, slot := range asset.inventorySlots() {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO asset_slots VALUES(?,?,?,?)`,
				mediaKey(asset.Media), slot.Season, slot.Episode, asset.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
