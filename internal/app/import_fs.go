package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type importSnapshot struct {
	Directory string `json:"directory,omitempty"`
	Device    uint64 `json:"device"`
	Inode     uint64 `json:"inode"`
	Size      int64  `json:"size"`
	ModNS     int64  `json:"mod_ns"`
}

func snapshot(info os.FileInfo) (importSnapshot, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return importSnapshot{}, bad("INVALID_FILE", "只允许普通文件")
	}
	return importSnapshot{Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Size: info.Size(), ModNS: info.ModTime().UnixNano()}, nil
}

func (s importSnapshot) matches(info os.FileInfo) bool {
	actual, err := snapshot(info)
	return err == nil && actual == s
}

func planSnapshot(file PlanFile) importSnapshot {
	return importSnapshot{Device: file.Device, Inode: file.Inode, Size: file.Size, ModNS: file.ModNS, Directory: file.DirectoryDigest}
}

func within(root, target string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(target) {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func isVideo(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mp4", ".m4v", ".avi", ".ts", ".mov", ".m2ts":
		return true
	}
	return false
}

func importPath(name string) bool {
	if name == "" || !filepath.IsAbs(name) || filepath.Clean(name) != name || len(name) > 4096 || !utf8.ValidString(name) || strings.ContainsRune(name, '\\') {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Every directory component is opened relative to its already-open parent with
// O_NOFOLLOW. A path can never be redirected through a symlink during a walk.
func importDir(name string, create bool) (*os.File, error) {
	if !importPath(name) {
		return nil, bad("INVALID_PATH", "目录必须是规范的绝对路径")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(name, "/"), "/") {
		if part == "" {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil && create && errors.Is(openErr, unix.ENOENT) {
			if makeErr := unix.Mkdirat(fd, part, 0750); makeErr != nil && !errors.Is(makeErr, unix.EEXIST) {
				unix.Close(fd)
				return nil, makeErr
			}
			next, openErr = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if openErr == nil {
				_ = unix.Fsync(fd)
			}
		}
		unix.Close(fd)
		if openErr != nil {
			if errors.Is(openErr, unix.ENOENT) {
				return nil, os.ErrNotExist
			}
			// Keep the public guard/error code while allowing a concrete source
			// module caller to distinguish an OS permission failure from a link
			// or path authorization rejection.
			return nil, fmt.Errorf("%w: %w", bad("INVALID_PATH", "目录不可访问或包含符号链接"), openErr)
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), name), nil
}

func importOpen(name string) (*os.File, error) {
	if !importPath(name) {
		return nil, bad("INVALID_PATH", "文件路径无效")
	}
	parent, err := importDir(filepath.Dir(name), false)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(name), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, bad("INVALID_FILE", "源文件不是普通文件或包含符号链接")
	}
	return file, nil
}

func snapshotPath(name string) (importSnapshot, error) {
	file, err := importOpen(name)
	if err != nil {
		return importSnapshot{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return importSnapshot{}, err
	}
	return snapshot(info)
}

func importCopy(ctx context.Context, source io.Reader, target io.Writer, check func() error) (string, error) {
	hash := sha256.New()
	buffer := make([]byte, 256<<10)
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if check != nil {
			if err := check(); err != nil {
				return "", err
			}
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			written, writeErr := target.Write(buffer[:count])
			if writeErr != nil {
				return "", writeErr
			}
			if written != count {
				return "", io.ErrShortWrite
			}
			hash.Write(buffer[:count])
		}
		if readErr == io.EOF {
			return hex.EncodeToString(hash.Sum(nil)), nil
		}
		if readErr != nil {
			return "", readErr
		}
	}
}

func importName(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, value)
	value = strings.Trim(strings.Join(strings.Fields(value), " "), " .")
	runes := []rune(value)
	if len(runes) > 100 {
		value = string(runes[:100])
	}
	return value
}

func (a *App) importRoot(key string) (Root, error) {
	for _, root := range a.Settings().Roots {
		if root.ID == key {
			if !importPath(root.Path) || root.Path == "/" {
				return Root{}, bad("INVALID_ROOT", "媒体库路径无效")
			}
			return root, nil
		}
	}
	return Root{}, bad("INVALID_ROOT", "请选择已配置的媒体库目录")
}

func (a *App) sourceAllowed(name string) bool {
	if !importPath(name) {
		return false
	}
	for _, root := range a.Settings().AllowedRoots {
		if within(root, name) {
			return true
		}
	}
	return false
}
