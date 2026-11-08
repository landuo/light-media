package app

import (
	"context"
	"encoding/json"
	"path/filepath"

	"light-media/internal/plugins"
)

func (a *App) originalHostLocalReadGrant(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Path string `json:"path"`
	}
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if err := a.originalMetadataAllowed(request.Path); err != nil {
		return nil, err
	}
	privateDir, err := filepath.EvalSymlinks(a.Dir)
	if err != nil {
		return nil, bad("FILE_PROTECTED", "无法确认应用数据目录边界")
	}
	if within(a.Dir, request.Path) || sharedDirectoryContains(privateDir, request.Path) {
		return nil, bad("FILE_PROTECTED", "应用数据目录受保护")
	}
	// The importer opens every component without following symlinks and
	// snapshots only regular files. The worker receives identity, not bytes.
	file, err := snapshotPath(request.Path)
	if err != nil {
		return nil, err
	}
	var root string
	for _, candidate := range a.browserRoots() {
		if len(candidate.Path) > len(root) && candidate.Path != request.Path && within(candidate.Path, request.Path) {
			root = candidate.Path
		}
	}
	if root == "" {
		return nil, bad("INVALID_PATH", "文件不在当前允许目录内")
	}
	relative, err := filepath.Rel(root, request.Path)
	if err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	return json.Marshal(plugins.StorageLocalReadGrantV1{
		SchemaVersion:    1,
		Root:             root,
		RelativePath:     relative,
		Device:           file.Device,
		Inode:            file.Inode,
		Size:             file.Size,
		ModifiedUnixNano: file.ModNS,
	})
}
