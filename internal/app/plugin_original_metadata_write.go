package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type originalMetadataSnapshotKey struct{}
type originalMetadataWriteCheckKey struct{}

// Sidecars use the same O_NOFOLLOW directory traversal and exact file stamps
// as native publication. An explicit overwrite exchanges files atomically, so
// a changed target can be restored without discarding its contents.
func (a *App) writeOriginalMetadataSidecar(ctx context.Context, mediaPath, target string, content []byte, overwrite bool, supplied ...map[string]any) error {
	if len(content) == 0 {
		return nil
	}
	if err := a.originalMetadataAllowed(mediaPath); err != nil {
		return err
	}
	if err := a.originalMetadataAllowed(target); err != nil {
		return err
	}
	if strings.HasSuffix(target, ".nfo") && len(content) > 1<<20 {
		return bad("REVIEW_METADATA_SIZE", "元数据旁车超过大小限制")
	}
	guard := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if check, ok := ctx.Value(originalMetadataWriteCheckKey{}).(func() error); ok {
			if err := check(); err != nil {
				return err
			}
		}
		if err := a.originalMetadataAllowed(target); err != nil {
			return err
		}
		if before, ok := ctx.Value(originalMetadataSnapshotKey{}).(browserStamp); ok {
			after, err := browserStat(mediaPath)
			if err != nil || !sameBrowserIdentity(before, after) || os.FileMode(before.Mode).IsRegular() && before != after {
				return bad("REVIEW_METADATA_SOURCE_CHANGED", "刮削媒体文件发生变化，已停止旁车写入")
			}
		}
		return nil
	}
	if err := guard(); err != nil {
		return err
	}
	parent, err := importDir(filepath.Dir(target), false)
	if err != nil {
		return err
	}
	defer parent.Close()
	var old *importSnapshot
	if file, err := importOpen(target); err == nil {
		stat, err := file.Stat()
		_ = file.Close()
		if err != nil {
			return err
		}
		stamp, err := snapshot(stat)
		if err != nil {
			return err
		}
		if !overwrite {
			return nil
		}
		old = &stamp
	} else if !os.IsNotExist(err) {
		return err
	}
	stage := ".light-media-metadata-" + id("") + filepath.Ext(target)
	fd, err := unix.Openat(int(parent.Fd()), stage, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0666)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), stage)
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = unix.Unlinkat(int(parent.Fd()), stage, 0)
		}
	}()
	if _, err = file.Write(content); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	newStamp, err := snapshot(stat)
	if err != nil {
		return err
	}
	if err = guard(); err != nil {
		return err
	}
	if err = a.prepareOriginalMetadataDirectoryWrite(ctx, target, stage, newStamp, old, content); err != nil {
		return err
	}
	base, err := originalLocalFileItem(filepath.Dir(target))
	if err != nil {
		return err
	}
	var baseItem any = base
	if len(supplied) != 0 && supplied[0] != nil {
		baseItem = supplied[0]
	}
	result, err := a.originalMetadataStorage(ctx, "upload_file", map[string]any{"fileitem": baseItem, "path": filepath.Join(filepath.Dir(target), stage), "new_name": filepath.Base(target)}, func() (any, error) {
		if err := guard(); err != nil {
			return nil, err
		}
		if old == nil {
			if err = importRenameExclusive(int(parent.Fd()), stage, filepath.Base(target)); err != nil {
				return nil, err
			}
		} else {
			if err = originalMetadataExchange(int(parent.Fd()), stage, filepath.Base(target)); err != nil {
				return nil, err
			}
			prior, err := importOpen(filepath.Join(filepath.Dir(target), stage))
			matched := false
			if err == nil {
				st, e := prior.Stat()
				matched = e == nil && old.matches(st)
				_ = prior.Close()
			}
			if !matched {
				current, e := snapshotPath(target)
				if e == nil && current == newStamp {
					e = originalMetadataExchange(int(parent.Fd()), stage, filepath.Base(target))
				}
				if e != nil || current != newStamp {
					cleanup = false
					return nil, bad("REVIEW_METADATA_RECOVERY", "旁车目标并发变化，已保留交换文件供核对")
				}
				return nil, bad("REVIEW_METADATA_CONFLICT", "旁车目标已变化，已保留原文件")
			}
		}
		if err = unix.Fsync(int(parent.Fd())); err != nil && !errors.Is(err, unix.EINVAL) {
			return nil, err
		}
		return originalLocalFileItem(target)
	})
	if err != nil {
		return err
	}
	_, err = originalMetadataStorageItem(result)
	return err
}
