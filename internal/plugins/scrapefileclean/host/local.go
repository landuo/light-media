package scrapehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins/scrapefileclean"
)

func validPath(p string) bool {
	if p == "" || len(p) > 4096 || !utf8.ValidString(p) || !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.Contains(p, "\\") {
		return false
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func inside(root, p string) bool {
	if !validPath(p) {
		return false
	}
	r, e := filepath.Rel(root, p)
	return e == nil && r != ".." && !strings.HasPrefix(r, "../")
}
func openDir(p string) (*os.File, error) {
	if !validPath(p) {
		return nil, ErrUnsafe
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), p), nil
}
func stamp(info os.FileInfo) (Stamp, error) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (!info.Mode().IsRegular() && !info.IsDir()) {
		return Stamp{}, ErrUnsafe
	}
	return Stamp{uint64(s.Dev), uint64(s.Ino), info.Size(), info.ModTime().UnixNano(), uint32(info.Mode())}, nil
}
func atStamp(f *os.File, name string) (Stamp, error) {
	fd, e := unix.Openat(int(f.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return Stamp{}, e
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return Stamp{}, e
	}
	return stamp(info)
}
func fileStamp(f *os.File) (Stamp, error) {
	i, e := f.Stat()
	if e != nil {
		return Stamp{}, e
	}
	return stamp(i)
}
func sameEntity(a, b Stamp) bool { return a.Dev == b.Dev && a.Inode == b.Inode && a.Mode == b.Mode }
func digest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type localOps struct {
	h            *Host
	roots        []RootStamp
	pins, frozen map[string]Pin
	serial       uint64
	ctx          context.Context
	scope        string
	errors       []error
}

func newLocal(h *Host, roots []Root) (*localOps, error) {
	o := &localOps{h: h, pins: map[string]Pin{}, frozen: map[string]Pin{}}
	ids := map[string]bool{}
	for _, r := range roots {
		if r.ID == "" || ids[r.ID] || !validPath(r.Path) || r.Path == "/" || (r.Role != "source" && r.Role != "media") {
			return nil, ErrUnsafe
		}
		ids[r.ID] = true
		for _, p := range []string{"/etc", "/proc", "/sys", "/dev", "/var/run"} {
			if inside(p, r.Path) {
				return nil, ErrUnsafe
			}
		}
		f, e := openDir(r.Path)
		if e != nil {
			return nil, e
		}
		s, e := fileStamp(f)
		f.Close()
		if e != nil {
			return nil, e
		}
		for _, old := range o.roots {
			if sameEntity(old.File, s) || inside(old.Root.Path, r.Path) || inside(r.Path, old.Root.Path) {
				return nil, errors.New("overlapping authority roots are ambiguous")
			}
		}
		o.roots = append(o.roots, RootStamp{r, s})
	}
	return o, nil
}
func (o *localOps) root(p string) (RootStamp, error) {
	for _, r := range o.roots {
		if inside(r.Root.Path, p) {
			f, e := openDir(r.Root.Path)
			if e != nil {
				return r, fmt.Errorf("%w: authority root unavailable: %v", ErrUnsafe, e)
			}
			s, e := fileStamp(f)
			f.Close()
			if e != nil || !sameEntity(s, r.File) {
				return r, ErrUnsafe
			}
			return r, nil
		}
	}
	return RootStamp{}, ErrUnsafe
}
func (o *localOps) openWatchDir(p string) (*os.File, error) {
	if _, e := o.root(p); e != nil {
		return nil, e
	}
	return openDir(p)
}
func (o *localOps) capture(p string) (Pin, error) {
	r, e := o.root(p)
	if e != nil {
		return Pin{}, e
	}
	if p == r.Root.Path {
		f, e := openDir(p)
		if e != nil {
			return Pin{}, e
		}
		defer f.Close()
		s, e := fileStamp(f)
		return Pin{RootID: r.Root.ID, ParentPath: p, Parent: s, File: s}, e
	}
	parent, e := openDir(filepath.Dir(p))
	if e != nil {
		return Pin{}, e
	}
	defer parent.Close()
	ps, e := fileStamp(parent)
	if e != nil {
		return Pin{}, e
	}
	s, e := atStamp(parent, filepath.Base(p))
	if e != nil {
		return Pin{}, e
	}
	return Pin{RootID: r.Root.ID, ParentPath: filepath.Dir(p), Parent: ps, File: s}, nil
}
func (o *localOps) pin(p string, replace bool) (Pin, error) {
	got, e := o.capture(p)
	if e != nil {
		return Pin{}, e
	}
	if old, ok := o.pins[p]; ok && !replace {
		return old, nil
	}
	o.serial++
	got.Epoch = o.serial
	o.pins[p] = got
	return got, nil
}
func (o *localOps) remember(e error) {
	if e != nil {
		o.errors = append(o.errors, e)
	}
}
func (o *localOps) Exists(p string) bool {
	_, e := o.capture(p)
	if e != nil && !errors.Is(e, fs.ErrNotExist) {
		o.remember(e)
		return true // Inability to prove absence must never authorize cleanup.
	}
	return e == nil
}
func (o *localOps) IsDir(p string) bool {
	pin, e := o.capture(p)
	if e != nil {
		if !errors.Is(e, fs.ErrNotExist) {
			o.remember(e)
		}
		return false
	}
	return os.FileMode(pin.File.Mode).IsDir()
}
func (o *localOps) IsSymlink(p string) bool {
	if _, e := o.root(p); e != nil {
		o.remember(e)
		return true
	}
	info, e := os.Lstat(p)
	return e == nil && info.Mode()&os.ModeSymlink != 0
}
func (o *localOps) Stat(p string) (scrapefileclean.Identity, error) {
	s, e := o.pin(p, true)
	return scrapefileclean.Identity{Dev: s.File.Dev, Inode: s.File.Inode}, e
}
func (o *localOps) Children(p string) ([]scrapefileclean.Entry, error) {
	if _, e := o.root(p); e != nil {
		return nil, e
	}
	f, e := openDir(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	rows, e := f.Readdir(-1)
	if e != nil {
		return nil, e
	}
	out := []scrapefileclean.Entry{}
	for _, row := range rows {
		name := filepath.Join(p, row.Name())
		if strings.HasPrefix(row.Name(), ".light-media-scrape-") {
			continue
		}
		if row.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !row.IsDir() && !row.Mode().IsRegular() {
			continue
		}
		if _, e = o.pin(name, false); e != nil {
			return nil, e
		}
		out = append(out, scrapefileclean.Entry{Path: name, Dir: row.IsDir()})
	}
	return out, nil
}
func (o *localOps) Scan(p string) ([]scrapefileclean.Tracked, error) {
	var out []scrapefileclean.Tracked
	var walk func(string) error
	walk = func(path string) error {
		if o.ctx != nil && o.ctx.Err() != nil {
			return o.ctx.Err()
		}
		rows, e := o.Children(path)
		if e != nil {
			o.remember(e)
			return e
		}
		for _, row := range rows {
			if !row.Dir {
				id, e := o.Stat(row.Path)
				if e != nil {
					o.remember(e)
					return e
				}
				out = append(out, scrapefileclean.Tracked{Path: row.Path, Identity: id})
			}
		}
		for _, row := range rows {
			if row.Dir {
				if e = walk(row.Path); e != nil {
					return e
				}
			}
		}
		return nil
	}
	e := walk(p)
	return out, e
}

func (o *localOps) expected(p string, dir bool) (Pin, error) {
	s, ok := o.frozen[p]
	if !ok {
		s, ok = o.pins[p]
	}
	if !ok {
		return Pin{}, ErrUnsafe
	}
	if dir {
		current, e := o.capture(p)
		if e != nil {
			return Pin{}, e
		}
		if !sameEntity(current.File, s.File) || !sameEntity(current.Parent, s.Parent) {
			return Pin{}, ErrUnsafe
		}
		current.Epoch = s.Epoch
		return current, nil
	}
	return s, nil
}
func (o *localOps) Unlink(p string) error    { return o.remove(p, false) }
func (o *localOps) RemoveDir(p string) error { return o.remove(p, true) }
func (o *localOps) RemoveTree(p string) error {
	// Freeze the entire selected tree before the first effect. Files that appear
	// after this plan cannot enter a later recursive traversal and be consumed.
	type selected struct {
		path string
		dir  bool
	}
	var plan []selected
	var walk func(string) error
	walk = func(path string) error {
		rows, e := o.Children(path)
		if e != nil {
			return e
		}
		for _, r := range rows {
			if r.Dir {
				if e = walk(r.Path); e != nil {
					return e
				}
			} else {
				plan = append(plan, selected{r.Path, false})
			}
		}
		plan = append(plan, selected{path, true})
		return nil
	}
	if e := walk(p); e != nil {
		return e
	}
	for _, item := range plan {
		if e := o.remove(item.path, item.dir); e != nil {
			return e
		}
	}
	return nil
}

func (o *localOps) fault(phase, p string) error {
	if o.h.opts.fault != nil {
		return o.h.opts.fault(phase, p)
	}
	return nil
}
func (o *localOps) storeEffect(x Effect) error {
	changed := false
	err := o.h.opts.Journal.Update(o.ctx, func(s *Durable) error {
		old, exists := s.Effects[x.ID]
		changed = !exists || old.State != x.State
		s.Effects[x.ID] = x
		return nil
	})
	if err == nil && changed {
		switch x.State {
		case "confirmed":
			o.h.log(o.ctx, "INFO", "effect_"+x.Kind, "已确认"+sourceEffectName(x.Kind)+"："+x.Path)
		case "uncertain":
			o.h.log(o.ctx, "ERROR", "effect_uncertain", "文件操作结果尚未确认，停止重试："+x.Path+"；"+x.Error)
		}
	}
	return err
}
func (o *localOps) remove(p string, dir bool) error {
	if o.ctx == nil {
		return ErrUnsafe
	}
	if e := o.ctx.Err(); e != nil {
		return e
	}
	r, e := o.root(p)
	if e != nil {
		return e
	}
	if p == r.Root.Path {
		return ErrUnsafe
	}
	pin, e := o.expected(p, dir)
	if e != nil {
		return e
	}
	if pin.RootID != r.Root.ID {
		return ErrUnsafe
	}
	kind := "unlink"
	if dir {
		kind = "rmdir"
	}
	id := digest([]any{o.h.binding, o.scope, kind, p, pin})
	saved, e := o.h.opts.Journal.Read(o.ctx)
	if e != nil {
		return e
	}
	effect, exists := saved.Effects[id]
	if exists && effect.State == "confirmed" {
		return nil
	}
	if exists && (effect.State == "uncertain" || effect.State == "failed") {
		return ErrUncertain
	}
	if !exists {
		effect = Effect{ID: id, Scope: o.scope, Kind: kind, Path: p, Role: r.Root.Role, Binding: o.h.binding, Expected: pin, Stage: ".light-media-scrape-" + id[:32] + ".part", State: "planned"}
		if e = o.storeEffect(effect); e != nil {
			return e
		}
	}
	parent, e := openDir(pin.ParentPath)
	if e != nil {
		return e
	}
	defer parent.Close()
	parentOK := func() error {
		if _, e := o.root(p); e != nil {
			return e
		}
		current, e := openDir(pin.ParentPath)
		if e != nil {
			return fmt.Errorf("%w: canonical parent unavailable: %v", ErrUnsafe, e)
		}
		defer current.Close()
		a, e := fileStamp(current)
		if e != nil || !sameEntity(a, pin.Parent) {
			return ErrUnsafe
		}
		a, e = fileStamp(parent)
		if e != nil || !sameEntity(a, pin.Parent) {
			return ErrUnsafe
		}
		return nil
	}
	if e = parentOK(); e != nil {
		return e
	}
	stage, e := atStamp(parent, effect.Stage)
	if errors.Is(e, fs.ErrNotExist) {
		if effect.State == "staged" {
			effect.State = "uncertain"
			effect.Error = "staged entity disappeared before confirmed receipt"
			_ = o.storeEffect(effect)
			return ErrUncertain
		}
		var actual Stamp
		actual, e = atStamp(parent, filepath.Base(p))
		if e != nil {
			return e
		}
		if actual != pin.File {
			return ErrUnsafe
		}
		if e = o.fault("before_rename", p); e != nil {
			return e
		}
		if e = parentOK(); e != nil {
			return e
		}
		if e = o.ctx.Err(); e != nil {
			return e
		}
		if e = renameExclusive(int(parent.Fd()), filepath.Base(p), effect.Stage); e != nil {
			return e
		}
		if e = parent.Sync(); e != nil {
			return e
		}
		stage, e = atStamp(parent, effect.Stage)
	}
	if e != nil {
		restore := renameExclusive(int(parent.Fd()), effect.Stage, filepath.Base(p))
		_ = parent.Sync()
		effect.State = "uncertain"
		effect.Error = fmt.Sprintf("quarantine unavailable=%v restore=%v", e, restore)
		_ = o.storeEffect(effect)
		return fmt.Errorf("%w: quarantine unavailable: %v", ErrUncertain, e)
	}
	if stage != pin.File {
		restore := renameExclusive(int(parent.Fd()), effect.Stage, filepath.Base(p))
		_ = parent.Sync()
		effect.State = "uncertain"
		effect.Error = "quarantine identity mismatch; restore=" + fmt.Sprint(restore)
		_ = o.storeEffect(effect)
		return ErrUnsafe
	}
	if e = parentOK(); e != nil {
		effect.State = "uncertain"
		effect.Error = "canonical parent changed"
		_ = o.storeEffect(effect)
		return e
	}
	effect.State = "staged"
	if e = o.storeEffect(effect); e != nil {
		return e
	}
	if e = o.fault("after_stage", p); e != nil {
		return e
	}
	if e = parentOK(); e != nil {
		return e
	}
	// A private quarantine is checked again immediately before unlink. App's
	// write lease must also exclude its own concurrent filesystem mutations.
	actual, e := atStamp(parent, effect.Stage)
	if e != nil || actual != pin.File {
		return ErrUnsafe
	}
	flags := 0
	if dir {
		flags = unix.AT_REMOVEDIR
	}
	if e = o.ctx.Err(); e != nil {
		return e
	}
	if e = unix.Unlinkat(int(parent.Fd()), effect.Stage, flags); e != nil {
		restore := renameExclusive(int(parent.Fd()), effect.Stage, filepath.Base(p))
		_ = parent.Sync()
		effect.State = "failed"
		effect.Error = fmt.Sprintf("unlink=%v restore=%v", e, restore)
		_ = o.storeEffect(effect)
		return e
	}
	if e = parent.Sync(); e != nil {
		return e
	}
	if e = o.fault("after_unlink", p); e != nil {
		return e
	}
	effect.State = "confirmed"
	return o.storeEffect(effect)
}
func (o *localOps) side(kind, p string, payload any) (bool, error) {
	r, e := o.root(p)
	if e != nil {
		return false, e
	}
	x := Effect{Scope: o.scope, Kind: kind, Path: p, Role: r.Root.Role, Binding: o.h.binding}
	if expected, ok := o.frozen[p]; ok {
		x.Expected = expected
	} else if expected, ok := o.pins[p]; ok {
		x.Expected = expected
	}
	x.Payload, e = json.Marshal(payload)
	if e != nil {
		return false, e
	}
	x.ID = digest([]any{o.h.binding, o.scope, kind, p, string(x.Payload)})
	s, e := o.h.opts.Journal.Read(o.ctx)
	if e != nil {
		return false, e
	}
	old, ok := s.Effects[x.ID]
	if ok && old.State == "confirmed" {
		return old.Result, nil
	}
	if ok && old.State == "uncertain" {
		return false, ErrUncertain
	}
	x.State = "planned"
	if e = o.storeEffect(x); e != nil {
		return false, e
	}
	result, e := o.h.opts.Effects.Commit(o.ctx, x)
	if e != nil {
		x.State = "uncertain"
		x.Error = e.Error()
		_ = o.storeEffect(x)
		return false, e
	}
	x.State = "confirmed"
	x.Result = result
	return result, o.storeEffect(x)
}
func (o *localOps) History(kind, p string) (bool, error) { return o.side("history_"+kind, p, nil) }
func (o *localOps) Event(p string) error {
	ok, e := o.side("download_file_deleted", p, map[string]string{"src": p})
	if e == nil && !ok {
		return errors.New("download deletion event was not committed")
	}
	return e
}
func (o *localOps) Notice(title, text string) error {
	// Notification ownership is the host scope; no worker can choose a recipient.
	p := o.roots[0].Root.Path
	ok, e := o.side("notification", p, map[string]string{"title": title, "text": text})
	if e == nil && !ok {
		return errors.New("notification outbox was not committed")
	}
	return e
}
func rootsEqual(a, b []RootStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Root != b[i].Root || !sameEntity(a[i].File, b[i].File) {
			return false
		}
	}
	return true
}

var _ scrapefileclean.Ops = (*localOps)(nil)
