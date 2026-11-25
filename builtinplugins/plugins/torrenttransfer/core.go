package torrenttransfer

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"sync/atomic"
)

// Core is one active business invocation. Host is never stored on Plugin or
// reused after that invocation ends. Mutable source object state is shared.
type Core struct {
	Config         Config
	Host           Host
	state          *runtimeState
	configSnapshot func() Config
}
type runtimeState struct {
	mu             sync.Mutex
	Recheck        map[string][]string
	recheckRunning bool
	stopped        atomic.Bool
}

func newRuntimeState() *runtimeState { return &runtimeState{Recheck: map[string][]string{}} }
func NewCore(c Config, h Host) (*Core, error) {
	if h == nil {
		return nil, errors.New("transfer host required")
	}
	return &Core{Config: c, Host: h, state: newRuntimeState()}, nil
}
func (c *Core) config() Config {
	if c.configSnapshot != nil {
		return c.configSnapshot()
	}
	return c.Config
}
func (c *Core) Stop() { c.state.stopped.Store(true) }
func (c *Core) service(ctx context.Context, name any) (*Service, error) {
	if !truth(name) {
		logAt(ctx, c.Host, 142, "WARN", "尚未配置下载器，请检查配置")
		return nil, nil
	}
	n, ok := name.(string)
	if !ok {
		logAt(ctx, c.Host, 147, "WARN", "获取下载器 "+text(name)+" 实例失败，请检查配置")
		return nil, nil
	}
	s, e := c.Host.Service(ctx, n)
	if e != nil {
		return nil, e
	}
	if s == nil {
		logAt(ctx, c.Host, 147, "WARN", "获取下载器 "+n+" 实例失败，请检查配置")
		return nil, nil
	}
	if s.Inactive {
		logAt(ctx, c.Host, 151, "WARN", "下载器 "+n+" 未连接，请检查配置")
		return nil, nil
	}
	return s, nil
}
func posixNorm(s string) string {
	if s == "" {
		return "."
	}
	v := path.Clean(s)
	if strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "///") {
		v = "/" + v
	}
	return v
}

// ConvertSavePath intentionally preserves source string-prefix replacement,
// including /data matching /data2. The trusted Host still binds the result to
// an actual completed source task and the owner's explicit path configuration.
func ConvertSavePath(save, from, to string) *string {
	v := convertSavePathValue(save, from, to)
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return &s
}
func convertSavePathValue(save, from, to any) any {
	if !truth(save) {
		return to
	}
	if !truth(to) || !truth(from) {
		return save
	}
	s, ok := save.(string)
	if !ok {
		return nil
	}
	f, ok := from.(string)
	if !ok {
		return nil
	}
	t, ok := to.(string)
	if !ok {
		return nil
	}
	s = strings.ReplaceAll(posixNorm(s), "\\", "/")
	f = strings.ReplaceAll(posixNorm(f), "\\", "/")
	t = strings.ReplaceAll(posixNorm(t), "\\", "/")
	if strings.HasPrefix(s, f) {
		return strings.Replace(s, f, t, 1)
	}
	return nil
}
func GetHash(t Torrent, kind string) string {
	key := "hash"
	if kind != "qbittorrent" {
		key = "hashString"
	}
	s, _ := t[key].(string)
	return s
}
func GetSavePath(t Torrent, kind string) string {
	key := "save_path"
	if kind != "qbittorrent" {
		key = "download_dir"
	}
	s, _ := t[key].(string)
	return s
}
func GetCategory(t Torrent, kind string) string {
	if kind != "qbittorrent" {
		return ""
	}
	s, _ := t["category"].(string)
	return pyStrip(s)
}
func GetLabels(t Torrent, kind string) []string {
	out := []string{}
	if kind == "qbittorrent" {
		s, ok := t["tags"].(string)
		if !ok {
			return out
		}
		for _, v := range strings.Split(s, ",") {
			out = append(out, pyStrip(v))
		}
		return out
	}
	if !truth(t["labels"]) {
		return out
	}
	switch x := t["labels"].(type) {
	case []string:
		return append(out, x...)
	case []any:
		for _, v := range x {
			if s, ok := v.(string); ok {
				out = append(out, s)
			} else {
				return []string{}
			}
		}
	}
	return out
}
func CanSeed(t Torrent, kind string) bool {
	if kind == "qbittorrent" {
		return t["state"] == "pausedUP" || t["state"] == "stoppedUP"
	}
	s, _ := t["status"].(map[string]any)
	return truth(s["stopped"]) && number(t["percent_done"]) == 1
}

