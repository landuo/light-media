package brushflow

import (
	"context"
	"fmt"
	"light-media/internal/plugins/schedule"
	"net/url"
	"sort"
	"strings"
	"time"
)

func (p *Core) UpdateSettings(ctx context.Context, raw Row) (Response, error) {
	r, e := ValidateSettings(raw)
	if e != nil {
		return Response{}, e
	}
	p.mu.Lock()
	was := p.cfg.GlobalDynamic()
	p.cfg.Settings = r
	if was && !p.cfg.GlobalDynamic() {
		for _, t := range p.cfg.Tasks {
			if t.B("proxy_delete") && !truth(t.Row["delete_size_range"]) {
				t.Row["proxy_delete"] = false
			}
		}
	}
	p.mu.Unlock()
	if e = p.saveConfig(ctx); e != nil {
		return Response{}, e
	}
	v, e := p.Status(ctx)
	return Response{Success: true, Data: v}, e
}
func (p *Core) CreateTask(ctx context.Context, raw Row) (Response, error) {
	r, e := ValidateTaskPayload(raw)
	if e != nil {
		return Response{}, e
	}
	r["id"] = p.h.NewID()
	t := NormalizeTask(r, p.h.NewID)
	valid, e := p.validateReference(ctx, t, true)
	if e != nil {
		return Response{}, e
	}
	if !valid {
		return Response{Message: "站点或下载器配置无效"}, nil
	}
	p.mu.Lock()
	p.cfg.Tasks = append(p.cfg.Tasks, t)
	p.mu.Unlock()
	p.taskLock(t.ID())
	p.SetRuntime(t.ID(), Row{"state": "idle", "operation": nil, "last_error": nil})
	if e = p.saveConfig(ctx); e != nil {
		return Response{}, e
	}
	v, e := p.Detail(ctx, t.ID(), "active", 1, 50)
	return Response{Success: true, Data: v}, e
}
func (p *Core) UpdateTask(ctx context.Context, id string, raw Row) (Response, error) {
	if _, ok := p.task(id); !ok {
		return Response{Message: "刷流任务不存在"}, nil
	}
	if p.busy(id) {
		return Response{Message: "任务正在执行，请稍后再修改"}, nil
	}
	r, e := ValidateTaskPayload(raw)
	if e != nil {
		return Response{}, e
	}
	r["id"] = id
	t := NormalizeTask(r, p.h.NewID)
	valid, e := p.validateReference(ctx, t, true)
	if e != nil {
		return Response{}, e
	}
	if !valid {
		return Response{Message: "站点或下载器配置无效"}, nil
	}
	p.mu.Lock()
	for i, old := range p.cfg.Tasks {
		if old.ID() == id {
			p.cfg.Tasks[i] = t
		}
	}
	p.mu.Unlock()
	if e = p.saveConfig(ctx); e != nil {
		return Response{}, e
	}
	v, e := p.Detail(ctx, id, "active", 1, 50)
	return Response{Success: true, Data: v}, e
}
func (p *Core) DeleteTask(ctx context.Context, id string) (Response, error) {
	t, ok := p.task(id)
	if !ok {
		return Response{Message: "刷流任务不存在"}, nil
	}
	if p.busy(id) {
		return Response{Message: "任务正在执行，请稍后再删除"}, nil
	}
	r, e := p.records(ctx, id, "torrents")
	if e != nil {
		return Response{}, e
	}
	for _, v := range r.Values {
		if !truth(v["deleted"]) {
			return Response{Message: "任务仍有活跃种子，请先处理后再删除"}, nil
		}
	}
	p.mu.Lock()
	for i, v := range p.cfg.Tasks {
		if v.ID() == id {
			p.cfg.Tasks = append(p.cfg.Tasks[:i], p.cfg.Tasks[i+1:]...)
			break
		}
	}
	delete(p.runtime, id)
	delete(p.locks, id)
	p.mu.Unlock()
	for _, n := range DataNames {
		if e = p.h.Del(ctx, taskKey(id, n)); e != nil {
			return Response{}, e
		}
	}
	if e = p.saveConfig(ctx); e != nil {
		return Response{}, e
	}
	if e = p.h.Queue(ctx, id, "cleanup_task_tag"); e != nil {
		p.h.Log(ctx, "warning", "提交刷流任务标签清理失败："+e.Error())
	}
	_ = t
	v, e := p.Status(ctx)
	return Response{Success: true, Data: v}, e
}
func (p *Core) StateTask(ctx context.Context, id string, enabled bool) (Response, error) {
	p.mu.Lock()
	found := false
	for _, t := range p.cfg.Tasks {
		if t.ID() == id {
			t.Row["enabled"] = enabled
			found = true
		}
	}
	p.mu.Unlock()
	if !found {
		return Response{Message: "刷流任务不存在"}, nil
	}
	if e := p.saveConfig(ctx); e != nil {
		return Response{}, e
	}
	v, e := p.Detail(ctx, id, "active", 1, 50)
	return Response{Success: true, Data: v}, e
}
func (p *Core) ClearTask(ctx context.Context, id string) (Response, error) {
	if _, ok := p.task(id); !ok {
		return Response{Message: "刷流任务不存在"}, nil
	}
	if p.busy(id) {
		return Response{Message: "任务正在执行，请稍后再清除"}, nil
	}
	for _, n := range DataNames {
		var v any = Row{}
		if n == "runs" {
			v = []any{}
		}
		if e := p.put(ctx, id, n, v); e != nil {
			return Response{}, e
		}
	}
	v, e := p.Detail(ctx, id, "active", 1, 50)
	return Response{Success: true, Data: v}, e
}
func (p *Core) Submit(ctx context.Context, id, op string) (Response, error) {
	t, ok := p.task(id)
	if !ok {
		return Response{Message: "刷流任务不存在"}, nil
	}
	if !p.Enabled() || !t.B("enabled") {
		return Response{Message: "插件或任务未启用"}, nil
	}
	p.mu.Lock()
	r := p.runtime[id]
	if r["state"] == "queued" || r["state"] == "running" {
		p.mu.Unlock()
		return Response{Message: "任务已有操作正在执行"}, nil
	}
	if r == nil {
		r = Row{}
	}
	r["state"] = "queued"
	r["operation"] = op
	r["last_error"] = nil
	p.runtime[id] = r
	p.mu.Unlock()
	if e := p.h.Queue(ctx, id, op); e != nil {
		p.SetRuntime(id, Row{"state": "idle", "operation": nil, "last_error": e.Error()})
		p.h.Log(ctx, "error", fmt.Sprintf("提交刷流任务 [%s] 失败：%s", t.Name(), e))
		return Response{Message: "任务提交失败"}, nil
	}
	v, e := p.Summary(ctx, id, nil)
	return Response{Success: true, Message: "任务已提交", Data: v}, e
}
func sourceDomain(raw string) string {
	if raw == "" {
		return ""
	}
	for _, d := range []string{"u2.dmhy.org", "pt.ecust.pp.ua", "pt.gtkpw.xyz", "pt.gtk.pw"} {
		if strings.Contains(raw, d) {
			return d
		}
	}
	host := raw
	if strings.HasPrefix(raw, "http") {
		u, e := url.Parse(raw)
		if e != nil {
			return ""
		}
		host = u.Host
	}
	parts := strings.Split(host, ".")
	if len(parts) > 3 {
		return host
	}
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, ".")
}
func (p *Core) users(ctx context.Context) (map[string]UserData, error) {
	us, e := p.h.SiteUsers(ctx)
	if e != nil {
		return nil, e
	}
	m := map[string]UserData{}
	for _, u := range us {
		k := sourceDomain(u.Domain)
		if _, ok := m[k]; k != "" && !ok {
			m[k] = u
		}
	}
	return m, nil
}
func (p *Core) ratio(ctx context.Context, t Task, users map[string]UserData) (Row, error) {
	r := Row{"enabled": t.B("site_ratio_control"), "target": t.Row["site_ratio_target"], "current": nil, "available": false, "unlimited": false, "reached": false, "updated_at": nil}
	if !t.B("site_ratio_control") || t.N("site_ratio_target") == 0 {
		return r, nil
	}
	s, e := p.site(ctx, t)
	if e != nil || s == nil {
		return r, e
	}
	if users == nil {
		users, e = p.users(ctx)
		if e != nil {
			return nil, e
		}
	}
	u, ok := users[sourceDomain(s.Domain)]
	if !ok {
		return r, nil
	}
	ratio := parseNumber(u.Ratio)
	if ratio == nil {
		return r, nil
	}
	upload, download := number(parseNumber(u.Upload)), number(parseNumber(u.Download))
	unlimited := number(ratio) == 0 && upload > 0 && download <= 0
	r["current"] = number(ratio)
	if unlimited {
		r["current"] = nil
	}
	r["available"] = true
	r["unlimited"] = unlimited
	r["reached"] = unlimited || number(ratio) >= t.N("site_ratio_target")
	v := []string{}
	if u.UpdatedDay != "" {
		v = append(v, u.UpdatedDay)
	}
	if u.UpdatedTime != "" {
		v = append(v, u.UpdatedTime)
	}
	r["updated_at"] = optional(strings.Join(v, " "))
	return r, nil
}
func (p *Core) evaluateRatio(ctx context.Context, t Task) (bool, string, Row, error) {
	r, e := p.ratio(ctx, t, nil)
	if e != nil {
		return false, "", r, e
	}
	if !truth(r["enabled"]) {
		return true, "", r, nil
	}
	if !truth(r["available"]) {
		return false, "暂无站点分享率统计，等待数据更新", r, nil
	}
	if truth(r["reached"]) {
		current := fmt.Sprintf("%.2f", number(r["current"]))
		if truth(r["unlimited"]) {
			current = "无限"
		}
		return false, fmt.Sprintf("站点分享率 %s，已达到目标 %.2f", current, number(r["target"])), r, nil
	}
	return true, "", r, nil
}
func (p *Core) Status(ctx context.Context) (Row, error) {
	c := p.Config()
	var us map[string]UserData
	for _, t := range c.Tasks {
		if t.B("site_ratio_control") {
			var e error
			us, e = p.users(ctx)
			if e != nil {
				return nil, e
			}
			break
		}
	}
	tasks := []any{}
	aggregate := Row{"task_count": len(c.Tasks), "enabled_count": 0, "running_count": 0, "active_count": 0, "uploaded": float64(0), "downloaded": float64(0), "seeding_size": float64(0)}
	for _, t := range c.Tasks {
		r, e := p.Summary(ctx, t.ID(), us)
		if e != nil {
			return nil, e
		}
		tasks = append(tasks, r)
		if truth(r["enabled"]) {
			aggregate["enabled_count"] = number(aggregate["enabled_count"]) + 1
		}
		if r["state"] == "running" || r["state"] == "brush" || r["state"] == "check" {
			aggregate["running_count"] = number(aggregate["running_count"]) + 1
		}
		st := row(r["statistic"])
		aggregate["active_count"] = number(aggregate["active_count"]) + number(st["active"])
		for _, k := range []string{"uploaded", "downloaded"} {
			aggregate[k] = number(aggregate[k]) + number(st[k])
		}
		aggregate["seeding_size"] = number(aggregate["seeding_size"]) + number(r["seeding_size"])
	}
	sites, e := p.h.Sites(ctx)
	if e != nil {
		return nil, e
	}
	ds, e := p.h.Downloaders(ctx)
	if e != nil {
		return nil, e
	}
	so, do := []any{}, []any{}
	for _, s := range sites {
		if !s.Public {
			so = append(so, Row{"title": s.Name, "value": s.ID})
		}
	}
	for _, d := range ds {
		do = append(do, Row{"title": d.Name, "value": d.Name})
	}
	r := clone(c.Settings)
	r["summary"] = aggregate
	r["tasks"] = tasks
	r["options"] = Row{"sites": so, "downloaders": do}
	return r, nil
}
func (p *Core) Summary(ctx context.Context, id string, us map[string]UserData) (Row, error) {
	t, ok := p.task(id)
	if !ok {
		return Row{}, nil
	}
	st, e := p.statistic(ctx, id)
	if e != nil {
		return nil, e
	}
	rs, e := p.records(ctx, id, "torrents")
	if e != nil {
		return nil, e
	}
	history, e := p.runs(ctx, id)
	if e != nil {
		return nil, e
	}
	rt := p.Runtime(id)
	ratio, e := p.ratio(ctx, t, us)
	if e != nil {
		return nil, e
	}
	state := "running"
	switch {
	case !p.Enabled():
		state = "disabled"
	case !t.B("enabled"):
		state = "paused"
	case rt["state"] == "queued" || rt["state"] == "running":
		state = text(fallback(rt["operation"], "running"))
	case truth(ratio["enabled"]) && !truth(ratio["available"]):
		state = "ratio_unavailable"
	case truth(ratio["reached"]):
		state = "waiting_ratio"
	case !p.inTime(t):
		state = "waiting"
	case truth(rt["last_error"]):
		state = "error"
	}
	name := "站点已删除"
	s, e := p.site(ctx, t)
	if e != nil {
		return nil, e
	}
	if s != nil {
		name = s.Name
	}
	var last any
	if len(history) > 0 {
		last = history[0]
	}
	return Row{"id": t.ID(), "name": t.Name(), "enabled": t.B("enabled"), "site_id": t.Row["site_id"], "site_name": name, "downloader": t.S("downloader"), "brush_interval": t.Row["brush_interval"], "check_interval": t.Row["check_interval"], "cron": t.Row["cron"], "active_time_range": t.Row["active_time_range"], "state": state, "operation": rt["operation"], "last_error": rt["last_error"], "next_run_at": p.nextRun(t, history), "last_run": last, "statistic": st, "seeding_size": seedingSize(rs), "site_ratio": ratio}, nil
}
func (p *Core) Detail(ctx context.Context, id, state string, page, pageSize int) (Row, error) {
	t, ok := p.task(id)
	if !ok {
		return nil, configError("刷流任务不存在")
	}
	ts, e := p.records(ctx, id, "torrents")
	if e != nil {
		return nil, e
	}
	arch, e := p.records(ctx, id, "archived")
	if e != nil {
		return nil, e
	}
	rows := []any{}
	for _, k := range ts.Order {
		v := ts.Values[k]
		if state == "active" && truth(v["deleted"]) || state == "deleted" && !truth(v["deleted"]) {
			continue
		}
		rows = append(rows, v)
	}
	if state != "active" {
		for _, k := range arch.Order {
			rows = append(rows, arch.Values[k])
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return number(row(rows[i])["time"]) > number(row(rows[j])["time"]) })
	total := len(rows)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	summary, e := p.Summary(ctx, id, nil)
	if e != nil {
		return nil, e
	}
	runs, e := p.runs(ctx, id)
	if e != nil {
		return nil, e
	}
	if len(runs) > 20 {
		runs = runs[:20]
	}
	return Row{"task": clone(t.Row), "summary": summary, "runs": runs, "torrents": Row{"items": rows[start:end], "total": total, "page": page, "page_size": pageSize, "state": state}}, nil
}
func (p *Core) nextRun(t Task, history []any) any {
	now := p.h.Now()
	var next time.Time
	if t.S("cron") != "" {
		cron, e := schedule.ParseCrontab(t.S("cron"), now.Location().String())
		if e != nil {
			return nil
		}
		next = cron.First(now)
	} else {
		for _, v := range history {
			r := row(v)
			if r["kind"] == "brush" && truth(r["started_at"]) {
				base, e := time.Parse(time.RFC3339, text(r["started_at"]))
				if e != nil {
					return nil
				}
				next = base.Add(time.Duration(t.N("brush_interval")) * time.Minute)
				if next.Before(now) {
					next = now
				}
				break
			}
		}
		if next.IsZero() {
			next = now.Add(time.Duration(t.N("brush_interval")) * time.Minute)
		}
	}
	if next.IsZero() {
		return nil
	}
	return next.Format("2006-01-02T15:04-07:00")
}
func (p *Core) inTime(t Task) bool {
	s := t.S("active_time_range")
	if !validTimeRange(s) {
		return true
	}
	now := p.h.Now()
	v := float64(now.Hour()*60*60+now.Minute()*60+now.Second()) + float64(now.Nanosecond())/1e9
	parts := strings.Split(s, "-")
	start, _ := time.Parse("15:04", sourceDecimalASCII(parts[0]))
	end, _ := time.Parse("15:04", sourceDecimalASCII(parts[1]))
	a, b := start.Hour()*3600+start.Minute()*60, end.Hour()*3600+end.Minute()*60
	if a <= b {
		return float64(a) <= v && v <= float64(b)
	}
	return v >= float64(a) || v <= float64(b)
}
