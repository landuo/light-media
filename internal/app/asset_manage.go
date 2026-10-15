package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type assetRemovalFile struct {
	Path        string         `json:"path"`
	Snapshot    importSnapshot `json:"snapshot"`
	Kind        string         `json:"kind"`
	Deleted     bool           `json:"deleted"`
	Quarantine  string         `json:"quarantine,omitempty"`
	Quarantined bool           `json:"quarantined,omitempty"`
}
type assetRemovalPreview struct {
	ID       string             `json:"preview_id"`
	AssetID  string             `json:"asset_id"`
	RootID   string             `json:"root_id"`
	RootPath string             `json:"root_path"`
	Files    []assetRemovalFile `json:"files"`
	Expires  string             `json:"expires_at"`
	State    string             `json:"state"`
}

func (a *App) assetAPI(w http.ResponseWriter, r *http.Request, p string) (any, error) {
	parts := strings.Split(p, "/")
	if len(parts) == 0 || len(parts) > 2 || parts[0] == "" {
		return nil, bad("INVALID_ASSET", "无效资产 ID")
	}
	ctx := r.Context()
	var asset Asset
	if err := a.Store.Get(ctx, "asset", parts[0], &asset); err != nil {
		return nil, err
	}
	if len(parts) == 1 && r.Method == "GET" {
		return asset, nil
	}
	if len(parts) != 2 || r.Method != "POST" {
		return nil, &APIError{"NOT_FOUND", "接口不存在", 404}
	}
	switch parts[1] {
	case "check":
		if err := a.checkAsset(ctx, asset.ID); err != nil {
			return nil, err
		}
		var checked Asset
		err := a.Store.Get(ctx, "asset", asset.ID, &checked)
		return checked, err
	case "scrape":
		return a.enqueue(ctx, "scrape", map[string]string{"id": asset.ID}, id("wf_"))
	case "sync", "fnos-sync":
		return a.enqueue(ctx, "fnos_sync", map[string]string{"id": asset.ID}, id("wf_"))
	case "remove-preview":
		return a.previewAssetRemoval(ctx, asset)
	case "remove":
		var in struct {
			PreviewID   string `json:"preview_id"`
			Confirm     bool   `json:"confirm"`
			DeleteFiles *bool  `json:"delete_files"`
			Exclude     *bool  `json:"exclude"`
			Reacquire   *bool  `json:"reacquire"`
		}
		if err := decodeBody(w, r, &in); err != nil {
			return nil, err
		}
		if !in.Confirm || in.PreviewID == "" {
			return nil, bad("REMOVAL_PREVIEW_REQUIRED", "请先获取删除预览，并提交其 preview_id 与明确确认")
		}
		deleteFiles, exclude := true, true
		if in.DeleteFiles != nil {
			deleteFiles = *in.DeleteFiles
		}
		if in.Exclude != nil {
			exclude = *in.Exclude
		}
		if in.Reacquire != nil {
			exclude = !*in.Reacquire
		}
		err := a.removeAsset(ctx, asset.ID, in.PreviewID, deleteFiles, exclude)
		return map[string]bool{"removed": err == nil, "excluded": exclude}, err
	case "resolve":
		var in struct {
			Action string `json:"action"`
		}
		if err := decodeBody(w, r, &in); err != nil {
			return nil, err
		}
		err := a.resolveMissingAsset(ctx, asset, in.Action)
		return map[string]bool{"accepted": err == nil}, err
	}
	return nil, &APIError{"NOT_FOUND", "接口不存在", 404}
}