func (c *Core) download(ctx context.Context, s Service, content []byte, save any, t Torrent) (string, error) {
	// Original __download resolves the source service again before either kind.
	from, e := c.service(ctx, c.config().values["fromdownloader"])
	if e != nil {
		return "", e
	}
	if s.Type == "qbittorrent" {
		tag, e := c.Host.RandomTag(ctx, 10)
		if e != nil {
			return "", e
		}
		tags := append([]string{}, c.config().tags...)
		if c.config().flag("remainoldtag") {
			if from == nil {
				return "", errors.New("source AttributeError: missing source service")
			}
			tags = unique(append(append(GetLabels(t, from.Type), c.config().tags...), tag))
		} else {
			tags = append(tags, tag)
		}
		var category any
		if c.config().flag("remainoldcat") {
			if from == nil {
				return "", errors.New("source AttributeError: missing source service")
			}
			category = GetCategory(t, from.Type)
		}
		cfg := c.config()
		r, e := c.Host.AddPaused(ctx, s, AddSpec{Content: content, SavePath: save, Tags: tags, Category: category, SkipChecking: cfg.values["skipverify"], SkipCheckingRaw: cfg.skipRaw})
		if e != nil {
			return "", e
		}
		if !r.SourceTruthy {
			return "", nil
		}
		hash, e := c.Host.FindByTag(ctx, s, tag)
		if e != nil {
			return "", e
		}
		if hash == "" {
			logAt(ctx, c.Host, 650, "ERROR", s.Name+" 下载任务添加成功，但获取任务信息失败！")
		}
		return hash, nil
	}
	if s.Type == "transmission" {
		tags := append([]string{}, c.config().tags...)
		if c.config().flag("remainoldtag") {
			if from == nil {
				return "", errors.New("source AttributeError: missing source service")
			}
			tags = unique(append(GetLabels(t, from.Type), c.config().tags...))
		}
		r, e := c.Host.AddPaused(ctx, s, AddSpec{Content: content, SavePath: save, Tags: tags, SkipChecking: false})
		if e != nil {
			return "", e
		}
		if !r.SourceTruthy {
			return "", nil
		}
		if s.actualKind() == "qbittorrent" {
			return "", errors.New("source AttributeError: 'tuple' object has no attribute 'hashString'")
		}
		return r.Hash, nil
	}
	logAt(ctx, c.Host, 670, "ERROR", "不支持的下载器类型")
	return "", nil
}

type selected struct {
	hash, save string
	torrent    Torrent
}

// transferCursor is only the original for-loop's in-memory locals. It is
// never persisted or recovered; host calls are supplied by each current phase.
type transferCursor struct {
	candidates          []Torrent
	scan                int
	selecting, finished bool
	items               []selected
	from, to            Service
	next                int
	counts              Counts
}

