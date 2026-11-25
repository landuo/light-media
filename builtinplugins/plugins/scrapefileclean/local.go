package scrapefileclean

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	sdk "light-media/builtinplugins/internal/sdk"
)

func validPath(path string) bool {
	return len(path) <= 4096 && utf8.ValidString(path) && filepath.IsAbs(path) &&
		filepath.Clean(path) == path && !strings.Contains(path, "\\") && strings.IndexFunc(path, unicode.IsControl) < 0
}

func internalPath(path string) bool {
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if strings.HasPrefix(component, ".light-media-") {
			return true
		}
	}
	return false
}

func inside(root, path string) bool {
	if !validPath(path) {
		return false
	}
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

type localOps struct {
	ctx    context.Context
	host   sdk.HostCaller
	roots  []authorizedRoot
	listed map[string]Identity
	fence  error
	loss   func() error
}

type authorizedRoot struct {
	path string
	id   Identity
}

func (o *localOps) authorize(path string) error {
	if !validPath(path) || path == "/" {
		return fs.ErrPermission
	}
	item, err := sdk.NewModuleFileItem(map[string]any{"storage": "local", "type": "dir", "path": path})
	if err != nil {
		return err
	}
	present, err := (sdk.Storage{Host: o.host}).Exists(o.ctx, item)
	if err != nil {
		return err
	}
	if present == nil || !*present {
		return fs.ErrNotExist
	}
	dir, err := openNativeDirectory(path, nil)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return err
	}
	id, err := fileIdentity(info)
	if err != nil {
		return err
	}
	o.roots = append(o.roots, authorizedRoot{path, id})
	return nil
}

func (o *localOps) allowed(path string) bool {
	if internalPath(path) {
		return false
	}
	for _, root := range o.roots {
		if inside(root.path, path) {
			return true
		}
	}
	return false
}

func (o *localOps) open(path string) (*os.File, error) {
	if err := o.ctx.Err(); err != nil {
		return nil, err
	}
	if internalPath(path) {
		return nil, fs.ErrPermission
	}
	var pin *authorizedRoot
	for i := range o.roots {
		root := &o.roots[i]
		if inside(root.path, path) && (pin == nil || len(root.path) > len(pin.path)) {
			pin = root
		}
	}
	if pin == nil {
		return nil, fs.ErrPermission
	}
	return openNativeDirectory(path, pin)
}

func (o *localOps) info(path string) (os.FileInfo, error) {
	for _, root := range o.roots {
		if root.path == path {
			file, err := o.open(path)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			return file.Stat()
		}
	}
	if !o.allowed(path) {
		return nil, fs.ErrPermission
	}
	file, err := o.open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return os.Lstat(filepath.Join("/proc/self/fd", strconv.FormatUint(uint64(file.Fd()), 10), filepath.Base(path)))
}

func (o *localOps) Exists(path string) bool { _, err := o.info(path); return err == nil }
func (o *localOps) IsDir(path string) bool {
	info, err := o.info(path)
	return err == nil && info.IsDir()
}
func (o *localOps) IsSymlink(path string) bool {
	info, err := o.info(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func fileIdentity(info os.FileInfo) (Identity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}, errors.New("local file identity unavailable")
	}
	return Identity{Dev: uint64(stat.Dev), Inode: uint64(stat.Ino)}, nil
}

func (o *localOps) Stat(path string) (Identity, error) {
	info, err := o.info(path)
	if err != nil {
		return Identity{}, err
	}
	if !info.Mode().IsRegular() {
		return Identity{}, fs.ErrPermission
	}
	return fileIdentity(info)
}

func (o *localOps) readDir(path string) ([]os.FileInfo, error) {
	file, err := o.open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if o.listed != nil {
		if info, err := file.Stat(); err == nil {
			if id, err := fileIdentity(info); err == nil {
				o.listed[path] = id
			}
		}
	}
	return file.Readdir(-1)
}

