package removelink

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime/debug"

	"strings"
	"sync"
	"time"
	"unicode"
)

type Machine struct {
	mu                 sync.Mutex
	queueMu            sync.Mutex
	errorsMu           sync.Mutex
	metaMu             sync.Mutex
	defaultOp          *operation
	timerWake          func()
	configMu           sync.RWMutex
	Config             Config
	State              []Tracked
	Queue              []*Task
	TimerDue           *time.Time
	Watches            []Watch
	Ops                Ops
	Home               string
	Now                func() time.Time
	Errors             []error
	SourceErrors       []error
	unstartedObservers int
}
type Watch struct{ Path, Mode string }

func New(ops Ops) *Machine {
	m := &Machine{Config: NewConfig(), State: []Tracked{}, Queue: []*Task{}, Watches: []Watch{}, Ops: ops, Now: sourceNow}
	m.defaultOp = &operation{Machine: m, Ops: ops}
	return m
}

// Keep the physical instant/fold for datetime.timestamp(), but compare source
// naive datetime values through sourceWall. Python uses both forms in its queue.
func sourceNow() time.Time {
	return time.Now().Round(0).Truncate(time.Microsecond)
}
func sourceWall(n time.Time) time.Time {
	return time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), n.Minute(), n.Second(), n.Nanosecond(), time.UTC)
}
func (m *Machine) StateValue() any { return m.currentConfig().Enabled }
func (m *Machine) currentConfig() Config {
	m.configMu.RLock()
	defer m.configMu.RUnlock()
	return m.Config
}
func (m *Machine) applyConfig(raw json.RawMessage) error {
	m.configMu.Lock()
	defer m.configMu.Unlock()
	return m.Config.Apply(raw)
}
func (m *operation) Init(raw json.RawMessage) (err error) {
	defer capture(&err)
	m.log("INFO", "初始化媒体文件清理插件")
	if e := m.applyConfig(raw); e != nil {
		return e
	}
	return m.initConfigured(false)
}

