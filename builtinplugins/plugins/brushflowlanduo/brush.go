package brushflowlanduo

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	pyre "light-media/builtinplugins/plugins/brushflowlanduo/compat/pythonre"
)

func (p *Core) Brush(ctx context.Context, id string) error {
	t, ok := p.task(id)
	if !ok || !p.Enabled() || !t.B("enabled") {
		return nil
	}
	lock := p.taskLock(id)
	if !lock.TryLock() {
		p.h.Log(ctx, "info", fmt.Sprintf("刷流任务 [%s] 已有操作执行中，本轮跳过", t.Name()))
		return nil
	}
	defer lock.Unlock()
	t, ok = p.task(id)
	if !ok || !p.Enabled() || !t.B("enabled") {
		return nil
	}
	r := p.newReport("brush")
	p.SetRuntime(id, Row{"state": "running", "operation": "brush", "last_error": nil})
	p.brush.Lock()
	err := p.runBrush(ctx, t, r)
	p.brush.Unlock()
	if err != nil {
		r["success"] = false
		r["error"] = err.Error()
		p.SetRuntime(id, Row{"last_error": err.Error()})
		p.h.Log(ctx, "error", fmt.Sprintf("刷流任务 [%s] 执行失败：%s", t.Name(), err))
	} else {
		r["success"] = r["result"] != "downloader_unavailable" && r["result"] != "site_missing"
	}
	r["finished_at"] = p.h.Now().Format(time.RFC3339)
	saveErr := p.appendRun(ctx, id, r)
	p.SetRuntime(id, Row{"state": "idle", "operation": nil})
	if err != nil {
		return err
	}
	return saveErr
}
func (p *Core) DownloadSizes(ctx context.Context, name string) (bool, bool, []float64, error) {
	d, e := p.h.Downloader(ctx, name)
	if e != nil {
		return false, false, nil, e
	}
	if d == nil {
		return false, false, nil, nil
	}
	if !d.Active {
		return true, false, nil, nil
	}
	ts, e := p.h.Torrents(ctx, name)
	if e != nil {
		return true, true, nil, e
	}
	ss := []float64{}
	for _, t := range ts {
		ss = append(ss, number(t.Raw["total_size"]))
	}
	return true, true, ss, nil
}
func (p *Core) sizeTasks() []SizeTask {
	out := []SizeTask{}
	for _, t := range p.Config().Tasks {
		out = append(out, SizeTask{Downloader: t.S("downloader"), DiskSize: floatptr(t.Row["disksize"])})
	}
	return out
}
func (p *Core) sizeCondition(ctx context.Context, t Task, size, global float64) (bool, string, error) {
	c := p.Config()
	return EvaluateSizeCondition(ctx, &SizeTask{Downloader: t.S("downloader"), DiskSize: floatptr(t.Row["disksize"])}, size, floatptr(c.Settings["global_disksize"]), &global, p.sizeTasks(), p)
}

type brushDownloadBudget struct {
	taskLimit, globalLimit int
	taskCount, globalCount int
}

func (b brushDownloadBudget) blockReason() string {
	if b.globalLimit != 0 && b.globalCount >= b.globalLimit {
		return fmt.Sprintf("全局同时下载任务数达到上限 %d", b.globalLimit)
	}
	if b.taskLimit != 0 && b.taskCount >= b.taskLimit {
		return fmt.Sprintf("同时下载任务数达到上限 %d", b.taskLimit)
	}
	return ""
}

func (p *Core) downloadBudget(ctx context.Context, task Task, ts *RecordSet) (brushDownloadBudget, error) {
	c := p.Config()
	budget := brushDownloadBudget{
		taskLimit:   int(task.N("maxdlcount")),
		globalLimit: int(number(c.Settings["global_maxdlcount"])),
	}
	if budget.taskLimit == 0 && budget.globalLimit == 0 {
		// No slot limit needs the list, but existing records still need the
		// daemon counters so summaries and detail rows do not stay at 0.
		if torrents, e := p.h.Torrents(ctx, task.S("downloader")); e == nil {
			updateRecords(p, torrents, ts)
		} else {
			p.h.Log(ctx, "warning", "刷新刷流记录进度失败："+e.Error())
		}
		return budget, nil
	}
	downloader, taskTag := task.S("downloader"), task.Tag()
	names := []string{downloader}
	if budget.globalLimit != 0 {
		for _, t := range c.Tasks {
			if name := t.S("downloader"); name != "" {
				names = append(names, name)
			}
		}
	}
	for _, name := range unique(names) {
		// Queued, checking and paused torrents still occupy a slot until complete.
		torrents, err := p.h.Torrents(ctx, name)
		if err != nil {
			return budget, fmt.Errorf("获取下载器 [%s] 刷流下载数量失败：%w", name, err)
		}
		updateRecords(p, torrents, ts)
		for _, torrent := range torrents {
			progress := torrent.Raw["progress"]
			if torrent.Kind == "transmission" {
				progress = torrent.Raw["percentDone"]
			}
			if number(progress) >= 1 {
				continue
			}
			tags := torrentLabels(torrent)
			if name == downloader && contains(tags, taskTag) {
				budget.taskCount++
			}
			if contains(tags, GlobalTag) {
				budget.globalCount++
			}
		}
	}
	return budget, nil
}

