package seedhost

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	seed "light-media/internal/plugins/cleaninvalidseed"
)

func validPath(p string) bool {
	return utf8.ValidString(p) && filepath.IsAbs(p) && filepath.Clean(p) == p && p != "/" && !strings.ContainsAny(p, "\x00\\\r\n")
}
func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
func fileStamp(info os.FileInfo) (Stamp, error) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (!info.Mode().IsRegular() && !info.IsDir()) {
		return Stamp{}, ErrUnsafe
	}
	return Stamp{uint64(s.Dev), uint64(s.Ino), info.Size(), info.ModTime().UnixNano(), uint32(info.Mode())}, nil
}
func sameEntity(a, b Stamp) bool { return a.Dev == b.Dev && a.Inode == b.Inode && a.Mode == b.Mode }
func openDir(path string) (*os.File, error) {
	if !validPath(path) {
		return nil, ErrUnsafe
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}
func currentStamp(f *os.File) (Stamp, error) {
	info, err := f.Stat()
	if err != nil {
		return Stamp{}, err
	}
	return fileStamp(info)
}
func atStamp(parent *os.File, name string) (Stamp, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return Stamp{}, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	return currentStamp(f)
}
func AuthorizeRoot(id, path, role string) (Root, error) {
	if id == "" || (role != "source" && role != "media") {
		return Root{}, ErrUnsafe
	}
	f, err := openDir(path)
	if err != nil {
		return Root{}, err
	}
	defer f.Close()
	s, err := currentStamp(f)
	return Root{ID: id, Path: path, Role: role, Stamp: s}, err
}
func validateRoots(roots []Root) error {
	for n, r := range roots {
		if r.ID == "" || (r.Role != "source" && r.Role != "media") {
			return ErrUnsafe
		}
		f, err := openDir(r.Path)
		if err != nil {
			return err
		}
		s, err := currentStamp(f)
		f.Close()
		if err != nil || !sameEntity(r.Stamp, s) {
			return ErrUnsafe
		}
		for _, old := range roots[:n] {
			if old.ID == r.ID || inside(old.Path, r.Path) || inside(r.Path, old.Path) {
				return ErrUnsafe
			}
		}
	}
	return nil
}
func (e *execution) root(path string) (Root, error) {
	if !validPath(path) || e.ctx.Err() != nil {
		return Root{}, ErrUnsafe
	}
	for _, r := range e.o.opts.Roots {
		if inside(r.Path, path) {
			if err := validateRoots([]Root{r}); err != nil {
				return r, err
			}
			return r, nil
		}
	}
	return Root{}, ErrUnsafe
}
func (e *execution) capture(path string) (Pin, error) {
	r, err := e.root(path)
	if err != nil {
		return Pin{}, err
	}
	parent, err := openDir(filepath.Dir(path))
	if err != nil {
		return Pin{}, err
	}
	defer parent.Close()
	ps, err := currentStamp(parent)
	if err != nil {
		return Pin{}, err
	}
	s, err := atStamp(parent, filepath.Base(path))
	if err != nil {
		return Pin{}, err
	}
	return Pin{RootID: r.ID, Path: path, Parent: filepath.Dir(path), File: s, ParentFile: ps}, nil
}
func (e *execution) remember(path string) (Pin, error) {
	p, err := e.capture(path)
	if err != nil {
		return p, err
	}
	if old, ok := e.pins[path]; ok {
		if old.File != p.File || !sameEntity(old.ParentFile, p.ParentFile) {
			return p, ErrUnsafe
		}
		return old, nil
	}
	proof, pinErr := e.o.opts.Core.Pin(e.ctx, e.inv, p)
	if pinErr != nil {
		return p, pinErr
	}
	e.proofs[path] = append([]byte(nil), proof...)
	e.pins[path] = p
	return p, nil
}
func (e *execution) Exists(path string) bool {
	_, err := e.remember(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		e.fault(err)
		return false
	}
	return true
}
func (e *execution) IsFile(path string) bool {
	p, err := e.remember(path)
	if err != nil {
		e.fault(err)
		return false
	}
	return os.FileMode(p.File.Mode).IsRegular()
}
func (e *execution) IsDir(path string) bool {
	p, err := e.remember(path)
	if err != nil {
		e.fault(err)
		return false
	}
	return os.FileMode(p.File.Mode).IsDir()
}
func (e *execution) Stat(path string) (seed.FileStat, error) {
	p, err := e.remember(path)
	if err != nil {
		e.fault(err)
		return seed.FileStat{}, err
	}
	return seed.FileStat{Size: p.File.Size, MTime: float64(p.File.ModNS) / 1e9}, nil
}
func (e *execution) Children(path string) (result []string, retErr error) {
	defer func() {
		if retErr != nil {
			e.fault(retErr)
		}
	}()
	p, err := e.remember(path)
	if err != nil {
		return nil, err
	}
	if !os.FileMode(p.File.Mode).IsDir() {
		return nil, ErrUnsafe
	}
	f, err := openDir(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := currentStamp(f)
	if err != nil || s != p.File {
		return nil, ErrUnsafe
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		if _, err = e.remember(child); err != nil {
			e.fault(err)
			return nil, err
		}
		out = append(out, child)
	}
	e.readable[path] = true
	return out, nil
}
func (e *execution) Descendants(path string) (result []string, retErr error) {
	defer func() {
		if retErr != nil {
			e.fault(retErr)
		}
	}()
	out := []string{}
	var walk func(string) error
	walk = func(path string) error {
		rows, err := e.Children(path)
		if err != nil {
			return err
		}
		for _, child := range rows {
			out = append(out, child)
			if os.FileMode(e.pins[child].File.Mode).IsDir() {
				if err = walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err := walk(path)
	return out, err
}
func (e *execution) completeLists() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.readFailure || len(e.selected) == 0 {
		return ErrUnsafe
	}
	for name := range e.selected {
		if !e.lists[name] {
			return ErrUnsafe
		}
	}
	return nil
}
func (e *execution) Unlink(path string) error     { return e.delete(path, false) }
func (e *execution) RemoveTree(path string) error { return e.delete(path, true) }
func (e *execution) delete(path string, tree bool) (retErr error) {
	defer func() {
		if retErr != nil {
			e.fault(retErr)
		}
	}()
	if err := e.completeLists(); err != nil {
		e.fault(err)
		return err
	}
	pin, ok := e.pins[path]
	if !ok || !e.readable[pin.Parent] {
		e.fault(ErrUnsafe)
		return ErrUnsafe
	}
	root, err := e.root(path)
	if err != nil || path == root.Path {
		return ErrUnsafe
	}
	if tree != os.FileMode(pin.File.Mode).IsDir() {
		return ErrUnsafe
	}
	manifest := []Pin{pin}
	if tree {
		for child, p := range e.pins {
			if child != path && inside(path, child) {
				manifest = append(manifest, p)
			}
		}
		sort.Slice(manifest, func(i, j int) bool { return manifest[i].Path < manifest[j].Path })
	}
	e.ordinal++
	intent := e.scoped(Effect{ID: e.inv.RunID + ":file:" + digest([]any{e.ordinal, path}), RunID: e.inv.RunID, Binding: e.inv.Binding, Kind: "file_delete", Operation: "unlink", Phase: "started", Pin: &pin, Manifest: manifest})
	if tree {
		intent.Operation = "remove_tree"
	}
	intent.ExpectedCore = map[string]json.RawMessage{}
	for _, p := range manifest {
		proof, ok := e.proofs[p.Path]
		if !ok {
			return ErrUnsafe
		}
		intent.ExpectedCore[p.Path] = proof
	}
	proof, release, err := e.o.opts.Core.Guard(e.ctx, e.inv, intent)
	if err != nil {
		e.fault(err)
		return err
	}
	if release == nil {
		return ErrUnsafe
	}
	defer release()
	intent.Proof = proof
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	stage := ".cleanup-seed-" + hex.EncodeToString(nonce)
	intent.Stage = filepath.Join(pin.Parent, stage)
	if err = e.o.opts.Journal.Update(e.ctx, func(_ *sql.Tx, s *State) error {
		if _, exists := s.Effects[intent.ID]; exists {
			return ErrAlready
		}
		r := s.Runs[e.inv.RunID]
		if r.Phase != "started" {
			return ErrUnsafe
		}
		s.Effects[intent.ID] = intent
		return nil
	}); err != nil {
		e.fault(err)
		return err
	}
	parent, err := openDir(pin.Parent)
	if err == nil {
		defer parent.Close()
		var p Stamp
		p, err = currentStamp(parent)
		if err == nil && !sameEntity(p, pin.ParentFile) {
			err = ErrUnsafe
		}
		if err == nil {
			var current Stamp
			current, err = atStamp(parent, filepath.Base(path))
			if err == nil && current != pin.File {
				err = ErrUnsafe
			}
		}
	}
	renamed := false
	if err == nil {
		err = renameNoReplace(int(parent.Fd()), filepath.Base(path), int(parent.Fd()), stage)
		renamed = err == nil
	}
	if err == nil {
		var moved Stamp
		moved, err = atStamp(parent, stage)
		if err == nil && moved != pin.File {
			err = ErrUnsafe
		}
	}
	if err == nil && tree {
		err = e.validateTree(intent.Stage, path, manifest)
	}
	if err == nil {
		order := append([]Pin(nil), manifest...)
		sort.SliceStable(order, func(i, j int) bool { return len(order[i].Path) > len(order[j].Path) })
		for _, entry := range order {
			actual := intent.Stage
			if entry.Path != path {
				rel, _ := filepath.Rel(path, entry.Path)
				actual = filepath.Join(intent.Stage, rel)
			}
			if err = e.deleteStep(intent, entry, actual); err != nil {
				break
			}
		}
	}
	if err != nil && renamed {
		// Never overwrite a new original path. A partially removed tree remains in
		// quarantine with its durable intent; only explicit reconciliation may act.
		_ = renameNoReplace(int(parent.Fd()), stage, int(parent.Fd()), filepath.Base(path))
	}
	if current, exists := e.pins[pin.Parent]; exists {
		if dir, openErr := openDir(pin.Parent); openErr == nil {
			if actual, stampErr := currentStamp(dir); stampErr == nil && sameEntity(actual, current.File) {
				current.File = actual
				e.pins[pin.Parent] = current
			}
			dir.Close()
		}
	}
	intent.Phase = "confirmed"
	if err != nil {
		intent.Phase = "unknown"
		intent.ErrorCode = "FILE_DELETE_UNCONFIRMED"
		e.fault(ErrUnknown)
	}
	saveErr := e.persist(func(_ *sql.Tx, s *State) error { s.Effects[intent.ID] = intent; return nil })
	if saveErr != nil {
		e.fault(ErrUnknown)
		return ErrUnknown
	}
	return err
}
func (e *execution) validateTree(actual, original string, manifest []Pin) error {
	expected := map[string]Pin{}
	for _, p := range manifest {
		expected[p.Path] = p
	}
	seen := map[string]bool{}
	var walk func(string, string) error
	walk = func(actual, original string) error {
		f, err := openDir(actual)
		if err != nil {
			return err
		}
		defer f.Close()
		entries, err := f.ReadDir(-1)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			orig := filepath.Join(original, entry.Name())
			p, ok := expected[orig]
			if !ok {
				return ErrUnsafe
			}
			current, err := atStamp(f, entry.Name())
			if err != nil || current != p.File {
				return ErrUnsafe
			}
			seen[orig] = true
			if os.FileMode(current.Mode).IsDir() {
				if err = walk(filepath.Join(actual, entry.Name()), orig); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(actual, original); err != nil {
		return err
	}
	returnIf := len(seen) == len(expected)-1
	if !returnIf {
		return ErrUnsafe
	}
	return nil
}
func (e *execution) deleteStep(parentIntent Effect, pin Pin, actual string) error {
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	quarantine := ".cleanup-leaf-" + hex.EncodeToString(nonce)
	// Persist the selected entry's frozen proof, not the complete tree map in
	// every leaf receipt. The parent intent already retains that manifest. This
	// avoids quadratic journal growth after partially deleting a normal tree.
	entryProof, ok := parentIntent.ExpectedCore[pin.Path]
	if !ok {
		return ErrUnsafe
	}
	expected := map[string]json.RawMessage{pin.Path: entryProof}
	step := e.scoped(Effect{ID: parentIntent.ID + ":step:" + digest(pin), RunID: e.inv.RunID, Binding: e.inv.Binding, Kind: "file_step", Operation: "unlink", Phase: "started", Pin: &pin, ExpectedCore: expected, Proof: raw(expected), StageFrom: actual, Stage: filepath.Join(filepath.Dir(actual), quarantine)})
	dir := os.FileMode(pin.File.Mode).IsDir()
	if dir {
		step.Operation = "rmdir"
	}
	if err := e.o.opts.Journal.Update(e.ctx, func(_ *sql.Tx, s *State) error {
		if _, exists := s.Effects[step.ID]; exists {
			return ErrAlready
		}
		s.Effects[step.ID] = step
		return nil
	}); err != nil {
		return err
	}
	f, err := openDir(filepath.Dir(actual))
	if err != nil {
		return err
	}
	defer f.Close()
	parent, err := currentStamp(f)
	if err != nil || !sameEntity(parent, pin.ParentFile) {
		return ErrUnsafe
	}
	current, err := atStamp(f, filepath.Base(actual))
	if err != nil {
		return err
	}
	if dir {
		if !sameEntity(current, pin.File) {
			return ErrUnsafe
		}
	} else if current != pin.File {
		return ErrUnsafe
	}
	name := filepath.Base(actual)
	if err = renameNoReplace(int(f.Fd()), name, int(f.Fd()), quarantine); err != nil {
		return err
	}
	moved, checkErr := atStamp(f, quarantine)
	match := checkErr == nil && moved == pin.File
	if dir {
		match = checkErr == nil && sameEntity(moved, pin.File)
	}
	if !match {
		_ = renameNoReplace(int(f.Fd()), quarantine, int(f.Fd()), name)
		return ErrUnsafe
	}
	flags := 0
	if dir {
		flags = unix.AT_REMOVEDIR
	}
	if err = unix.Unlinkat(int(f.Fd()), quarantine, flags); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	step.Phase = "fs_confirmed"
	step.Result = raw(map[string]bool{"filesystem_removed": true})
	if !dir {
		step.Facts = []FileFact{{Pin: pin, Proof: step.Proof}}
	}
	if err = e.persist(func(_ *sql.Tx, s *State) error { s.Effects[step.ID] = step; return nil }); err != nil {
		return err
	}
	return e.persist(func(tx *sql.Tx, s *State) error {
		if err := e.o.opts.Core.Commit(e.ctx, tx, e.inv, step); err != nil {
			return err
		}
		step.Phase = "confirmed"
		s.Effects[step.ID] = step
		return nil
	})
}

// ReconcileConfirmedFiles repairs facts, never deletes or resumes an old run.
func (o *Owner) ReconcileConfirmedFiles(ctx context.Context, inv Invocation) error {
	select {
	case o.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-o.gate }()
	s, err := o.opts.Journal.Read(ctx)
	if err != nil {
		return err
	}
	for id, effect := range s.Effects {
		if effect.RunID != inv.RunID || effect.Phase != "fs_confirmed" {
			continue
		}
		_, release, err := o.opts.Core.Guard(ctx, inv, effect)
		if err != nil {
			return err
		}
		if release == nil {
			return ErrUnsafe
		}
		err = o.opts.Journal.Update(ctx, func(tx *sql.Tx, current *State) error {
			old, ok := current.Effects[id]
			if !ok || digest(old) != digest(effect) {
				return ErrUnsafe
			}
			if err := o.opts.Core.Commit(ctx, tx, inv, effect); err != nil {
				return err
			}
			effect.Phase = "confirmed"
			current.Effects[id] = effect
			return nil
		})
		release()
		if err != nil {
			return err
		}
	}
	return nil
}
