package scrapefileclean

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Test-only adapter. No exported concrete backend can delete real media.
type fixtureOps struct {
	root           string
	trace          []map[string]any
	deny           map[string]bool
	missingHistory map[string]bool
}

func (o *fixtureOps) owned(p string) bool {
	r, e := filepath.Rel(o.root, p)
	return e == nil && r != ".." && !strings.HasPrefix(r, "../")
}
func (o *fixtureOps) Exists(p string) bool {
	if !o.owned(p) {
		return false
	}
	_, e := os.Stat(p)
	return e == nil
}
func (o *fixtureOps) IsDir(p string) bool {
	if !o.owned(p) {
		return false
	}
	s, e := os.Stat(p)
	return e == nil && s.IsDir()
}
func (o *fixtureOps) IsSymlink(p string) bool {
	if !o.owned(p) {
		return false
	}
	s, e := os.Lstat(p)
	return e == nil && s.Mode()&os.ModeSymlink != 0
}
func (o *fixtureOps) Stat(p string) (Identity, error) {
	if !o.owned(p) {
		return Identity{}, fs.ErrPermission
	}
	s, e := os.Stat(p)
	if e != nil {
		return Identity{}, e
	}
	id := s.Sys().(*syscall.Stat_t)
	return Identity{Dev: uint64(id.Dev), Inode: uint64(id.Ino)}, nil
}
func (o *fixtureOps) Children(p string) ([]Entry, error) {
	if !o.owned(p) {
		return nil, fs.ErrPermission
	}
	files, e := os.ReadDir(p)
	if e != nil {
		return nil, e
	}
	out := []Entry{}
	for _, f := range files {
		path := filepath.Join(p, f.Name())
		out = append(out, Entry{path, o.IsDir(path)})
	}
	return out, nil
}
func (o *fixtureOps) Scan(p string) ([]Tracked, error) {
	if !o.owned(p) {
		return nil, fs.ErrPermission
	}
	out := []Tracked{}
	var walk func(string) error
	walk = func(dir string) error {
		rows, e := o.Children(dir)
		if e != nil {
			return e
		}
		for _, file := range rows {
			if !file.Dir {
				if id, e := o.Stat(file.Path); e == nil {
					out = append(out, Tracked{file.Path, id})
				}
			}
		}
		for _, file := range rows {
			if file.Dir && !o.IsSymlink(file.Path) {
				_ = walk(file.Path)
			}
		}
		return nil
	}
	err := walk(p)
	return out, err
}
func (o *fixtureOps) Unlink(p string) error {
	if !o.owned(p) {
		return fs.ErrPermission
	}
	if o.deny[p] {
		return fs.ErrPermission
	}
	o.trace = append(o.trace, map[string]any{"op": "unlink", "path": p})
	s, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if s.IsDir() {
		return errors.New("directory")
	}
	return os.Remove(p)
}
func (o *fixtureOps) RemoveTree(p string) error {
	if !o.owned(p) {
		return fs.ErrPermission
	}
	o.trace = append(o.trace, map[string]any{"op": "rmtree", "path": p})
	return os.RemoveAll(p)
}
func (o *fixtureOps) RemoveDir(p string) error {
	if !o.owned(p) {
		return fs.ErrPermission
	}
	return os.Remove(p)
}
func (o *fixtureOps) History(kind, p string) (bool, error) {
	o.trace = append(o.trace, map[string]any{"op": "history_lookup", "kind": kind, "path": p})
	id := kind + ":" + p
	if o.missingHistory[id] {
		return false, nil
	}
	o.trace = append(o.trace, map[string]any{"op": "history_delete", "id": id})
	return true, nil
}
func (o *fixtureOps) Event(p string) error {
	o.trace = append(o.trace, map[string]any{"op": "event", "event": "DownloadFileDeleted", "src": p})
	return nil
}
func (o *fixtureOps) Notice(title, text string) error {
	o.trace = append(o.trace, map[string]any{"op": "notify", "mtype": "SiteMessage", "title": title, "text": text})
	return nil
}
func canonical(v any) any {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	var value any
	if e = json.Unmarshal(b, &value); e != nil {
		panic(e)
	}
	return value
}
func finalFiles(root string) ([]string, error) {
	out := []string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == root || d.Name() == ".owned-fixture" {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		if d.IsDir() {
			rel += "/"
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}