func (p *Core) averageBandwidth(ctx context.Context) (*float64, *float64, error) {
	up, down := []float64{}, []float64{}
	for i := 0; i < 5; i++ {
		infos, e := p.h.TransferInfo(ctx)
		if e != nil {
			return nil, nil, e
		}
		var u, d float64
		for _, v := range infos {
			u += v.UploadSpeed
			d += v.DownloadSpeed
		}
		up = append(up, u)
		down = append(down, d)
		if i < 4 {
			if e = p.h.Sleep(ctx, 3*time.Second); e != nil {
				return nil, nil, e
			}
		}
	}
	if len(up) == 0 {
		return nil, nil, nil
	}
	u, d := 0.0, 0.0
	for i := range up {
		u += up[i]
		d += down[i]
	}
	u /= float64(len(up))
	d /= float64(len(down))
	return &u, &d, nil
}
func (p *Core) bandwidthCondition(ctx context.Context, t Task) (bool, string, error) {
	c := p.Config()
	u, d, e := p.averageBandwidth(ctx)
	if e != nil {
		return false, "", e
	}
	if u == nil || d == nil {
		return true, "", nil
	}
	for _, v := range []struct {
		value  any
		speed  float64
		reason string
	}{{c.Settings["global_maxupspeed"], *u, "全局总上传带宽达到上限"}, {c.Settings["global_maxdlspeed"], *d, "全局总下载带宽达到上限"}, {t.Row["maxupspeed"], *u, "总上传带宽达到上限"}, {t.Row["maxdlspeed"], *d, "总下载带宽达到上限"}} {
		if n := number(v.value); n != 0 && v.speed >= n*1024 {
			return false, fmt.Sprintf("%s %s KB/s", v.reason, text(v.value)), nil
		}
	}
	return true, "", nil
}
func (p *Core) runBrush(ctx context.Context, t Task, r Row) error {
	valid, e := p.validateReference(ctx, t, true)
	if e != nil {
		return e
	}
	if !valid {
		r["result"] = "downloader_unavailable"
		return nil
	}
	ds, e := p.service(ctx, t)
	if e != nil {
		return e
	}
	if ds == nil {
		r["result"] = "downloader_unavailable"
		return nil
	}
	if !p.inTime(t) {
		r["result"] = "outside_active_time"
		reason(r, "不在开启时间段", 1)
		return nil
	}
	s, e := p.site(ctx, t)
	if e != nil {
		return e
	}
	if s == nil {
		r["result"] = "site_missing"
		return nil
	}
	passed, why, ratio, e := p.evaluateRatio(ctx, t)
	if e != nil {
		return e
	}
	if truth(ratio["enabled"]) {
		r["site_ratio"] = ratio
	}
	if !passed {
		r["result"] = "site_ratio_blocked"
		reason(r, why, 1)
		return nil
	}
	ts, e := p.records(ctx, t.ID(), "torrents")
	if e != nil {
		return e
	}
	global := 0.0
	if truth(p.Config().Settings["global_disksize"]) {
		global, e = TotalDownloaderSize(ctx, p.sizeTasks(), p)
		if e != nil {
			return e
		}
	}
	passed, why, e = p.sizeCondition(ctx, t, seedingSize(ts), global)
	if e != nil {
		return e
	}
	if !passed {
		r["result"] = "precondition_blocked"
		reason(r, why, 1)
		return nil
	}
	budget, e := p.downloadBudget(ctx, t, ts)
	if e != nil {
		return e
	}
	if why = budget.blockReason(); why != "" {
		r["result"] = "precondition_blocked"
		reason(r, why, 1)
		return nil
	}
	passed, why, e = p.bandwidthCondition(ctx, t)
	if e != nil {
		return e
	}
	if !passed {
		r["result"] = "precondition_blocked"
		reason(r, why, 1)
		return nil
	}
	all := NewRecords()
	for _, task := range p.Config().Tasks {
		v, e := p.records(ctx, task.ID(), "torrents")
		if e != nil {
			return e
		}
		for _, k := range v.Order {
			all.Set(k, v.Values[k])
		}
	}
	titles := []string{}
	if t.B("except_subscribe") {
		titles, e = p.h.SubscribeTitles(ctx)
		if e != nil {
			return e
		}
	}
	if e = p.brushCandidates(ctx, t, *s, ts, all, titles, r, global, budget); e != nil {
		return e
	}
	if e = p.put(ctx, t.ID(), "torrents", ts); e != nil {
		return e
	}
	_, e = p.recalculate(ctx, t.ID())
	return e
}
func (p *Core) brushCandidates(ctx context.Context, t Task, s Site, ts, all *RecordSet, titles []string, r Row, global float64, budget brushDownloadBudget) error {
	p.h.Log(ctx, "info", fmt.Sprintf("刷流任务 [%s] 开始获取站点 %s 的新种子", t.Name(), s.Name))
	candidates, e := p.h.Browse(ctx, s.ID, t.B("rss_support"))
	if e != nil {
		return e
	}
	if len(candidates) == 0 {
		r["result"] = "no_candidates"
		return nil
	}
	r["source_count"] = len(candidates)
	if t.B("except_subscribe") {
		filtered := []Row{}
		for _, v := range candidates {
			exclude := false
			for _, title := range titles {
				if strings.Contains(text(v["title"]), title) || strings.Contains(text(v["description"]), title) {
					exclude = true
					break
				}
			}
			if exclude {
				p.h.Log(ctx, "info", "命中订阅内容，排除种子："+text(v["title"])+"|"+text(v["description"]))
			} else {
				filtered = append(filtered, v)
			}
		}
		n := len(candidates) - len(filtered)
		r["subscription_excluded"] = n
		if n > 0 {
			reason(r, "命中订阅内容", n)
		}
		candidates = filtered
	}
	r["candidate_count"] = len(candidates)
	sort.SliceStable(candidates, func(i, j int) bool { return text(candidates[i]["pubdate"]) > text(candidates[j]["pubdate"]) })
	size := seedingSize(ts)
	for _, v := range candidates {
		if why := budget.blockReason(); why != "" {
			reason(r, why, 1)
			r["result"] = "precondition_blocked"
			break
		}
		adding := number(v["size"])
		if adding < 0 {
			adding = 0
		}
		passed, why, e := CanAddSize(ctx, &SizeTask{Downloader: t.S("downloader"), DiskSize: floatptr(t.Row["disksize"])}, size, adding, floatptr(p.Config().Settings["global_disksize"]), global, p.sizeTasks(), p)
		if e != nil {
			return e
		}
		if !passed {
			reason(r, why, 1)
			r["result"] = "waiting_for_space"
			p.h.Log(ctx, "info", fmt.Sprintf("刷流任务 [%s] 已达到保种上限，暂停添加新种子，等待保种删除释放空间", t.Name()))
			break
		}
		passed, why, e = p.candidateCondition(ctx, t, v, all)
		if e != nil {
			return e
		}
		if !passed {
			reason(r, why, 1)
			continue
		}
		record := p.candidateRecord(t, s, v)
		if t.B("del_no_free") {
			ok, e := p.verifyCandidate(ctx, t, record)
			if e != nil {
				return e
			}
			if !ok {
				reason(r, "促销状态或安全截止时间无法确认", 1)
				continue
			}
		}
		hash, e := p.h.Download(ctx, t, v, record, p.h.RandomTag())
		if e != nil {
			return e
		}
		if hash == "" {
			reason(r, "下载器添加失败", 1)
			continue
		}
		// Reserve the slot immediately, even if the downloader list still lags.
		budget.taskCount++
		budget.globalCount++
		ts.Set(hash, record)
		all.Set(hash, record)
		if t.B("del_no_free") {
			if e := p.startProtectedDownload(ctx, t, ts, record, hash); e != nil {
				return e
			}
		}
		size += number(v["size"])
		global += number(v["size"])
		r["added_count"] = number(r["added_count"]) + 1
		r["added_titles"] = append(list(r["added_titles"]), v["title"])
		if e = p.h.Event(ctx, Event{Name: "brushflow_download_added", Hash: hash, Data: record, Downloader: t.S("downloader")}); e != nil {
			return e
		}
		p.h.Log(ctx, "info", fmt.Sprintf("刷流任务 [%s] 新增种子：%s|%s", t.Name(), text(v["title"]), text(v["description"])))
		if e = p.addNotice(ctx, t, v); e != nil {
			return e
		}
	}
	r["filtered_count"] = mathMax(number(r["candidate_count"])-number(r["added_count"]), 0)
	if r["result"] == nil {
		r["result"] = "completed"
	}
	return nil
}

