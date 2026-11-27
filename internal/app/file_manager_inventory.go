package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Only a library entry at the operated pathname moves. A second hardlink to
// the same inode (most importantly the downloader's source) is independent.
type browserRemovedAsset struct {
	ID, Path, State, RemovedDigest string
	Slots                          []browserInventorySlot
}

type browserInventorySlot struct {
	MediaKey        string
	Season, Episode int
}

type browserInventoryUndo struct {
	Assets []browserRemovedAsset
	Files  []managedFile
}

// Embedded only in the database receipt, never in a list or confirmation response.
type browserInventoryReceipt struct {
	Inventory map[int]browserInventoryUndo `json:"inventory_undo,omitempty"`
}

func browserInventoryConflict() error {
	return bad("FILE_INVENTORY_CHANGED", "媒体文件的库存归属已变化，请核对文件和媒体库")
}

// The durable done-bit is the commit marker for both the filesystem effect and
// its index update. Physical effects that survived a crash reach this same
// method after the caller verifies its frozen manifest at the new location.
func (a *App) putFileManagerOperation(ctx context.Context, plan browserOperation, restore bool) error {
	return a.fileManagerInventoryTransaction(ctx, plan, restore, false)
}

func (a *App) checkFileManagerInventory(ctx context.Context, plan browserOperation, restore bool) error {
	return a.fileManagerInventoryTransaction(ctx, plan, restore, true)
}

func (a *App) fileManagerInventoryTransaction(ctx context.Context, plan browserOperation, restore, preflight bool) error {
	if plan.Action != "delete" && plan.Action != "rename" {
		if preflight {
			return nil
		}
		return a.Store.Put(ctx, "file_operation", plan.ID, plan)
	}
	roots := a.Settings().Roots
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous struct {
		browserInventoryReceipt
		Items []struct{ Done bool }
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, "SELECT payload FROM file_operation WHERE id=?", plan.ID).Scan(&raw); err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &previous); err != nil {
		return err
	}
	if len(previous.Items) != len(plan.Items) {
		return browserInventoryConflict()
	}
	identity := cachedMountedDirectoryIdentity()
	for i, item := range plan.Items {
		desired := item.Done
		if preflight {
			desired = !restore
		}
		if desired == previous.Items[i].Done {
			continue
		}
		if restore && !desired {
			err = restoreBrowserInventory(ctx, tx, previous.Inventory[i])
			delete(previous.Inventory, i)
		} else if !restore && desired {
			undo := browserInventoryUndo{}
			for _, parent := range browserInventoryParents(item.Source, roots, identity) {
				from, to := filepath.Join(parent, filepath.Base(item.Source)), filepath.Join(parent, filepath.Base(item.Target))
				if plan.Action == "rename" {
					// The preview established that the destination did not exist. A
					// prior DB claim at this absent path is stale; a newer one may be live.
					if err = retireBrowserDestination(ctx, tx, to, plan.CreatedAt); err != nil {
						return err
					}
				}
				if err = updateBrowserInventory(ctx, tx, plan.Action, item, from, to, &undo); err != nil {
					return err
				}
			}
			if !preflight && !item.Unrecoverable && (len(undo.Assets) != 0 || len(undo.Files) != 0) {
				if previous.Inventory == nil {
					previous.Inventory = make(map[int]browserInventoryUndo)
				}
				previous.Inventory[i] = undo
			}
		} else {
			return browserInventoryConflict()
		}
		if err != nil {
			return err
		}
	}
	if preflight {
		return nil
	}
	receipt := struct {
		browserOperation
		browserInventoryReceipt
	}{plan, previous.browserInventoryReceipt}
	if err = txPut(ctx, tx, "file_operation", plan.ID, receipt); err != nil {
		return err
	}
	return tx.Commit()
}

func browserInventoryParents(source string, roots []Root, identity func(string) (mountedDirectoryID, error)) []string {
	parent := filepath.Dir(source)
	result := []string{parent}
	for _, root := range roots {
		if !importPath(root.Path) || root.Path == "/" {
			continue
		}
		if alias, ok := sharedDirectoryPathWithIdentity(root.Path, parent, identity); ok && !slices.Contains(result, alias) {
			result = append(result, alias)
		}
	}
	return result
}

