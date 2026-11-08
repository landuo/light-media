package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins"
)

const originalScrapeCleanRemoveCapability = plugins.ScrapeCleanRemoveCapability

type originalScrapeCleanRemoveRequest struct {
	Path   string  `json:"path"`
	Kind   string  `json:"kind"`
	Device *uint64 `json:"device"`
	Inode  *uint64 `json:"inode"`
}

func (a *App) originalHostScrapeCleanResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	object := scope.object
	if object == nil || object.app != a || object.installation.PluginID != "ScrapeFileClean" || scope.phase != "private_watch" || !object.originalPrivateSchedulerScopeAllowed(ctx) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "刮削清理须绑定实际私有监控调用")
	}
	var request originalScrapeCleanRemoveRequest
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	if request.Device == nil || request.Inode == nil || *request.Inode == 0 {
		return nil, bad("INVALID_PLUGIN_VALUE", "刮削清理须指定文件设备和 inode")
	}
	switch request.Kind {
	case "file", "empty_dir", "tree":
	default:
		return nil, bad("INVALID_PLUGIN_VALUE", "未知刮削清理项目类型")
	}
	removed, err := a.originalHostScrapeCleanDelete(ctx, scope, request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(removed)
}

// A retained watcher may finish a callback while stopping. Its own applied
// config remains the authority, rather than a later save or a clone's config.
func originalScrapeCleanPathAllowed(ctx context.Context, object *originalPluginInstance, path string) bool {
	object.configMu.RLock()
	raw := copyOriginalBaseJSON(object.appliedConfig)
	object.configMu.RUnlock()
	if init := originalLiveInitialization(ctx, object); init != nil && len(init.config) != 0 {
		raw = init.config
	}
	var config map[string]json.RawMessage
	if !importPath(path) || json.Unmarshal(raw, &config) != nil || !originalBaseJSONTruthy(config["enabled"]) {
		return false
	}
	monitors, _ := sourceSiteString(config["monitor_dirs"])
	monitored := false
	for _, item := range strings.Split(monitors, "\n") {
		root := filepath.Clean(sourceSiteTrim(item))
		if !importPath(root) {
			continue
		}
		if within(path, root) {
			return false // An outer tree cannot remove an independently watched root.
		}
		monitored = monitored || within(root, path)
	}
	if !monitored {
		return false
	}
	excluded, _ := sourceSiteString(config["exclude_dirs"])
	for _, item := range strings.Split(excluded, "\n") {
		root := filepath.Clean(sourceSiteTrim(item))
		if importPath(root) && within(root, path) {
			return false
		}
	}
	keywords, _ := sourceSiteString(config["exclude_keywords"])
	for _, keyword := range strings.Split(keywords, "\n") {
		if keyword != "" && strings.Contains(path, keyword) {
			return false
		}
	}
	return true
}

func originalScrapeCleanManifestMatches(request originalScrapeCleanRemoveRequest, manifest []browserStamp) bool {
	if len(manifest) == 0 || manifest[0].Path != request.Path || manifest[0].Device != *request.Device || manifest[0].Inode != *request.Inode {
		return false
	}
	mode := os.FileMode(manifest[0].Mode)
	switch request.Kind {
	case "file":
		return mode.IsRegular() && len(manifest) == 1
	case "empty_dir":
		return mode.IsDir() && len(manifest) == 1
	case "tree":
		return mode.IsDir()
	}
	return false
}

func (a *App) originalHostScrapeCleanDelete(ctx context.Context, scope originalBaseCallScope, request originalScrapeCleanRemoveRequest) (bool, error) {
	// Settings changes and selected downloader mutations use the same lock
	// order. Freeze the authorized roots and protect live downloader sources.
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return false, err
	}
	path := request.Path
	if !originalScrapeCleanPathAllowed(ctx, scope.object, path) || a.originalMetadataAllowed(path) != nil || a.browserMutationAllowedWithReferences(ctx, path, false) != nil {
		return false, ctx.Err()
	}
	before, err := browserManifestWithHardlinkGuard(ctx, path, false)
	if err != nil || !originalScrapeCleanManifestMatches(request, before) {
		return false, ctx.Err() // Absent, changed, special, or nonempty. No effect.
	}
	if err := a.guardBrowserDownloaderSources(ctx, []string{path}); err != nil {
		return false, ctx.Err()
	}
	after, err := browserManifestWithHardlinkGuard(ctx, path, false)
	if err != nil || !sameBrowserManifest(before, after) {
		return false, ctx.Err()
	}
	removed, err := a.originalHostScrapeCleanQuarantine(ctx, scope, request, after)
	if err != nil || !removed {
		return removed, err
	}
	// A confirmed filesystem effect still needs its native index update when
	// the caller's request is canceled after unlinking. Keep the import lock.
	reconcile, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := a.originalScrapeCleanMarkMissing(reconcile, after); err != nil {
		return false, bad("FILE_INDEX_UNCERTAIN", "刮削清理已删除文件，但媒体索引未能核对")
	}
	return true, nil
}

