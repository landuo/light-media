package scrapefileclean

import (
	"encoding/json"
	"fmt"
	py "light-media/internal/releasemeta/legacyanime"
	"strings"
	"sync"
	"time"
)

type Machine struct {
	mu       sync.Mutex
	Config   Config
	State    []Tracked
	Queue    []*Task
	TimerDue *time.Time
	Watches  []string
	Ops      Ops
	Home     string
	UserHome func(string) (string, error)
	Now      func() time.Time
	Errors   []error
}

func New(ops Ops) *Machine {
	return &Machine{Config: NewConfig(), State: []Tracked{}, Queue: []*Task{}, Watches: []string{}, Ops: ops, Now: time.Now}
}
func (m *Machine) GetState() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.Config.Enabled }
func (m *Machine) Init(raw json.RawMessage) (err error) {
	defer capture(&err)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stop()
	if e := m.Config.apply(raw); e != nil {
		return e
	}
	m.Queue = []*Task{}
	m.State = []Tracked{}
	if !m.Config.Enabled {
		return nil
	}
	monitor := m.Config.text("monitor_dirs", "split")
	dirs := []string{}
	for _, p := range strings.Split(monitor, "\n") {
		p = py.Strip(p)
		if p != "" {
			dirs = append(dirs, p)
		}
	}
	m.Watches = append(m.Watches, dirs...)
	now := m.Now()
	for _, dir := range dirs {
		rows, e := m.Ops.Scan(dir)
		if e != nil {
			continue
		}
		for _, r := range rows {
			if temporary(r.Path) || !m.Ops.Exists(r.Path) || m.Ops.IsSymlink(r.Path) {
				continue
			}
			r.Identity.Added = now
			m.put(r)
		}
	}
	return nil
}
func (m *Machine) Stop() { m.mu.Lock(); defer m.mu.Unlock(); m.stop() }
func (m *Machine) stop() { m.Watches = []string{}; m.TimerDue = nil; m.Queue = []*Task{} }
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
func (m *Machine) remember(p any) {
	if f, ok := p.(effectFailure); ok {
		m.Errors = append(m.Errors, f.err)
	} else {
		panic(p)
	}
}
func (m *Machine) withinMonitor(path string) bool {
	for _, dir := range strings.Split(m.Config.text("monitor_dirs", "split"), "\n") {
		dir = py.Strip(dir)
		if dir != "" && sameOrChild(path, dir, m.Home, m.UserHome) {
			return true
		}
	}
	return false
}
func (m *Machine) add(path string) {
	if temporary(path) || m.Config.KeywordExcluded(path) {
		return
	}
	if !m.Ops.Exists(path) || m.Ops.IsDir(path) || m.Ops.IsSymlink(path) {
		return
	}
	id, e := m.Ops.Stat(path)
	if e != nil {
		return
	}
	id.Added = m.Now()
	m.put(Tracked{path, id})
}
func (m *Machine) Created(path string, isDir bool) (err error) {
	defer capture(&err)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !isDir {
		m.add(cleanPath(path))
	}
	return nil
}
func (m *Machine) Moved(src, dest string, isDir bool) (err error) {
	defer capture(&err)
	m.mu.Lock()
	defer m.mu.Unlock()
	if isDir {
		return nil
	}
	src, dest = cleanPath(src), cleanPath(dest)
	old, hasOld := m.pop(src)
	oldDest, hasDest := m.pop(dest)
	if hasDest && (!hasOld || !same(oldDest.Identity, old.Identity)) {
		if m.Config.Delayed {
			m.put(oldDest)
			m.deleted(dest)
			m.pop(dest)
		} else {
			m.cleanup(&Task{Path: dest, Identity: oldDest.Identity}, true)
		}
	}
	m.add(dest)
	next, exists := m.find(dest)
	if hasOld && (!exists || !same(next.Identity, old.Identity)) {
		if m.withinMonitor(dest) {
			return nil
		}
		m.put(old)
		m.deleted(src)
	}
	return nil
}
func (m *Machine) Deleted(path string, isDir bool) (err error) {
	defer capture(&err)
	m.mu.Lock()
	defer m.mu.Unlock()
	path = cleanPath(path)
	if isDir {
		if m.Config.Torrents && !m.Config.KeywordExcluded(path) {
			checked(m.Ops.Event(path))
		}
		return nil
	}
	if temporary(path) || m.Config.KeywordExcluded(path) {
		return nil
	}
	m.deleted(path)
	return nil
}
func (m *Machine) deleted(path string) {
	old, exists := m.pop(path)
	if !exists {
		return
	}
	if m.Config.Delayed {
		known := []string{}
		for _, other := range m.State {
			if same(other.Identity, old.Identity) {
				known = append(known, other.Path)
			}
		}
		task := &Task{Path: path, Identity: old.Identity, At: m.Now(), KnownPaths: known}
		m.Queue = append(m.Queue, task)
		if m.TimerDue == nil {
			due := m.Now().Add(time.Duration(m.Config.Delay) * time.Second)
			m.TimerDue = &due
		}
	} else {
		m.cleanup(&Task{Path: path, Identity: old.Identity}, false)
	}
}
func (m *Machine) Tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() {
		if p := recover(); p != nil {
			m.remember(p)
			m.TimerDue = nil
		}
	}()
	now := m.Now()
	due := []*Task{}
	for _, task := range m.Queue {
		if !task.Processed && now.Sub(task.At) >= time.Duration(m.Config.Delay)*time.Second {
			due = append(due, task)
		}
	}
	for _, task := range due {
		m.execute(task)
	}
	pending := []*Task{}
	for _, task := range m.Queue {
		if !task.Processed {
			pending = append(pending, task)
		}
	}
	m.Queue = pending
	m.TimerDue = nil
	for _, task := range pending {
		at := task.At.Add(time.Duration(m.Config.Delay) * time.Second)
		if at.Before(now.Add(time.Second)) {
			at = now.Add(time.Second)
		}
		if m.TimerDue == nil || at.Before(*m.TimerDue) {
			m.TimerDue = &at
		}
	}
}
func (m *Machine) execute(task *Task) {
	defer func() {
		task.Processed = true
		if p := recover(); p != nil {
			m.remember(p)
		}
	}()
	if m.Ops.Exists(task.Path) {
		id, e := m.Ops.Stat(task.Path)
		if e == nil && same(id, task.Identity) {
			return
		}
		m.cleanup(task, true)
		return
	}
	for _, other := range m.State {
		if same(other.Identity, task.Identity) && other.Path != task.Path && !contains(task.KnownPaths, other.Path) {
			m.scrap(task.Path)
			return
		}
	}
	m.cleanup(task, false)
}
func (m *Machine) cleanup(task *Task, skipPathEffects bool) {
	deleted := []string{}
	if !skipPathEffects {
		m.scrap(task.Path)
		if m.Config.Torrents && !m.Config.IsScrap(task.Path) {
			checked(m.Ops.Event(task.Path))
		}
		m.history(task.Path)
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				m.remember(p)
			}
		}()
		for _, other := range append([]Tracked{}, m.State...) {
			if !same(other.Identity, task.Identity) {
				continue
			}
			if !m.unlink(other.Path) {
				continue
			}
			deleted = append(deleted, other.Path)
			m.scrap(other.Path)
			if m.Config.Torrents && !m.Config.IsScrap(other.Path) {
				checked(m.Ops.Event(other.Path))
			}
			m.history(other.Path)
		}
	}()
	if m.Config.Notify && len(deleted) > 0 {
		checked(m.Ops.Notice("🧹 源文件联动清理", m.NotificationText(task.Path, deleted, false)))
	}
}
func (m *Machine) NotificationText(path string, deleted []string, delayed bool) string {
	parts := []string{"🗂️ 源文件：" + path}
	if len(deleted) == 1 {
		parts = append(parts, "🔗 硬链接："+deleted[0])
	} else if len(deleted) > 0 {
		parts = append(parts, fmt.Sprintf("🔗 删除了 %d 个硬链接文件", len(deleted)))
	}
	if m.Config.History {
		parts = append(parts, "📝 已清理转移记录")
	}
	if m.Config.Torrents {
		parts = append(parts, "🌱 已联动删除种子")
	}
	if m.Config.Scrap {
		parts = append(parts, "🖼️ 已清理刮削文件")
	}
	prefix := "⚡ 立即删除完成"
	if delayed {
		prefix = "⏰ 延迟删除完成"
	}
	return prefix + "\n\n" + strings.Join(parts, "\n")
}
func (m *Machine) unlink(path string) bool {
	if m.Config.Excluded(path, m.Home, m.UserHome) || m.Config.KeywordExcluded(path) {
		return false
	}
	err := m.Ops.Unlink(path)
	if err != nil {
		if missing(err) {
			m.pop(path)
		} else {
			m.Errors = append(m.Errors, err)
		}
		return false
	}
	m.pop(path)
	return true
}
func (m *Machine) history(path string) {
	if !m.Config.History {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			m.remember(p)
		}
	}()
	found, e := m.Ops.History("dest", path)
	checked(e)
	if !found {
		_, e = m.Ops.History("src", path)
		checked(e)
	}
}
