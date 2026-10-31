package app

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// This private port preserves SystemUtils.clear's two passes. Names are relative
// to one already selected owned sandbox, never plugin-supplied filesystem paths.
type originalLocalBackupCleanupPort interface {
	Exists() (bool, error)
	RGlob() ([]string, error)
	IsFile(string) (bool, error)
	MTime(string) (float64, error)
	Unlink(string) error
	IsDir(string) (bool, error)
	IterDir(string) ([]string, error)
	RemoveDir(string) error
}

type originalLocalBackupCleanupClock interface {
	Now() time.Time
	FromTimestamp(float64) (time.Time, error)
}

type originalLocalBackupSystemClock struct{}

func (originalLocalBackupSystemClock) Now() time.Time { return time.Now() }
func (originalLocalBackupSystemClock) FromTimestamp(value float64) (time.Time, error) {
	return originalLocalBackupTimestamp(value, time.Local)
}

// CPython datetime.fromtimestamp rounds the stat float to microseconds, using
// ties-to-even. Keep local civil fields: Python subtracts naive datetimes, not
// UTC instants or a Go monotonic duration (which differ across local DST changes).
func originalLocalBackupTimestamp(value float64, location *time.Location) (time.Time, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < -62135683200 || value > 253402387200 {
		return time.Time{}, errors.New("temporary material timestamp is outside datetime range")
	}
	seconds, fraction := math.Modf(value)
	microseconds := int64(math.RoundToEven(fraction * 1e6))
	stamp := time.Unix(int64(seconds), microseconds*1000).In(location)
	if stamp.Year() < 1 || stamp.Year() > 9999 {
		return time.Time{}, errors.New("temporary material timestamp is outside datetime range")
	}
	return stamp, nil
}

func originalLocalBackupAgeDays(now, modified time.Time) int64 {
	civil := func(value time.Time) time.Time {
		return time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), value.Minute(), value.Second(), value.Nanosecond()/1000*1000, time.UTC)
	}
	now, modified = civil(now), civil(modified)
	seconds := now.Unix() - modified.Unix()
	if now.Nanosecond() < modified.Nanosecond() {
		seconds--
	}
	days := seconds / 86400
	if seconds < 0 && seconds%86400 != 0 {
		days-- // Python timedelta.days floors; Go signed division truncates.
	}
	return days
}