func (a *App) previewAssetRemoval(ctx context.Context, asset Asset) (assetRemovalPreview, error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if asset.State == "removed" {
		return assetRemovalPreview{}, bad("INVALID_ASSET_STATE", "资产已移除")
	}
	root, err := a.importRoot(asset.RootID)
	if err != nil {
		return assetRemovalPreview{}, err
	}
	if !within(root.Path, asset.Path) {
		return assetRemovalPreview{}, bad("REVIEW_ROOT_CHANGED", "资产不在配置库中")
	}
	dir, err := importDir(root.Path, false)
	if err != nil {
		return assetRemovalPreview{}, bad("ROOT_UNAVAILABLE", "媒体库根目录不可访问，不能生成删除预览")
	}
	dir.Close()
	var unfinishedRaw []byte
	unfinishedErr := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='removal_preview' AND json_extract(payload,'$.asset_id')=? AND json_extract(payload,'$.state')='deleting' ORDER BY updated_at DESC LIMIT 1`, asset.ID).Scan(&unfinishedRaw)
	if unfinishedErr == nil {
		var unfinished assetRemovalPreview
		if json.Unmarshal(unfinishedRaw, &unfinished) != nil || unfinished.RootID != root.ID || unfinished.RootPath != root.Path {
			return assetRemovalPreview{}, bad("REVIEW_REMOVAL_STATE", "存在待恢复删除操作，但其库配置已变化")
		}
		return unfinished, nil
	} else if !errors.Is(unfinishedErr, sql.ErrNoRows) {
		return assetRemovalPreview{}, unfinishedErr
	}
	preview := assetRemovalPreview{ID: id("remove_"), AssetID: asset.ID, RootID: asset.RootID, RootPath: root.Path, Files: []assetRemovalFile{}, Expires: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano), State: "preview"}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='managed_file' AND json_extract(payload,'$.asset_id')=?`, asset.ID)
	if err != nil {
		return preview, err
	}
	owned := []managedFile{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return preview, err
		}
		var file managedFile
		if json.Unmarshal(raw, &file) != nil {
			rows.Close()
			return preview, bad("REVIEW_MANAGED_FILE", "自管文件记录损坏")
		}
		owned = append(owned, file)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return preview, err
	}
	for _, file := range owned {
		if file.AssetID != asset.ID || file.RootID != asset.RootID || !within(root.Path, file.Path) || file.Path == asset.SourcePath {
			return preview, bad("REVIEW_MANAGED_FILE", "自管文件的路径或归属不一致")
		}
		actual, err := snapshotPath(file.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || actual != file.Snapshot {
			return preview, bad("REVIEW_MANAGED_FILE", "文件已被外部修改，不能纳入自动删除")
		}
		preview.Files = append(preview.Files, assetRemovalFile{Path: file.Path, Snapshot: actual, Kind: file.Kind})
	}
	if err = a.Store.Put(ctx, "removal_preview", preview.ID, preview); err != nil {
		return preview, err
	}
	return preview, nil
}

