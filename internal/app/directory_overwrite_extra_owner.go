package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// The managed auxiliary is proven by its own publication and frozen plan. A
// video asset is optional: a main task may have failed or never been selected.
func verifyIndependentExtraOwnerTx(ctx context.Context, tx *sql.Tx, owner managedFile, rootPath string) error {
	invalid := func() error { return bad("REVIEW_OVERWRITE_OWNER", "附加文件缺少可核对的原发布归属") }
	if !auxiliaryMaterialKind(owner.Kind) || owner.AssetID == "" || !within(rootPath, owner.Path) {
		return invalid()
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM records WHERE kind='import_receipt' AND json_extract(payload,'$.target')=? AND json_extract(payload,'$.state')='published'`, owner.Path)
	if err != nil {
		return err
	}
	var receipts []importReceipt
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var receipt importReceipt
		if json.Unmarshal(raw, &receipt) == nil && receipt.StageSnapshot == owner.Snapshot && receipt.SourceSnapshot == owner.SourceSnapshot {
			receipts = append(receipts, receipt)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, receipt := range receipts {
		parts := strings.Split(receipt.ID, "/")
		if len(parts) < 2 || len(parts) > 3 {
			continue
		}
		var raw []byte
		var plan ImportPlan
		var meta importMetadata
		if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='import_plan' AND id=?`, parts[0]).Scan(&raw); err != nil {
			return err
		}
		if json.Unmarshal(raw, &plan) != nil || plan.ID != parts[0] || plan.RootID != owner.RootID || mediaKey(plan.Media) != owner.MediaKey {
			return invalid()
		}
		if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='import_meta' AND id=?`, plan.ID).Scan(&raw); err != nil {
			return err
		}
		if json.Unmarshal(raw, &meta) != nil || meta.RootPath != rootPath {
			return invalid()
		}
		for _, file := range plan.Files {
			if file.ID != parts[1] || file.AssetID != owner.AssetID {
				continue
			}
			candidates := []struct {
				file      PlanFile
				kind, key string
			}{{file, file.MaterialKind, plan.ID + "/" + file.ID}}
			for index, side := range meta.Sidecars[file.ID] {
				candidates = append(candidates, struct {
					file      PlanFile
					kind, key string
				}{originalTransferSideFile(file, side, index), sidecarMaterialKind(side), fmt.Sprintf("%s/%s/side-%d", plan.ID, file.ID, index)})
			}
			for _, candidate := range candidates {
				if candidate.key == receipt.ID && candidate.kind == owner.Kind && candidate.file.Source == receipt.Source && candidate.file.Target == owner.Path && planSnapshot(candidate.file) == owner.SourceSnapshot {
					return nil
				}
			}
		}
	}
	return invalid()
}
