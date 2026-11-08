//go:build !linux

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Development-platform fallback retains the existing directory-bound Go path.
// Delivered Linux storage uses its native search-only descriptors instead.
func pluginStorageEntryInfo(path string) (os.FileInfo, error) {
	parent, err := importDir(filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	parentInfo, err := parent.Stat()
	if err != nil {
		return nil, err
	}
	rootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(parentInfo, rootInfo) {
		return nil, bad("FILE_PARENT_CHANGED", "文件父目录发生变化")
	}
	return root.Lstat(filepath.Base(path))
}

func (a *App) originalHostLocalRemove(ctx context.Context, scope originalBaseCallScope, path string, _ []browserStamp) (bool, error) {
	parent, err := importDir(filepath.Dir(path), false)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	defer root.Close()
	parentInfo, err := parent.Stat()
	if err != nil {
		return false, err
	}
	rootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(parentInfo, rootInfo) {
		return false, bad("FILE_PARENT_CHANGED", "删除前父目录发生变化")
	}
	if err = scope.validate(ctx); err != nil {
		return false, err
	}
	release, err := a.beginWrite()
	if err != nil {
		return false, err
	}
	defer release()
	if err = root.RemoveAll(filepath.Base(path)); err != nil {
		return false, nil
	}
	_, err = root.Lstat(filepath.Base(path))
	return errors.Is(err, os.ErrNotExist), nil
}
