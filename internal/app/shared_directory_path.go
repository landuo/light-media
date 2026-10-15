package app

import (
	"fmt"
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
	info, err := opened.Stat()
	opened.Close()
	if err != nil {
		return mountedDirectoryID{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return mountedDirectoryID{}, fmt.Errorf("directory identity unavailable")
	}
	return mountedDirectoryID{uint64(stat.Dev), uint64(stat.Ino)}, nil
}

// Containers may expose the same bind mount at more than one absolute path.
// Resolve only directory identity observed from the filesystem; no downloader
// setting or administrator-maintained string mapping participates.
func sharedDirectoryPath(source, candidate string) (string, bool) {
	return sharedDirectoryPathWithIdentity(source, candidate, mountedDirectoryIdentity)
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