func (o *localOps) Children(path string) ([]Entry, error) {
	rows, err := o.readDir(path)
	children := make([]Entry, 0, len(rows))
	for _, row := range rows {
		child := filepath.Join(path, row.Name())
		if o.allowed(child) {
			children = append(children, Entry{Path: child, Dir: row.IsDir()})
			if id, err := fileIdentity(row); err == nil && o.listed != nil {
				o.listed[child] = id
			}
		}
	}
	return children, err
}

func (o *localOps) Scan(path string) ([]Tracked, error) {
	path = cleanPath(path)
	var rows []Tracked
	var walk func(string) error
	walk = func(dir string) error {
		if err := o.ctx.Err(); err != nil {
			return err
		}
		entries, err := o.readDir(dir)
		for _, entry := range entries {
			if !entry.Mode().IsRegular() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if id, statErr := fileIdentity(entry); statErr == nil && o.allowed(path) {
				rows = append(rows, Tracked{Path: path, Identity: id})
			}
		}
		for _, entry := range entries {
			if entry.IsDir() {
				_ = walk(filepath.Join(dir, entry.Name())) // os.walk skips unreadable descendants.
			}
		}
		return err
	}
	err := walk(path)
	return rows, err
}

func (o *localOps) remove(path, kind string, expected *Identity) error {
	info, err := o.info(path)
	if err != nil {
		return err
	}
	if kind == "file" && !info.Mode().IsRegular() || kind != "file" && !info.IsDir() {
		return fs.ErrPermission
	}
	id, err := fileIdentity(info)
	if err != nil {
		return err
	}
	if expected != nil && !same(id, *expected) {
		return fs.ErrPermission
	}
	if expected == nil {
		if listed, ok := o.listed[path]; ok && !same(id, listed) {
			return fs.ErrPermission
		}
	}
	request, err := json.Marshal(struct {
		Path   string `json:"path"`
		Kind   string `json:"kind"`
		Device uint64 `json:"device"`
		Inode  uint64 `json:"inode"`
	}{path, kind, id.Dev, id.Inode})
	if err != nil {
		return err
	}
	result, err := o.effect(o.ctx, sdk.ScrapeCleanRemoveCapability, request)
	if err != nil {
		return err
	}
	var removed *bool
	if err := json.Unmarshal(result, &removed); err != nil || removed == nil {
		o.fence = errors.New("missing guarded deletion result")
		return o.fence
	}
	if !*removed {
		if _, err := o.info(path); errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return fs.ErrPermission
	}
	return nil
}

func (o *localOps) Unlink(path string) error { return o.remove(path, "file", nil) }
func (o *localOps) UnlinkTracked(path string, expected Identity) error {
	return o.remove(path, "file", &expected)
}
func (o *localOps) RemoveDir(path string) error  { return o.remove(path, "empty_dir", nil) }
func (o *localOps) RemoveTree(path string) error { return o.remove(path, "tree", nil) }

func (o *localOps) effect(ctx context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
	if o.fence != nil {
		return nil, o.fence
	}
	if o.loss != nil {
		if err := o.loss(); err != nil {
			o.fence = err
			return nil, err
		}
	}
	result, err := o.host(ctx, method, payload)
	if err != nil {
		o.fence = err
	}
	return result, err
}

func (o *localOps) History(by, path string) (bool, error) {
	request := sdk.TransferHistoryRequestV1{Source: path}
	if by == "dest" {
		request = sdk.TransferHistoryRequestV1{Target: path}
	}
	row, err := (sdk.History{Host: o.host}).Transfer(o.ctx, request)
	if err != nil || row.Item == nil {
		return false, err
	}
	_, err = (sdk.History{Host: o.effect}).DeleteTransfer(o.ctx, row.Item.ID)
	return true, err
}

func (o *localOps) Event(path string) error {
	data, _ := json.Marshal(map[string]string{"src": path})
	_, err := sdk.SendEvent(o.ctx, o.effect, sdk.EventSendRequest{Kind: sdk.BroadcastEventKind, Type: "downloadfile.deleted", Data: data})
	return err
}

func (o *localOps) Notice(title, text string) error {
	_, err := (sdk.Base{Host: o.effect}).PostMessage(o.ctx, map[string]any{"mtype": "站点", "title": title, "text": text})
	return err
}