// Retire rows for a target known to be absent at preview. This includes rows
// left by old manual moves, without transferring that older media's identity
// to the new file. A registration made since preview requires a fresh check.
func retireBrowserDestination(ctx context.Context, tx *sql.Tx, target, previewAt string) error {
	for _, table := range []string{"library_asset", "managed_file"} {
		where := "(path=? OR (path>=? AND path<?))"
		if table == "library_asset" {
			where += " AND state NOT IN ('removed','superseded','missing_confirmed')"
		}
		rows, err := tx.QueryContext(ctx, "SELECT id,updated_at FROM "+table+" WHERE "+where, target, target+"/", target+"0")
		if err != nil {
			return err
		}
		var ids []string
		preview, _ := time.Parse(time.RFC3339Nano, previewAt)
		for rows.Next() {
			var id, at string
			if err = rows.Scan(&id, &at); err != nil {
				rows.Close()
				return err
			}
			updated, e := time.Parse(time.RFC3339Nano, at)
			if e != nil || updated.After(preview) {
				rows.Close()
				return browserInventoryConflict()
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if table == "library_asset" {
				if _, err = tx.ExecContext(ctx, "UPDATE library_asset SET payload=json_set(payload,'$.state','removed'),updated_at=? WHERE id=?", now(), id); err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, "DELETE FROM asset_slots WHERE asset_id=?", id)
			} else {
				_, err = tx.ExecContext(ctx, "DELETE FROM managed_file WHERE id=?", id)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Query the indexed subtree once, independently of the number of files in its
// filesystem manifest. A range uses literal paths even if names contain % or _.
func browserInventorySubtree[T any](ctx context.Context, tx *sql.Tx, table, source string) ([]T, error) {
	rows, err := tx.QueryContext(ctx, "SELECT payload FROM "+table+" WHERE path=? OR (path>=? AND path<?)", source, source+"/", source+"0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []T
	for rows.Next() {
		var raw []byte
		var entry T
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func browserInventoryPathFree(ctx context.Context, tx *sql.Tx, path, assetID string) error {
	var occupied bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM library_asset WHERE path=? AND id!=? AND state NOT IN ('removed','superseded','missing_confirmed'))", path, assetID).Scan(&occupied)
	if err == nil && occupied {
		return browserInventoryConflict()
	}
	return err
}

func updateBrowserInventory(ctx context.Context, tx *sql.Tx, action string, item browserOperationItem, from, to string, undo *browserInventoryUndo) error {
	assets, err := browserInventorySubtree[Asset](ctx, tx, "library_asset", from)
	if err != nil {
		return err
	}
	files, err := browserInventorySubtree[managedFile](ctx, tx, "managed_file", from)
	if err != nil || len(files)+len(assets) == 0 {
		return err
	}
	moved := make(map[string]string, len(item.Manifest))
	stamps := make(map[string]browserStamp, len(item.Manifest))
	proofs := make(map[string]managedFile, len(files))
	for _, file := range files {
		if file.Kind == "video" {
			proofs[file.Path] = file
		}
	}
	for _, entry := range item.Manifest {
		rel, err := filepath.Rel(item.Source, entry.Path)
		if err != nil || !within(item.Source, entry.Path) {
			return browserInventoryConflict()
		}
		p := filepath.Join(from, rel)
		moved[p] = filepath.Join(to, rel)
		stamps[p] = entry
	}
	for _, asset := range assets {
		target, ok := moved[asset.Path]
		if !ok || asset.State == "removed" || asset.State == "superseded" || asset.State == "missing_confirmed" {
			continue
		}
		stamp := stamps[asset.Path]
		owner := proofs[asset.Path]
		proof := owner.Snapshot
		if proof.Inode == 0 && asset.IdentitySource == "manual" {
			var raw []byte
			e := tx.QueryRowContext(ctx, "SELECT payload FROM library_asset_proof WHERE id=? AND path=?", asset.ID, asset.Path).Scan(&raw)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e == nil {
				var identity libraryIdentity
				if err = json.Unmarshal(raw, &identity); err != nil {
					return err
				}
				proof = identity.Snapshot
			}
		}
		sameOwner := owner.AssetID == "" || owner.AssetID == asset.ID
		sameSize := proof.Inode != 0 || asset.Size == 0 || os.FileMode(stamp.Mode).IsDir() || asset.Size == stamp.Size
		matches := sameOwner && sameSize && browserInventoryMatches(stamp, proof)
		if action == "delete" || !matches {
			removed := browserRemovedAsset{ID: asset.ID, Path: asset.Path, State: asset.State}
			if removed.Slots, err = takeBrowserAssetSlots(ctx, tx, asset.ID); err != nil {
				return err
			}
			asset.State = "removed"
			raw, err := json.Marshal(asset)
			if err != nil {
				return err
			}
			removed.RemovedDigest = tokenHash(string(raw))
			if matches {
				undo.Assets = append(undo.Assets, removed)
			}
		} else {
			if err = browserInventoryPathFree(ctx, tx, target, asset.ID); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE library_asset_proof SET payload=json_set(payload,'$.path',?), updated_at=? WHERE id=? AND path=?", target, now(), asset.ID, asset.Path); err != nil {
				return err
			}
			asset.Path = target
		}
		if err = txPut(ctx, tx, "asset", asset.ID, asset); err != nil {
			return err
		}
	}
	for _, file := range files {
		target, ok := moved[file.Path]
		if !ok {
			continue
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM managed_file WHERE id=? AND path=?", tokenHash(file.Path), file.Path); err != nil {
			return err
		}
		if !browserInventoryMatches(stamps[file.Path], file.Snapshot) {
			continue
		}
		if action == "delete" {
			undo.Files = append(undo.Files, file)
		} else {
			file.Path = target
			if err = insertBrowserManagedFile(ctx, tx, file); err != nil {
				return err
			}
		}
	}

	return nil
}

func browserInventoryMatches(stamp browserStamp, proof importSnapshot) bool {
	if proof.Inode == 0 {
		return true
	}
	if stamp.Device != proof.Device || stamp.Inode != proof.Inode {
		return false
	}
	return os.FileMode(stamp.Mode).IsDir() || stamp.Size == proof.Size && stamp.ModNS == proof.ModNS
}

func insertBrowserManagedFile(ctx context.Context, tx *sql.Tx, file managedFile) error {
	var exists bool
	key := tokenHash(file.Path)
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM managed_file WHERE id=?)", key).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return browserInventoryConflict()
	}
	return txPut(ctx, tx, "managed_file", key, file)
}

func takeBrowserAssetSlots(ctx context.Context, tx *sql.Tx, id string) ([]browserInventorySlot, error) {
	rows, err := tx.QueryContext(ctx, "SELECT media_key,season,episode FROM asset_slots WHERE asset_id=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var slots []browserInventorySlot
	for rows.Next() {
		var slot browserInventorySlot
		if err = rows.Scan(&slot.MediaKey, &slot.Season, &slot.Episode); err != nil {
			return nil, err
		}
		slots = append(slots, slot)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM asset_slots WHERE asset_id=?", id)
	return slots, err
}

func restoreBrowserInventory(ctx context.Context, tx *sql.Tx, undo browserInventoryUndo) error {
	var raw []byte
	var err error
	for _, removed := range undo.Assets {
		if err = tx.QueryRowContext(ctx, "SELECT payload FROM library_asset WHERE id=?", removed.ID).Scan(&raw); err != nil {
			return err
		}
		if tokenHash(string(raw)) != removed.RemovedDigest {
			return browserInventoryConflict()
		}
		if err = browserInventoryPathFree(ctx, tx, removed.Path, removed.ID); err != nil {
			return err
		}
		var asset Asset
		if err = json.Unmarshal(raw, &asset); err != nil {
			return err
		}
		asset.State = removed.State
		if err = txPut(ctx, tx, "asset", asset.ID, asset); err != nil {
			return err
		}
		for _, slot := range removed.Slots {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO asset_slots(media_key,season,episode,asset_id) VALUES(?,?,?,?)", slot.MediaKey, slot.Season, slot.Episode, asset.ID); err != nil {
				return err
			}
		}
	}
	for _, file := range undo.Files {
		if err = insertBrowserManagedFile(ctx, tx, file); err != nil {
			return err
		}
	}
	return nil
}
