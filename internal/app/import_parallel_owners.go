package app

import (
	"context"
	"encoding/json"
)

// Replacement also updates the old asset's slots and can adopt its sidecars.
// Admit the complete ownership set, rather than serializing every replacement
// in the library. Existing recovery receipts still use the exclusive lane.
func (run *importAdmission) includeOverwriteOwners(ctx context.Context, meta importMetadata) error {
	candidates := make(map[string]bool)
	for target := range run.targets {
		candidates[target] = true
		if run.scanVersions && !run.extraTargets[target] {
			paths, err := run.app.directoryOverwriteLatestCandidates(ctx, target)
			if err != nil {
				return err
			}
			for _, path := range paths {
				if !within(meta.RootPath, path) || !run.addPath(path) {
					return bad("REVIEW_IMPORT_SCOPE_CHANGED", "无法确认旧版本路径")
				}
				candidates[path] = true
			}
		}
	}
	hashes := make([]string, 0, len(candidates))
	for path := range candidates {
		hashes = append(hashes, tokenHash(path))
	}
	raw, _ := json.Marshal(hashes)
	rows, err := run.app.Store.reader().QueryContext(ctx, `SELECT DISTINCT json_extract(payload,'$.asset_id') FROM records WHERE kind='managed_file' AND id IN (SELECT value FROM json_each(?))`, raw)
	if err != nil {
		return err
	}
	owners := []string{}
	for rows.Next() {
		var owner string
		if err = rows.Scan(&owner); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, owner)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(owners) == 0 {
		return err
	}
	raw, _ = json.Marshal(owners)
	rows, err = run.app.Store.reader().QueryContext(ctx, `SELECT payload FROM records WHERE kind='managed_file' AND json_extract(payload,'$.asset_id') IN (SELECT value FROM json_each(?))`, raw)
	if err != nil {
		return err
	}
	run.owned = make(map[string]managedFile)
	for rows.Next() {
		var data []byte
		var owner managedFile
		if err = rows.Scan(&data); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(data, &owner); err != nil {
			rows.Close()
			return err
		}
		if owner.AssetID == "" || !within(meta.RootPath, owner.Path) || !run.addPath(owner.Path) {
			rows.Close()
			return bad("REVIEW_IMPORT_SCOPE_CHANGED", "无法确认原资产及旁车范围")
		}
		run.owned[owner.Path] = owner
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = run.app.Store.reader().QueryContext(ctx, `SELECT media_key,season,episode FROM asset_slots WHERE asset_id IN (SELECT value FROM json_each(?))`, raw)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var slot importSlot
		if err = rows.Scan(&slot.media, &slot.season, &slot.episode); err != nil {
			return err
		}
		run.slots[slot] = struct{}{}
	}
	return rows.Err()
}
