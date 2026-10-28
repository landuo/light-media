package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// A directory asset owns a frozen tree. Metadata adds files to that same tree;
// keep the pre-write members and each staged inode durable until both native
// ownership records have advanced. An interrupted SQL commit can then recover
// only these writes, never adopt a newly discovered file in the directory.
type originalMetadataDirectory struct {
	Owned   managedFile                               `json:"owned"`
	Entries []originalDirectoryEntry                  `json:"entries"`
	Writes  map[string]originalMetadataDirectoryWrite `json:"writes"`
}
type originalMetadataDirectoryWrite struct {
	Snapshot importSnapshot  `json:"snapshot"`
	Previous *importSnapshot `json:"previous,omitempty"`
	Stage    string          `json:"stage"`
	SHA256   string          `json:"sha256"`
}
type originalMetadataDirectoriesKey struct{}

func (a *App) beginOriginalMetadataDirectory(ctx context.Context, asset Asset) (*originalMetadataDirectory, error) {
	var owned managedFile
	if err := a.Store.Get(ctx, "managed_file", tokenHash(asset.Path), &owned); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if owned.Snapshot.Directory == "" {
		return nil, nil
	}
	if owned.AssetID != asset.ID || owned.RootID != asset.RootID || owned.Kind != "video" {
		return nil, bad("REVIEW_METADATA_CONTEXT", "目录资产归属已变化")
	}
	var pending originalMetadataDirectory
	err := a.Store.Get(ctx, "original_metadata_directory", asset.ID, &pending)
	if err == nil {
		if pending.Owned != owned {
			return nil, bad("REVIEW_METADATA_CONTEXT", "目录刮削恢复归属不一致")
		}
		for path, write := range pending.Writes {
			stage := filepath.Join(filepath.Dir(filepath.Join(owned.Path, path)), write.Stage)
			stamp, e := snapshotPath(stage)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil || stamp != write.Snapshot && (write.Previous == nil || stamp != *write.Previous) {
				return nil, bad("REVIEW_METADATA_RECOVERY", "刮削暂存文件发生变化，已保留")
			}
			parent, e := importDir(filepath.Dir(stage), false)
			if e != nil {
				return nil, e
			}
			e = unix.Unlinkat(int(parent.Fd()), filepath.Base(stage), 0)
			parent.Close()
			if e != nil {
				return nil, e
			}
		}
		if err = a.finishOriginalMetadataDirectory(ctx, &pending); err != nil {
			return nil, err
		}
		if err = a.Store.Get(ctx, "managed_file", tokenHash(asset.Path), &owned); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	stamp, entries, err := snapshotOriginalDirectory(asset.Path)
	if err != nil || stamp != owned.Snapshot {
		return nil, bad("REVIEW_METADATA_SOURCE_CHANGED", "目录成员在刮削前发生变化")
	}
	return &originalMetadataDirectory{Owned: owned, Entries: entries, Writes: map[string]originalMetadataDirectoryWrite{}}, nil
}

func (a *App) prepareOriginalMetadataDirectoryWrite(ctx context.Context, target, stage string, stamp importSnapshot, previous *importSnapshot, content []byte) error {
	directories, _ := ctx.Value(originalMetadataDirectoriesKey{}).([]*originalMetadataDirectory)
	for _, directory := range directories {
		if !within(directory.Owned.Path, target) {
			continue
		}
		rel, err := filepath.Rel(directory.Owned.Path, target)
		if err != nil || rel == "." {
			return bad("REVIEW_METADATA_CONTEXT", "旁车不能替换目录资产本身")
		}
		digest := sha256.Sum256(content)
		directory.Writes[rel] = originalMetadataDirectoryWrite{Snapshot: stamp, Previous: previous, Stage: stage, SHA256: hex.EncodeToString(digest[:])}
		if err := a.Store.Put(ctx, "original_metadata_directory", directory.Owned.AssetID, directory); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) finishOriginalMetadataDirectory(ctx context.Context, directory *originalMetadataDirectory) error {
	if len(directory.Writes) == 0 {
		return nil
	}
	actual, entries, err := snapshotOriginalDirectory(directory.Owned.Path)
	if err != nil || !sameInode(actual, directory.Owned.Snapshot) {
		return bad("REVIEW_METADATA_SOURCE_CHANGED", "刮削目录已被替换")
	}
	expected := map[string]originalDirectoryEntry{}
	mutableDirs := map[string]bool{}
	for _, entry := range directory.Entries {
		expected[entry.Path] = entry
	}
	for path := range directory.Writes {
		for parent := filepath.Dir(path); parent != "."; parent = filepath.Dir(parent) {
			mutableDirs[parent] = true
		}
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		prior, present := expected[entry.Path]
		write, written := directory.Writes[entry.Path]
		valid := present && entry == prior
		if entry.Directory && present && prior.Directory && mutableDirs[entry.Path] {
			valid = sameInode(entry.Snapshot, prior.Snapshot)
		}
		if !entry.Directory && written {
			if entry.Snapshot == write.Snapshot {
				valid = true
			} else {
				// Storage modules may copy the upload instead of renaming its
				// staged inode. Only the exact authorized bytes can be adopted.
				file, e := importOpen(filepath.Join(directory.Owned.Path, entry.Path))
				if e != nil {
					return e
				}
				digest := sha256.New()
				n, e := io.Copy(digest, io.LimitReader(file, write.Snapshot.Size+1))
				info, statErr := file.Stat()
				file.Close()
				if e == nil && statErr == nil && entry.Snapshot.matches(info) && n == write.Snapshot.Size && hex.EncodeToString(digest.Sum(nil)) == write.SHA256 {
					valid = true
				}
			}
		}
		if !valid {
			return bad("REVIEW_METADATA_SOURCE_CHANGED", "目录包含未确认的刮削写入或已变化成员")
		}
		seen[entry.Path] = true
	}
	for path := range expected {
		if !seen[path] {
			return bad("REVIEW_METADATA_SOURCE_CHANGED", "刮削期间目录成员丢失")
		}
	}
	// Persist the managed tree and every matching publication receipt together.
	// Older receipts with a different snapshot retain their own authority.
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='managed_file' AND id=?`, tokenHash(directory.Owned.Path)).Scan(&raw); err != nil {
		return err
	}
	var current managedFile
	if json.Unmarshal(raw, &current) != nil || current != directory.Owned {
		return bad("REVIEW_METADATA_CONTEXT", "刮削提交时目录归属发生变化")
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,payload FROM records WHERE kind='import_receipt' AND json_extract(payload,'$.target')=? AND json_extract(payload,'$.state')='published'`, current.Path)
	if err != nil {
		return err
	}
	receipts := map[string]importReceipt{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		var receipt importReceipt
		if err = json.Unmarshal(raw, &receipt); err != nil {
			rows.Close()
			return err
		}
		if receipt.StageSnapshot == current.Snapshot && receipt.SourceSnapshot == current.SourceSnapshot {
			receipt.StageSnapshot = actual
			receipts[key] = receipt
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(receipts) == 0 {
		return bad("REVIEW_METADATA_CONTEXT", "目录刮削缺少对应发布收据")
	}
	current.Snapshot = actual
	if err = txPut(ctx, tx, "managed_file", tokenHash(current.Path), current); err != nil {
		return err
	}
	for key, receipt := range receipts {
		if err = txPut(ctx, tx, "import_receipt", key, receipt); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind='original_metadata_directory' AND id=?`, current.AssetID); err != nil {
		return err
	}
	return tx.Commit()
}