func (a *App) originalScrapeCleanMarkMissing(ctx context.Context, removed []browserStamp) error {
	for _, file := range removed {
		if !os.FileMode(file.Mode).IsRegular() {
			continue
		}
		var owned managedFile
		err := a.Store.Get(ctx, "managed_file", tokenHash(file.Path), &owned)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		want := importSnapshot{Device: file.Device, Inode: file.Inode, Size: file.Size, ModNS: file.ModNS}
		if owned.Kind != "video" || owned.Path != file.Path || owned.Snapshot != want {
			continue
		}
		if _, err := browserEntryStat(file.Path); err == nil {
			continue // A path recreated during the effect needs the ordinary asset review.
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		var asset Asset
		if err := a.Store.Get(ctx, "asset", owned.AssetID, &asset); err != nil {
			return err
		}
		if asset.ID != owned.AssetID || asset.Path != owned.Path || asset.RootID != owned.RootID || owned.MediaKey != mediaKey(asset.Media) || asset.State == "removed" || asset.State == "superseded" {
			continue
		}
		asset.State = "missing_pending"
		asset.FNOSState = "needs_attention"
		asset.MissingSeenAt = now()
		asset.Error = "联动清理已确认媒体文件删除，保留原身份与占用，等待核对"
		if err := a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) originalHostScrapeCleanQuarantine(ctx context.Context, scope originalBaseCallScope, request originalScrapeCleanRemoveRequest, expected []browserStamp) (bool, error) {
	return a.originalGuardedCleanupQuarantine(ctx, request.Path, request.Kind, ".light-media-scrape-clean-", "刮削清理", expected,
		func() error { return scope.validate(ctx) },
		func() bool { return originalScrapeCleanPathAllowed(ctx, scope.object, request.Path) })
}

func (a *App) originalGuardedCleanupQuarantine(ctx context.Context, path, kind, prefix, label string, expected []browserStamp, validate func() error, authorize func() bool) (bool, error) {
	parentPath, name := filepath.Dir(path), filepath.Base(path)
	parent, err := importDir(parentPath, false)
	if err != nil {
		return false, ctx.Err()
	}
	defer parent.Close()
	bound, boundErr := parent.Stat()
	currentParent, parentErr := pluginStorageEntryInfo(parentPath)
	current, entryErr := browserEntryStat(path)
	if boundErr != nil || parentErr != nil || !os.SameFile(bound, currentParent) || entryErr != nil || current != expected[0] {
		return false, ctx.Err()
	}
	if err := validate(); err != nil {
		return false, err
	}
	if !authorize() {
		return false, nil
	}
	release, err := a.beginWrite()
	if err != nil {
		return false, nil
	}
	defer release()
	fd := int(parent.Fd())
	quarantine := id(prefix)
	if err := importRenameExclusive(fd, name, quarantine); err != nil {
		return false, ctx.Err()
	}
	restore := func(partial bool) (bool, error) {
		if err := importRenameExclusive(fd, quarantine, name); err != nil {
			return false, bad("FILE_QUARANTINE_UNCERTAIN", label+"暂存项无法安全恢复，需核对原目录")
		}
		if partial {
			return false, bad("FILE_REMOVE_UNCERTAIN", label+"目录可能已部分删除，需核对剩余内容")
		}
		return false, ctx.Err()
	}
	target := filepath.Join(parentPath, quarantine)
	moved, err := browserManifestWithHardlinkGuard(ctx, target, false)
	if err != nil || !sameRebasedManifest(expected, moved, path, target) || validate() != nil {
		return restore(false)
	}
	flags := 0
	if kind != "file" {
		flags = unix.AT_REMOVEDIR
	}
	partial := false
	if kind == "tree" {
		partial, err = originalScrapeCleanRemoveChildren(target, moved)
		if err != nil {
			return restore(partial)
		}
	}
	// Empty directories use the kernel's nonrecursive rmdir semantics even
	// after both snapshots, so a concurrent new child can never be removed.
	if err := unix.Unlinkat(fd, quarantine, flags); err != nil {
		return restore(partial)
	}
	var check unix.Stat_t
	if err := unix.Fstatat(fd, quarantine, &check, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
		return false, bad("FILE_REMOVE_UNCERTAIN", label+"暂存项移除后无法核对")
	}
	return true, nil
}

func originalScrapeCleanRemoveChildren(path string, expected []browserStamp) (bool, error) {
	directory, err := os.OpenRoot(path)
	if err != nil {
		return false, err
	}
	defer directory.Close()
	info, err := directory.Stat(".")
	if err != nil {
		return false, err
	}
	actual, err := browserInfo(path, info)
	if err != nil || actual != expected[0] {
		return false, bad("FILE_SNAPSHOT_CHANGED", "刮削清理目录已变化")
	}
	// The frozen manifest is sorted by path. Remove descendants before
	// parents, one exact entry at a time. An unobserved child blocks rmdir
	// instead of being discovered and removed by a recursive traversal.
	removed := false
	for i := len(expected) - 1; i > 0; i-- {
		want := expected[i]
		relative, err := filepath.Rel(path, want.Path)
		if err != nil || relative == "." || !filepath.IsLocal(relative) {
			return removed, bad("FILE_SNAPSHOT_CHANGED", "刮削清理子项路径无效")
		}
		info, err := directory.Lstat(relative)
		if err != nil {
			return removed, err
		}
		actual, err := browserInfo(want.Path, info)
		if err != nil || !sameBrowserIdentity(actual, want) || !info.IsDir() && actual != want {
			return removed, bad("FILE_SNAPSHOT_CHANGED", "刮削清理子项已变化")
		}
		if err := directory.Remove(relative); err != nil {
			return removed, err
		}
		removed = true
	}
	return removed, nil
}
