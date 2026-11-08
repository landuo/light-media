package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"light-media/internal/releasemeta/legacyanime"
)

// One host-owned physical temp namespace, shared by plugin base/clone classes.
// It is separate from persistent per-instance Base data paths. No worker input
// selects this root or the immutable startup retention value.
type originalPluginTemporaryState struct {
	path     string
	days     *big.Int
	root     *os.Root
	dataInfo os.FileInfo
	rootInfo os.FileInfo
	now      func() time.Time // trusted clock seam; nil is the actual process clock
	location *time.Location
}

func originalPluginTemporaryDays(lookup func(string) (string, bool)) (*big.Int, bool) {
	raw, present := lookup("TEMP_FILE_DAYS")
	if !present {
		return big.NewInt(3), false
	}
	value, err := legacyanime.Integer(legacyanime.Strip(raw))
	if err != nil {
		return big.NewInt(3), true
	}
	return value, value.String() != raw
}

func (a *App) initializeOriginalPluginTemporary(days *big.Int) error {
	state := originalPluginTemporaryState{path: filepath.Join(a.Dir, "plugin-temp"), location: time.Local}
	normalized := false
	if days == nil {
		state.days, normalized = originalPluginTemporaryDays(os.LookupEnv)
	} else {
		state.days = new(big.Int).Set(days)
	}
	// Reuse the Base owned-directory primitive: fixed components only, never a
	// symbolic-link alias. os.Root is compared with the no-follow descriptor
	// before it is retained; all cleanup traversal is relative to this root.
	dataFD, err := originalBaseOpenDirectory(-1, a.Dir)
	if err != nil {
		return err
	}
	data := os.NewFile(uintptr(dataFD), a.Dir)
	defer data.Close()
	state.dataInfo, err = data.Stat()
	if err != nil {
		return err
	}
	tempFD, err := originalBaseEnsureDirectory(dataFD, "plugin-temp", true)
	if err != nil {
		return err
	}
	temp := os.NewFile(uintptr(tempFD), state.path)
	defer temp.Close()
	state.rootInfo, err = temp.Stat()
	if err != nil {
		return err
	}
	state.root, err = os.OpenRoot(state.path)
	if err != nil {
		return err
	}
	current, err := state.root.Stat(".")
	if err != nil || !os.SameFile(state.rootInfo, current) {
		state.root.Close()
		if err != nil {
			return err
		}
		return bad("PLUGIN_TEMP_ROOT_CHANGED", "插件临时目录身份已改变")
	}
	a.originalPluginTemporary = state
	if normalized {
		a.log("WARNING", "plugins", "temp_days_normalized", "TEMP_FILE_DAYS 已按原启动设置转换规则规范化", "", "")
	}
	return nil
}

func (s *originalPluginTemporaryState) currentRoot() error {
	for _, entry := range []struct {
		path string
		want os.FileInfo
	}{{filepath.Dir(s.path), s.dataInfo}, {s.path, s.rootInfo}} {
		info, err := os.Lstat(entry.path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !os.SameFile(entry.want, info) {
			return bad("PLUGIN_TEMP_ROOT_CHANGED", "插件临时目录身份已改变")
		}
	}
	return nil
}
func (a *App) originalPluginTemporaryPath() (string, error) {
	if a.originalPluginTemporary.root == nil {
		return "", bad("PLUGIN_TEMP_UNAVAILABLE", "插件共享临时目录未初始化")
	}
	if err := a.originalPluginTemporary.currentRoot(); err != nil {
		return "", err
	}
	return a.originalPluginTemporary.path, nil
}
func (a *App) originalPluginTemporaryDaysJSON() json.RawMessage {
	if a.originalPluginTemporary.days == nil {
		return json.RawMessage(`3`)
	}
	return json.RawMessage(a.originalPluginTemporary.days.String())
}

// Called after the existing session/submission/background joins. A maintenance
// error is logged by Close and must not prevent later log/store shutdown.
func (a *App) clearOriginalPluginTemporary() error {
	s := &a.originalPluginTemporary
	if s.root == nil {
		return nil
	}
	defer s.root.Close()
	if err := s.currentRoot(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		} // source Path.exists false
		return err
	}
	now := s.now
	if now == nil {
		now = time.Now
	}
	return originalPluginTemporaryClear(s.root, s.days, now, s.location)
}

func originalTempIgnoredPathError(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EBADF) || errors.Is(err, syscall.ELOOP)
}
func originalTempStatKind(root *os.Root, path string, directory bool) (bool, error) {
	info, err := root.Stat(path)
	if err != nil {
		if originalTempIgnoredPathError(err) {
			return false, nil
		}
		return false, err
	}
	if directory {
		return info.IsDir(), nil
	}
	return info.Mode().IsRegular(), nil
}

// Path.rglob('*') lists wildcard entries, then walks directories. In Python3.12
// wildcard list collection suppresses OSError from both scandir creation and
// iteration; Path.walk suppresses creation errors but propagates iteration
// errors. Owned-root guard errors remain visible in every phase.
func originalTempReadDir(root *os.Root, path string, suppressOpenOS, suppressReadOS bool) ([]os.DirEntry, error) {
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		var errno syscall.Errno
		if suppressOpenOS && errors.As(err, &errno) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		var errno syscall.Errno
		if suppressReadOS && errors.As(err, &errno) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}
