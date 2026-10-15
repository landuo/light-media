package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"time"
)

// Only the server-side history operation supplies this owner. The frozen
// records never authorize overwriting another target or claiming another slot.
type importReorganize struct {
	Asset    Asset         `json:"asset"`
	Video    managedFile   `json:"video"`
	Owned    []managedFile `json:"owned"`
	RootPath string        `json:"root_path"`
}

type reorganizeCleanup struct {
	ID         string         `json:"id"`
	OldAssetID string         `json:"old_asset_id"`
	NewAssetID string         `json:"new_asset_id"`
	PreviewID  string         `json:"preview_id"`
	NewPath    string         `json:"new_path"`
	NewRootID  string         `json:"new_root_id"`
	Snapshot   importSnapshot `json:"snapshot"`
	Scrape     bool           `json:"scrape"`
	State      string         `json:"state"`
}

func (a *App) importSourceAllowed(path string, manual bool) bool {
	if a.sourceAllowed(path) {
		return true
	}
	if !manual || !importPath(path) || browserInternal(path) {
		return false
	}
	for _, root := range a.Settings().Roots {
		if within(root.Path, path) {
			return true
		}
	}
	return false
}

func reorganizeSameAsset(actual, frozen Asset) bool {
	a, _ := json.Marshal(struct {
		ID, Root, Path, Source, Media, State, Part string
		Season                                     int
		Episodes                                   []int
	}{actual.ID, actual.RootID, actual.Path, actual.SourcePath, mediaKey(actual.Media), actual.State, actual.Part, actual.Season, actual.Episodes})
	b, _ := json.Marshal(struct {
		ID, Root, Path, Source, Media, State, Part string
		Season                                     int
		Episodes                                   []int
	}{frozen.ID, frozen.RootID, frozen.Path, frozen.SourcePath, mediaKey(frozen.Media), frozen.State, frozen.Part, frozen.Season, frozen.Episodes})
	return string(a) == string(b)
}

