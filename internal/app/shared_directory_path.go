package app

import (
	"os"
	"path/filepath"
	"syscall"
)

type mountedDirectoryID struct {
	Device uint64
	Inode  uint64
}

func mountedDirectoryIdentity(path string) (mountedDirectoryID, error) {
	opened, err := importDir(path, false)
	if err != nil {
		return mountedDirectoryID{}, err
	}
	defer opened.Close()
	// Every ancestor probe needs only dev/ino. Stat the same no-follow fd
	// directly rather than allocating an os.FileInfo for every probe.
	fd := int(opened.Fd())
	var stat syscall.Stat_t
	for {
		err = syscall.Fstat(fd, &stat)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return mountedDirectoryID{}, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	return mountedDirectoryID{uint64(stat.Dev), uint64(stat.Ino)}, nil
}

// Bind aliases are constant for most of a scan. Cache each ancestor within a
// single operation; the publication path performs a fresh ownership check.
func cachedMountedDirectoryIdentity() func(string) (mountedDirectoryID, error) {
	type result struct {
		id  mountedDirectoryID
		err error
	}
	seen := make(map[string]result)
	return func(path string) (mountedDirectoryID, error) {
		if hit, ok := seen[path]; ok {
			return hit.id, hit.err
		}
		id, err := mountedDirectoryIdentity(path)
		seen[path] = result{id, err}
		return id, err
	}
}

// Containers may expose the same bind mount at more than one absolute path.
// Resolve only directory identity observed from the filesystem; no downloader
// setting or administrator-maintained string mapping participates.
func sharedDirectoryPath(source, candidate string) (string, bool) {
	return sharedDirectoryPathWithIdentity(source, candidate, mountedDirectoryIdentity)
}

// Containment is directional: a daemon's parent directory overlapping a more
// specific rule does not make that rule own the whole parent directory.
func sharedDirectoryContains(source, candidate string) bool {
	return sharedDirectoryContainsWithIdentity(source, candidate, mountedDirectoryIdentity)
}

func sharedDirectoryContainsWithIdentity(source, candidate string, identity func(string) (mountedDirectoryID, error)) bool {
	mapped, relevant := sharedDirectoryPathWithIdentity(source, candidate, identity)
	return relevant && within(source, mapped)
}

func sharedDirectoryPathWithIdentity(source, candidate string, identity func(string) (mountedDirectoryID, error)) (string, bool) {
	if within(source, candidate) || within(candidate, source) {
		return candidate, true
	}
	type ancestor struct {
		path     string
		identity mountedDirectoryID
	}
	ancestors := []ancestor{}
	for path, depth := source, 0; path != "/" && depth < 32; path, depth = filepath.Dir(path), depth+1 {
		info, err := identity(path)
		if err == nil {
			ancestors = append(ancestors, ancestor{path, info})
		}
	}
	for path, depth := candidate, 0; path != "/" && depth < 32; path, depth = filepath.Dir(path), depth+1 {
		info, err := identity(path)
		if err != nil {
			continue
		}
		for _, known := range ancestors {
			if info != known.identity {
				continue
			}
			rel, err := filepath.Rel(path, candidate)
			if err != nil {
				return "", false
			}
			mapped := filepath.Join(known.path, rel)
			if within(source, mapped) || within(mapped, source) {
				return mapped, true
			}
		}
	}
	return "", false
}
