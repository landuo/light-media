package cleanuphost

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
	"light-media/internal/plugins/removelink"
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
	h                *Host
	roots            []RootStamp
	pins, frozen     map[string]Pin
	serial           uint64
	ctx              context.Context
	scope            string
	errors           []error
	noticeOrdinal    int
	noticeFatal      error
	barrierSelection map[string]Pin
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
	if len(o.roots) == 0 {
		return nil, ErrUnsafe
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
	if o.barrierSelection != nil {
		selected, ok := o.barrierSelection[p]
		if !ok || !sameEntity(got.File, selected.File) || !sameEntity(got.Parent, selected.Parent) || (!os.FileMode(got.File.Mode).IsDir() && got.File != selected.File) {
			return Pin{}, ErrUnsafe
		}
		// A resumed source call cannot enlarge its pre-notification selection.
		return selected, nil
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
func (o *localOps) Stat(p string) (removelink.Identity, error) {
	s, e := o.pin(p, true)
	return removelink.Identity{Dev: s.File.Dev, Inode: s.File.Inode}, e
}
func (o *localOps) Children(p string) ([]removelink.Entry, error) {
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
	out := []removelink.Entry{}
	for _, row := range rows {
		name := filepath.Join(p, row.Name())
		if strings.HasPrefix(row.Name(), ".light-media-rmlink-") {
			continue
		}
		if row.Mode()&os.ModeSymlink != 0 {
			return nil, ErrUnsafe
		}
		if !row.IsDir() && !row.Mode().IsRegular() {
			return nil, ErrUnsafe
		}
		if _, e = o.pin(name, false); e != nil {
			return nil, e
		}
		out = append(out, removelink.Entry{Path: name, Dir: row.IsDir()})
	}
	return out, nil
}
func (o *localOps) Scan(p string) ([]removelink.Tracked, error) {
	var out []removelink.Tracked
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
			if !row.Dir && !removelink.Trickplay(row.Path) {
				id, e := o.Stat(row.Path)
				if e != nil {
					o.remember(e)
					return e
				}
				out = append(out, removelink.Tracked{Path: row.Path, Identity: id})
			}
		}
		for _, row := range rows {
			if row.Dir && !removelink.Trickplay(row.Path) {
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
	return o.h.opts.Journal.Update(o.ctx, func(s *Durable) error { s.Effects[x.ID] = x; return nil })
}
func (o *localOps) remove(p string, dir bool) error {
	if o.noticeFatal != nil {
		return o.noticeFatal
	}
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
		effect = Effect{ID: id, Scope: o.scope, Kind: kind, Path: p, Role: r.Root.Role, Binding: o.h.binding, Expected: pin, Stage: ".light-media-rmlink-" + id[:32] + ".part", State: "planned"}
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
	return o.sideAt(kind, p, payload, "")
}
func (o *localOps) sideAt(kind, p string, payload any, site string) (result bool, err error) {
	if o.noticeFatal != nil {
		return false, o.noticeFatal
	}
	// Unknown notice outcomes poison the whole action, even when the source
	// catches an exception and a timer/stop loop advances to its next task.
	defer func() {
		if site != "" && err != nil {
			var source *removelink.SourceNoticeError
			if !errors.As(err, &source) || !source.Valid() {
				o.noticeFatal = err
				o.remember(err)
			}
		}
	}()
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
	legacyID := x.ID
	if site != "" {
		o.noticeOrdinal++
		x.SourceBarrierVersion, x.NoticeSite, x.NoticeOrdinal = 1, site, o.noticeOrdinal
		x.ID = digest([]any{o.h.binding, o.scope, kind, p, string(x.Payload), x.SourceBarrierVersion, x.NoticeSite, x.NoticeOrdinal})
	}
	s, e := o.h.opts.Journal.Read(o.ctx)
	if e != nil {
		return false, e
	}
	old, ok := s.Effects[x.ID]
	// Old confirmed notifications retain the historical outbox-only semantics.
	// Never retroactively route an already committed legacy notification.
	if !ok && site != "" {
		if legacy, exists := s.Effects[legacyID]; exists && legacy.State == "confirmed" && legacy.SourceBarrierVersion == 0 {
			return legacy.Result, nil
		}
	}
	if ok && old.State == "confirmed" {
		if old.SourceBarrierVersion == 1 {
			if reader, ok := o.h.opts.Effects.(interface {
				SourceNoticeResult(context.Context, Effect) error
			}); ok {
				receiptErr := reader.SourceNoticeResult(o.ctx, old)
				var source *removelink.SourceNoticeError
				matches := receiptErr == nil && old.SourceError == ""
				if errors.As(receiptErr, &source) && source.Valid() && old.SourceError == source.Class {
					matches = true
				}
				if !matches || !old.Result {
					return false, ErrUncertain
				}
			}
		}

		if old.SourceBarrierVersion == 1 && old.SourceError != "" {
			source := &removelink.SourceNoticeError{Class: old.SourceError}
			if !old.Result || !source.Valid() {
				return false, ErrUncertain
			}
			return old.Result, source
		}
		return old.Result, nil
	}
	if ok && old.State == "uncertain" {
		return false, ErrUncertain
	}
	x.State = "planned"
	if e = o.storeEffect(x); e != nil {
		return false, e
	}
	result, e = o.h.opts.Effects.Commit(o.ctx, x)
	if e != nil {
		var source *removelink.SourceNoticeError
		if site != "" && result && errors.As(e, &source) && source.Valid() {
			x.State, x.Result, x.SourceError = "confirmed", result, source.Class
			if save := o.storeEffect(x); save != nil {
				return false, save
			}
			return result, source
		}
		x.State = "uncertain"
		x.Error = e.Error()
		_ = o.storeEffect(x)
		if site != "" {
			return false, fmt.Errorf("%w: notification outcome: %v", ErrUncertain, e)
		}
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
func (o *localOps) NoticeAt(site, title, text string) error {
	switch site {
	case "empty_directory", "hardlink_immediate", "hardlink_delayed", "strm":
	default:
		return ErrUnsafe
	}
	if o.barrierSelection == nil {
		o.barrierSelection = make(map[string]Pin, len(o.pins)+len(o.frozen))
		for path, pin := range o.pins {
			o.barrierSelection[path] = pin
		}
		for path, pin := range o.frozen {
			o.barrierSelection[path] = pin
		}
	}

	p := o.roots[0].Root.Path
	ok, err := o.sideAt("notification", p, map[string]string{"title": title, "text": text}, site)
	if err == nil && !ok {
		err = errors.New("notification source return was not committed")
		o.noticeFatal = err
		o.remember(err)
	}
	return err
}

func (o *localOps) MediaExtensions() []string {
	return append([]string(nil), o.h.opts.MediaExtensions...)
}
func (o *localOps) StorageExists(x removelink.Item) (bool, error) {
	if x.Storage != "local" {
		return false, ErrUnsupported
	}
	_, e := o.capture(strings.TrimSuffix(x.Path, "/"))
	if errors.Is(e, fs.ErrNotExist) {
		return false, nil
	}
	return e == nil, e
}
func (o *localOps) StorageList(x removelink.Item) ([]removelink.Item, error) {
	if x.Storage != "local" {
		return nil, ErrUnsupported
	}
	p := strings.TrimSuffix(x.Path, "/")
	rows, e := o.Children(p)
	if e != nil {
		return nil, e
	}
	out := []removelink.Item{}
	for _, r := range rows {
		pin, e := o.pin(r.Path, false)
		if e != nil {
			return nil, e
		}
		kind := "file"
		if r.Dir {
			kind = "dir"
		}
		out = append(out, removelink.Item{Storage: "local", Path: r.Path, Type: kind, Name: filepath.Base(r.Path), Extension: strings.TrimPrefix(filepath.Ext(r.Path), "."), FileID: digest(pin)})
	}
	return out, nil
}
func (o *localOps) StorageDelete(x removelink.Item) (bool, error) {
	if x.Storage != "local" {
		return false, ErrUnsupported
	}
	p := strings.TrimSuffix(x.Path, "/")
	pin, ok := o.pins[p]
	if !ok || x.FileID != digest(pin) {
		return false, ErrUnsafe
	}
	e := o.remove(p, x.Type == "dir")
	return e == nil, e
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

var _ removelink.Ops = (*localOps)(nil)