// Plugin.init logs and applies before stopping observers, without retaining a
// caller across the stop/join. Direct Machine users keep the same source entry.
func (m *operation) initConfigured(stopStarted bool) (err error) {
	defer capture(&err)
	m.stopStarted(stopStarted)
	if ops, ok := m.Ops.(interface{ failure() error }); ok {
		if e := ops.failure(); e != nil {
			return e
		}
	}
	m.queueMu.Lock()
	m.Queue = []*Task{}
	m.queueMu.Unlock()
	if !Truth(m.currentConfig().Enabled) {
		return nil
	}
	if Truth(m.currentConfig().Delayed) {
		m.log("INFO", fmt.Sprintf("延迟删除功能已启用，延迟时间: %d 秒", m.currentConfig().Delay))
	} else {
		m.log("INFO", "延迟删除功能已禁用，将使用立即删除模式")
	}
	strmDirs := []string{}
	if Truth(m.currentConfig().STRM) {
		m.log("INFO", "STRM 文件删除监控功能已启用")
		m.currentConfig().ensureString("strm_path_mappings")
		if m.currentConfig().Mappings != "" {
			for _, mapping := range ParseMappings(m.currentConfig().Mappings) {
				strmDirs = append(strmDirs, mapping.Prefix)
			}
			m.log("INFO", fmt.Sprintf("配置了 %d 个 STRM 路径映射", len(strmDirs)))
			m.log("INFO", "STRM 监控目录："+pythonLogStrings(strmDirs))
		} else {
			m.log("WARNING", "STRM 监控已启用但未配置路径映射")
		}
	} else {
		m.log("INFO", "STRM 文件删除监控功能已禁用")
	}
	if Truth(m.currentConfig().STRM) {
		m.currentConfig().ensureString("strm_path_mappings")
	}
	m.currentConfig().ensureString("monitor_dirs")
	watches := []Watch{}
	hardlinkDirs := []string{}
	for _, p := range strings.Split(m.currentConfig().Monitor, "\n") {
		p = sourceStrip(p)
		if p != "" {
			hardlinkDirs = append(hardlinkDirs, p)
			watches = append(watches, Watch{p, "hardlink"})
		}
	}
	if m.currentConfig().Monitor != "" {
		m.log("INFO", "硬链接监控目录："+pythonLogStrings(hardlinkDirs))
	}
	if Truth(m.currentConfig().STRM) {
		for _, x := range ParseMappings(m.currentConfig().Mappings) {
			watches = append(watches, Watch{x.Prefix, "strm"})
		}
	}
	m.metaMu.Lock()
	m.Watches = watches
	m.metaMu.Unlock()
	if ops, ok := m.Ops.(interface{ StartWatch(Watch) }); ok {
		for _, w := range watches {
			ops.StartWatch(w)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.State = []Tracked{}
	scanStart := time.Now()
	now := m.Now()
	for _, w := range watches {
		if Trickplay(w.Path) {
			continue
		}
		if !m.Ops.Exists(w.Path) {
			m.log("WARNING", "监控目录不存在："+w.Path)
			continue
		}
		rows, e := m.Ops.Scan(w.Path)
		if e != nil {
			m.logCaught("ERROR", "扫描目录 "+w.Path+" 时发生错误："+e.Error())
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
	m.log("INFO", fmt.Sprintf("更新文件列表完成，共计 %d 个文件，耗时 %.2f 秒", len(m.State), time.Since(scanStart).Seconds()))
	if scan, ok := m.Ops.(interface{ ScanErrorCount() int }); ok && scan.ScanErrorCount() > 0 {
		m.log("WARNING", fmt.Sprintf("扫描过程中有 %d 个文件无法访问", scan.ScanErrorCount()))
	}
	m.log("DEBUG", "监控集合更新完成")
	return nil
}
func (m *operation) Stop() { m.stop() }
func (m *operation) stop() {
	m.stopStarted(false)
}
func (m *operation) stopStarted(started bool) {
	if !started {
		m.log("DEBUG", "开始停止服务")
	}
	m.metaMu.Lock()
	unstarted := m.unstartedObservers
	m.unstartedObservers = 0
	m.Watches = []Watch{}
	m.metaMu.Unlock()
	for i := 0; i < unstarted; i++ {
		// Python retains observers appended before a failed native start; its
		// subsequent join raises. There is no live Go goroutine to fabricate.
		fmt.Fprintln(os.Stderr, "cannot join thread before it is started")
		m.log("ERROR", "停止目录监控失败：cannot join thread before it is started")
	}
	m.log("DEBUG", "文件监控已停止")
	tasks := func() []*Task {
		m.queueMu.Lock()
		defer m.queueMu.Unlock()
		if m.TimerDue != nil {
			m.log("DEBUG", "延迟删除定时器已停止")
		}
		m.TimerDue = nil
		tasks := m.Queue
		if len(tasks) > 0 {
			m.log("INFO", fmt.Sprintf("处理剩余的 %d 个延迟删除任务", len(tasks)))
		}
		m.Queue = []*Task{}
		return tasks
	}()
	m.wakeTimer()
	for _, t := range tasks {
		m.queueMu.Lock()
		pending := !t.Processed
		m.queueMu.Unlock()
		if pending {
			m.execute(t)
		}
		m.queueMu.Lock()
		if !t.Processed {
			m.Queue = append(m.Queue, t)
		}
		m.queueMu.Unlock()
	}
	m.log("DEBUG", "服务停止完成")
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
func (m *operation) add(p string) {
	if Trickplay(p) {
		return
	}
	for _, ext := range []string{".!qB", ".part", ".mp", ".tmp", ".temp"} {
		if suffix(p) == ext {
			return
		}
	}
	if m.keywordExcludedLog(p, "DEBUG") {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.Ops.Exists(p) {
		return
	}
	id, e := m.Ops.Stat(p)
	if e == nil {
		id.Added = m.Now()
		m.put(Tracked{p, id})
		m.log("DEBUG", "添加文件到监控："+p)
	} else {
		var pathError *os.PathError
		if errors.As(e, &pathError) || errors.Is(e, fs.ErrPermission) || errors.Is(e, fs.ErrNotExist) {
			m.logCaught("DEBUG", "无法访问文件 "+p+"："+e.Error())
		} else {
			m.logCaught("ERROR", "新增文件记录失败："+e.Error())
		}
	}
}
func (m *operation) Created(p string, isDir bool) (err error) {
	defer capture(&err)
	p = cleanPath(p)
	if !isDir {
		m.log("INFO", "监测到新增文件："+p)
		m.add(p)
	}
	return
}
func (m *operation) Moved(src, dest, mode string, isDir bool) (err error) {
	src, dest = cleanPath(src), cleanPath(dest)
	defer capture(&err)
	if isDir {
		return
	}
	if Trickplay(src) || Trickplay(dest) {
		m.mu.Lock()
		m.pop(src)
		m.mu.Unlock()
		return
	}
	m.log("INFO", "监测到文件移动："+src+" -> "+dest)
	m.mu.Lock()
	old, ok := m.pop(src)
	m.mu.Unlock()
	m.add(dest)
	m.mu.Lock()
	next, exists := m.find(dest)
	m.mu.Unlock()
	if ok && (!exists || !same(next.Identity, old.Identity)) {
		m.mu.Lock()
		m.put(old)
		m.mu.Unlock()
		m.log("INFO", "移动目标未进入监控或文件实体不一致，按删除处理源文件："+src)
		if mode != "strm" {
			m.deleted(src)
		} else if sourceLower(suffix(src)) == ".strm" {
			m.strm(src)
		}
	}
	return
}
func (m *operation) Deleted(p, mode string, isDir bool) (err error) {
	p = cleanPath(p)
	defer capture(&err)
	if Trickplay(p) {
		m.mu.Lock()
		m.pop(p)
		m.mu.Unlock()
		return
	}
	if isDir {
		if Truth(m.currentConfig().Torrents) {
			m.log("INFO", "监测到删除文件夹："+p)
			m.event(p)
		}
		return
	}
	for _, ext := range []string{".!qB", ".part", ".mp"} {
		if suffix(p) == ext {
			return
		}
	}
	m.log("INFO", "监测到删除文件："+p)
	if m.keywordExcludedLog(p, "INFO") {
		return
	}
	if mode == "strm" {
		if sourceLower(suffix(p)) == ".strm" {
			m.strm(p)
		}
	} else {
		m.deleted(p)
	}
	return
}
func (m *operation) deleted(p string) {
	if Trickplay(p) {
		m.mu.Lock()
		m.pop(p)
		m.mu.Unlock()
		return
	}
	m.log("DEBUG", "处理删除事件: "+p)
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.pop(p)
	if !ok {
		m.log("DEBUG", "文件 "+p+" 未在监控列表中，跳过处理")
		return
	}
	if Truth(m.currentConfig().Delayed) {
		m.log("INFO", fmt.Sprintf("文件 %s 加入延迟删除队列，延迟 %d 秒", base(p), m.currentConfig().Delay))
		t := &Task{Path: p, Identity: old.Identity, At: m.Now()}
		m.queueMu.Lock()
		defer m.queueMu.Unlock()
		m.Queue = append(m.Queue, t)
		if m.TimerDue == nil {
			due := m.Now().Add(time.Duration(m.currentConfig().Delay) * time.Second)
			m.TimerDue = &due
			m.wakeTimer()
			m.log("DEBUG", "启动延迟删除定时器")
		} else {
			m.log("DEBUG", "延迟删除定时器已在运行，任务已加入队列")
		}
		return
	}
	m.cleanup(&Task{Path: p, Identity: old.Identity, At: m.Now()}, false)
}
func (m *operation) Tick() {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
			m.logRecoveredTrace("处理延迟删除队列失败：", x)
			m.queueMu.Lock()
			m.TimerDue = nil
			m.queueMu.Unlock()
			m.wakeTimer()
		}
	}()
	now := m.Now()
	ready := func() []*Task {
		m.queueMu.Lock()
		defer m.queueMu.Unlock()
		ready := []*Task{}
		for _, t := range m.Queue {
			if !t.Processed && sourceWall(now).Sub(sourceWall(t.At)) >= time.Duration(m.currentConfig().Delay)*time.Second {
				ready = append(ready, t)
			}
		}
		if len(ready) > 0 {
			m.log("DEBUG", fmt.Sprintf("处理延迟删除队列，待处理任务数: %d", len(ready)))
		}
		return ready
	}()
	for _, t := range ready {
		m.executeQueueTask(t)
	}
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	defer m.wakeTimer()
	out := []*Task{}
	for _, t := range m.Queue {
		if !t.Processed {
			out = append(out, t)
		}
	}
	cleaned := len(m.Queue) - len(out)
	m.Queue = out
	if cleaned > 0 {
		m.log("DEBUG", fmt.Sprintf("清理了 %d 个已处理的任务", cleaned))
	}
	m.TimerDue = nil
	var wait *time.Duration
	for _, t := range out {
		d := t.At.Add(time.Duration(m.currentConfig().Delay) * time.Second).Sub(now)
		if d < time.Second {
			d = time.Second
		}
		if wait == nil || d < *wait {
			wait = &d
		}
	}
	if wait != nil {
		m.log("DEBUG", fmt.Sprintf("还有 %d 个任务待处理，%.1f 秒后重新检查", len(out), wait.Seconds()))
		// Source starts a relative Timer after processing, but computes its
		// wait from the captured pre-processing now, including slow effects.
		due := m.Now().Add(*wait)
		m.TimerDue = &due
	} else {
		m.log("DEBUG", "延迟删除队列已清空，定时器停止")
	}
}
func (m *operation) executeQueueTask(t *Task) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
			m.logRecovered("处理延迟删除任务失败："+t.Path+" - ", x)
		}
	}()
	m.execute(t)
}
func (m *operation) execute(t *Task) {
	// A source finally marks the task that reached an unknown result processed;
	// later tasks never enter that source call and must remain pending.
	if m.noticeFailure() != nil {
		return
	}
	if ops, ok := m.Ops.(interface{ failure() error }); ok && ops.failure() != nil {
		return
	}
	defer func() {
		m.markProcessed(t)
		if x := recover(); x != nil {
			m.remember(x)
			m.logRecoveredTrace("执行延迟删除任务失败：", x)
		}
	}()
	if Trickplay(t.Path) {
		m.mu.Lock()
		m.pop(t.Path)
		m.mu.Unlock()
		return
	}
	m.log("DEBUG", "开始执行延迟删除任务: "+t.Path)
	if m.Ops.Exists(t.Path) {
		m.log("INFO", "文件 "+t.Path+" 已被重新创建，跳过删除操作")
		return
	}
	recent := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, x := range m.State {
			if Trickplay(x.Path) || x.Path == t.Path || !same(x.Identity, t.Identity) {
				continue
			}
			window := time.Duration(max(5, m.currentConfig().Delay)) * time.Second
			if sourceWall(x.Identity.Added).After(sourceWall(t.Identity.Added)) && !sourceWall(x.Identity.Added).Before(sourceWall(t.At).Add(-window)) {
				m.log("INFO", fmt.Sprintf("检测到相同文件实体的新文件 %s，添加时间 %s 接近删除事件 %s，可能是重新硬链接，跳过硬链接删除", x.Path, pythonLogTime(x.Identity.Added), pythonLogTime(t.At)))
				m.scrap(t.Path)
				return true
			}
		}
		return false
	}()
	if recent {
		return
	}
	m.log("DEBUG", "文件 "+t.Path+" 确认被删除且无重新硬链接，开始执行延迟删除操作")
	m.cleanup(t, true)
}
func (m *operation) cleanup(t *Task, delayed bool) {
	m.scrap(t.Path)
	if Truth(m.currentConfig().Torrents) && !m.currentConfig().IsScrap(t.Path) {
		m.event(t.Path)
	}
	m.history("src", t.Path)
	m.cleanupLinks(t, delayed)
}
func (m *operation) cleanupLinks(t *Task, delayed bool) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
			prefix := "删除硬链接文件发生错误："
			if delayed {
				prefix = "执行延迟删除任务失败："
			}
			m.logRecoveredTrace(prefix, x)
		}
	}()
	deleted := []string{}
	func() {
		if delayed {
			m.mu.Lock()
			defer m.mu.Unlock()
		}
		for _, x := range append([]Tracked{}, m.State...) {
			if !same(x.Identity, t.Identity) {
				continue
			}
			action := "立即删除"
			if delayed {
				action = "延迟删除"
			}
			if !m.unlink(x.Path, action) {
				continue
			}
			deleted = append(deleted, x.Path)
			m.scrap(x.Path)
			if Truth(m.currentConfig().Torrents) && !m.currentConfig().IsScrap(x.Path) {
				m.event(x.Path)
			}
			m.history("src", x.Path)
		}
	}()
	if Truth(m.currentConfig().Notify) && len(deleted) > 0 {
		parts := []string{"🗂️ 源文件：" + t.Path}
		if len(deleted) == 1 {
			parts = append(parts, "🔗 硬链接："+deleted[0])
		} else {
			parts = append(parts, fmt.Sprintf("🔗 删除了 %d 个硬链接文件", len(deleted)))
		}
		if Truth(m.currentConfig().History) {
			parts = append(parts, "📝 已清理转移记录")
		}
		if Truth(m.currentConfig().Torrents) {
			parts = append(parts, "🌱 已联动删除种子")
		}
		if Truth(m.currentConfig().Scrap) {
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
func (m *operation) unlink(p, action string) bool {
	if Trickplay(p) {
		m.pop(p)
		return false
	}
	if m.currentConfig().Excluded(p, m.Home) {
		m.log("DEBUG", "文件 "+p+" 在不删除目录中，跳过")
		return false
	}
	m.beforeWrite()
	m.log("INFO", action+"硬链接文件："+p)
	if e := m.Ops.Unlink(p); e != nil {
		if missing(e) {
			m.pop(p)
			m.log("WARNING", "硬链接文件已不存在，清理过期监控记录："+p)
		} else {
			m.recordError(e, false)
			m.logCaught("ERROR", "删除硬链接文件失败："+p+" - "+e.Error())
		}
		return false
	}
	m.pop(p)
	return true
}
func (m *operation) history(kind, p string) bool {
	if !Truth(m.currentConfig().History) {
		return false
	}
	m.beforeWrite()
	var ok bool
	var e error
	if records, detailed := m.Ops.(interface {
		HistoryRecord(string, string) (string, bool, error)
	}); detailed {
		var id string
		id, ok, e = records.HistoryRecord(kind, p)
		checked(e)
		if ok {
			message := "删除转移记录：" + id
			if kind == "dest" {
				message += " - " + p
			}
			m.log("INFO", message)
		}
	} else {
		ok, e = m.Ops.History(kind, p)
	}
	checked(e)
	if !ok && kind == "dest" {
		m.log("DEBUG", "未找到转移记录："+p)
	}
	return ok
}
func (m *operation) scrap(p string) {
	if Trickplay(p) || !Truth(m.currentConfig().Scrap) || !m.Ops.Exists(parent(p)) {
		return
	}
	if !m.currentConfig().IsScrap(p) {
		rows, e := m.Ops.Children(parent(p))
		if e != nil {
			m.recordError(e, false)
			m.logCaught("ERROR", "清理刮削文件发生错误："+e.Error()+".")
		}
		if e == nil {
			for _, x := range rows {
				if !strings.HasPrefix(base(x.Path), stem(p)) {
					continue
				}
				if x.Dir && sourceLower(suffix(x.Path)) == ".trickplay" && stem(x.Path) == stem(p) {
					m.beforeWrite()
					if e := m.Ops.RemoveTree(x.Path); e != nil {
						m.recordError(e, false)
						m.logCaught("ERROR", "清理刮削文件发生错误："+e.Error()+".")
						break
					}
					m.log("INFO", "删除刮削目录："+x.Path)
				} else if m.currentConfig().IsScrap(x.Path) {
					m.beforeWrite()
					if e := m.Ops.Unlink(x.Path); e != nil {
						m.recordError(e, false)
						m.logCaught("ERROR", "清理刮削文件发生错误："+e.Error()+".")
						break
					}
					m.log("INFO", "删除刮削文件："+x.Path)
				}
			}
		}
	}
	m.empty(p)
}
func (m *operation) onlyScrap(p string) (bool, error) {
	rows, e := m.Ops.Children(p)
	if e != nil {
		return false, e
	}
	for _, x := range rows {
		if x.Dir {
			if sourceLower(suffix(x.Path)) != ".trickplay" {
				return false, nil
			}
		} else if !m.currentConfig().IsScrap(x.Path) {
			return false, nil
		}
	}
	return true, nil
}
func (m *operation) empty(p string) {
	if Trickplay(p) {
		return
	}
	for {
		parent := parent(p)
		if m.currentConfig().Excluded(parent, m.Home) || !m.Ops.Exists(parent) {
			return
		} // Source's Path-vs-str monitor-boundary test is always false.
		only, scrapErr := m.onlyScrap(parent)
		if scrapErr != nil {
			m.recordError(scrapErr, false)
			m.logCaught("ERROR", "清理刮削文件发生错误："+scrapErr.Error()+".")
		}
		if only {
			rows, e := m.Ops.Children(parent)
			if e != nil {
				m.recordError(e, false)
				m.logCaught("ERROR", "清理刮削文件发生错误："+e.Error()+".")
			}
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
						m.recordError(e, false)
						m.logCaught("ERROR", "清理刮削文件发生错误："+e.Error()+".")
						break
					}
					kind := "文件"
					if x.Dir {
						kind = "目录"
					}
					m.log("INFO", "删除刮削"+kind+"："+x.Path)
				}
			}
		}
		rows, e := m.Ops.Children(parent)
		if e != nil {
			m.recordError(e, false)
			m.logCaught("ERROR", "清理空目录发生错误："+e.Error())
		}
		if e == nil && len(rows) > 0 {
			return
		}
		if e == nil {
			m.beforeWrite()
			if e := m.Ops.RemoveDir(parent); e != nil {
				m.recordError(e, false)
				m.logCaught("ERROR", "清理空目录发生错误："+e.Error())
			} else {
				m.log("INFO", "清理空目录："+parent)
				if Truth(m.currentConfig().Notify) {
					// Source catches a notification exception at this level, then
					// advances to the next parent. Infrastructure uncertainty is not caught.
					if e := m.sendNotice("empty_directory", "📁 目录清理", "🗑️ 清理空目录："+parent); knownSourceNotice(e) {
						m.recordError(e, true)
						m.logCaught("ERROR", "清理空目录发生错误："+e.Error())
					} else {
						checked(e)
					}
				}
			}
		}
		if parent == p {
			return
		}
		p = parent
	}
}