// PurePosixPath ordering compares path components, not the slash-joined string.
func originalLocalBackupPathLess(left, right string) bool {
	a, b := strings.Split(filepath.ToSlash(left), "/"), strings.Split(filepath.ToSlash(right), "/")
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func clearOriginalLocalBackupPort(port originalLocalBackupCleanupPort, clock originalLocalBackupCleanupClock, days int) error {
	exists, err := port.Exists()
	if err != nil || !exists {
		return err
	}
	files, err := port.RGlob()
	if err != nil {
		return err
	}
	for _, name := range files {
		isFile, err := port.IsFile(name)
		if err != nil {
			return err
		}
		if !isFile {
			continue
		}
		now := clock.Now() // Original evaluation order: now(), stat(), fromtimestamp().
		stamp, err := port.MTime(name)
		if err != nil {
			return err
		}
		modified, err := clock.FromTimestamp(stamp)
		if err != nil {
			return err
		}
		if originalLocalBackupAgeDays(now, modified) > int64(days) {
			if err := port.Unlink(name); err != nil {
				return err
			}
		}
	}
	folders, err := port.RGlob()
	if err != nil {
		return err
	}
	sort.Slice(folders, func(i, j int) bool { return originalLocalBackupPathLess(folders[j], folders[i]) })
	for _, name := range folders {
		isDir, err := port.IsDir(name)
		if err != nil {
			return err
		}
		if !isDir {
			continue
		}
		children, err := port.IterDir(name)
		if err != nil {
			return err
		}
		if len(children) == 0 {
			if err := port.RemoveDir(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// Settings.TEMP_FILE_DAYS defaults to 3 in fixed source config.py:176. The
// optional persisted setting is a host input, not a plugin filesystem grant.
func (a *App) originalLocalTorrentBackupTempDays() int {
	if configured := a.Settings().TempFileDays; configured != nil {
		return *configured
	}
	return 3
}

// clearOriginalLocalTorrentBackup is a shutdown-only private operation. The
// caller must first close admission and join a.wg, so a material's write+read
// cannot be split by age cleanup. The caller catches/logs errors, as source
// stop_modules.run_step does; a cleanup failure must not abort the rest of Close.
// days is explicit: unset/default configuration is resolved by the caller, not
// by treating a deliberate zero or negative value as the source default of 3.
func (a *App) clearOriginalLocalTorrentBackup(days int) error {
	if a == nil || !importPath(a.Dir) || a.Dir == "/" {
		return bad("INVALID_PATH", "临时材料缺少宿主私有目录")
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	port := &originalLocalBackupCleanupFS{
		root:     filepath.Join(a.Dir, "temp", "plugin-torrent-backup"),
		observed: make(map[string]os.FileInfo),
	}
	defer func() {
		if port.held != nil {
			_ = port.held.Close()
		}
	}()
	return clearOriginalLocalBackupPort(port, originalLocalBackupSystemClock{}, days)
}

type originalLocalBackupCleanupFS struct {
	root     string
	held     *os.File
	observed map[string]os.FileInfo
}

func (p *originalLocalBackupCleanupFS) Exists() (bool, error) {
	dir, err := importDir(p.root, false) // Missing sandbox is not created.
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	p.held = dir
	return true, nil
}

func (p *originalLocalBackupCleanupFS) checkRoot() error {
	if p.held == nil {
		return bad("INVALID_PATH", "临时材料目录尚未固定")
	}
	return sharedLocalStorageSameDirectory(p.root, p.held)
}

func (p *originalLocalBackupCleanupFS) path(name string) (string, error) {
	path := filepath.Join(p.root, name)
	if name == "" || name == "." || filepath.IsAbs(name) || filepath.Clean(name) != name || !within(p.root, path) || path == p.root || !importPath(path) || browserInternal(name) {
		return "", bad("INVALID_PATH", "临时材料只能位于专属私有目录")
	}
	if err := p.checkRoot(); err != nil {
		return "", err
	}
	return path, nil
}

func (p *originalLocalBackupCleanupFS) stat(name string) (os.FileInfo, error) {
	path, err := p.path(name)
	if err != nil {
		return nil, err
	}
	parent, info, err := sharedLocalStorageStat(path)
	if parent != nil {
		_ = parent.Close()
	}
	if err != nil {
		return nil, err
	}
	if err := p.checkRoot(); err != nil {
		return nil, err
	}
	return info, nil
}

func (p *originalLocalBackupCleanupFS) RGlob() ([]string, error) {
	if err := p.checkRoot(); err != nil {
		return nil, err
	}
	out := []string{}
	scan := func(relative string) ([]string, error) {
		path := p.root
		if relative != "" {
			var err error
			path, err = p.path(relative)
			if err != nil {
				return nil, err
			}
		}
		dir, err := importDir(path, false)
		if err != nil {
			return nil, err
		}
		entries, err := dir.ReadDir(-1)
		if err == nil {
			err = sharedLocalStorageSameDirectory(path, dir)
		}
		_ = dir.Close()
		if err != nil {
			return nil, err
		}
		directories := []string{}
		for _, entry := range entries {
			name := filepath.Join(relative, entry.Name())
			info, err := p.stat(name)
			if err != nil {
				return nil, err
			}
			out = append(out, name)
			if info.IsDir() {
				directories = append(directories, name)
			}
		}
		return directories, nil
	}
	// Bound Python 3.12 pathlib._RecursiveWildcardSelector yields root,
	// then each DFS walk parent's immediate directory children as one batch.
	// With root/a, root/b and a/x, starting points are root,a,b,a/x (not
	// root,a,a/x,b). Preserve each provider's entry order within those batches.
	var walkChildren func([]string) error
	walkChildren = func(children []string) error {
		next := make([][]string, len(children))
		for i, child := range children {
			var err error
			next[i], err = scan(child)
			if err != nil {
				return err
			}
		}
		for _, grandchildren := range next {
			if err := walkChildren(grandchildren); err != nil {
				return err
			}
		}
		return nil
	}
	children, err := scan("")
	if err != nil {
		return nil, err
	}
	if err := walkChildren(children); err != nil {
		return nil, err
	}
	return out, p.checkRoot()
}

func (p *originalLocalBackupCleanupFS) IsFile(name string) (bool, error) {
	info, err := p.stat(name)
	return err == nil && info.Mode().IsRegular(), err
}

func (p *originalLocalBackupCleanupFS) MTime(name string) (float64, error) {
	info, err := p.stat(name)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, bad("INVALID_FILE", "临时材料类型已变化")
	}
	p.observed[name] = info
	stamp := info.ModTime()
	// Same two-step stat float projection as S09 originalLocalFileItemFromStat.
	return float64(stamp.Unix()) + float64(float64(stamp.Nanosecond())*1e-9), nil
}

func (p *originalLocalBackupCleanupFS) IsDir(name string) (bool, error) {
	info, err := p.stat(name)
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		p.observed[name] = info
	}
	return info.IsDir(), nil
}

func (p *originalLocalBackupCleanupFS) IterDir(name string) ([]string, error) {
	path, err := p.path(name)
	if err != nil {
		return nil, err
	}
	dir, err := importDir(path, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	if err := sharedLocalStorageSameDirectory(path, dir); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out, p.checkRoot()
}

func (p *originalLocalBackupCleanupFS) remove(name string, directory bool) error {
	path, err := p.path(name)
	if err != nil {
		return err
	}
	parent, info, err := sharedLocalStorageStat(path)
	if parent != nil {
		defer parent.Close()
	}
	if err != nil {
		return err
	}
	before := p.observed[name]
	if before == nil || !os.SameFile(before, info) || info.IsDir() != directory || !directory && (!info.Mode().IsRegular() || info.Size() != before.Size() || !info.ModTime().Equal(before.ModTime())) {
		return bad("FILE_CHANGED", "临时材料在清理前已变化")
	}
	if err := sharedLocalStorageSameDirectory(filepath.Dir(path), parent); err != nil {
		return err
	}
	if err := p.checkRoot(); err != nil {
		return err
	}
	flags := 0
	if directory {
		flags = unix.AT_REMOVEDIR
	}
	return unix.Unlinkat(int(parent.Fd()), filepath.Base(path), flags)
}

func (p *originalLocalBackupCleanupFS) Unlink(name string) error    { return p.remove(name, false) }
func (p *originalLocalBackupCleanupFS) RemoveDir(name string) error { return p.remove(name, true) }