func (a *App) prepareImportReorganize(ctx context.Context, request ImportRequest) (*importReorganize, error) {
	if request.ReorganizeAssetID == "" {
		return nil, nil
	}
	if !request.Manual || len(request.Files) != 1 {
		return nil, bad("INVALID_REORGANIZE", "重新整理需要来自一条明确的整理历史")
	}
	frozen := &importReorganize{Owned: []managedFile{}}
	if err := a.Store.Get(ctx, "asset", request.ReorganizeAssetID, &frozen.Asset); err != nil {
		return nil, err
	}
	if frozen.Asset.State == "removed" || frozen.Asset.State == "superseded" {
		return nil, bad("REVIEW_REORGANIZE_CHANGED", "原整理资产已移除或被另一条整理结果替换")
	}
	root, err := a.importRoot(frozen.Asset.RootID)
	if err != nil || !within(root.Path, frozen.Asset.Path) {
		return nil, bad("REVIEW_REORGANIZE_ROOT", "原整理目标已不在其媒体库目录中")
	}
	frozen.RootPath = root.Path
	if err = a.Store.Get(ctx, "managed_file", tokenHash(frozen.Asset.Path), &frozen.Video); err != nil || frozen.Video.AssetID != frozen.Asset.ID || frozen.Video.RootID != frozen.Asset.RootID || frozen.Video.Kind != "video" || frozen.Video.MediaKey != mediaKey(frozen.Asset.Media) {
		return nil, bad("REVIEW_REORGANIZE_OWNER", "原整理目标缺少可核验的文件归属")
	}
	source := request.Files[0].Path
	stamp, err := snapshotPath(source)
	if err != nil {
		return nil, err
	}
	if source == frozen.Asset.Path {
		if stamp != frozen.Video.Snapshot {
			return nil, bad("REVIEW_REORGANIZE_SOURCE", "原整理目标已被外部修改")
		}
	} else if source == frozen.Asset.SourcePath {
		if stamp != frozen.Video.SourceSnapshot {
			return nil, bad("REVIEW_REORGANIZE_SOURCE", "下载源在原整理后已发生变化")
		}
	} else {
		return nil, bad("REVIEW_REORGANIZE_SOURCE", "重新整理的源文件不属于所选历史")
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='managed_file' AND json_extract(payload,'$.asset_id')=?`, frozen.Asset.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var own managedFile
		if json.Unmarshal(raw, &own) != nil || own.RootID != root.ID || !within(root.Path, own.Path) || own.Path == frozen.Asset.SourcePath {
			rows.Close()
			return nil, bad("REVIEW_REORGANIZE_OWNER", "原整理文件归属或路径不一致")
		}
		frozen.Owned = append(frozen.Owned, own)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = a.guardImportReorganize(ctx, frozen); err != nil {
		return nil, err
	}
	return frozen, nil
}

func (a *App) guardImportReorganize(ctx context.Context, frozen *importReorganize) error {
	if frozen == nil {
		return nil
	}
	var current Asset
	if err := a.Store.Get(ctx, "asset", frozen.Asset.ID, &current); err != nil || !reorganizeSameAsset(current, frozen.Asset) {
		return bad("REVIEW_REORGANIZE_CHANGED", "原整理结果在预览后发生变化，请重新整理")
	}
	root, err := a.importRoot(frozen.Asset.RootID)
	if err != nil || root.Path != frozen.RootPath {
		return bad("REVIEW_REORGANIZE_ROOT", "原整理媒体库目录已改变")
	}
	for _, file := range frozen.Owned {
		var actual managedFile
		if a.Store.Get(ctx, "managed_file", tokenHash(file.Path), &actual) != nil || actual.AssetID != file.AssetID || actual.RootID != file.RootID || actual.Snapshot != file.Snapshot {
			return bad("REVIEW_REORGANIZE_OWNER", "原整理文件的归属在预览后改变")
		}
		stamp, inspectErr := snapshotPath(file.Path)
		if inspectErr != nil && !errors.Is(inspectErr, os.ErrNotExist) || inspectErr == nil && stamp != file.Snapshot {
			return bad("REVIEW_REORGANIZE_CHANGED", "原整理文件已被外部修改，已保留原结果")
		}
	}
	return nil
}

func reorganizeReserveTx(ctx context.Context, tx *sql.Tx, plan ImportPlan) error {
	if plan.ReorganizeAssetID == "" {
		return nil
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='import_meta' AND id=?`, plan.ID).Scan(&raw); err != nil {
		return err
	}
	var meta importMetadata
	if json.Unmarshal(raw, &meta) != nil || meta.Reorganize == nil || meta.Reorganize.Asset.ID != plan.ReorganizeAssetID {
		return bad("REVIEW_REORGANIZE_OWNER", "重新整理缺少原文件归属快照")
	}
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='asset' AND id=?`, plan.ReorganizeAssetID).Scan(&raw); err != nil {
		return err
	}
	var current Asset
	if json.Unmarshal(raw, &current) != nil || !reorganizeSameAsset(current, meta.Reorganize.Asset) {
		return bad("REVIEW_REORGANIZE_CHANGED", "原整理结果已被另一操作替换")
	}
	return nil
}

// Register the replacement and its cleanup in the same transaction as the new
// asset. No old file or slot is removed before the new video is published.
func commitImportReorganizeTx(ctx context.Context, tx *sql.Tx, plan ImportPlan, meta importMetadata, asset Asset, owned []managedFile) error {
	if meta.Reorganize == nil {
		return nil
	}
	if err := reorganizeReserveTx(ctx, tx, plan); err != nil {
		return err
	}
	old := meta.Reorganize.Asset
	if _, err := tx.ExecContext(ctx, `DELETE FROM asset_slots WHERE asset_id=?`, old.ID); err != nil {
		return err
	}
	old.State, old.Error = "superseded", "已由重新整理的新资产替换，旧自管文件等待核验清理"
	if err := txPut(ctx, tx, "asset", old.ID, old); err != nil {
		return err
	}
	transferred := map[string]bool{}
	for _, file := range owned {
		transferred[file.Path] = true
	}
	preview := assetRemovalPreview{ID: "reorganize_" + plan.ID, AssetID: old.ID, RootID: old.RootID, RootPath: meta.Reorganize.RootPath, Files: []assetRemovalFile{}, Expires: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano), State: "preview"}
	for _, file := range meta.Reorganize.Owned {
		if transferred[file.Path] {
			continue
		}
		if _, err := snapshotPath(file.Path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		preview.Files = append(preview.Files, assetRemovalFile{Path: file.Path, Snapshot: file.Snapshot, Kind: file.Kind})
	}
	if err := txPut(ctx, tx, "removal_preview", preview.ID, preview); err != nil {
		return err
	}
	var video managedFile
	for _, file := range owned {
		if file.Kind == "video" {
			video = file
		}
	}
	cleanup := reorganizeCleanup{ID: plan.ID, OldAssetID: old.ID, NewAssetID: asset.ID, PreviewID: preview.ID, NewPath: asset.Path, NewRootID: asset.RootID, Snapshot: video.Snapshot, Scrape: importBool(plan.Scraping, true), State: "pending"}
	if err := txPut(ctx, tx, "reorganize_cleanup", cleanup.ID, cleanup); err != nil {
		return err
	}
	_, err := enqueueTx(ctx, tx, "reorganize_cleanup", map[string]string{"id": plan.ID}, plan.WorkflowID)
	return err
}

func (a *App) runReorganizeCleanup(ctx context.Context, job Job, key string) error {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if err := a.jobCancelled(ctx, job); err != nil {
		return err
	}
	var cleanup reorganizeCleanup
	if err := a.Store.Get(ctx, "reorganize_cleanup", key, &cleanup); err != nil {
		return err
	}
	if cleanup.State == "complete" {
		return nil
	}
	var replacement Asset
	if a.Store.Get(ctx, "asset", cleanup.NewAssetID, &replacement) != nil || replacement.State != "present" || replacement.Path != cleanup.NewPath || replacement.RootID != cleanup.NewRootID {
		return bad("REVIEW_REORGANIZE_REPLACEMENT", "新整理结果不可核验，已保留旧目标文件")
	}
	root, rootErr := a.importRoot(replacement.RootID)
	if rootErr != nil || !within(root.Path, replacement.Path) {
		return bad("REVIEW_REORGANIZE_ROOT", "新整理媒体库目录已变化，已保留旧目标文件")
	}
	var video managedFile
	stamp, err := snapshotPath(replacement.Path)
	if err != nil || stamp != cleanup.Snapshot || a.Store.Get(ctx, "managed_file", tokenHash(replacement.Path), &video) != nil || video.AssetID != replacement.ID || video.Snapshot != cleanup.Snapshot {
		return bad("REVIEW_REORGANIZE_REPLACEMENT", "新整理视频已变化，旧目标文件保留")
	}
	var preview assetRemovalPreview
	if err = a.Store.Get(ctx, "removal_preview", cleanup.PreviewID, &preview); err != nil {
		return err
	}
	if preview.State != "completed" {
		if preview.State == "preview" {
			preview.Expires = time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)
			if err = a.Store.Put(ctx, "removal_preview", preview.ID, preview); err != nil {
				return err
			}
		}
		if err = a.removeAssetLocked(ctx, cleanup.OldAssetID, preview.ID, true, false, true); err != nil {
			return err
		}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if cleanup.Scrape {
		if _, err = enqueueTx(ctx, tx, "scrape", map[string]string{"id": cleanup.NewAssetID}, job.WorkflowID); err != nil {
			return err
		}
	}
	cleanup.State = "complete"
	if err = txPut(ctx, tx, "reorganize_cleanup", cleanup.ID, cleanup); err != nil {
		return err
	}
	return tx.Commit()
}
