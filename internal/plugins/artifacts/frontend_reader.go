package artifacts

import (
	"errors"
	"io/fs"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"light-media/internal/plugins"
)

// FrontendReader reuses a full verification for one loaded frontend instance
// on the host-owned local filesystem. Its zero value is ready to use. All
// access is serialized with package installation and replacement by operationMu.
type FrontendReader struct {
	installed Acquired
	verified  []frontendFileStamp
}

// ReadInstalled returns the verified frontend package. The returned data and
// the manifest passed to validate must be treated as read-only.
func (r *FrontendReader) ReadInstalled(root, codeID string, validate func(plugins.Manifest, string) error) (Acquired, error) {
	operationMu.Lock()
	defer operationMu.Unlock()
	return r.readInstalled(root, codeID, validate)
}

func (r *FrontendReader) readInstalled(root, codeID string, validate func(plugins.Manifest, string) error) (Acquired, error) {
	target, err := installedTarget(root, codeID, validate)
	if err != nil {
		return Acquired{}, err
	}
	if target == r.installed.Command.Dir && frontendSnapshotMatches(r.verified) {
		if err = validate(r.installed.Manifest, r.installed.HostContract); err != nil {
			return Acquired{}, err
		}
		return r.installed, nil
	}
	*r = FrontendReader{}
	snapshot := frontendSnapshot(target)
	installed, err := readInstalledTarget(target, validate)
	if err != nil {
		return Acquired{}, err
	}
	if snapshot != nil && installed.Manifest.Frontend != nil {
		if !frontendSnapshotMatches(snapshot) {
			return Acquired{}, errors.New("installed frontend changed during verification")
		}
		r.installed, r.verified = installed, snapshot
	}
	return installed, nil
}

type frontendFileStamp struct {
	path                 string
	device, inode, links uint64
	mode                 uint32
	size                 int64
	mtime, ctime         unix.Timespec
}

// Local filesystems may give successive changes in the same kernel clock tick
// identical ctimes. Verify recent packages in full until their stamps settle.
const frontendStampMinAge = 20 * time.Millisecond

func statFrontendFile(path string) (frontendFileStamp, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return frontendFileStamp{}, err
	}
	return frontendFileStamp{
		path: path, device: uint64(stat.Dev), inode: stat.Ino, links: uint64(stat.Nlink),
		mode: uint32(stat.Mode), size: stat.Size, mtime: stat.Mtim, ctime: stat.Ctim,
	}, nil
}

// Directory stamps detect added and removed entries. File ctime detects writes
// even when the caller restores mtime. A failed or oversized snapshot leaves
// the full verification path available without holding cache state.
func frontendSnapshot(target string) []frontendFileStamp {
	var snapshot []frontendFileStamp
	stableBefore := time.Now().Add(-frontendStampMinAge).UnixNano()
	err := filepath.WalkDir(target, func(path string, _ fs.DirEntry, err error) error {
		if err != nil || len(snapshot) >= 2*MaxArchiveEntries {
			snapshot = nil
			return fs.SkipAll
		}
		stamp, err := statFrontendFile(path)
		if err != nil || stamp.inode == 0 || stamp.ctime == (unix.Timespec{}) || stamp.ctime.Nano() > stableBefore {
			snapshot = nil
			return fs.SkipAll
		}
		snapshot = append(snapshot, stamp)
		return nil
	})
	if err != nil {
		return nil
	}
	return snapshot
}

func frontendSnapshotMatches(snapshot []frontendFileStamp) bool {
	for _, expected := range snapshot {
		actual, err := statFrontendFile(expected.path)
		if err != nil || actual != expected {
			return false
		}
	}
	return len(snapshot) > 0
}
