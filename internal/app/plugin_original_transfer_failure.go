package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
)

// Source __handle_transfer ends an explicitly rejected task. The existing Go
// host reserves media slots before publication; release only this manual plan's
// unpublished selected slots so a new explicit action can use the new config.
// Download-owned reservations and any actual/uncertain publication stay owned.
func (a *App) persistOriginalTransferFailure(ctx context.Context, plan ImportPlan, meta importMetadata, selected []int, failure error) error {
	var api *APIError
	if !errors.As(failure, &api) || plan.IntentID != "" {
		return a.Store.Put(ctx, "import_plan", plan.ID, plan)
	}
	switch api.Code {
	case "PLUGIN_TRANSFER_FAILED", "PLUGIN_TRANSFER_RESULT_INVALID", "REVIEW_PLUGIN_TRANSFER_TARGET":
	default:
		return a.Store.Put(ctx, "import_plan", plan.ID, plan)
	}
	release := []PlanFile{}
	for _, index := range selected {
		file := plan.Files[index]
		if file.Status == "imported" || file.Status == "already_imported" {
			continue
		}
		source, err := snapshotPath(file.Source)
		if err != nil || source != planSnapshot(file) {
			continue
		}
		if _, err := snapshotPath(file.Target); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		safe := true
		for _, side := range meta.Sidecars[file.ID] {
			stamp, err := snapshotPath(side.Target)
			if !errors.Is(err, os.ErrNotExist) {
				// A successful independent extra does not occupy the absent
				// main video's media slot. Its own verified ownership remains.
				var owned managedFile
				if err != nil || file.AssetID == "" || a.Store.Get(ctx, "managed_file", tokenHash(side.Target), &owned) != nil || owned.AssetID != file.AssetID || owned.Path != side.Target || owned.Snapshot != stamp || owned.SourceSnapshot != side.Snapshot || !auxiliaryMaterialKind(owned.Kind) {
					safe = false
					break
				}
			}
		}
		if !safe {
			continue
		}
		var receipt importReceipt
		err = a.Store.Get(ctx, "import_receipt", plan.ID+"/"+file.ID, &receipt)
		if err == nil && receipt.State != "planned" {
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		release = append(release, file)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
		return err
	}
	for _, file := range release {
		for _, episode := range importEpisodes(file.Episodes) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM reservations WHERE media_key=? AND season=? AND episode=? AND intent_id=?`, mediaKey(plan.Media), file.Season, episode, plan.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
