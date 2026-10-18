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

type Core struct {
	Config         Config
	Host           Host
	Recheck        map[string][]string
	recheckRunning bool
	stopped        atomic.Bool
	mu             sync.Mutex
}

func NewCore(c Config, h Host) (*Core, error) {
	if h == nil {
		return nil, errors.New("transfer host required")
	}
	return &Core{Config: c, Host: h, Recheck: map[string][]string{}}, nil
}
func RestoreCore(s State, h Host) (*Core, error) {
	c, e := RestoreConfig(s.Config)
	if e != nil {
		return nil, e
	}
	x, e := NewCore(c, h)
	if e != nil {
		return nil, e
	}
	for k, v := range s.Recheck {
		x.Recheck[k] = append([]string{}, v...)
	}
	x.recheckRunning = s.RecheckRunning
	return x, nil
}
func (c *Core) Snapshot() (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, e := c.Config.Snapshot()
	r := map[string][]string{}
	for k, v := range c.Recheck {
		r[k] = append([]string{}, v...)
	}
	return State{m, r, c.recheckRunning}, e
}
func (c *Core) Stop() { c.stopped.Store(true) }
func (c *Core) service(ctx context.Context, name any) (*Service, error) {
	if !truth(name) {
		logAt(ctx, c.Host, 142, "WARN", "尚未配置下载器，请检查配置")
		return nil, nil
	}
	n, e := asString(name)
	if e != nil {
		return nil, e
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
	if save == "" {
		return &to
	}
	if to == "" || from == "" {
		return &save
	}
	save = strings.ReplaceAll(posixNorm(save), "\\", "/")
	from = strings.ReplaceAll(posixNorm(from), "\\", "/")
	to = strings.ReplaceAll(posixNorm(to), "\\", "/")
	if strings.HasPrefix(save, from) {
		v := strings.Replace(save, from, to, 1)
		return &v
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
	return strings.TrimSpace(s)
}
func GetLabels(t Torrent, kind string) []string {
	out := []string{}
	if kind == "qbittorrent" {
		s, ok := t["tags"].(string)
		if !ok {
			return out
		}
		for _, v := range strings.Split(s, ",") {
			out = append(out, strings.TrimSpace(v))
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

func (c *Core) download(ctx context.Context, s Service, content []byte, save string, t Torrent) (string, error) {
	// Original __download resolves the source service again before either kind.
	from, e := c.service(ctx, c.Config.values["fromdownloader"])
	if e != nil {
		return "", e
	}
	if s.Type == "qbittorrent" {
		tag, e := c.Host.RandomTag(ctx, 10)
		if e != nil {
			return "", e
		}
		tags := append([]string{}, c.Config.tags...)
		if c.Config.flag("remainoldtag") {
			if from == nil {
				return "", errors.New("source AttributeError: missing source service")
			}
			tags = unique(append(append(GetLabels(t, from.Type), c.Config.tags...), tag))
		} else {
			tags = append(tags, tag)
		}
		var category any
		if c.Config.flag("remainoldcat") {
			if from == nil {
				return "", errors.New("source AttributeError: missing source service")
			}
			category = GetCategory(t, from.Type)
		}
		r, e := c.Host.AddPaused(ctx, s, AddSpec{content, save, tags, category, c.Config.flag("skipverify")})
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
		tags := append([]string{}, c.Config.tags...)
		if c.Config.flag("remainoldtag") {
			if from == nil {
				return "", errors.New("source AttributeError: missing source service")
			}
			tags = unique(append(GetLabels(t, from.Type), c.Config.tags...))
		}
		r, e := c.Host.AddPaused(ctx, s, AddSpec{Content: content, SavePath: save, Tags: tags})
		if e != nil {
			return "", e
		}
		if !r.SourceTruthy {
			return "", nil
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

func (c *Core) Transfer(ctx context.Context) (Counts, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := Counts{}
	h := c.Host
	logAt(ctx, h, 677, "INFO", "开始转移做种任务 ...")
	valid, e := c.Config.Validate(ctx, h)
	if e != nil || !valid {
		return counts, e
	}
	from, e := c.service(ctx, c.Config.values["fromdownloader"])
	if e != nil {
		return counts, e
	}
	to, e := c.service(ctx, c.Config.values["todownloader"])
	if e != nil {
		return counts, e
	}
	if from == nil || to == nil {
		return counts, nil
	}
	torrents, e := h.Completed(ctx, *from)
	if e != nil {
		return counts, e
	}
	if len(torrents) == 0 {
		logAt(ctx, h, 694, "INFO", "下载器 "+from.Name+" 没有已做种的种子")
		return counts, nil
	}
	logAt(ctx, h, 692, "INFO", fmt.Sprintf("下载器 %s 已做种的种子数：%d", from.Name, len(torrents)))
	items := []selected{}
	for _, t := range torrents {
		if c.stopped.Load() {
			logAt(ctx, h, 701, "INFO", "转移服务停止")
			return counts, nil
		}
		if e = ctx.Err(); e != nil {
			return counts, e
		}
		hash, save := GetHash(t, from.Type), GetSavePath(t, from.Type)
		if c.Config.flag("nopaths") && save != "" {
			filter, e := c.Config.string("nopaths")
			if e != nil {
				return counts, e
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
				continue
			}
		}
		labels := GetLabels(t, from.Type)
		empty := len(labels) == 0 || (len(labels) == 1 && labels[0] == "")
		category := GetCategory(t, from.Type)
		if c.Config.flag("includecategory") {
			s, e := c.Config.string("includecategory")
			if e != nil {
				return counts, e
			}
			if !contains(strings.Split(s, ","), category) {
				logAt(ctx, h, 733, "INFO", fmt.Sprintf("种子 %s 不含有转移分类 %s，跳过 ...", hash, s))
				continue
			}
		}
		if empty {
			if !c.Config.flag("transferemptylabel") {
				continue
			}
		} else {
			skip := false
			if c.Config.flag("nolabels") {
				s, e := c.Config.string("nolabels")
				if e != nil {
					return counts, e
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
				continue
			}
			if c.Config.flag("includelabels") {
				s, e := c.Config.string("includelabels")
				if e != nil {
					return counts, e
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
				continue
			}
		}
		items = append(items, selected{hash, save, t})
	}
	if len(items) == 0 {
		logAt(ctx, h, 924, "INFO", "没有需要转移的种子")
		logAt(ctx, h, 925, "INFO", "转移做种任务执行完成")
		return counts, nil
	}
	counts.Total = len(items)
	logAt(ctx, h, 770, "INFO", fmt.Sprintf("需要转移的种子数：%d", counts.Total))
	backup, e := c.Config.string("fromtorrentpath")
	if e != nil {
		return counts, e
	}
	fromRoot, e := c.Config.string("frompath")
	if e != nil {
		return counts, e
	}
	toRoot, e := c.Config.string("topath")
	if e != nil {
		return counts, e
	}
	for _, item := range items {
		file := path.Join(backup, item.hash+".torrent")
		exists, e := h.Exists(ctx, file)
		if e != nil {
			return counts, e
		}
		if !exists {
			logAt(ctx, h, 787, "ERROR", "种子文件不存在："+file)
			counts.Fail++
			continue
		}
		duplicate, e := h.Torrents(ctx, *to, []string{item.hash})
		if e != nil {
			return counts, e
		}
		if len(duplicate) > 0 {
			if c.Config.flag("deleteduplicate") {
				logAt(ctx, h, 797, "INFO", "删除重复的源下载器任务（不含文件）："+item.hash+" ...")
				if _, e = h.DeleteSource(ctx, *from, item.hash); e != nil {
					return counts, e
				}
				counts.DeleteDuplicate++
			} else {
				logAt(ctx, h, 801, "INFO", item.hash+" 已在目的下载器中，跳过 ...")
				counts.Skip++
			}
			continue
		}
		save := ConvertSavePath(item.save, fromRoot, toRoot)
		if save == nil || *save == "" {
			logAt(ctx, h, 811, "ERROR", "转换保存路径失败："+item.save)
			counts.Fail++
			continue
		}
		if from.Type == "qbittorrent" {
			content, e := h.ReadFile(ctx, file)
			if e != nil {
				return counts, e
			}
			if len(content) == 0 {
				logAt(ctx, h, 821, "WARN", "读取种子文件失败："+file)
				counts.Fail++
				continue
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
				logAt(ctx, h, 829, "WARN", fmt.Sprintf("解析种子文件 %s 失败：%s", file, e))
				counts.Fail++
				continue
			}
			if !truth(main["announce"]) {
				logAt(ctx, h, 834, "INFO", item.hash+" 未发现tracker信息，尝试补充tracker信息...")
				resume := path.Join(backup, item.hash+".fastresume")
				ok, e := h.Exists(ctx, resume)
				if e != nil {
					return counts, e
				}
				if !ok {
					logAt(ctx, h, 838, "WARN", "fastresume文件不存在："+resume)
					counts.Fail++
					continue
				}
				raw, e := h.ReadFile(ctx, resume)
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
								file, e = h.WriteTemp(ctx, item.hash+".torrent", content)
							}
						}
					}
				}
				if e != nil {
					logAt(ctx, h, 861, "ERROR", fmt.Sprintf("解析fastresume文件 %s 出错：%s", resume, e))
					counts.Fail++
					continue
				}
			}
		}
		logAt(ctx, h, 866, "INFO", "添加转移做种任务到下载器 "+to.Name+"："+file)
		content, e := h.ReadFile(ctx, file)
		if e != nil {
			return counts, e
		}
		id, e := c.download(ctx, *to, content, *save, item.torrent)
		if e != nil {
			return counts, e
		}
		if id == "" {
			counts.Fail++
			logAt(ctx, h, 874, "ERROR", "添加下载任务失败："+file)
			continue
		}
		logAt(ctx, h, 878, "INFO", "成功添加转移做种任务，种子文件："+file)
		if to.Type == "qbittorrent" {
			if c.Config.flag("skipverify") {
				if c.Config.flag("autostart") {
					logAt(ctx, h, 884, "INFO", id+" 跳过校验，开启自动开始，注意观察种子的完整性")
					c.addRecheck(ctx, *to, id)
				} else {
					logAt(ctx, h, 888, "INFO", id+" 跳过校验，请自行检查手动开始任务...")
				}
			} else {
				logAt(ctx, h, 890, "INFO", "qbittorrent 开始校验 "+id+" ...")
				if _, e = h.Recheck(ctx, *to, []string{id}); e != nil {
					return counts, e
				}
				c.addRecheck(ctx, *to, id)
			}
		} else {
			c.addRecheck(ctx, *to, id)
		}
		if c.Config.flag("deletesource") {
			logAt(ctx, h, 898, "INFO", "删除源下载器任务（不含文件）："+item.hash+" ...")
			if _, e = h.DeleteSource(ctx, *from, item.hash); e != nil {
				return counts, e
			}
		}
		counts.Success++
		if e = h.SaveData(ctx, from.Name+"-"+item.hash, map[string]any{"to_download": to.Name, "to_download_id": id, "delete_source": c.Config.values["deletesource"], "delete_duplicate": c.Config.values["deleteduplicate"]}); e != nil {
			return counts, e
		}
	}
	if counts.Success > 0 && c.Config.flag("autostart") {
		if e = c.checkRecheck(ctx); e != nil {
			return counts, e
		}
	}
	if c.Config.flag("notify") {
		if e = h.Notify(ctx, Notice{"SiteMessage", "【转移做种任务执行完成】", fmt.Sprintf("总数：%d，成功：%d，失败：%d，跳过：%d，删除重复：%d", counts.Total, counts.Success, counts.Fail, counts.Skip, counts.DeleteDuplicate)}); e != nil {
			return counts, e
		}
	}
	logAt(ctx, h, 925, "INFO", "转移做种任务执行完成")
	return counts, nil
}
func (c *Core) addRecheck(ctx context.Context, s Service, id string) {
	logAt(ctx, c.Host, 929, "INFO", "添加校验检查任务："+id+" ...")
	if c.Recheck == nil {
		c.Recheck = map[string][]string{}
	}
	c.Recheck[s.Name] = append(c.Recheck[s.Name], id)
}
func (c *Core) CheckRecheck(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checkRecheck(ctx)
}
func (c *Core) checkRecheck(ctx context.Context) error {
	if len(c.Recheck) == 0 || !c.Config.flag("todownloader") || c.recheckRunning {
		return nil
	}
	to, e := c.service(ctx, c.Config.values["todownloader"])
	if e != nil || to == nil {
		return e
	}
	ids := c.Recheck[to.Name]
	if len(ids) == 0 {
		return nil
	}
	logAt(ctx, c.Host, 957, "INFO", "开始检查下载器 "+to.Name+" 的校验任务 ...")
	c.recheckRunning = true
	torrents, e := c.Host.Torrents(ctx, *to, ids)
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
			c.Recheck[to.Name] = left
		} else {
			logAt(ctx, c.Host, 981, "INFO", "没有新的任务校验完成，将在下次个周期继续检查 ...")
		}
	} else if torrents == nil {
		logAt(ctx, c.Host, 984, "INFO", "下载器 "+to.Name+" 查询校验任务失败，将在下次继续查询 ...")
	} else {
		logAt(ctx, c.Host, 986, "INFO", "下载器 "+to.Name+" 中没有需要检查的校验任务，清空待处理列表")
		c.Recheck[to.Name] = []string{}
	}
	c.recheckRunning = false
	return nil
}