func (m *operation) remember(x any) {
	if e, ok := x.(effectFailure); ok {
		if knownSourceNotice(e.err) {
			m.recordError(e.err, true)
		} else {
			m.recordError(e.err, false)
		}
	} else {
		panic(x)
	}
}
func (m *operation) event(p string) { m.beforeWrite(); checked(m.Ops.Event(p)) }
func (m *operation) beforeWrite() {
	checked(m.noticeFailure())
	if ops, ok := m.Ops.(interface{ failure() error }); ok {
		checked(ops.failure())
	}
}
func (m *operation) log(level, message string) {
	if ops, ok := m.Ops.(interface{ Log(string, string) error }); ok {
		checked(ops.Log(level, message))
	}
}
func (m *operation) sendNotice(site, title, text string) error {
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
		m.failNotice(err)
	}
	return err
}
func (m *operation) notice(site, title, text string) { checked(m.sendNotice(site, title, text)) }
func (m *operation) storageExists(x Item) bool {
	ok, e := m.Ops.StorageExists(x)
	checked(e)
	return ok
}
func (m *operation) storageList(x Item) []Item { v, e := m.Ops.StorageList(x); checked(e); return v }
func (m *operation) storageDelete(x Item) bool {
	m.beforeWrite()
	ok, e := m.Ops.StorageDelete(x)
	checked(e)
	return ok
}

