package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"light-media/internal/plugins/mediaserverruntime"
)

// The source's physical Path.resolve is evaluated only inside the existing
// browserRoots/local-storage authority. Existing native policy rejects links,
// private data, special files and noncanonical/relative path spellings. We do
// not relax that policy or use lexical cleaning as a substitute for a physical
// check. Every existing prefix is checked through the existing no-follow stat
// helper; a missing tail is permitted like resolve(strict=False).
func (a *App) originalMediaServerResolveRefreshPath(ctx context.Context, current mediaserverruntime.ReferenceCurrent, path string) (string, error) {
	check := func() error {
		if err := originalMediaServerRefreshCurrent(ctx, current); err != nil {
			return err
		}
		return a.sharedLocalStorageAllowed(path)
	}
	if err := check(); err != nil {
		return "", err
	}
	probe := path
	for {
		if err := check(); err != nil {
			return "", err
		}
		parent, _, statErr := sharedLocalStorageStat(probe)
		if parent != nil {
			parent.Close()
		}
		if err := check(); err != nil {
			return "", err
		}
		if statErr == nil {
			return path, nil
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		next := filepath.Dir(probe)
		if next == probe {
			return "", statErr
		}
		probe = next
	}
}
