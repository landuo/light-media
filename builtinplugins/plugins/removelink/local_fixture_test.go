package removelink

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// Fixture-only adapter restricted to each test-owned temporary root.
type fixtureOps struct {
	traceMu                       sync.Mutex
	storageErrors                 map[string]bool
	root                          string
	trace                         []map[string]any
	storage                       []Item
	media                         []string
	scanOrder                     []string
	failUnlink                    map[string]error
	historyMissing, historyErrors map[string]bool
	identities                    map[string]Identity
}

func (o *fixtureOps) record(row map[string]any) {
	o.traceMu.Lock()
	o.trace = append(o.trace, row)
	o.traceMu.Unlock()
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
func (o *fixtureOps) Stat(p string) (Identity, error) {
	if !o.owned(p) {
		return Identity{}, fs.ErrPermission
	}
	if value, ok := o.identities[p]; ok {
		return value, nil
	}
	x, e := os.Stat(p)
	if e != nil {
		return Identity{}, e
	}
	s := x.Sys().(*syscall.Stat_t)
	return Identity{Dev: uint64(s.Dev), Inode: uint64(s.Ino)}, nil
}
func (o *fixtureOps) Children(p string) ([]Entry, error) {
	if !o.owned(p) {
		return nil, fs.ErrPermission
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	xs, e := f.Readdir(-1)
	if e != nil {
		return nil, e
	}
	out := []Entry{}
	for _, x := range xs {
		out = append(out, Entry{filepath.Join(p, x.Name()), x.IsDir()})
	}
	return out, nil
}
func (o *fixtureOps) Scan(p string) ([]Tracked, error) {
	if o.scanOrder != nil {
		out := []Tracked{}
		for _, name := range o.scanOrder {
			if rel, e := filepath.Rel(p, name); e == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
				if id, e := o.Stat(name); e == nil {
					out = append(out, Tracked{name, id})
				}
			}
		}
		return out, nil
	}
	out := []Tracked{}
	var walk func(string)
	walk = func(path string) {
		rows, e := o.Children(path)
		if e != nil {
			return
		}
		for _, x := range rows {
			if Trickplay(x.Path) {
				continue
			}
			if x.Dir {
				walk(x.Path)
			} else if id, e := o.Stat(x.Path); e == nil {
				out = append(out, Tracked{x.Path, id})
			}
		}
	}
	walk(p)
	return out, nil
}
func (o *fixtureOps) Unlink(p string) error {
	if !o.owned(p) {
		return fs.ErrPermission
	}
	if e := o.failUnlink[p]; e != nil {
		return e
	}
	x, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if x.IsDir() {
		return errors.New("is a directory")
	}
	return os.Remove(p)
}
func (o *fixtureOps) RemoveTree(p string) error {
	if !o.owned(p) {
		return fs.ErrPermission
	}
	return os.RemoveAll(p)
}
func (o *fixtureOps) RemoveDir(p string) error {
	if !o.owned(p) {
		return fs.ErrPermission
	}
	return os.Remove(p)
}
func (o *fixtureOps) History(kind, p string) (bool, error) {
	key := kind + ":" + p
	if o.historyMissing[key] {
		return false, nil
	}
	o.record(map[string]any{"op": "history", "id": key})
	if o.historyErrors[key] {
		return false, errors.New("RuntimeError:synthetic history failure")
	}
	return true, nil
}
func (o *fixtureOps) Event(p string) error {
	o.record(map[string]any{"op": "event", "event": "DownloadFileDeleted", "src": p})
	return nil
}
func (o *fixtureOps) Notice(title, text string) error {
	o.record(map[string]any{"op": "notify", "mtype": "SiteMessage", "title": title, "text": text})
	return nil
}
func (o *fixtureOps) StorageExists(item Item) (bool, error) {
	if o.storageErrors["exists:"+strings.TrimRight(item.Path, "/")] {
		return false, errors.New("RuntimeError:synthetic storage failure")
	}
	for _, x := range o.storage {
		if x.Storage == item.Storage && strings.TrimRight(x.Path, "/") == strings.TrimRight(item.Path, "/") && x.Type == "dir" {
			return true, nil
		}
	}
	return false, nil
}
func (o *fixtureOps) StorageList(item Item) ([]Item, error) {
	if o.storageErrors["list:"+strings.TrimRight(item.Path, "/")] {
		return nil, errors.New("RuntimeError:synthetic storage failure")
	}
	out := []Item{}
	for _, x := range o.storage {
		if x.Storage == item.Storage && filepath.Dir(x.Path) == strings.TrimRight(item.Path, "/") && strings.TrimRight(x.Path, "/") != strings.TrimRight(item.Path, "/") {
			out = append(out, x)
		}
	}
	return out, nil
}
func (o *fixtureOps) StorageDelete(item Item) (bool, error) {
	o.record(map[string]any{"op": "storage_delete", "storage": item.Storage, "path": item.Path, "type": item.Type, "fileid": item.FileID})
	if o.storageErrors["delete:"+item.Path] {
		return false, errors.New("RuntimeError:synthetic storage failure")
	}
	for i, x := range o.storage {
		if x.Storage == item.Storage && x.Path == item.Path {
			if x.Extra["fail"] == true {
				return false, nil
			}
			o.storage = append(o.storage[:i], o.storage[i+1:]...)
			break
		}
	}
	return true, nil
}
func (o *fixtureOps) MediaExtensions() []string { return o.media }
func storageJSON(raw []byte) []Item {
	var values []map[string]json.RawMessage
	json.Unmarshal(raw, &values)
	out := []Item{}
	for _, fields := range values {
		b, _ := json.Marshal(fields)
		var item Item
		json.Unmarshal(b, &item)
		item.Extra = map[string]any{}
		for k, v := range fields {
			if k == "fail" {
				json.Unmarshal(v, new(any))
				item.Extra[k], _ = Decode(v)
			}
		}
		out = append(out, item)
	}
	return out
}