func (m *operation) keywordExcluded(p string) bool {
	m.currentConfig().ensureString("exclude_keywords")
	return keywords(m.currentConfig().Keywords, p)
}

func (m *operation) keywordExcludedLog(p, level string) bool {
	m.currentConfig().ensureString("exclude_keywords")
	for _, keyword := range strings.Split(m.currentConfig().Keywords, "\n") {
		if keyword != "" && strings.Contains(p, keyword) {
			m.log(level, p+" 命中过滤关键字 "+keyword+"，不处理")
			return true
		}
	}
	return false
}

func (m *operation) logCaught(level, message string) {
	if ops, ok := m.Ops.(interface{ failure() error }); ok && ops.failure() != nil {
		return // Guard/caller revocation supplies no later logging authority.
	}
	m.log(level, message)
}
func (m *operation) logRecovered(prefix string, x any) {
	m.logRecoveredLevel("ERROR", prefix, x)
}
func (m *operation) logRecoveredLevel(level, prefix string, x any) {
	if e, ok := x.(effectFailure); ok {
		m.logCaught(level, prefix+sourceCaughtErrorText(e.err))
	}
}
func (m *operation) logRecoveredTrace(prefix string, x any) {
	if e, ok := x.(effectFailure); ok {
		m.logCaught("ERROR", prefix+sourceCaughtErrorText(e.err)+" - "+strings.TrimSpace(string(debug.Stack())))
	}
}
func pythonLogTime(t time.Time) string {
	s := t.Format("2006-01-02 15:04:05")
	if t.Nanosecond() != 0 {
		s += fmt.Sprintf(".%06d", t.Nanosecond()/1000)
	}
	return s
}
func pythonLogStrings(values []string) string {
	parts := make([]string, len(values))
	for i, value := range values {
		quote := rune('\'')
		if strings.Contains(value, "'") && !strings.Contains(value, `"`) {
			quote = '"'
		}
		var b strings.Builder
		b.WriteRune(quote)
		for _, r := range value {
			switch {
			case r == quote || r == '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case r == '\t':
				b.WriteString(`\t`)
			case r == '\n':
				b.WriteString(`\n`)
			case r == '\r':
				b.WriteString(`\r`)
			case unicode.IsPrint(r):
				b.WriteRune(r)
			case r <= 0xff:
				fmt.Fprintf(&b, `\x%02x`, r)
			case r <= 0xffff:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				fmt.Fprintf(&b, `\U%08x`, r)
			}
		}
		b.WriteRune(quote)
		parts[i] = b.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