func originalTempGlob(root *os.Root, visit func(string) error) error {
	emit := func(dir string) error {
		entries, err := originalTempReadDir(root, dir, true, true)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := visit(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if err := emit("."); err != nil {
		return err
	}
	stack := []string{"."}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := originalTempReadDir(root, dir, true, false)
		if err != nil {
			return err
		}
		dirs := []string{}
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(dir, e.Name()))
			}
		}
		for _, child := range dirs {
			if err := emit(child); err != nil {
				return err
			}
		}
		for i := len(dirs) - 1; i >= 0; i-- {
			stack = append(stack, dirs[i])
		}
	}
	return nil
}

func originalPluginTemporaryClear(root *os.Root, days *big.Int, now func() time.Time, location *time.Location) error {
	if location == nil {
		location = time.Local
	}
	if days == nil {
		days = big.NewInt(3)
	}
	err := originalTempGlob(root, func(path string) error {
		isFile, err := originalTempStatKind(root, path, false)
		if err != nil || !isFile {
			return err
		}
		// Original expression evaluates now() before the second stat/fromtimestamp.
		at := now()
		info, err := root.Stat(path)
		if err != nil {
			return err
		}
		expired, err := originalPluginTempExpired(at, info.ModTime(), days, location)
		if err != nil {
			return err
		}
		if expired {
			return originalTempRemove(root, path, false)
		}
		return nil
	})
	if err != nil {
		return err
	}
	var paths []string
	if err = originalTempGlob(root, func(path string) error { paths = append(paths, path); return nil }); err != nil {
		return err
	}
	sort.Slice(paths, func(i, j int) bool { return originalTempPathLess(paths[j], paths[i]) })
	for _, path := range paths {
		isDir, err := originalTempStatKind(root, path, true)
		if err != nil {
			return err
		}
		if !isDir {
			continue
		}
		entries, err := originalTempReadDir(root, path, false, false)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			if err := originalTempRemove(root, path, true); err != nil {
				return err
			}
		}
	}
	return nil
}
func originalTempRemove(root *os.Root, path string, directory bool) error {
	parent, err := root.OpenFile(filepath.Dir(path), os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer parent.Close()
	flag := 0
	if directory {
		flag = unix.AT_REMOVEDIR
	}
	// unlinkat never follows the final entry and distinguishes unlink/rmdir.
	if err := unix.Unlinkat(int(parent.Fd()), filepath.Base(path), flag); err != nil {
		return &os.PathError{Op: "unlinkat", Path: path, Err: err}
	}
	return nil
}

// Convert stat.st_mtime's binary64 seconds to CPython microseconds first.
// Comparing UTC elapsed time would be wrong across process-local DST changes;
// naive local fields also discard fold/offset information during subtraction.
func originalPluginTempExpired(now, mtime time.Time, days *big.Int, location *time.Location) (bool, error) {
	stamp := float64(mtime.Unix()) + 1e-9*float64(mtime.Nanosecond())
	whole, fraction := math.Modf(stamp)
	rounded := time.Unix(int64(whole), int64(math.RoundToEven(fraction*1e6))*1000).In(location)
	localNow := now.In(location)
	if rounded.Year() < 1 || rounded.Year() > 9999 {
		return false, fmt.Errorf("year %d is out of range", rounded.Year())
	}
	if localNow.Year() < 1 || localNow.Year() > 9999 {
		return false, fmt.Errorf("year %d is out of range", localNow.Year())
	}
	wall := func(t time.Time) int64 {
		v := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1000*1000, time.UTC)
		return v.Unix()*1_000_000 + int64(v.Nanosecond()/1000)
	}
	delta := wall(localNow) - wall(rounded)
	const day = int64(86400 * 1_000_000)
	age := delta / day
	if delta < 0 && delta%day != 0 {
		age--
	}
	return big.NewInt(age).Cmp(days) > 0, nil
}

// PurePath ordering compares components, not the complete slash-joined text.
// Source filesystem strings use surrogateescape for invalid UTF-8 bytes.
func originalTempPathLess(a, b string) bool {
	ap, bp := strings.Split(a, string(filepath.Separator)), strings.Split(b, string(filepath.Separator))
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if c := originalTempFSCompare(ap[i], bp[i]); c != 0 {
			return c < 0
		}
	}
	return len(ap) < len(bp)
}
func originalTempFSCompare(a, b string) int {
	next := func(s string) (rune, int) {
		r, n := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && n == 1 {
			return rune(0xdc00 + int(s[0])), 1
		}
		return r, n
	}
	for a != "" && b != "" {
		ar, an := next(a)
		br, bn := next(b)
		if ar < br {
			return -1
		}
		if ar > br {
			return 1
		}
		a = a[an:]
		b = b[bn:]
	}
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	return 1
}
