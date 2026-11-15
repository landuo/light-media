package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
)

// MoviePilot has no per-leaf ownership index: transferring into an existing
// destination directory publishes the planned members and keeps unrelated
// entries already present. Admit the same append when the root is the exact
// directory this asset already owns, and reuse that asset instead of
// registering a second asset for one path. Members already owned by another
// asset are still reported as conflicts rather than silently re-assigned.
func (a *App) admitOwnedDirectoryAppend(ctx context.Context, receipt *importReceipt, file PlanFile, owner managedFile) error {
	if owner.Kind != "video" || owner.AssetID == "" || owner.Path != file.Target || owner.Snapshot.Directory == "" {
		return bad("DIRECTORY_MERGE_OWNED_TARGET", "已有目标被非目录资产占用，请先核对归属")
	}
	actual, err := snapshotTransferPath(file.Target)
	if err != nil || sharedDirectoryIdentity(actual) != sharedDirectoryIdentity(owner.Snapshot) {
		return bad("REVIEW_DIRECTORY_MERGE", "已有目录在资产登记后已被替换")
	}
	var asset Asset
	if err := a.Store.Get(ctx, "asset", owner.AssetID, &asset); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return bad("DIRECTORY_MERGE_OWNED_TARGET", "已有目录的资产记录缺失，请先核对归属")
		}
		return err
	}
	if asset.Path != file.Target || asset.RootID != owner.RootID || (asset.State != "present" && asset.State != "external") {
		return bad("DIRECTORY_MERGE_OWNED_TARGET", "已有目录资产与当前目标不一致")
	}
	if receipt.MediaKey != "" && mediaKey(asset.Media) != receipt.MediaKey {
		return bad("IMPORT_CONFLICT", "同一目录不能追加不同影视身份的成员")
	}
	if asset.Season != file.Season {
		return bad("IMPORT_CONFLICT", "同一目录不能追加不同季的成员")
	}
	receipt.AppendAssetID = asset.ID
	return nil
}

// The directory asset keeps the inventory of every episode it owns. Appending
// another source directory into the same root extends that inventory instead of
// replacing the earlier plan's episodes.
func mergeAppendInts(prior, current []int) []int {
	if len(prior) == 0 {
		return append([]int(nil), current...)
	}
	out := append([]int(nil), prior...)
	for _, value := range current {
		found := false
		for _, existing := range out {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			out = append(out, value)
		}
	}
	sort.Ints(out)
	return out
}

func mergeAppendScopes(prior, current []canonicalEpisodeScope) []canonicalEpisodeScope {
	out := cloneEpisodeScopes(prior)
	for _, scope := range current {
		merged := false
		for i := range out {
			if out[i].Season != scope.Season {
				continue
			}
			out[i].Episodes = mergeAppendInts(out[i].Episodes, scope.Episodes)
			merged = true
			break
		}
		if !merged {
			out = append(out, canonicalEpisodeScope{Season: scope.Season, Episodes: append([]int(nil), scope.Episodes...)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Season < out[j].Season })
	return out
}

// supersedeReplacedDirectoryMembersTx applies the ownership part of MoviePilot's
// overwrite: a directory transfer that replaced another asset's file takes over
// that exact leaf, the old record is removed compare-and-delete, and its asset
// is superseded with its slots released. Other files of the old asset stay as
// they are; no unrelated target is touched.
func (a *App) supersedeReplacedDirectoryMembersTx(ctx context.Context, tx *sql.Tx, receipt importReceipt) error {
	if receipt.DirectoryMerge == nil || receipt.SharedDirectory == nil {
		return nil
	}
	for relative, child := range receipt.DirectoryMerge.Children {
		if child.Replaced == nil || !child.Published || child.Replaced.AssetID == receipt.AppendAssetID {
			continue
		}
		member, ok := receipt.SharedDirectory.Members[relative]
		if !ok {
			continue
		}
		target := filepath.Join(receipt.Target, relative)
		actual, err := snapshotOwnedTransferPath(target, member.Snapshot, member.Softlink, true, nil)
		if err != nil || actual != member.Snapshot {
			return bad("REVIEW_DIRECTORY_MERGE", "已替换成员与本次发布不一致")
		}
		old := *child.Replaced
		var raw string
		err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='managed_file' AND id=?`, tokenHash(old.Path)).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		var current managedFile
		if json.Unmarshal([]byte(raw), &current) != nil || !reflect.DeepEqual(current, old) {
			return bad("REVIEW_DIRECTORY_MERGE", "被替换成员的登记已变化，请重新预览")
		}
		// The payload compare above is the guard; SQLite may store the payload as
		// BLOB while a string binding would compare unequal, so delete by id.
		if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind='managed_file' AND id=?`, tokenHash(old.Path)); err != nil {
			return err
		}
		var prior Asset
		if err = a.Store.Get(ctx, "asset", old.AssetID, &prior); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if prior.State != "present" && prior.State != "external" {
			continue
		}
		prior.State, prior.Error = "superseded", "已由目录整理覆盖同名目标，旧资产不再作为独立版本"
		if err = txPut(ctx, tx, "asset", prior.ID, prior); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM asset_slots WHERE asset_id=?`, prior.ID); err != nil {
			return err
		}
	}
	return nil
}
