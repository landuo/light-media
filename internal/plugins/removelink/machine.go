package removelink

import (
	"encoding/json"
	"fmt"

	py "light-media/internal/releasemeta/legacyanime"
	"strings"
	"sync"
	"time"
)

type Machine struct {
	mu           sync.Mutex
	Config       Config
	State        []Tracked
	Queue        []*Task
	TimerDue     *time.Time
	Watches      []Watch
	Ops          Ops
	Home         string
	Now          func() time.Time
	Errors       []error
	SourceErrors []error
	noticeFatal  error
}
type Watch struct{ Path, Mode string }

func New(ops Ops) *Machine {
	return &Machine{Config: NewConfig(), State: []Tracked{}, Queue: []*Task{}, Watches: []Watch{}, Ops: ops, Now: time.Now}
}
func (m *Machine) StateValue() any { m.mu.Lock(); defer m.mu.Unlock(); return m.Config.Enabled }
func (m *Machine) Init(raw json.RawMessage) (err error) {
	defer capture(&err)
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.Config.Apply(raw); e != nil {
		return e
	}
	m.stop()
	m.Queue = []*Task{}
	if !Truth(m.Config.Enabled) {
		return nil
	}
	if Truth(m.Config.STRM) {
		m.Config.ensureString("strm_path_mappings")
	}
	m.Config.ensureString("monitor_dirs")
	watches := []Watch{}
	for _, p := range strings.Split(m.Config.Monitor, "\n") {
		p = strings.TrimSpace(p)
		if p != "" {
			watches = append(watches, Watch{p, "hardlink"})
		}
	}
	if Truth(m.Config.STRM) {
		for _, x := range ParseMappings(m.Config.Mappings) {
			watches = append(watches, Watch{x.Prefix, "strm"})
		}
	}
	m.Watches = watches
	m.State = []Tracked{}
	now := m.Now()
	for _, w := range watches {
		if Trickplay(w.Path) {
			continue
		}
		rows, e := m.Ops.Scan(w.Path)
		if e != nil {
			continue
		}
		for _, r := range rows {
			if Trickplay(r.Path) {
				continue
			}
			r.Identity.Added = now
			m.put(r)
		}
	}
	return nil
}
func (m *Machine) Stop() { m.mu.Lock(); defer m.mu.Unlock(); m.stop() }
func (m *Machine) stop() {
	m.Watches = []Watch{}
	m.TimerDue = nil
	tasks := m.Queue
	m.Queue = []*Task{}
	for _, t := range tasks {
		if !t.Processed {
			m.execute(t)
		}
		if !t.Processed {
			m.Queue = append(m.Queue, t)
		}
	}
}
func (m *Machine) put(r Tracked) {
	for i, x := range m.State {
		if x.Path == r.Path {
			m.State[i] = r
			return
		}
	}
	m.State = append(m.State, r)
}
func (m *Machine) pop(p string) (Tracked, bool) {
	for i, x := range m.State {
		if x.Path == p {
			m.State = append(m.State[:i], m.State[i+1:]...)
			return x, true
		}
	}
	return Tracked{}, false
}
func (m *Machine) find(p string) (Tracked, bool) {
	for _, x := range m.State {
		if x.Path == p {
			return x, true
		}
	}
	return Tracked{}, false
}
func same(a, b Identity) bool { return a.Dev == b.Dev && a.Inode == b.Inode }
func (m *Machine) add(p string) {
	if Trickplay(p) {
		return
	}
	for _, ext := range []string{".!qB", ".part", ".mp", ".tmp", ".temp"} {
		if suffix(p) == ext {
			return
		}
	}
	if m.keywordExcluded(p) {
		return
	}
	if !m.Ops.Exists(p) {
		return
	}
	id, e := m.Ops.Stat(p)
	if e == nil {
		id.Added = m.Now()
		m.put(Tracked{p, id})
	}
}
func (m *Machine) Created(p string, isDir bool) (err error) {
	defer capture(&err)
	p = cleanPath(p)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !isDir {
		m.add(p)
	}
	return
}
func (m *Machine) Moved(src, dest, mode string, isDir bool) (err error) {
	src, dest = cleanPath(src), cleanPath(dest)
	defer capture(&err)
	m.mu.Lock()
	defer m.mu.Unlock()
	if isDir {
		return
	}
	if Trickplay(src) || Trickplay(dest) {
		m.pop(src)
		return
	}
	old, ok := m.pop(src)
	m.add(dest)
	next, exists := m.find(dest)
	if ok && (!exists || !same(next.Identity, old.Identity)) {
		m.put(old)
		if mode != "strm" {
			m.deleted(src)
		} else if py.LowerCase(suffix(src)) == ".strm" {
			m.strm(src)
		}
	}
	return
}
func (m *Machine) Deleted(p, mode string, isDir bool) (err error) {
	p = cleanPath(p)
	defer capture(&err)
	m.mu.Lock()
	defer m.mu.Unlock()
	if Trickplay(p) {
		m.pop(p)
		return
	}
	if isDir {
		if Truth(m.Config.Torrents) {
			m.event(p)
		}
		return
	}
	for _, ext := range []string{".!qB", ".part", ".mp"} {
		if suffix(p) == ext {
			return
		}
	}
	if m.keywordExcluded(p) {
		return
	}
	if mode == "strm" {
		if py.LowerCase(suffix(p)) == ".strm" {
			m.strm(p)
		}
	} else {
		m.deleted(p)
	}
	return
}
func (m *Machine) deleted(p string) {
	if Trickplay(p) {
		m.pop(p)
		return
	}
	old, ok := m.pop(p)
	if !ok {
		return
	}
	t := &Task{Path: p, Identity: old.Identity, At: m.Now()}
	if Truth(m.Config.Delayed) {
		m.Queue = append(m.Queue, t)
		if m.TimerDue == nil {
			due := m.Now().Add(time.Duration(m.Config.Delay) * time.Second)
			m.TimerDue = &due
		}
		return
	}
	m.cleanup(t, false)
}
func (m *Machine) Tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	for _, t := range m.Queue {
		if !t.Processed && now.Sub(t.At) >= time.Duration(m.Config.Delay)*time.Second {
			m.execute(t)
		}
	}
	out := []*Task{}
	for _, t := range m.Queue {
		if !t.Processed {
			out = append(out, t)
		}
	}
	m.Queue = out
	m.TimerDue = nil
	for _, t := range out {
		due := t.At.Add(time.Duration(m.Config.Delay) * time.Second)
		minDue := now.Add(time.Second)
		if due.Before(minDue) {
			due = minDue
		}
		if m.TimerDue == nil || due.Before(*m.TimerDue) {
			m.TimerDue = &due
		}
	}
}
func (m *Machine) execute(t *Task) {
	// A source finally marks the task that reached an unknown result processed;
	// later tasks never enter that source call and must remain pending.
	if m.noticeFatal != nil {
		return
	}
	defer func() {
		t.Processed = true
		if x := recover(); x != nil {
			m.remember(x)
		}
	}()
	if Trickplay(t.Path) {
		m.pop(t.Path)
		return
	}
	if m.Ops.Exists(t.Path) {
		return
	}
	for _, x := range m.State {
		if Trickplay(x.Path) || x.Path == t.Path || !same(x.Identity, t.Identity) {
			continue
		}
		window := time.Duration(max(5, m.Config.Delay)) * time.Second
		if x.Identity.Added.After(t.Identity.Added) && !x.Identity.Added.Before(t.At.Add(-window)) {
			m.scrap(t.Path)
			return
		}
	}
	m.cleanup(t, true)
}
func (m *Machine) cleanup(t *Task, delayed bool) {
	m.scrap(t.Path)
	if Truth(m.Config.Torrents) && !m.Config.IsScrap(t.Path) {
		m.event(t.Path)
	}
	m.history("src", t.Path)
	m.cleanupLinks(t, delayed)
}
func (m *Machine) cleanupLinks(t *Task, delayed bool) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
		}
	}()
	deleted := []string{}
	for _, x := range append([]Tracked{}, m.State...) {
		if !same(x.Identity, t.Identity) {
			continue
		}
		if !m.unlink(x.Path) {
			continue
		}
		deleted = append(deleted, x.Path)
		m.scrap(x.Path)
		if Truth(m.Config.Torrents) && !m.Config.IsScrap(x.Path) {
			m.event(x.Path)
		}
		m.history("src", x.Path)
	}
	if Truth(m.Config.Notify) && len(deleted) > 0 {
		parts := []string{"🗂️ 源文件：" + t.Path}
		if len(deleted) == 1 {
			parts = append(parts, "🔗 硬链接："+deleted[0])
		} else {
			parts = append(parts, fmt.Sprintf("🔗 删除了 %d 个硬链接文件", len(deleted)))
		}
		if Truth(m.Config.History) {
			parts = append(parts, "📝 已清理转移记录")
		}
		if Truth(m.Config.Torrents) {
			parts = append(parts, "🌱 已联动删除种子")
		}
		if Truth(m.Config.Scrap) {
			parts = append(parts, "🖼️ 已清理刮削文件")
		}
		prefix := "⚡ 立即删除完成"
		if delayed {
			prefix = "⏰ 延迟删除完成"
		}
		site := "hardlink_immediate"
		if delayed {
			site = "hardlink_delayed"
		}
		m.notice(site, "🧹 媒体文件清理", prefix+"\n\n"+strings.Join(parts, "\n"))
	}
}
func (m *Machine) unlink(p string) bool {
	if Trickplay(p) {
		m.pop(p)
		return false
	}
	if m.Config.Excluded(p, m.Home) {
		return false
	}
	m.beforeWrite()
	if e := m.Ops.Unlink(p); e != nil {
		if missing(e) {
			m.pop(p)
		} else {
			m.Errors = append(m.Errors, e)
		}
		return false
	}
	m.pop(p)
	return true
}
func (m *Machine) history(kind, p string) bool {
	if !Truth(m.Config.History) {
		return false
	}
	m.beforeWrite()
	ok, e := m.Ops.History(kind, p)
	checked(e)
	return ok
}
func (m *Machine) scrap(p string) {
	if Trickplay(p) || !Truth(m.Config.Scrap) || !m.Ops.Exists(parent(p)) {
		return
	}
	if !m.Config.IsScrap(p) {
		rows, e := m.Ops.Children(parent(p))
		if e != nil {
			m.Errors = append(m.Errors, e)
		}
		if e == nil {
			for _, x := range rows {
				if !strings.HasPrefix(base(x.Path), stem(p)) {
					continue
				}
				if x.Dir && py.LowerCase(suffix(x.Path)) == ".trickplay" && stem(x.Path) == stem(p) {
					m.beforeWrite()
					if e := m.Ops.RemoveTree(x.Path); e != nil {
						m.Errors = append(m.Errors, e)
						break
					}
				} else if m.Config.IsScrap(x.Path) {
					m.beforeWrite()
					if e := m.Ops.Unlink(x.Path); e != nil {
						m.Errors = append(m.Errors, e)
						break
					}
				}
			}
		}
	}
	m.empty(p)
}
func (m *Machine) onlyScrap(p string) (bool, error) {
	rows, e := m.Ops.Children(p)
	if e != nil {
		return false, e
	}
	for _, x := range rows {
		if x.Dir {
			if py.LowerCase(suffix(x.Path)) != ".trickplay" {
				return false, nil
			}
		} else if !m.Config.IsScrap(x.Path) {
			return false, nil
		}
	}
	return true, nil
}
func (m *Machine) empty(p string) {
	if Trickplay(p) {
		return
	}
	for {
		parent := parent(p)
		if m.Config.Excluded(parent, m.Home) || !m.Ops.Exists(parent) {
			return
		} // Source's Path-vs-str monitor-boundary test is always false.
		only, scrapErr := m.onlyScrap(parent)
		if scrapErr != nil {
			m.Errors = append(m.Errors, scrapErr)
		}
		if only {
			rows, e := m.Ops.Children(parent)
			if e == nil {
				for _, x := range rows {
					if x.Dir {
						m.beforeWrite()
						e = m.Ops.RemoveTree(x.Path)
					} else {
						m.beforeWrite()
						e = m.Ops.Unlink(x.Path)
					}
					if e != nil {
						m.Errors = append(m.Errors, e)
						break
					}
				}
			}
		}
		rows, e := m.Ops.Children(parent)
		if e != nil {
			m.Errors = append(m.Errors, e)
		}
		if e == nil && len(rows) > 0 {
			return
		}
		if e == nil {
			m.beforeWrite()
			if e := m.Ops.RemoveDir(parent); e != nil {
				m.Errors = append(m.Errors, e)
			} else if Truth(m.Config.Notify) {
				// Source catches a notification exception at this level, then
				// advances to the next parent. Infrastructure uncertainty is not caught.
				if e := m.sendNotice("empty_directory", "📁 目录清理", "🗑️ 清理空目录："+parent); knownSourceNotice(e) {
					m.SourceErrors = append(m.SourceErrors, e)
				} else {
					checked(e)
				}
			}
		}
		if parent == p {
			return
		}
		p = parent
	}
}

