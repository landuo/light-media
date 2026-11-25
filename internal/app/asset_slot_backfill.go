package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"light-media/internal/media"
)

// backfillAssetSlots gives every asset that already describes its episodes the
// asset_slots rows the subscription coverage reads. Assets imported before the
// import path wrote those rows were invisible to the library count, so a
// subscription showed fewer downloaded episodes than the library actually held.
func backfillAssetSlots(ctx context.Context, db *sql.DB) error {
	// A single transaction can stream assets while writing the separate slot
	// table. Keep only the current payload alive, including on old databases.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM library_asset WHERE record_kind='asset'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw sql.RawBytes
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		var asset Asset
		if json.Unmarshal(raw, &asset) != nil || asset.ID == "" {
			continue
		}
		if asset.State == "removed" || asset.State == "superseded" {
			// A previously retired file cannot prove an episode is in the library.
			if _, err = tx.ExecContext(ctx, "DELETE FROM asset_slots WHERE asset_id=?", asset.ID); err != nil {
				return err
			}
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
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = reconcileInventorySubscriptions(ctx, tx, nil); err != nil {
		return err
	}
	return tx.Commit()
}

// dropBlurayStreamObservations removes observations that still point at a member
// of BDMV/STREAM. MoviePilot promotes such an event to the disc root before it
// is dispatched (monitorBlurayDiscRoot), so an observation keyed by the m2ts file
// is stale by construction: rescanning now records the disc instead.
func dropBlurayStreamObservations(ctx context.Context, db *sql.DB) (int, error) {
	result, err := db.ExecContext(ctx, `DELETE FROM directory_file WHERE record_kind='directory_file' AND lower(path) LIKE '%/bdmv/stream/%'`)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

// dropDeadReservations removes reservations whose intent is gone or has reached
// a terminal state. A reservation only expresses "this slot is being fetched
// right now"; once that download is cancelled or failed the row described nothing
// and kept the subscribe at 目标已满足 without ever searching again.
func dropDeadReservations(ctx context.Context, db *sql.DB) (int, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=''
 OR NOT EXISTS(SELECT 1 FROM downloadhistory i WHERE i.uid=reservations.intent_id)
 OR EXISTS(SELECT 1 FROM downloadhistory i WHERE i.uid=reservations.intent_id
    AND json_extract(i.payload,'$.state') IN ('cancelled','failed'))`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// processMemory reads the kernel's own view of this process. cgroup counters
// (what `docker stats` shows) include page cache, so they climb with file reads
// and never distinguish a growing process from a warm cache.
func processMemory() map[string]any {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return nil
	}
	want := map[string]string{
		"VmRSS": "VmRSS", "VmSize": "VmSize", "VmHWM": "VmHWM",
		"RssAnon": "RssAnon", "RssFile": "RssFile", "RssShmem": "RssShmem",
	}
	out := map[string]any{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key, ok := want[strings.TrimSuffix(fields[0], ":")]
		if !ok {
			continue
		}
		if value, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			out[key] = value * 1024
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