func (c *Core) Transfer(ctx context.Context) (Counts, error) {
	pass, e := c.beginTransfer(ctx)
	if e != nil || pass == nil {
		return Counts{}, e
	}
	for !pass.finished {
		if e = c.advanceTransfer(ctx, pass); e != nil {
			return pass.counts, e
		}
	}
	return pass.counts, nil
}
func (c *Core) advanceTransfer(ctx context.Context, pass *transferCursor) error {
	if pass.selecting {
		if e := c.selectOne(ctx, pass); e != nil {
			return e
		}
		if pass.scan == len(pass.candidates) && !pass.finished {
			pass.selecting = false
			if len(pass.items) == 0 {
				logAt(ctx, c.Host, 924, "INFO", "没有需要转移的种子")
				logAt(ctx, c.Host, 925, "INFO", "转移做种任务执行完成")
				pass.finished = true
			} else {
				pass.counts.Total = len(pass.items)
				logAt(ctx, c.Host, 770, "INFO", fmt.Sprintf("需要转移的种子数：%d", len(pass.items)))
			}
		}
		return nil
	}
	if e := c.transferOne(ctx, pass); e != nil {
		return e
	}
	if pass.next == len(pass.items) {
		if e := c.finishTransfer(ctx, pass); e != nil {
			return e
		}
		pass.finished = true
	}
	return nil
}
func (c *Core) beginTransfer(ctx context.Context) (*transferCursor, error) {
	h := c.Host
	logAt(ctx, h, 677, "INFO", "开始转移做种任务 ...")
	valid, e := c.config().Validate(ctx, h)
	if e != nil || !valid {
		return nil, e
	}
	from, e := c.service(ctx, c.config().values["fromdownloader"])
	if e != nil {
		return nil, e
	}
	to, e := c.service(ctx, c.config().values["todownloader"])
	if e != nil {
		return nil, e
	}
	if from == nil || to == nil {
		return nil, nil
	}
	torrents, e := h.Completed(ctx, *from)
	if e != nil {
		return nil, e
	}
	if len(torrents) == 0 {
		logAt(ctx, h, 694, "INFO", "下载器 "+from.Name+" 没有已做种的种子")
		return nil, nil
	}
	logAt(ctx, h, 692, "INFO", fmt.Sprintf("下载器 %s 已做种的种子数：%d", from.Name, len(torrents)))
	return &transferCursor{candidates: torrents, selecting: true, items: []selected{}, from: *from, to: *to}, nil
}
func (c *Core) selectOne(ctx context.Context, pass *transferCursor) error {
	if pass.scan >= len(pass.candidates) {
		return errors.New("selection cursor exhausted")
	}
	t := pass.candidates[pass.scan]
	pass.scan++
	from := &pass.from
	h := c.Host
	if c.state.stopped.Load() {
		logAt(ctx, h, 701, "INFO", "转移服务停止")
		pass.finished = true
		return nil
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	hash, save := GetHash(t, from.Type), GetSavePath(t, from.Type)
	if c.config().flag("nopaths") && save != "" {
		filter, e := c.config().string("nopaths")
		if e != nil {
			return e
		}
		skip := false
		for _, p := range strings.Split(filter, "\n") {
			if strings.HasPrefix(posixNorm(save), posixNorm(p)) {
				logAt(ctx, h, 714, "INFO", fmt.Sprintf("种子 %s 保存路径 %s 不需要转移，跳过 ...", hash, save))
				skip = true
				break
			}
		}
		if skip {
			return nil
		}
	}
	labels := GetLabels(t, from.Type)
	empty := len(labels) == 0 || (len(labels) == 1 && labels[0] == "")
	category := GetCategory(t, from.Type)
	if c.config().flag("includecategory") {
		s, e := c.config().string("includecategory")
		if e != nil {
			return e
		}
		if !contains(strings.Split(s, ","), category) {
			logAt(ctx, h, 733, "INFO", fmt.Sprintf("种子 %s 不含有转移分类 %s，跳过 ...", hash, s))
			return nil
		}
	}
	if empty {
		if !c.config().flag("transferemptylabel") {
			return nil
		}
	} else {
		skip := false
		if c.config().flag("nolabels") {
			s, e := c.config().string("nolabels")
			if e != nil {
				return e
			}
			for _, label := range strings.Split(s, ",") {
				if contains(labels, label) {
					logAt(ctx, h, 745, "INFO", fmt.Sprintf("种子 %s 含有不转移标签 %s，跳过 ...", hash, label))
					skip = true
					break
				}
			}
		}
		if skip {
			return nil
		}
		if c.config().flag("includelabels") {
			s, e := c.config().string("includelabels")
			if e != nil {
				return e
			}
			for _, label := range strings.Split(s, ",") {
				if !contains(labels, label) {
					logAt(ctx, h, 755, "INFO", fmt.Sprintf("种子 %s 不含有转移标签 %s，跳过 ...", hash, label))
					skip = true
					break
				}
			}
		}
		if skip {
			return nil
		}
	}
	pass.items = append(pass.items, selected{hash, save, t})

	return nil
}
func (c *Core) transferOne(ctx context.Context, pass *transferCursor) error {
	if pass.next >= len(pass.items) {
		return errors.New("torrent transfer cursor exhausted")
	}
	item := pass.items[pass.next]
	pass.next++
	counts := &pass.counts
	from, to := &pass.from, &pass.to
	backup, e := asString(c.config().values["fromtorrentpath"])
	if e != nil {
		return e
	}
	file := path.Join(backup, item.hash+".torrent")
	exists, e := c.Host.Exists(ctx, file)
	if e != nil {
		return e
	}
	if !exists {
		logAt(ctx, c.Host, 787, "ERROR", "种子文件不存在："+file)
		counts.Fail++
		return nil
	}
	duplicate, e := c.Host.Torrents(ctx, *to, []string{item.hash})
	if e != nil {
		return e
	}
	if len(duplicate) > 0 {
		if c.config().flag("deleteduplicate") {
			logAt(ctx, c.Host, 797, "INFO", "删除重复的源下载器任务（不含文件）："+item.hash+" ...")
			if _, e = c.Host.DeleteSource(ctx, *from, item.hash); e != nil {
				return e
			}
			counts.DeleteDuplicate++
		} else {
			logAt(ctx, c.Host, 801, "INFO", item.hash+" 已在目的下载器中，跳过 ...")
			counts.Skip++
		}
		return nil
	}
	save := convertSavePathValue(item.save, c.config().values["frompath"], c.config().values["topath"])
	if !truth(save) {
		logAt(ctx, c.Host, 811, "ERROR", "转换保存路径失败："+item.save)
		counts.Fail++
		return nil
	}
	if from.Type == "qbittorrent" {
		content, e := c.Host.ReadFile(ctx, file)
		if e != nil {
			return e
		}
		if len(content) == 0 {
			logAt(ctx, c.Host, 821, "WARN", "读取种子文件失败："+file)
			counts.Fail++
			return nil
		}
		decoded, e := bdecode(content)
		var main map[string]any
		if e == nil {
			var ok bool
			main, ok = decoded.(map[string]any)
			if !ok {
				e = errors.New("source AttributeError: decoded value has no get")
			}
		}
		if e != nil {
			logAt(ctx, c.Host, 829, "WARN", fmt.Sprintf("解析种子文件 %s 失败：%s", file, e))
			counts.Fail++
			return nil
		}
		if !truth(main["announce"]) {
			logAt(ctx, c.Host, 834, "INFO", item.hash+" 未发现tracker信息，尝试补充tracker信息...")
			resume := path.Join(backup, item.hash+".fastresume")
			ok, e := c.Host.Exists(ctx, resume)
			if e != nil {
				return e
			}
			if !ok {
				logAt(ctx, c.Host, 838, "WARN", "fastresume文件不存在："+resume)
				counts.Fail++
				return nil
			}
			raw, e := c.Host.ReadFile(ctx, resume)
			var parsed any
			if e == nil {
				parsed, e = bdecode(raw)
			}
			if e == nil {
				var m map[string]any
				var ok bool
				m, ok = parsed.(map[string]any)
				if !ok {
					e = errors.New("source AttributeError: fastresume has no get")
				} else if trackers, ok := m["trackers"].([]any); ok && len(trackers) > 0 && truth(trackers[0]) {
					var tracker any
					var firstlen int
					switch first := trackers[0].(type) {
					case []any:
						firstlen = len(first)
						tracker = first[0]
					case string:
						firstlen = len([]rune(first))
						tracker = string([]rune(first)[0])
					case []byte:
						firstlen = len(first)
						tracker = newByteInteger(first[0])
					default:
						e = errors.New("source TypeError: tracker object is not subscriptable")
					}
					if e == nil {
						main["announce"] = tracker
						if len(trackers) > 1 || firstlen > 1 {
							main["announce-list"] = trackers
						}
						content, e = bencode(main)
						if e == nil {
							file, e = c.Host.WriteTemp(ctx, item.hash+".torrent", content)
						}
					}
				}
			}
			if e != nil {
				logAt(ctx, c.Host, 861, "ERROR", fmt.Sprintf("解析fastresume文件 %s 出错：%s", resume, e))
				counts.Fail++
				return nil
			}
		}
	}
	logAt(ctx, c.Host, 866, "INFO", "添加转移做种任务到下载器 "+to.Name+"："+file)
	content, e := c.Host.ReadFile(ctx, file)
	if e != nil {
		return e
	}
	id, e := c.download(ctx, *to, content, save, item.torrent)
	if e != nil {
		return e
	}
	if id == "" {
		counts.Fail++
		logAt(ctx, c.Host, 874, "ERROR", "添加下载任务失败："+file)
		return nil
	}
	logAt(ctx, c.Host, 878, "INFO", "成功添加转移做种任务，种子文件："+file)
	if to.Type == "qbittorrent" {
		if c.config().flag("skipverify") {
			if c.config().flag("autostart") {
				logAt(ctx, c.Host, 884, "INFO", id+" 跳过校验，开启自动开始，注意观察种子的完整性")
				c.addRecheck(ctx, *to, id)
			} else {
				logAt(ctx, c.Host, 888, "INFO", id+" 跳过校验，请自行检查手动开始任务...")
			}
		} else {
			logAt(ctx, c.Host, 890, "INFO", "qbittorrent 开始校验 "+id+" ...")
			if _, e = c.Host.Recheck(ctx, *to, []string{id}); e != nil {
				return e
			}
			c.addRecheck(ctx, *to, id)
		}
	} else {
		c.addRecheck(ctx, *to, id)
	}
	if c.config().flag("deletesource") {
		logAt(ctx, c.Host, 898, "INFO", "删除源下载器任务（不含文件）："+item.hash+" ...")
		if _, e = c.Host.DeleteSource(ctx, *from, item.hash); e != nil {
			return e
		}
	}
	counts.Success++
	if e = c.Host.SaveData(ctx, from.Name+"-"+item.hash, map[string]any{"to_download": to.Name, "to_download_id": id, "delete_source": c.config().values["deletesource"], "delete_duplicate": c.config().values["deleteduplicate"]}); e != nil {
		return e
	}

	return nil
}
func (c *Core) finishTransfer(ctx context.Context, pass *transferCursor) error {
	counts := &pass.counts
	if counts.Success > 0 && c.config().flag("autostart") {
		if e := c.checkRecheck(ctx); e != nil {
			return e
		}
	}
	if c.config().flag("notify") {
		if e := c.Host.Notify(ctx, Notice{"SiteMessage", "【转移做种任务执行完成】", fmt.Sprintf("总数：%d，成功：%d，失败：%d，跳过：%d，删除重复：%d", counts.Total, counts.Success, counts.Fail, counts.Skip, counts.DeleteDuplicate)}); e != nil {
			return e
		}
	}
	logAt(ctx, c.Host, 925, "INFO", "转移做种任务执行完成")
	return nil
}

func (c *Core) addRecheck(ctx context.Context, s Service, id string) {
	logAt(ctx, c.Host, 929, "INFO", "添加校验检查任务："+id+" ...")
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	c.state.Recheck[s.Name] = append(c.state.Recheck[s.Name], id)
}
func (c *Core) CheckRecheck(ctx context.Context) error { return c.checkRecheck(ctx) }
func (c *Core) checkRecheck(ctx context.Context) error {
	state := c.state
	state.mu.Lock()
	early := len(state.Recheck) == 0 || !c.config().flag("todownloader") || state.recheckRunning
	state.mu.Unlock()
	if early {
		return nil
	}
	to, e := c.service(ctx, c.config().values["todownloader"])
	if e != nil || to == nil {
		return e
	}
	state.mu.Lock()
	ids := append([]string{}, state.Recheck[to.Name]...)
	if len(ids) == 0 {
		state.mu.Unlock()
		return nil
	}
	state.recheckRunning = true
	state.mu.Unlock()
	logAt(ctx, c.Host, 957, "INFO", "开始检查下载器 "+to.Name+" 的校验任务 ...")
	torrents, e := c.Host.Torrents(ctx, *to, ids)
	// No finally: the original gate remains wedged on a raised query/start.
	if e != nil {
		return e
	}
	if len(torrents) > 0 {
		ready := []string{}
		for _, t := range torrents {
			if CanSeed(t, to.Type) {
				ready = append(ready, GetHash(t, to.Type))
			}
		}
		if len(ready) > 0 {
			logAt(ctx, c.Host, 974, "INFO", fmt.Sprintf("共 %d 个任务校验完成，开始做种", len(ready)))
			if _, e = c.Host.Start(ctx, *to, ready); e != nil {
				return e
			}
			left := []string{}
			for _, id := range unique(ids) {
				if !contains(ready, id) {
					left = append(left, id)
				}
			}
			state.mu.Lock()
			state.Recheck[to.Name] = left
			state.mu.Unlock()
		} else {
			logAt(ctx, c.Host, 981, "INFO", "没有新的任务校验完成，将在下次个周期继续检查 ...")
		}
	} else if torrents == nil {
		logAt(ctx, c.Host, 984, "INFO", "下载器 "+to.Name+" 查询校验任务失败，将在下次继续查询 ...")
	} else {
		logAt(ctx, c.Host, 986, "INFO", "下载器 "+to.Name+" 中没有需要检查的校验任务，清空待处理列表")
		state.mu.Lock()
		state.Recheck[to.Name] = []string{}
		state.mu.Unlock()
	}
	state.mu.Lock()
	state.recheckRunning = false
	state.mu.Unlock()
	return nil
}