func (m *Machine) remember(x any) {
	if e, ok := x.(effectFailure); ok {
		if knownSourceNotice(e.err) {
			m.SourceErrors = append(m.SourceErrors, e.err)
		} else {
			m.Errors = append(m.Errors, e.err)
		}
	} else {
		panic(x)
	}
}
func (m *Machine) event(p string) { m.beforeWrite(); checked(m.Ops.Event(p)) }
func (m *Machine) beforeWrite()   { checked(m.noticeFatal) }
func (m *Machine) sendNotice(site, title, text string) error {
	m.beforeWrite()
	var err error
	if ops, ok := m.Ops.(interface {
		NoticeAt(string, string, string) error
	}); ok {
		err = ops.NoticeAt(site, title, text)
	} else {
		err = m.Ops.Notice(title, text)
	}
	if err != nil && !knownSourceNotice(err) {
		m.noticeFatal = err
	}
	return err
}
func (m *Machine) notice(site, title, text string) { checked(m.sendNotice(site, title, text)) }
func (m *Machine) storageExists(x Item) bool       { ok, e := m.Ops.StorageExists(x); checked(e); return ok }
func (m *Machine) storageList(x Item) []Item       { v, e := m.Ops.StorageList(x); checked(e); return v }
func (m *Machine) storageDelete(x Item) bool {
	m.beforeWrite()
	ok, e := m.Ops.StorageDelete(x)
	checked(e)
	return ok
}

func (m *Machine) keywordExcluded(p string) bool {
	m.Config.ensureString("exclude_keywords")
	return keywords(m.Config.Keywords, p)
}
