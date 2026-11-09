package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type directoryOverwriteCipher struct {
	Sealed []byte `json:"sealed"`
}

func (a *App) saveDirectoryOverwrite(ctx context.Context, op directoryOverwriteOperation) error {
	raw, e := json.Marshal(op)
	if e != nil {
		return e
	}
	if len(raw) > 1<<20 {
		return bad("REVIEW_OVERWRITE_LIMIT", "覆盖收据超过原生持久边界")
	}
	sealed, e := a.pluginStore.seal("directory_overwrite", "system", op.ID, raw)
	if e != nil {
		return e
	}
	return a.Store.Put(ctx, "directory_overwrite", op.ID, directoryOverwriteCipher{sealed})
}
func (a *App) readDirectoryOverwriteOperation(ctx context.Context, id string) (directoryOverwriteOperation, error) {
	var cipher directoryOverwriteCipher
	var op directoryOverwriteOperation
	if e := a.Store.Get(ctx, "directory_overwrite", id, &cipher); e != nil {
		return op, e
	}
	raw, e := a.pluginStore.unseal("directory_overwrite", "system", id, cipher.Sealed)
	if e != nil {
		return op, e
	}
	if json.Unmarshal(raw, &op) != nil || op.Version != 1 || op.ID != id {
		return op, bad("REVIEW_OVERWRITE_RECEIPT", "覆盖收据身份无效")
	}
	return op, nil
}
func (a *App) knownDirectoryOverwritePublication(ctx context.Context, op *directoryOverwriteOperation) (bool, error) {
	var r importReceipt
	e := a.Store.Get(ctx, "import_receipt", op.ID, &r)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if r.Source != op.Source || r.Target != op.Target || r.SourceSnapshot != op.SourceSnapshot || r.ID != op.ID {
		return false, bad("REVIEW_OVERWRITE_RECEIPT", "覆盖发布收据与原输入不一致")
	}
	if r.State != "staged" && r.State != "published" {
		return false, nil
	}
	if r.Stage != ".light-media-"+tokenHash(op.ID)[:32]+".part" || r.StageSnapshot.Size != op.SourceSnapshot.Size || op.Method == "hardlink" && r.StageSnapshot != op.SourceSnapshot {
		return false, bad("REVIEW_OVERWRITE_RECEIPT", "发布文件身份与实际整理方式不符")
	}
	actual, e := snapshotPath(op.Target)
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if actual != r.StageSnapshot {
		return false, bad("REVIEW_OVERWRITE_TARGET", "正式目标已由其他文件替换，未继续覆盖")
	}
	// Already published facts are readable after cancellation or policy changes.
	// This does not authorize another filesystem mutation or cleanup.
	op.Published = r
	op.State = "published"
	return true, nil
}
func (a *App) publishDirectoryOverwrite(ctx context.Context, key, source, target, rootID, method string, locked importSnapshot, policy importOverwritePolicy, check func() error) (importReceipt, error) {
	op, e := a.readDirectoryOverwriteOperation(ctx, key)
	if errors.Is(e, sql.ErrNoRows) {
		if e = check(); e != nil {
			return importReceipt{}, e
		}
		op, e = a.readDirectoryOverwrite(ctx, key, source, target, rootID, method, locked, policy)
		if e != nil {
			return importReceipt{}, e
		}
		if e = guardParallelOverwrite(ctx, op); e != nil {
			return importReceipt{}, e
		}
		if e = a.saveDirectoryOverwrite(ctx, op); e != nil {
			return importReceipt{}, e
		}
	} else if e != nil {
		return importReceipt{}, e
	}
	if op.Source != source || op.Target != target || op.RootID != rootID || op.Method != method || op.SourceSnapshot != locked || !sameOverwritePolicy(op.Policy, policy) {
		return importReceipt{}, bad("REVIEW_OVERWRITE_RECEIPT", "覆盖重试与封存输入不一致")
	}
	if e = guardParallelOverwrite(ctx, op); e != nil {
		return importReceipt{}, e
	}
	if known, e := a.knownDirectoryOverwritePublication(ctx, &op); e != nil {
		return importReceipt{}, e
	} else if known {
		return op.Published, nil
	}
	if op.State == "registered" || op.State == "complete" {
		return importReceipt{}, bad("REVIEW_PUBLISHED_MISSING", "已登记的覆盖目标消失，不能重新发布")
	}
	if e = a.requireNativeDirectoryPublication(ctx, key, source, target, rootID, method, locked, policy); e != nil {
		return importReceipt{}, e
	}
	if e = check(); e != nil {
		return importReceipt{}, e
	}
	if e = a.guardDirectoryOverwriteExtensionPolicy(ctx, op); e != nil {
		return importReceipt{}, e
	}
	root, e := a.importRoot(op.RootID)
	if e != nil || root.Path != op.RootPath {
		return importReceipt{}, bad("REVIEW_ROOT_CHANGED", "覆盖目录已经变化")
	}
	if _, e = a.overwriteDispatcher(ctx); e != nil {
		return importReceipt{}, e
	}
	if active, _ := ctx.Value(originalTransferWriteContextKey{}).(*originalTransferWriteContext); active != nil && op.Decision.DeleteVersions {
		// The pinned handler removes other versions before TransferIntercept.
		// A veto therefore still observes those removals. Keep recoverable host
		// backups, while preserving the source's visible ordering and side effect.
		removed := []directoryOverwriteTarget{}
		for _, old := range op.Targets {
			if old.Path == target {
				removed = append(removed, old)
				continue
			}
			if e = check(); e != nil {
				return importReceipt{}, e
			}
			deleted, err := a.originalStorageQuarantineVersion(ctx, old)
			if err != nil {
				return importReceipt{}, err
			}
			if deleted {
				removed = append(removed, old)
			}
		}
		op.Targets = removed
		if e = a.saveDirectoryOverwrite(ctx, op); e != nil {
			return importReceipt{}, e
		}
	}
	if e = a.originalTransferInterceptWrite(ctx, source, target, method, op.Decision.Overwrite); e != nil {
		return importReceipt{}, e
	}
	if e = a.originalCheckTransferTargetAfterIntercept(ctx, target, op.Decision.Overwrite); e != nil {
		return importReceipt{}, e
	}
	restore := func() {
		for i := len(op.Targets) - 1; i >= 0; i-- {
			_ = restoreDirectoryOverwrite(op.Targets[i])
		}
	}
	for _, old := range op.Targets {
		if e = check(); e != nil {
			restore()
			return importReceipt{}, e
		}
		if e = a.guardDirectoryOverwriteExtensionPolicy(ctx, op); e != nil {
			restore()
			return importReceipt{}, e
		}
		if e = quarantineDirectoryOverwrite(old); e != nil {
			restore()
			return importReceipt{}, e
		}
	}
	op.State = "quarantined"
	if e = a.saveDirectoryOverwrite(ctx, op); e != nil {
		restore()
		return importReceipt{}, e
	}
	receipt, e := a.publishImport(ctx, key, source, target, locked, method, check)
	if e != nil {
		// A failing post-publication acknowledgement is not a failed transfer. Read
		// its exact local receipt and target before considering a restoration.
		finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if known, ke := a.knownDirectoryOverwritePublication(finish, &op); ke == nil && known {
			_ = a.saveDirectoryOverwrite(finish, op)
			return op.Published, nil
		}
		for i := len(op.Targets) - 1; i >= 0; i-- {
			_ = restoreDirectoryOverwrite(op.Targets[i])
		}
		return receipt, e
	}
	op.State = "published"
	op.Published = receipt
	if e = a.saveDirectoryOverwrite(ctx, op); e != nil {
		return receipt, e
	}
	return receipt, nil
}
func sameOverwritePolicy(a, b importOverwritePolicy) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func quarantineDirectoryOverwrite(old directoryOverwriteTarget) error {
	parent, e := importDir(filepath.Dir(old.Path), false)
	if e != nil {
		return e
	}
	defer parent.Close()
	backup := filepath.Join(filepath.Dir(old.Path), old.Backup)
	if stamp, e := snapshotPath(backup); e == nil {
		if stamp != old.Snapshot {
			return bad("REVIEW_OVERWRITE_BACKUP", "覆盖暂存文件身份已变化")
		}
		if _, e = os.Lstat(old.Path); !errors.Is(e, os.ErrNotExist) {
			return bad("REVIEW_OVERWRITE_TARGET", "原路径出现另一文件，未继续覆盖")
		}
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if stamp, e := snapshotPath(old.Path); e != nil || stamp != old.Snapshot {
		return bad("REVIEW_OVERWRITE_TARGET", "覆盖前目标身份已变化")
	}
	if e = importRenameExclusive(int(parent.Fd()), filepath.Base(old.Path), old.Backup); e != nil {
		return e
	}
	stamp, e := snapshotPath(backup)
	if e != nil || stamp != old.Snapshot {
		_ = importRenameExclusive(int(parent.Fd()), old.Backup, filepath.Base(old.Path))
		_ = parent.Sync()
		return bad("REVIEW_OVERWRITE_TARGET", "覆盖时目标被外部替换，已保留文件供核对")
	}
	return parent.Sync()
}
func restoreDirectoryOverwrite(old directoryOverwriteTarget) error {
	parent, e := importDir(filepath.Dir(old.Path), false)
	if e != nil {
		return e
	}
	defer parent.Close()
	backup := filepath.Join(filepath.Dir(old.Path), old.Backup)
	stamp, e := snapshotPath(backup)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || stamp != old.Snapshot {
		return bad("REVIEW_OVERWRITE_BACKUP", "恢复覆盖前文件时身份不符")
	}
	if e = importRenameExclusive(int(parent.Fd()), old.Backup, filepath.Base(old.Path)); e != nil {
		return e
	}
	return parent.Sync()
}

// Called in the same transaction as the new asset/slots. A source cancellation
// must not erase an already observed publication; the caller owns final scope.
func (a *App) commitDirectoryOverwriteTx(ctx context.Context, tx *sql.Tx, id string, replacement Asset) error {
	return a.commitDirectoryOverwriteFileTx(ctx, tx, id, replacement, nil)
}
func (a *App) commitDirectoryOverwriteExtraTx(ctx context.Context, tx *sql.Tx, id string, replacement Asset, material managedFile) error {
	return a.commitDirectoryOverwriteFileTx(ctx, tx, id, replacement, &material)
}
func (a *App) commitDirectoryOverwriteFileTx(ctx context.Context, tx *sql.Tx, id string, replacement Asset, material *managedFile) error {
	var cipher directoryOverwriteCipher
	var raw []byte
	e := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='directory_overwrite' AND id=?`, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if json.Unmarshal(raw, &cipher) != nil {
		return bad("REVIEW_OVERWRITE_RECEIPT", "覆盖密文不可读")
	}
	raw, e = a.pluginStore.unseal("directory_overwrite", "system", id, cipher.Sealed)
	if e != nil {
		return e
	}
	var op directoryOverwriteOperation
	if json.Unmarshal(raw, &op) != nil || op.ID != id || op.Version != 1 {
		return bad("REVIEW_OVERWRITE_RECEIPT", "覆盖记录无效")
	}
	var r importReceipt
	if e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='import_receipt' AND id=?`, id).Scan(&raw); e != nil {
		return e
	}
	if json.Unmarshal(raw, &r) != nil || r.Target != op.Target || r.Source != op.Source || r.SourceSnapshot != op.SourceSnapshot || (r.State != "published" && r.State != "staged") {
		return bad("REVIEW_OVERWRITE_RECEIPT", "没有实际发布结果，不能转移整理归属")
	}
	if r.Stage != ".light-media-"+tokenHash(op.ID)[:32]+".part" || r.StageSnapshot.Size != op.SourceSnapshot.Size || op.Method == "hardlink" && r.StageSnapshot != op.SourceSnapshot {
		return bad("REVIEW_OVERWRITE_RECEIPT", "实际整理发布身份不符")
	}
	if actual, e := snapshotPath(op.Target); e != nil || actual != r.StageSnapshot {
		return bad("REVIEW_OVERWRITE_TARGET", "发布结果已被外部改动")
	}
	expectedTarget := replacement.Path
	if material != nil {
		if material.AssetID != replacement.ID || material.RootID != replacement.RootID || material.MediaKey != mediaKey(replacement.Media) || material.SourceSnapshot != op.SourceSnapshot || material.Snapshot != r.StageSnapshot || !auxiliaryMaterialKind(material.Kind) {
			return bad("REVIEW_OVERWRITE_RECEIPT", "附加文件与真实发布结果不一致")
		}
		expectedTarget = material.Path
	}
	if expectedTarget != op.Target || replacement.RootID != op.RootID {
		return bad("REVIEW_OVERWRITE_RECEIPT", "资产目标与实际覆盖发布结果不一致")
	}
	if r.State == "staged" {
		r.State = "published"
		if e = txPut(ctx, tx, "import_receipt", id, r); e != nil {
			return e
		}
	}
	for _, old := range op.Targets {
		if old.Owner == nil {
			continue
		}
		owner := old.Owner
		if e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='managed_file' AND id=?`, tokenHash(old.Path)).Scan(&raw); e != nil {
			return e
		}
		var current managedFile
		if json.Unmarshal(raw, &current) != nil || current != *owner {
			return bad("REVIEW_OVERWRITE_OWNER", "原目标登记已被其他操作修改")
		}
		var asset Asset
		e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='asset' AND id=?`, owner.AssetID).Scan(&raw)
		if material != nil && errors.Is(e, sql.ErrNoRows) {
			// Independent extras and successful extras of a rejected main have
			// real publication ownership without a video inventory row.
			if e = verifyIndependentExtraOwnerTx(ctx, tx, *owner, op.RootPath); e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else if json.Unmarshal(raw, &asset) != nil || asset.RootID != op.RootID || material == nil && asset.Path != old.Path {
			return bad("REVIEW_OVERWRITE_OWNER", "原整理记录不能匹配覆盖文件")
		}
		if material == nil {
			asset.State = "superseded"
			asset.Error = "已按目录覆盖策略替换视频文件"
			if e = txPut(ctx, tx, "asset", asset.ID, asset); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `DELETE FROM asset_slots WHERE asset_id=?`, asset.ID); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM records WHERE kind='managed_file' AND id=?`, tokenHash(old.Path)); e != nil {
			return e
		}
		// Same-path replacement also transfers verified, already owned metadata
		// and subtitles. Latest's differently named old sidecars remain untouched.
		if material == nil && old.Path == replacement.Path {
			rows, err := tx.QueryContext(ctx, `SELECT id,payload FROM records WHERE kind='managed_file' AND json_extract(payload,'$.asset_id')=?`, asset.ID)
			if err != nil {
				return err
			}
			type row struct {
				id  string
				raw []byte
			}
			entries := []row{}
			for rows.Next() {
				var v row
				if err = rows.Scan(&v.id, &v.raw); err != nil {
					rows.Close()
					return err
				}
				entries = append(entries, v)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, v := range entries {
				var own managedFile
				if json.Unmarshal(v.raw, &own) != nil {
					return bad("REVIEW_OVERWRITE_OWNER", "原旁车记录损坏")
				}
				if own.Kind == "video" {
					continue
				}
				if run := parallelImport(ctx); run != nil && run.owned[own.Path] != own {
					return bad("REVIEW_IMPORT_SCOPE_CHANGED", "原资产的旁车归属已超出当前整理范围")
				}
				actual, err := snapshotPath(own.Path)
				if err != nil || actual != own.Snapshot {
					return bad("REVIEW_OVERWRITE_OWNER", "原旁车已变化，未转移其归属")
				}
				own.AssetID = replacement.ID
				own.MediaKey = mediaKey(replacement.Media)
				if err = txPut(ctx, tx, "managed_file", v.id, own); err != nil {
					return err
				}
			}
		}
	}
	// An inventory slot represents presence, not permission to delete all
	// versions. When differently named versions coexist, retain those records and
	// files and point only these newly materialized scopes at the new asset.
	if material == nil {
		for _, ep := range importEpisodes(replacement.Episodes) {
			if _, e = tx.ExecContext(ctx, `DELETE FROM asset_slots WHERE media_key=? AND season=? AND episode=? AND asset_id!=?`, mediaKey(replacement.Media), replacement.Season, ep, replacement.ID); e != nil {
				return e
			}
		}
	}
	op.Published = r
	op.State = "registered"
	raw, e = json.Marshal(op)
	if e != nil {
		return e
	}
	sealed, e := a.pluginStore.seal("directory_overwrite", "system", id, raw)
	if e != nil {
		return e
	}
	return txPut(ctx, tx, "directory_overwrite", id, directoryOverwriteCipher{sealed})
}
func (a *App) finishDirectoryOverwrite(ctx context.Context, id string) error {
	op, e := a.readDirectoryOverwriteOperation(ctx, id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if op.State == "complete" {
		return nil
	}
	if e = guardParallelOverwrite(ctx, op); e != nil {
		return e
	}
	if op.State != "registered" {
		return bad("REVIEW_OVERWRITE_RECEIPT", "覆盖资产尚未在事务中登记")
	}
	if e = a.guardDirectoryOverwriteExtensionPolicy(ctx, op); e != nil {
		return e
	}
	root, e := a.importRoot(op.RootID)
	if e != nil || root.Path != op.RootPath {
		return bad("REVIEW_ROOT_CHANGED", "覆盖清理的目录已变化")
	}
	if actual, e := snapshotPath(op.Target); e != nil || actual != op.Published.StageSnapshot {
		return bad("REVIEW_OVERWRITE_TARGET", "新文件身份已变化，旧版本暂存文件保留")
	}
	for _, old := range op.Targets {
		parent, e := importDir(filepath.Dir(old.Path), false)
		if e != nil {
			return e
		}
		backup := filepath.Join(filepath.Dir(old.Path), old.Backup)
		stamp, e := snapshotPath(backup)
		if e == nil && stamp == old.Snapshot {
			e = unix.Unlinkat(int(parent.Fd()), old.Backup, 0)
			if e == nil {
				e = parent.Sync()
			}
		} else if errors.Is(e, os.ErrNotExist) {
			e = nil
		} else if e == nil {
			e = bad("REVIEW_OVERWRITE_BACKUP", "暂存旧文件被外部修改，未删除")
		}
		parent.Close()
		if e != nil {
			return e
		}
	}
	op.State = "complete"
	return a.saveDirectoryOverwrite(ctx, op)
}

func (a *App) finishImportOverwrites(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata) error {
	for i := range meta.Sidecars[file.ID] {
		if e := a.finishDirectoryOverwrite(ctx, fmt.Sprintf("%s/%s/side-%d", plan.ID, file.ID, i)); e != nil {
			return e
		}
	}
	return a.finishDirectoryOverwrite(ctx, plan.ID+"/"+file.ID)
}