func (p *Core) startProtectedDownload(ctx context.Context, task Task, records *RecordSet, record Row, hash string) error {
	// The host has already added the torrent paused. A failed save cannot
	// authorize Start, and an interrupted start leaves the record available.
	if err := p.put(ctx, task.ID(), "torrents", records); err != nil {
		return err
	}
	expiry, _ := p.resolveExpiry(task, record)
	now := p.h.Now()
	stamp := float64(now.UnixNano()) / 1e9
	if expiry != nil && stamp-number(record["promotion_verified_at"]) < promotionProbe && now.Add(promotionStopMargin).Before(*expiry) {
		started, err := p.h.Start(ctx, task.S("downloader"), []string{hash})
		if err != nil || started {
			return err
		}
	}
	record["promotion_paused_at"] = float64(p.h.Now().UnixNano()) / 1e9
	record["promotion_pause_reason"] = promotionPauseVerify
	if err := p.put(ctx, task.ID(), "torrents", records); err != nil {
		return err
	}
	p.h.Log(ctx, "warning", fmt.Sprintf("刷流任务 [%s] 新种子保持暂停，需重新核实促销后恢复：%s", task.Name(), text(record["title"])))
	return nil
}

func mathMax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func (p *Core) pubMinutes(ctx context.Context, v any) float64 {
	if !truth(v) {
		return 0
	}
	s := strings.ReplaceAll(strings.ReplaceAll(text(v), "T", " "), "Z", "")
	t, e := time.ParseInLocation("2006-01-02 15:04:05", s, p.h.Now().Location())
	if e != nil {
		p.h.Log(ctx, "error", fmt.Sprintf("解析发布时间 %s 失败：%s", text(v), e))
		return 0
	}
	return p.h.Now().Sub(t).Seconds() / 60
}
func (p *Core) candidateCondition(ctx context.Context, t Task, v Row, all *RecordSet) (bool, string, error) {
	for _, k := range all.Order {
		r := all.Values[k]
		if truth(v["page_url"]) {
			if text(v["site_name"])+text(v["page_url"]) == text(r["site_name"])+text(r["page_url"]) {
				return false, "重复种子", nil
			}
		} else if text(v["site_name"]) == text(r["site_name"]) && text(v["title"]) == text(r["title"]) && number(v["size"]) == number(r["size"]) && text(v["description"]) == text(r["description"]) {
			return false, "重复种子", nil
		}
	}
	if truth(v["title"]) {
		for _, r := range all.Values {
			if text(v["site_name"]) != text(r["site_name"]) && text(v["title"]) == text(r["title"]) && !truth(r["deleted"]) && !complete(r) {
				return false, "其他站点存在尚未下载完成的相同种子", nil
			}
		}
	}
	if t.S("freeleech") != "" && (v["downloadvolumefactor"] == nil || number(v["downloadvolumefactor"]) != 0) {
		return false, "非免费种子", nil
	}
	if t.S("freeleech") == "2xfree" && number(v["uploadvolumefactor"]) != 2 {
		return false, "非双倍上传种子", nil
	}
	if t.S("hr") == "yes" && truth(v["hit_and_run"]) {
		return false, "存在 H&R", nil
	}
	for _, k := range []string{"include", "exclude"} {
		if t.S(k) == "" {
			continue
		}
		re, e := pyre.Compile("(?i)" + t.S(k))
		if e != nil {
			return false, "", e
		}
		found := false
		for _, textKey := range []string{"title", "description"} {
			if truth(v[textKey]) {
				ok, e := re.Search(text(v[textKey]))
				if e != nil {
					return false, "", e
				}
				found = found || ok
			}
		}
		if k == "include" && !found {
			return false, "不符合包含规则", nil
		}
		if k == "exclude" && found {
			return false, "符合排除规则", nil
		}
	}
	if s := t.S("size"); s != "" {
		limits := ranges(s, 1024*1024*1024)
		n := number(v["size"])
		if len(limits) == 1 && n < limits[0] {
			return false, "种子大小低于下限", nil
		}
		if len(limits) > 1 && (n < limits[0] || n > limits[1]) {
			return false, "种子大小不在范围内", nil
		}
	}
	if s := t.S("seeder"); s != "" {
		limits := ranges(s, 1)
		n := number(v["seeders"])
		if len(limits) == 1 && n > limits[0] {
			return false, "做种人数超过上限", nil
		}
		if len(limits) > 1 && (n < limits[0] || n > limits[1]) {
			return false, "做种人数不在范围内", nil
		}
	}
	if s := t.S("pubtime"); s != "" {
		limits := ranges(s, 1)
		n := p.pubMinutes(ctx, v["pubdate"]) - t.N("timezone_offset")*60
		if len(limits) == 1 && n > limits[0] {
			return false, "发布时间超过上限", nil
		}
		if len(limits) > 1 && (n < limits[0] || n > limits[1]) {
			return false, "发布时间不在范围内", nil
		}
	}
	return true, "", nil
}