func (a *App) removeAsset(ctx context.Context, assetID, previewID string, deleteFiles, exclude bool) error {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	var preview assetRemovalPreview
	if err := a.Store.Get(ctx, "removal_preview", previewID, &preview); err != nil {
		return bad("REMOVAL_PREVIEW_REQUIRED", "删除预览不存在，请重新预览")
	}
	expires, err := time.Parse(time.RFC3339Nano, preview.Expires)
	if err != nil || (preview.State == "preview" && time.Now().After(expires)) || preview.AssetID != assetID || (preview.State != "preview" && preview.State != "deleting") {
		return bad("REMOVAL_PREVIEW_REQUIRED", "删除预览已失效、已使用或不属于此资产")
	}
	var asset Asset
	if err = a.Store.Get(ctx, "asset", assetID, &asset); err != nil {
		return err
	}
	if asset.State == "removed" {
		return bad("INVALID_ASSET_STATE", "资产已移除，请勿重复执行旧预览")
	}
	root, err := a.importRoot(asset.RootID)
	if err != nil {
		return err
	}
	if root.Path != preview.RootPath || root.ID != preview.RootID || !within(root.Path, asset.Path) {
		return bad("REVIEW_ROOT_CHANGED", "媒体库配置变化，请重新预览")
	}
	rootDir, err := importDir(root.Path, false)
	if err != nil {
		return bad("ROOT_UNAVAILABLE", "媒体库根目录不可访问")
	}
	rootDir.Close()
	release, err := a.beginWrite()
	if err != nil {
		return err
	}
	defer release()
	if deleteFiles {
		for index := range preview.Files {
			file := &preview.Files[index]
			if file.Deleted {
				continue
			}
			if !within(root.Path, file.Path) || file.Path == asset.SourcePath {
				return bad("REVIEW_REMOVE_PATH", "删除路径无效")
			}
			var owned managedFile
			if err = a.Store.Get(ctx, "managed_file", tokenHash(file.Path), &owned); err != nil || owned.AssetID != asset.ID || owned.RootID != asset.RootID || owned.Snapshot != file.Snapshot {
				return bad("REVIEW_MANAGED_FILE", "文件所有权在预览后发生变化")
			}
			if file.Quarantine == "" {
				file.Quarantine = ".light-media-remove-" + tokenHash(preview.ID + file.Path)[:32] + ".part"
			}
			if _, e := removalLocation(*file); e != nil {
				return e
			}
		}
		preview.State = "deleting"
		if err = a.Store.Put(ctx, "removal_preview", preview.ID, preview); err != nil {
			return err
		}
		for index := range preview.Files {
			file := &preview.Files[index]
			if file.Deleted {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			parent, err := importDir(filepath.Dir(file.Path), false)
			if err != nil {
				return err
			}
			location, err := removalLocation(*file)
			if err != nil {
				parent.Close()
				return err
			}
			if location == "source" {
				err = importRenameExclusive(int(parent.Fd()), filepath.Base(file.Path), file.Quarantine)
				if err != nil {
					parent.Close()
					return bad("REVIEW_MANAGED_FILE", "无法独占暂存待删除文件，已停止")
				}
				actual, inspectErr := snapshotPath(filepath.Join(filepath.Dir(file.Path), file.Quarantine))
				if inspectErr != nil || actual != file.Snapshot {
					_ = importRenameExclusive(int(parent.Fd()), file.Quarantine, filepath.Base(file.Path))
					_ = parent.Sync()
					parent.Close()
					return bad("REVIEW_MANAGED_FILE", "删除时路径被替换；已保留文件并停止，请核对原路径及暂存文件")
				}
				if err = parent.Sync(); err != nil {
					parent.Close()
					return err
				}
				file.Quarantined = true
				if err = a.Store.Put(ctx, "removal_preview", preview.ID, preview); err != nil {
					parent.Close()
					return err
				}
			}
			if location != "gone" && !file.Quarantined {
				file.Quarantined = true
				if err = a.Store.Put(ctx, "removal_preview", preview.ID, preview); err != nil {
					parent.Close()
					return err
				}
			}
			if location != "gone" {
				err = unix.Unlinkat(int(parent.Fd()), file.Quarantine, 0)
			}
			if err == nil {
				err = parent.Sync()
			}
			parent.Close()
			if err != nil {
				return err
			}
			file.Deleted = true
			if err = a.Store.Put(ctx, "removal_preview", preview.ID, preview); err != nil {
				return err
			}
		}
	} else if preview.State == "deleting" {
		return bad("REVIEW_REMOVAL_STATE", "删除已开始，请按原预览继续完成，不能中途改变文件处理方式")
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	asset.State = "removed"
	asset.FNOSState = "needs_attention"
	asset.Error = "资产已由用户移除；下载源和下载器任务保留"
	if err = txPut(ctx, tx, "asset", asset.ID, asset); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_slots WHERE asset_id=?`, asset.ID); err != nil {
		return err
	}
	for _, episode := range importEpisodes(asset.Episodes) {
		key := fmt.Sprintf("%s:%d:%d", mediaKey(asset.Media), asset.Season, episode)
		if err = txPut(ctx, tx, "exclusion", key, map[string]any{"blocked": exclude, "reason": "user_removed", "at": now()}); err != nil {
			return err
		}
	}
	preview.State = "completed"
	if err = txPut(ctx, tx, "removal_preview", preview.ID, preview); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.log("INFO", "assets", "removed", "已按一次性预览移除自管发布文件；下载源保留", "", "")
	return nil
}

func removalLocation(file assetRemovalFile) (string, error) {
	if file.Quarantine == "" || filepath.Base(file.Quarantine) != file.Quarantine || !strings.HasPrefix(file.Quarantine, ".light-media-remove-") {
		return "", bad("REVIEW_MANAGED_FILE", "删除暂存收据无效")
	}
	quarantine := filepath.Join(filepath.Dir(file.Path), file.Quarantine)
	if current, err := snapshotPath(quarantine); err == nil {
		if current != file.Snapshot {
			return "", bad("REVIEW_MANAGED_FILE", "删除暂存文件指纹不符")
		}
		return "quarantine", nil
	} else if !os.IsNotExist(err) {
		return "", bad("REVIEW_MANAGED_FILE", "删除暂存路径不可安全访问")
	}
	current, err := snapshotPath(file.Path)
	if err == nil && current == file.Snapshot {
		return "source", nil
	}
	if file.Quarantined && (err == nil || os.IsNotExist(err)) {
		return "gone", nil
	}
	return "", bad("REVIEW_MANAGED_FILE", "文件在预览后发生变化，请重新预览")
}

type assetPresence struct {
	Count int    `json:"count"`
	Last  string `json:"last"`
}

func (a *App) checkAsset(ctx context.Context, assetID string) error {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	return a.checkAssetLocked(ctx, assetID)
}

func (a *App) checkAssetLocked(ctx context.Context, assetID string) error {
	var asset Asset
	if err := a.Store.Get(ctx, "asset", assetID, &asset); err != nil {
		return err
	}
	if asset.State == "removed" || asset.State == "missing_confirmed" {
		return nil
	}
	root, err := a.importRoot(asset.RootID)
	if err != nil {
		return err
	}
	if !within(root.Path, asset.Path) {
		return bad("REVIEW_ROOT_CHANGED", "资产已不在配置库中")
	}
	directory, err := importDir(root.Path, false)
	if err != nil {
		asset.Error = "媒体库根目录不可访问；未据此判定文件缺失"
		_ = a.Store.Put(ctx, "asset", asset.ID, asset)
		return bad("ROOT_UNAVAILABLE", asset.Error)
	}
	directory.Close()
	actual, err := snapshotPath(asset.Path)
	if err == nil {
		var own managedFile
		changed := a.Store.Get(ctx, "managed_file", tokenHash(asset.Path), &own) == nil && own.Kind == "video" && own.Snapshot != actual
		if asset.IdentitySource == "manual" {
			var proof libraryIdentity
			if e := a.Store.Get(ctx, "asset_identity", asset.ID, &proof); e != nil || proof.Path != asset.Path || proof.RootPath != root.Path || proof.Snapshot != actual {
				changed = true
			}
		}
		if changed {
			asset.State = "changed"
			asset.Error = "文件指纹发生变化，保留原身份与占用，需人工核对"
		} else {
			asset.State = "present"
			if asset.SourcePath == "" {
				asset.State = "external"
			}
			asset.Error = ""
			asset.Size = actual.Size
			asset.MissingSeenAt = ""
		}
		if err = a.Store.Delete(ctx, "asset_presence", asset.ID); err != nil {
			return err
		}
		return a.Store.Put(ctx, "asset", asset.ID, asset)
	}
	if !os.IsNotExist(err) {
		asset.Error = "资产路径不可安全访问；未据此判定文件缺失"
		_ = a.Store.Put(ctx, "asset", asset.ID, asset)
		return bad("ASSET_UNAVAILABLE", asset.Error)
	}
	var presence assetPresence
	err = a.Store.Get(ctx, "asset_presence", asset.ID, &presence)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	previous, _ := time.Parse(time.RFC3339Nano, presence.Last)
	if presence.Count == 0 || time.Since(previous) >= time.Second {
		presence.Count++
		presence.Last = now()
	}
	if asset.MissingSeenAt == "" {
		asset.MissingSeenAt = now()
	}
	asset.State = "missing_suspected"
	asset.Error = "首次成功检查未找到视频；保留资产占用，等待复核"
	if presence.Count >= 2 {
		asset.State = "missing_pending"
		asset.Error = "两次成功检查确认文件缺失，等待用户处理；不会自动补回"
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "asset_presence", asset.ID, presence); err != nil {
		return err
	}
	if err = txPut(ctx, tx, "asset", asset.ID, asset); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) resolveMissingAsset(ctx context.Context, asset Asset, action string) error {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if action != "confirm_missing" && action != "keep_waiting" && action != "allow_reacquire" {
		return bad("INVALID_ACTION", "未知的缺失处理操作")
	}
	var current Asset
	if err := a.Store.Get(ctx, "asset", asset.ID, &current); err != nil {
		return err
	}
	asset = current
	if action == "keep_waiting" {
		if asset.State != "missing_pending" && asset.State != "missing_suspected" {
			return bad("INVALID_ASSET_STATE", "资产当前不处于待确认缺失状态")
		}
		asset.Error = "用户选择继续等待；原身份与占用保留"
		return a.Store.Put(ctx, "asset", asset.ID, asset)
	}
	if action == "confirm_missing" && asset.State != "missing_pending" {
		return bad("REVIEW_MISSING", "需完成两次根目录可达的缺失核对后才能确认")
	}
	if action == "allow_reacquire" && asset.State != "missing_pending" && asset.State != "missing_confirmed" && asset.State != "removed" {
		return bad("INVALID_ASSET_STATE", "只有缺失或已移除资产才能允许重新获取")
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	blocked := action == "confirm_missing"
	for _, episode := range importEpisodes(asset.Episodes) {
		key := fmt.Sprintf("%s:%d:%d", mediaKey(asset.Media), asset.Season, episode)
		if err = txPut(ctx, tx, "exclusion", key, map[string]any{"blocked": blocked, "reason": action, "at": now()}); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_slots WHERE asset_id=?`, asset.ID); err != nil {
		return err
	}
	asset.State = "missing_confirmed"
	asset.Error = "用户已确认缺失，默认排除自动补回"
	if !blocked {
		asset.Error = "用户明确允许重新获取；活动订阅将在后续检查时按规则处理"
	}
	if err = txPut(ctx, tx, "asset", asset.ID, asset); err != nil {
		return err
	}
	return tx.Commit()
}
