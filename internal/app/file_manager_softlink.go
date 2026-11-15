package app

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Interactive reads may project only an exact persisted link entry, never a
// path supplied by an unowned symlink. Generic browserStat/manifest stay nofollow.
func (a *App) browserOwnedSoftlink(ctx context.Context, path string, requireReferent bool) (managedFile, bool, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return managedFile{}, false, nil
	}
	owned, ok, err := a.ownedSoftlinkEntry(ctx, path, requireReferent)
	if err != nil || !ok {
		return owned, ok, err
	}
	var asset Asset
	if err = a.Store.Get(ctx, "asset", owned.AssetID, &asset); err != nil {
		return owned, false, err
	}
	if asset.State != "present" && asset.State != "external" {
		return owned, false, bad("REVIEW_SOFTLINK", "链接所属资产当前不是有效媒体")
	}
	if owned.Softlink == nil || owned.Softlink.Directory != nil {
		return owned, false, bad("REVIEW_SOFTLINK", "文件链接缺少精确普通引用证明")
	}
	return owned, true, nil
}

func (a *App) browserProjectSoftlink(ctx context.Context, entry *browserEntry, protection browserProtection) {
	owned, ok, err := a.browserOwnedSoftlink(ctx, entry.Path, false)
	if err != nil || !ok {
		entry.Type, entry.Protected = "symlink", "符号链接未取得当前整理归属，保留只读条目"
		return
	}
	entry.Type, entry.Softlink = "file", true
	entry.Size = owned.Softlink.SourceSnapshot.Size
	entry.Modified = time.Unix(0, owned.Softlink.SourceSnapshot.ModNS).UTC().Format(time.RFC3339)
	entry.Protected = protection.reason(entry.Path)
	if _, _, err = a.browserOwnedSoftlink(ctx, entry.Path, true); err != nil {
		entry.Broken = true
		if entry.Protected == "" {
			entry.Protected = "链接引用文件缺失或已变化，请通过整理历史处理"
		}
	}
}

func (a *App) browserReadableItem(ctx context.Context, path string) (*os.File, error) {
	file, err := importOpen(path)
	if err == nil {
		return file, nil
	}
	owned, ok, linkErr := a.browserOwnedSoftlink(ctx, path, true)
	if linkErr != nil {
		return nil, linkErr
	}
	if !ok {
		return nil, err
	}
	if !a.importSourceAllowed(owned.Softlink.Source, true) {
		return nil, bad("FILE_ROOT_REJECTED", "链接引用不在当前允许媒体目录内")
	}
	file, err = importOpen(owned.Softlink.Source)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !owned.Softlink.SourceSnapshot.matches(info) {
		file.Close()
		return nil, bad("REVIEW_SOFTLINK_SOURCE", "链接引用文件在打开期间已变化")
	}
	if _, _, err = a.browserOwnedSoftlink(ctx, path, true); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func (a *App) browserScanStamp(ctx context.Context, path string) (browserStamp, error) {
	stamp, err := browserStat(path)
	if err == nil {
		return stamp, nil
	}
	owned, ok, linkErr := a.browserOwnedSoftlink(ctx, path, true)
	if linkErr != nil {
		return browserStamp{}, linkErr
	}
	if !ok {
		return browserStamp{}, err
	}
	reference := owned.Softlink.SourceSnapshot
	return browserStamp{Path: path, Device: owned.Snapshot.Device, Inode: owned.Snapshot.Inode, Size: reference.Size, ModNS: reference.ModNS, Mode: uint32(0640)}, nil
}

func (a *App) browserOrganizeLinkedSource(ctx context.Context, path string) (bool, error) {
	owned, ok, err := a.browserOwnedSoftlink(ctx, path, true)
	if err != nil || !ok {
		return false, err
	}
	if owned.Kind == "directory_member" {
		return false, bad("REVIEW_REORGANIZE_OWNER", "目录成员请通过整理历史重新整理所属目录")
	}
	if owned.Kind != "video" && !auxiliaryMaterialKind(owned.Kind) {
		return false, bad("REVIEW_REORGANIZE_OWNER", "文件链接缺少视频或关联材料归属")
	}
	if !a.importSourceAllowed(owned.Softlink.Source, true) || browserInternal(owned.Softlink.Source) || filepath.IsAbs(owned.Softlink.Source) == false {
		return false, bad("FILE_ROOT_REJECTED", "链接引用不在当前允许媒体目录内")
	}
	return true, nil
}
