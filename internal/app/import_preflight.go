package app

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const organizeProbeMaxAge = 30 * time.Second

type importTargetProbe struct {
	mu        sync.Mutex
	key       importTargetProbeKey
	checkedAt time.Time
}

type importTargetProbeKey struct {
	batch, path   string
	device, inode uint64
	mode          os.FileMode
	uid, gid      uint32
}

// A directory batch may reuse its own recent successful probe for the same
// opened root. Space and file identity checks still run for each plan, and
// actual publication continues to validate and write the target normally.
func (p *importTargetProbe) check(ctx context.Context, directory *os.File, info os.FileInfo) error {
	work, ok := ctx.Value(organizeFileKey{}).(organizeFileWork)
	stat, haveStat := info.Sys().(*syscall.Stat_t)
	if !ok || work.Job.ParentJobID == "" || work.BatchID == "" || !haveStat {
		return importProbeWrite(directory)
	}
	key := importTargetProbeKey{
		batch: work.BatchID, path: directory.Name(), device: uint64(stat.Dev), inode: uint64(stat.Ino),
		mode: info.Mode(), uid: stat.Uid, gid: stat.Gid,
	}
	p.mu.Lock()
	recent := p.key == key && time.Since(p.checkedAt) < organizeProbeMaxAge
	p.mu.Unlock()
	if recent && unix.Faccessat(int(directory.Fd()), ".", unix.W_OK|unix.X_OK, unix.AT_EACCESS) == nil {
		return nil
	}
	if err := importProbeWrite(directory); err != nil {
		return err
	}
	p.mu.Lock()
	p.key, p.checkedAt = key, time.Now()
	p.mu.Unlock()
	return nil
}

func importAvailableSpace(directory *os.File) (int64, error) {
	var state unix.Statfs_t
	if err := unix.Fstatfs(int(directory.Fd()), &state); err != nil {
		return 0, bad("ROOT_SPACE_UNAVAILABLE", "无法取得媒体库可用空间")
	}
	blocks, blockSize := uint64(state.Bavail), uint64(state.Bsize)
	if blockSize == 0 {
		return 0, bad("ROOT_SPACE_UNAVAILABLE", "文件系统返回了无效容量")
	}
	if blocks > uint64(math.MaxInt64)/blockSize {
		return math.MaxInt64, nil
	}
	return int64(blocks * blockSize), nil
}

// The permission probe never creates a canonical media target. Its random
// 16-byte file is opened exclusively, synced, checked, unlinked and synced.
func importProbeWrite(directory *os.File) error {
	name := ".light-media-preview-" + id("") + ".probe"
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return bad("ROOT_NOT_WRITABLE", "媒体库目录不能独占创建文件，请检查挂载和写入权限")
	}
	file := os.NewFile(uintptr(fd), filepath.Join(directory.Name(), name))
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	locked, err := snapshot(info)
	if err != nil {
		return err
	}
	defer func() {
		var actual unix.Stat_t
		if unix.Fstatat(int(directory.Fd()), name, &actual, unix.AT_SYMLINK_NOFOLLOW) == nil && uint64(actual.Dev) == locked.Device && uint64(actual.Ino) == locked.Inode {
			_ = unix.Unlinkat(int(directory.Fd()), name, 0)
			_ = directory.Sync()
		}
	}()
	if _, err = file.Write([]byte("light-media-test")); err != nil {
		return bad("ROOT_NOT_WRITABLE", "媒体库小文件写入失败")
	}
	if err = file.Sync(); err != nil {
		return bad("ROOT_NOT_WRITABLE", "媒体库文件同步失败")
	}
	if err = file.Close(); err != nil {
		return err
	}
	var current unix.Stat_t
	if err = unix.Fstatat(int(directory.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil || uint64(current.Dev) != locked.Device || uint64(current.Ino) != locked.Inode {
		return bad("REVIEW_ROOT_PROBE", "写入探针指纹发生变化，未删除不明文件")
	}
	if err = unix.Unlinkat(int(directory.Fd()), name, 0); err != nil {
		return bad("ROOT_PROBE_CLEANUP", "自建权限探针清理失败，请检查媒体库目录")
	}
	if err = directory.Sync(); err != nil {
		return bad("ROOT_NOT_WRITABLE", "媒体库目录同步失败")
	}
	return nil
}

func importSpaceEstimate(plan ImportPlan, meta importMetadata) int64 {
	needed := int64(0)
	shared := false
	add := func(value int64) {
		if value < 0 || value > math.MaxInt64-needed {
			needed = math.MaxInt64
		} else {
			needed += value
		}
	}
	for _, file := range plan.Files {
		if file.Status != "ready" {
			continue
		}
		if meta.Methods[file.ID] == "copy" {
			add(file.Size)
		}
		add(64 << 10)
		for _, sidecar := range meta.Sidecars[file.ID] {
			add(sidecar.Snapshot.Size + 4096)
		}
		if !shared && importBool(plan.Scraping, true) {
			add(40 << 20)
			shared = true
		} // two bounded poster/fanart files
		if plan.Media.MediaType == "tv" && importBool(plan.Scraping, true) {
			add(40 << 20)
		} // bounded season poster + still
	}
	return needed
}
