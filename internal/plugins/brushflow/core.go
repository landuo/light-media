package brushflow

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/plugins/schedule"
	"reflect"
	"sort"
	"sync"
	"time"
)

type Core struct {
	h       Host
	mu      sync.RWMutex
	cfg     Config
	locks   map[string]*sync.Mutex
	runtime map[string]Row
	brush   sync.Mutex
	global  sync.Mutex
	warned  map[string]float64
}

func newCore(h Host, c Config) *Core {
	p := &Core{h: h, cfg: c, locks: map[string]*sync.Mutex{}, runtime: map[string]Row{}, warned: map[string]float64{}}
	for _, t := range c.Tasks {
		p.locks[t.ID()] = &sync.Mutex{}
		p.runtime[t.ID()] = Row{"state": "idle", "operation": nil, "last_error": nil}
	}
	return p
}
func New(ctx context.Context, h Host, raw Row) (*Core, error) {
	c, e := NormalizeConfig(ctx, raw, h)
	if e != nil {
		return nil, e
	}
	p := newCore(h, c)
	a, _ := json.Marshal(raw)
	b, _ := json.Marshal(c.Map())
	if !reflect.DeepEqual(a, b) {
		if e = h.SaveConfig(ctx, c.Map()); e != nil {
			return nil, e
		}
	}
	if e = p.migrateData(ctx); e != nil {
		return nil, e
	}
	if c.Enabled() {
		if e = h.Queue(ctx, "", "cleanup_tags"); e != nil {
			h.Log(ctx, "warning", "提交刷流标签清理任务失败："+e.Error())
		}
	}
	if c.LegacyOnce {
		for _, t := range c.Tasks {
			if t.B("enabled") {
				if e = h.Queue(ctx, t.ID(), "brush"); e != nil {
					return nil, e
				}
				if e = h.Queue(ctx, t.ID(), "check"); e != nil {
					return nil, e
				}
			}
		}
	}
	return p, nil
}
func NewReadProjection(h Host, c Config) *Core { return newCore(h, c) }
func (p *Core) Config() Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c := Config{Settings: clone(p.cfg.Settings)}
	for _, t := range p.cfg.Tasks {
		c.Tasks = append(c.Tasks, Task{clone(t.Row)})
	}
	return c
}
func (p *Core) task(id string) (Task, bool) {
	for _, t := range p.Config().Tasks {
		if t.ID() == id {
			return t, true
		}
	}
	return Task{}, false
}
func (p *Core) taskLock(id string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	l := p.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		p.locks[id] = l
	}
	return l
}
func (p *Core) SetRuntime(id string, updates Row) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.runtime[id]
	if r == nil {
		r = Row{"state": "idle", "operation": nil, "last_error": nil}
	}
	for k, v := range updates {
		r[k] = v
	}
	p.runtime[id] = r
}
func (p *Core) Runtime(id string) Row {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return clone(p.runtime[id])
}
func (p *Core) busy(id string) bool {
	r := p.Runtime(id)
	if r["state"] == "queued" || r["state"] == "running" {
		return true
	}
	l := p.taskLock(id)
	if !l.TryLock() {
		return true
	}
	l.Unlock()
	return false
}
func (p *Core) Enabled() bool { return p.Config().Enabled() }
func (p *Core) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.runtime {
		r["state"] = "idle"
		r["operation"] = nil
	}
}
func (p *Core) Services(ctx context.Context) []Service {
	c := p.Config()
	s := []Service{}
	if !c.Enabled() {
		return s
	}
	for _, t := range c.Tasks {
		if !t.B("enabled") {
			continue
		}
		mins := int(t.N("brush_interval"))
		cron := t.S("cron")
		if cron != "" {
			if _, e := schedule.ParseCrontab(cron, p.h.Now().Location().String()); e != nil {
				p.h.Log(ctx, "error", fmt.Sprintf("刷流任务 [%s] CRON 表达式无效：%s", t.Name(), e))
				cron = ""
			}
		}
		s = append(s, Service{ID: "Task_" + t.ID() + "_Brush", Name: "刷流刷新 - " + t.Name(), TaskID: t.ID(), Operation: "brush", Cron: cron, Minutes: mins})
		check := int(t.N("check_interval"))
		if t.B("del_no_free") {
			check = 15
		}
		s = append(s, Service{ID: "Task_" + t.ID() + "_Check", Name: "刷流检查 - " + t.Name(), TaskID: t.ID(), Operation: "check", Minutes: check})
		if t.B("del_no_free") {
			s = append(s, Service{ID: "Task_" + t.ID() + "_PromotionExpiry", Name: "促销到期检查 - " + t.Name(), TaskID: t.ID(), Operation: "promotion_expiry", Minutes: 1})
		}
	}
	return s
}
func (p *Core) Sidebar() []Row {
	c := p.Config()
	if !c.Enabled() || !truth(c.Settings["show_sidebar_nav"]) {
		return []Row{}
	}
	return []Row{{"nav_key": "main", "title": "站点刷流", "icon": "mdi-sync", "section": "organize", "permission": "manage", "order": 45}}
}
func (p *Core) saveConfig(ctx context.Context) error {
	if e := p.h.SaveConfig(ctx, p.Config().Map()); e != nil {
		return e
	}
	if e := p.h.RefreshSchedules(ctx, p.Services(ctx)); e != nil {
		p.h.Log(ctx, "error", "更新站点刷流调度失败："+e.Error())
	}
	return nil
}
func (p *Core) get(ctx context.Context, id, name string) (any, error) {
	return p.h.Get(ctx, taskKey(id, name))
}
func (p *Core) records(ctx context.Context, id, name string) (*RecordSet, error) {
	v, e := p.get(ctx, id, name)
	if e != nil {
		return nil, e
	}
	return recordSet(v)
}
func (p *Core) put(ctx context.Context, id, name string, v any) error {
	return p.h.Put(ctx, taskKey(id, name), v)
}
func (p *Core) runs(ctx context.Context, id string) ([]any, error) {
	v, e := p.get(ctx, id, "runs")
	if raw, ok := v.(json.RawMessage); ok {
		e = json.Unmarshal(raw, &v)
	}
	return list(v), e
}
func (p *Core) statistic(ctx context.Context, id string) (Row, error) {
	v, e := p.get(ctx, id, "statistic")
	if raw, ok := v.(json.RawMessage); ok {
		e = json.Unmarshal(raw, &v)
	}
	r := Row{"count": 0, "deleted": 0, "uploaded": 0, "downloaded": 0, "unarchived": 0, "active": 0, "active_uploaded": 0, "active_downloaded": 0}
	for k, x := range row(v) {
		r[k] = x
	}
	return r, e
}
func (p *Core) recalculate(ctx context.Context, id string) (Row, error) {
	ts, e := p.records(ctx, id, "torrents")
	if e != nil {
		return nil, e
	}
	a, e := p.records(ctx, id, "archived")
	if e != nil {
		return nil, e
	}
	combined := Rows{}
	for k, v := range a.Values {
		combined[k] = v
	}
	for k, v := range ts.Values {
		combined[k] = v
	}
	r := Row{"count": len(combined), "deleted": 0, "uploaded": float64(0), "downloaded": float64(0), "unarchived": 0, "active": 0, "active_uploaded": float64(0), "active_downloaded": float64(0)}
	for _, v := range combined {
		if truth(v["deleted"]) {
			r["deleted"] = number(r["deleted"]) + 1
		}
		for _, k := range []string{"uploaded", "downloaded"} {
			r[k] = number(r[k]) + number(v[k])
		}
	}
	for _, v := range ts.Values {
		if truth(v["deleted"]) {
			r["unarchived"] = number(r["unarchived"]) + 1
		} else {
			r["active"] = number(r["active"]) + 1
			r["active_uploaded"] = number(r["active_uploaded"]) + number(v["uploaded"])
			r["active_downloaded"] = number(r["active_downloaded"]) + number(v["downloaded"])
		}
	}
	return r, p.put(ctx, id, "statistic", r)
}
func (p *Core) migrateData(ctx context.Context) error {
	v, e := p.h.Get(ctx, "task_data_schema_version")
	if e != nil {
		return e
	}
	if number(v) >= 2 {
		return nil
	}
	c := p.Config()
	sites, e := p.h.Sites(ctx)
	if e != nil {
		return e
	}
	names := map[int]string{}
	for _, s := range sites {
		names[s.ID] = s.Name
	}
	for _, name := range []string{"torrents", "archived", "unmanaged"} {
		v, e := p.h.Get(ctx, name)
		if e != nil {
			return e
		}
		old, e := recordSet(v)
		if e != nil {
			return e
		}
		buckets := map[string]*RecordSet{}
		for _, t := range c.Tasks {
			buckets[t.ID()] = NewRecords()
		}
		for _, k := range old.Order {
			r := old.Values[k]
			var selected *Task
			for _, t := range c.Tasks {
				if text(r["site"]) == fmt.Sprint(int(t.N("site_id"))) {
					v := t
					selected = &v
				}
			}
			if selected == nil {
				for _, t := range c.Tasks {
					if names[int(t.N("site_id"))] == text(r["site_name"]) && names[int(t.N("site_id"))] != "" {
						v := t
						selected = &v
					}
				}
			}
			if selected == nil && len(c.Tasks) == 1 {
				selected = &c.Tasks[0]
			}
			if selected != nil {
				r = clone(r)
				r["task_id"] = selected.ID()
				r["task_name"] = selected.Name()
				buckets[selected.ID()].Set(k, r)
			}
		}
		for _, t := range c.Tasks {
			current, e := p.records(ctx, t.ID(), name)
			if e != nil {
				return e
			}
			if len(current.Values) == 0 && len(buckets[t.ID()].Values) > 0 {
				if e = p.put(ctx, t.ID(), name, buckets[t.ID()]); e != nil {
					return e
				}
			}
		}
	}
	for _, t := range c.Tasks {
		if _, e = p.recalculate(ctx, t.ID()); e != nil {
			return e
		}
	}
	return p.h.Put(ctx, "task_data_schema_version", 2)
}
func (p *Core) appendRun(ctx context.Context, id string, r Row) error {
	history, e := p.runs(ctx, id)
	if e != nil {
		return e
	}
	history = append([]any{clone(r)}, history...)
	if len(history) > 50 {
		history = history[:50]
	}
	return p.put(ctx, id, "runs", history)
}
func (p *Core) newReport(kind string) Row {
	return Row{"id": p.h.NewID(), "kind": kind, "started_at": p.h.Now().Format(time.RFC3339), "finished_at": nil, "success": nil, "result": nil, "error": nil, "source_count": 0, "subscription_excluded": 0, "candidate_count": 0, "filtered_count": 0, "added_count": 0, "deleted_count": 0, "paused_count": 0, "active_count": 0, "reason_counts": Row{}, "added_titles": []any{}}
}
func reason(r Row, s string, n int) {
	v := row(r["reason_counts"])
	v[s] = number(v[s]) + float64(n)
	r["reason_counts"] = v
}
func seedingSize(r *RecordSet) float64 {
	var n float64
	for _, v := range r.Values {
		if !truth(v["deleted"]) {
			n += number(v["size"])
		}
	}
	return n
}
func (p *Core) site(ctx context.Context, t Task) (*Site, error) {
	ss, e := p.h.Sites(ctx)
	if e != nil {
		return nil, e
	}
	for _, s := range ss {
		if s.ID == int(t.N("site_id")) {
			return &s, nil
		}
	}
	return nil, nil
}
func (p *Core) service(ctx context.Context, t Task) (*Downloader, error) {
	ds, e := p.h.Downloaders(ctx)
	if e != nil {
		return nil, e
	}
	for _, d := range ds {
		if d.Name == t.S("downloader") {
			if !d.Active {
				p.systemError(ctx, fmt.Sprintf("刷流任务 [%s] 下载器未连接", t.Name()))
				return nil, nil
			}
			return &d, nil
		}
	}
	p.systemError(ctx, fmt.Sprintf("刷流任务 [%s] 获取下载器实例失败，请检查配置", t.Name()))
	return nil, nil
}
func (p *Core) validateReference(ctx context.Context, t Task, notify bool) (bool, error) {
	ss, e := p.h.Sites(ctx)
	if e != nil {
		return false, e
	}
	ds, e := p.h.Downloaders(ctx)
	if e != nil {
		return false, e
	}
	ok := validReference(t, ss, ds)
	if !ok && notify {
		p.systemError(ctx, fmt.Sprintf("刷流任务 [%s] 引用的站点或下载器不存在", t.Name()))
	}
	return ok, nil
}
func (p *Core) systemError(ctx context.Context, s string) {
	p.h.Log(ctx, "error", s)
	_ = p.h.Message(ctx, Notice{Title: "站点刷流", Text: s, System: true})
}
func (p *Core) lockAll() func() {
	c := p.Config()
	ids := []string{}
	for _, t := range c.Tasks {
		ids = append(ids, t.ID())
	}
	sort.Strings(ids)
	locks := []*sync.Mutex{}
	for _, id := range ids {
		l := p.taskLock(id)
		l.Lock()
		locks = append(locks, l)
	}
	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
	}
}
