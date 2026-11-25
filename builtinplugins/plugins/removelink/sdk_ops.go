package removelink

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	plugins "light-media/builtinplugins/internal/sdk"
)

// sdkOps is owned by one RemoveLink pass. The caller and its context are bound
// only while its fresh callback is running, and cleared before every yield.
// The synchronous source stack retains this adapter, never an invocation grant.
type sdkOps struct {
	host             plugins.HostCaller
	ctx              context.Context
	yield            func() bool
	rpc              int
	err              error
	authorized       map[string]bool
	media            []string
	mediaRead        bool
	scanErrors       int
	watch            func(Watch) error
	watchStartFailed func()
}

const phaseHostCalls = 24 // Every actual HostCaller call is counted, plus one Submit.

func (o *sdkOps) bind(ctx context.Context, host plugins.HostCaller) {
	o.ctx, o.host, o.rpc = ctx, host, 0
	o.authorized = map[string]bool{}
}
func (o *sdkOps) unbind() { o.ctx, o.host, o.authorized = nil, nil, nil }
func (o *sdkOps) failed(err error) error {
	if err != nil {
		o.err = err
	}
	return err
}
func (o *sdkOps) failure() error {
	if o.err != nil {
		return o.err
	}
	if o.ctx == nil {
		return context.Canceled
	}
	return o.ctx.Err()
}
func (o *sdkOps) call(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if e := o.failure(); e != nil {
		return nil, e
	}
	if o.rpc >= phaseHostCalls {
		o.unbind()
		if o.yield == nil || !o.yield() {
			o.err = context.Canceled
			return nil, o.err
		}
	}
	if e := o.failure(); e != nil {
		return nil, e
	}
	o.rpc++
	result, e := o.host(o.ctx, method, raw)
	if e != nil {
		// The public transport cannot prove a caught native exception. Guard,
		// cancellation and opaque host failures stop this source attempt.
		o.err = e
	}
	return result, e
}
func (o *sdkOps) authorizeDir(path string) error {
	if e := o.failure(); e != nil {
		return e
	}
	if o.authorized[path] {
		return nil
	}
	item, e := plugins.NewModuleFileItem(map[string]any{"storage": "local", "path": path, "type": "dir"})
	if e != nil {
		return e
	}
	s := plugins.Storage{Host: o.call}
	exists, e := s.Exists(context.Background(), item)
	if e != nil {
		return o.failed(e)
	}
	if exists != nil && *exists {
		if _, e = s.List(context.Background(), item, false); e != nil {
			return o.failed(e)
		}
	}
	o.authorized[path] = true
	return nil
}
func (o *sdkOps) Exists(path string) bool {
	// The object itself may be an authorized root. Authorizing its parent
	// would incorrectly reject an ordinary exists(root) during ancestor cleanup.
	exists, e := o.StorageExists(Item{Storage: "local", Path: path, Type: "file"})
	if e != nil {
		o.err = e
	}
	return e == nil && exists
}
func (o *sdkOps) Stat(path string) (Identity, error) {
	if e := o.authorizeDir(filepath.Dir(path)); e != nil {
		return Identity{}, e
	}
	info, e := os.Stat(path)
	if e != nil {
		return Identity{}, e
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}, errors.New("local stat identity unavailable")
	}
	return Identity{Dev: uint64(stat.Dev), Inode: uint64(stat.Ino)}, nil
}
func (o *sdkOps) Children(path string) ([]Entry, error) {
	if e := o.authorizeDir(path); e != nil {
		return nil, e
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	rows, e := f.Readdir(-1)
	if e != nil {
		return nil, e
	}
	out := []Entry{}
	for _, info := range rows {
		p := filepath.Join(path, info.Name())
		dir := info.IsDir()
		if info.Mode()&os.ModeSymlink != 0 {
			if target, e := os.Stat(p); e == nil {
				dir = target.IsDir()
			}
		}
		out = append(out, Entry{p, dir})
	}
	return out, nil
}
func (o *sdkOps) Scan(path string) ([]Tracked, error) {
	out := []Tracked{}
	var walk func(string) error
	walk = func(dir string) error {
		rows, e := o.Children(dir)
		if e != nil {
			if o.err == nil {
				var pathError *os.PathError
				if errors.As(e, &pathError) {
					return nil // os.walk's default onerror ignores list errors.
				}
			}
			return e
		}
		// os.walk yields this directory's files before descending its dirs.
		for _, x := range rows {
			if x.Dir || Trickplay(x.Path) {
				continue
			}
			if id, e := o.Stat(x.Path); e == nil {
				out = append(out, Tracked{x.Path, id})
			} else if o.err != nil {
				return o.err
			} else if !missing(e) {
				o.scanErrors++
				if e := o.Log("DEBUG", "无法访问文件 "+x.Path+"："+e.Error()); e != nil {
					return e
				}
			}
		}
		for _, x := range rows {
			if !x.Dir || Trickplay(x.Path) {
				continue
			}
			if info, e := os.Lstat(x.Path); e != nil || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if e := walk(x.Path); e != nil && o.err != nil {
				return e
			}
		}
		return nil
	}
	err := walk(path)
	return out, err
}
func (o *sdkOps) ScanErrorCount() int { return o.scanErrors }
func (o *sdkOps) delete(path, kind string) error {
	// Path.unlink requires parent write/search, not directory read/list. The
	// actual target Storage.Delete keeps the host root/snapshot/ownership guards.
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if kind == "file" && info.IsDir() {
		return syscall.EISDIR
	}
	if kind == "empty_dir" {
		rows, e := o.Children(path)
		if e != nil {
			return e
		}
		if len(rows) != 0 {
			return syscall.ENOTEMPTY
		}
	}
	item := Item{Storage: "local", Path: path, Type: "file"}
	if info.IsDir() {
		item.Type = "dir"
	}
	ok, e := o.StorageDelete(item)
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("local deletion not confirmed")
	}
	return nil
}
func (o *sdkOps) Unlink(path string) error     { return o.delete(path, "file") }
func (o *sdkOps) RemoveTree(path string) error { return o.delete(path, "tree") }
func (o *sdkOps) RemoveDir(path string) error  { return o.delete(path, "empty_dir") }
func (o *sdkOps) History(mode, path string) (bool, error) {
	_, ok, e := o.HistoryRecord(mode, path)
	return ok, e
}
func (o *sdkOps) HistoryRecord(mode, path string) (string, bool, error) {
	r := plugins.TransferHistoryRequestV1{Source: path}
	if mode == "dest" {
		r = plugins.TransferHistoryRequestV1{Target: path}
	}
	h := plugins.History{Host: o.call}
	result, e := h.Transfer(context.Background(), r)
	if e != nil || result.Item == nil {
		return "", false, o.failed(e)
	}
	_, e = h.DeleteTransfer(context.Background(), result.Item.ID)
	return result.Item.ID, e == nil, o.failed(e) // Source ignores delete()'s return value.
}
func (o *sdkOps) Event(path string) error {
	data, _ := json.Marshal(map[string]any{"src": path})
	_, e := plugins.SendEvent(context.Background(), o.call, plugins.EventSendRequest{Kind: plugins.BroadcastEventKind, Type: "downloadfile.deleted", Data: data})
	return o.failed(e)
}
func (o *sdkOps) Notice(title, text string) error {
	_, e := (plugins.Base{Host: o.call}).PostMessage(context.Background(), map[string]any{"mtype": "站点", "title": title, "text": text})
	return o.failed(e)
}
func (o *sdkOps) Log(level, message string) error {
	if len(message) > 2000 {
		message = message[:2000]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return o.failed((plugins.Logger{Host: o.call}).Log(context.Background(), plugins.LogLevel(level), message))
}
func sourceFileItem(x Item) (*plugins.ModuleFileItem, error) {
	return plugins.NewModuleFileItem(map[string]any{"storage": x.Storage, "path": x.Path, "type": x.Type, "name": x.Name, "extension": x.Extension, "fileid": x.FileID})
}
func (o *sdkOps) StorageExists(x Item) (bool, error) {
	if x.Storage != "local" {
		return false, fs.ErrPermission
	}
	item, e := sourceFileItem(x)
	if e != nil {
		return false, e
	}
	result, e := (plugins.Storage{Host: o.call}).Exists(context.Background(), item)
	return result != nil && *result, o.failed(e)
}
func (o *sdkOps) StorageList(x Item) ([]Item, error) {
	if x.Storage != "local" {
		return nil, fs.ErrPermission
	}
	item, e := sourceFileItem(x)
	if e != nil {
		return nil, e
	}
	rows, e := (plugins.Storage{Host: o.call}).List(context.Background(), item, false)
	if e != nil {
		return nil, o.failed(e)
	}
	out := []Item{}
	for _, row := range rows {
		if row == nil {
			continue
		}
		var x Item
		raw, _ := json.Marshal(row)
		if e := json.Unmarshal(raw, &x); e != nil {
			return nil, o.failed(e)
		}
		out = append(out, x)
	}
	return out, nil
}
func (o *sdkOps) StorageDelete(x Item) (bool, error) {
	if x.Storage != "local" {
		return false, fs.ErrPermission
	}
	item, e := sourceFileItem(x)
	if e != nil {
		return false, e
	}
	result, e := (plugins.Storage{Host: o.call}).Delete(context.Background(), item)
	return result != nil && *result, o.failed(e)
}
func (o *sdkOps) MediaExtensions() []string {
	if !o.mediaRead {
		o.mediaRead = true
		v, e := (plugins.SystemSettings{Host: o.call}).Get(context.Background(), "RMT_MEDIAEXT")
		if e == nil {
			e = json.Unmarshal(v.Value, &o.media)
		}
		if e != nil {
			o.err = e
		}
	}
	return append([]string(nil), o.media...)
}
func (o *sdkOps) watchError(w Watch, err error) {
	kind := "硬链接"
	if w.Mode == "strm" {
		kind = " STRM "
	}
	message := w.Path + " 启动" + kind + "监控失败：" + err.Error()
	if strings.Contains(err.Error(), "inotify") && strings.Contains(err.Error(), "reached") {
		checked(o.Log("WARNING", "目录监控服务启动出现异常："+err.Error()+"，请在宿主机上（不是docker容器内）执行以下命令并重启："+inotifySourceInstructions))
	} else {
		checked(o.Log("ERROR", message))
	}
	raw, _ := json.Marshal(map[string]any{"message": message, "title": "媒体文件清理"})
	_, e := o.call(context.Background(), plugins.SystemMessagePutCapability, raw)
	checked(e)
}
func (o *sdkOps) StartWatch(w Watch) {
	if w.Path == "" {
		return
	}
	if e := o.authorizeDir(w.Path); e != nil {
		checked(e)
	}
	if o.watch != nil {
		if e := o.watch(w); e != nil {
			if failure := o.failure(); failure != nil {
				checked(failure)
			}
			if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				checked(o.failed(e))
			}
			if o.watchStartFailed != nil {
				o.watchStartFailed()
			}
			o.watchError(w, e)
		} else {
			kind := "硬链接"
			if w.Mode == "strm" {
				kind = " STRM "
			}
			checked(o.Log("INFO", w.Path+" 的"+kind+"监控服务启动"))
		}
	}
}

const inotifySourceInstructions = "\n                             echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf\n                             echo fs.inotify.max_user_instances=524288 | sudo tee -a /etc/sysctl.conf\n                             sudo sysctl -p\n                             "

var _ Ops = (*sdkOps)(nil)
