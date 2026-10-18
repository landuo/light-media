package brushflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type association struct {
	Task   Task
	Record Row
}
type deleteCandidate struct {
	Task                         Task
	Hash, Downloader             string
	Record                       Row
	Size, SeedingTime            float64
	PreReason, ConditionalReason string
	Proxy, Completed, HR         bool
	Associations                 []association
	Reasons                      map[string][2]string
}
type deleteEntry struct {
	Candidate   deleteCandidate
	Reason      string
	TaskReasons map[string]string
}
type torrentKey struct{ Downloader, Hash string }

func selectGlobal(candidates []deleteCandidate, total, min, max float64) ([]deleteEntry, float64, bool) {
	selected := []deleteEntry{}
	seen := map[torrentKey]bool{}
	remaining := total
	selectOne := func(c deleteCandidate, why string, field int, dynamic bool) {
		key := torrentKey{c.Downloader, c.Hash}
		if seen[key] {
			return
		}
		seen[key] = true
		reasons := map[string]string{}
		associations := c.Associations
		if len(associations) == 0 {
			associations = []association{{c.Task, c.Record}}
		}
		for _, a := range associations {
			reason := why
			if v, ok := c.Reasons[a.Task.ID()]; ok && field >= 0 && v[field] != "" {
				reason = v[field]
			}
			if dynamic {
				reason = "触发全局动态删除阈值，" + reason
			}
			reasons[a.Task.ID()] = reason
		}
		selected = append(selected, deleteEntry{c, why, reasons})
		remaining = mathMax(remaining-c.Size, 0)
	}
	for _, c := range candidates {
		if c.PreReason != "" {
			selectOne(c, c.PreReason, 0, false)
		}
	}
	threshold := remaining >= max
	if !threshold {
		return selected, remaining, false
	}
	for _, c := range candidates {
		if remaining <= min {
			break
		}
		if !c.Proxy && c.ConditionalReason != "" {
			selectOne(c, c.ConditionalReason, 1, false)
		}
	}
	if remaining > min {
		for _, c := range candidates {
			if remaining <= min {
				break
			}
			if c.Proxy && c.ConditionalReason != "" {
				selectOne(c, "触发全局动态删除阈值，"+c.ConditionalReason, 1, true)
			}
		}
	}
	fallback := []deleteCandidate{}
	for _, c := range candidates {
		if c.Proxy && c.Completed && !c.HR {
			fallback = append(fallback, c)
		}
	}
	sort.SliceStable(fallback, func(i, j int) bool { return fallback[i].SeedingTime > fallback[j].SeedingTime })
	for _, c := range fallback {
		if remaining <= min {
			break
		}
		selectOne(c, "触发全局动态删除阈值，系统按做种时间清理", -1, false)
	}
	return selected, remaining, true
}
func (p *Core) collectGlobal(ctx context.Context) ([]deleteCandidate, float64, map[string]*RecordSet, map[string]Downloader, error) {
	cfg := p.Config()
	records := map[string]*RecordSet{}
	services := map[string]Downloader{}
	associations := map[torrentKey][]association{}
	for _, t := range cfg.Tasks {
		rs, e := p.records(ctx, t.ID(), "torrents")
		if e != nil {
			return nil, 0, nil, nil, e
		}
		records[t.ID()] = rs
		for _, h := range rs.Order {
			r := rs.Values[h]
			if !truth(r["deleted"]) {
				k := torrentKey{t.S("downloader"), h}
				associations[k] = append(associations[k], association{t, r})
			}
		}
	}
	cache := map[string][]DownloadState{}
	candidateRows := map[torrentKey][]deleteCandidate{}
	candidateOrder := []torrentKey{}
	counted := map[torrentKey]bool{}
	total := 0.0
	for _, t := range cfg.Tasks {
		if !t.B("enabled") {
			continue
		}
		rs := records[t.ID()]
		name := t.S("downloader")
		all, ok := cache[name]
		if !ok {
			ds, e := p.h.Downloaders(ctx)
			if e != nil {
				return nil, 0, nil, nil, e
			}
			var service *Downloader
			for _, d := range ds {
				if d.Name == name {
					v := d
					service = &v
				}
			}
			if service == nil || !service.Active {
				return nil, 0, nil, nil, configError(fmt.Sprintf("全局动态删种无法获取下载器 [%s] 实时状态，本轮已中止", name))
			}
			all, e = p.h.Torrents(ctx, name)
			if e != nil {
				return nil, 0, nil, nil, configError(fmt.Sprintf("全局动态删种获取下载器 [%s] 种子失败，本轮已中止", name))
			}
			cache[name] = all
			services[name] = *service
		}
		byHash := map[string]DownloadState{}
		for _, d := range all {
			if h := torrentHash(d); h != "" {
				byHash[h] = d
			}
		}
		check := []DownloadState{}
		hashes := append([]string{}, rs.Order...)
		for _, h := range hashes {
			if d, ok := byHash[h]; ok {
				check = append(check, d)
			}
		}
		updateRecords(p, check, rs)
		if e := p.missing(ctx, t, rs, hashes, all); e != nil {
			return nil, 0, nil, nil, e
		}
		if e := p.put(ctx, t.ID(), "torrents", rs); e != nil {
			return nil, 0, nil, nil, e
		}
		for _, d := range check {
			h := torrentHash(d)
			r := rs.Values[h]
			if r == nil || truth(r["deleted"]) {
				continue
			}
			k := torrentKey{name, h}
			if !counted[k] {
				info := p.torrentInfo(d)
				total += number(fallback(info["total_size"], fallback(r["size"], 0)))
				counted[k] = true
			}
		}
		for _, d := range filterTags(check, t.S("delete_except_tags")) {
			h := torrentHash(d)
			r := rs.Values[h]
			if r == nil || truth(r["deleted"]) {
				continue
			}
			info := p.torrentInfo(d)
			pre := ""
			if !truth(r["hit_and_run"]) {
				expired, why, e := p.promotionExpired(ctx, t, info, r)
				if e != nil {
					return nil, 0, nil, nil, e
				}
				timeout := t.N("download_time") != 0 && !complete(info) && number(info["dltime"]) >= t.N("download_time")*3600
				if expired {
					pre = why
				} else if timeout {
					pre = "下载耗时达到 " + text(t.Row["download_time"]) + " 小时"
				}
			}
			should, conditional, e := p.deleteCondition(ctx, t, info, r)
			if e != nil {
				return nil, 0, nil, nil, e
			}
			if !should {
				conditional = ""
			}
			key := torrentKey{name, h}
			if _, ok := candidateRows[key]; !ok {
				candidateOrder = append(candidateOrder, key)
			}
			candidateRows[key] = append(candidateRows[key], deleteCandidate{Task: t, Hash: h, Record: r, Downloader: name, Size: number(fallback(info["total_size"], fallback(r["size"], 0))), PreReason: pre, ConditionalReason: conditional, Proxy: t.B("proxy_delete"), Completed: complete(info), HR: truth(r["hit_and_run"]), SeedingTime: number(info["seeding_time"])})
		}
	}
	candidates := []deleteCandidate{}
	for _, key := range candidateOrder {
		rows := candidateRows[key]
		ass := associations[key]
		if len(rows) != len(ass) {
			continue
		}
		c := rows[0]
		c.Associations = ass
		c.Reasons = map[string][2]string{}
		for _, r := range rows {
			c.Proxy = c.Proxy && r.Proxy
			c.Completed = c.Completed && r.Completed
			c.HR = c.HR || r.HR
			if r.PreReason == "" {
				c.PreReason = ""
			}
			if r.ConditionalReason == "" {
				c.ConditionalReason = ""
			}
			if r.SeedingTime > c.SeedingTime {
				c.SeedingTime = r.SeedingTime
			}
			c.Reasons[r.Task.ID()] = [2]string{r.PreReason, r.ConditionalReason}
		}
		candidates = append(candidates, c)
	}
	return candidates, total, records, services, nil
}
func (p *Core) GlobalDelete(ctx context.Context) (int, error) {
	cfg := p.Config()
	if !cfg.GlobalDynamic() {
		return 0, nil
	}
	if !p.global.TryLock() {
		p.h.Log(ctx, "info", "已有全局动态删种正在执行，本轮跳过")
		return 0, nil
	}
	defer p.global.Unlock()
	unlock := p.lockAll()
	defer unlock()
	candidates, total, records, services, e := p.collectGlobal(ctx)
	if e != nil {
		return 0, e
	}
	limits := ranges(text(cfg.Settings["global_delete_size_range"]), 1024*1024*1024)
	min, max := limits[0], limits[0]
	if len(limits) > 1 {
		max = limits[1]
	}
	plan, _, threshold := selectGlobal(candidates, total, min, max)
	if len(plan) == 0 {
		if threshold {
			p.h.Log(ctx, "info", fmt.Sprintf("全局做种体积 %.1f GB 已达到动态删种上限，但没有符合任务策略的可删除种子", BytesToGB(total)))
		} else {
			p.h.Log(ctx, "info", fmt.Sprintf("全局做种体积 %.1f GB，未达到动态删种上限 %.1f GB", BytesToGB(total), BytesToGB(max)))
		}
		return 0, nil
	}
	groups := map[string][]deleteEntry{}
	order := []string{}
	for _, v := range plan {
		name := v.Candidate.Downloader
		if _, ok := groups[name]; !ok {
			order = append(order, name)
		}
		groups[name] = append(groups[name], v)
	}
	deleted := []deleteEntry{}
	for _, name := range order {
		entries := groups[name]
		service, ok := services[name]
		if !ok {
			continue
		}
		hashes := []string{}
		for _, v := range entries {
			hashes = append(hashes, v.Candidate.Hash)
		}
		hashes = unique(hashes)
		if service.Kind == "qbittorrent" {
			if e = p.h.Reannounce(ctx, name, hashes); e != nil {
				if boundary(e) {
					return 0, e
				}
				p.h.Log(ctx, "warning", fmt.Sprintf("全局动态删种重新汇报下载器 [%s] 失败：%s", name, e))
			}
		}
		ok, e := p.h.Delete(ctx, name, hashes, true)
		if e != nil {
			if boundary(e) {
				return 0, e
			}
			p.h.Log(ctx, "error", fmt.Sprintf("全局动态删种调用下载器 [%s] 删除失败：%s", name, e))
			continue
		}
		if ok {
			deleted = append(deleted, entries...)
		}
	}
	deletedAt := float64(p.h.Now().UnixNano()) / 1e9
	affected := map[string]bool{}
	recorded := []deleteEntry{}
	notices := []struct {
		task   Task
		record Row
		why    string
	}{}
	for _, v := range deleted {
		did := false
		ass := v.Candidate.Associations
		if len(ass) == 0 {
			ass = []association{{v.Candidate.Task, v.Candidate.Record}}
		}
		for _, a := range ass {
			rs := records[a.Task.ID()]
			r := rs.Values[v.Candidate.Hash]
			if r == nil {
				continue
			}
			why := v.TaskReasons[a.Task.ID()]
			if why == "" {
				why = v.Reason
			}
			r["deleted"] = true
			r["deleted_time"] = deletedAt
			affected[a.Task.ID()] = true
			notices = append(notices, struct {
				task   Task
				record Row
				why    string
			}{a.Task, r, why})
			did = true
		}
		if did {
			recorded = append(recorded, v)
		}
	}
	for _, t := range cfg.Tasks {
		if affected[t.ID()] {
			if e = p.put(ctx, t.ID(), "torrents", records[t.ID()]); e != nil {
				return 0, e
			}
			if _, e = p.recalculate(ctx, t.ID()); e != nil {
				return 0, e
			}
		}
	}
	for _, n := range notices {
		if e = p.deleteNotice(ctx, n.task, n.record, n.why); e != nil {
			if boundary(e) {
				return 0, e
			}
			p.h.Log(ctx, "warning", fmt.Sprintf("全局动态删种发送任务 [%s] 通知失败：%s", n.task.Name(), e))
		}
		p.h.Log(ctx, "info", fmt.Sprintf("全局动态删种删除任务 [%s] 种子：%s，原因：%s", n.task.Name(), text(n.record["title"]), n.why))
	}
	remaining := total
	for _, v := range deleted {
		remaining -= v.Candidate.Size
	}
	remaining = mathMax(remaining, 0)
	if threshold && len(limits) > 1 && len(recorded) > 0 {
		names := []string{}
		seen := map[string]bool{}
		for _, v := range recorded {
			ass := v.Candidate.Associations
			if len(ass) == 0 {
				ass = []association{{v.Candidate.Task, v.Candidate.Record}}
			}
			for _, a := range ass {
				if a.Task.B("notify") && !seen[a.Task.ID()] {
					names = append(names, a.Task.Name())
					seen[a.Task.ID()] = true
				}
			}
		}
		if len(names) > 0 {
			if e = p.h.Message(ctx, Notice{Title: "【刷流任务全局动态删除】", Text: fmt.Sprintf("任务：%s\n删除：%d 个种子\n当前做种：%.1f GB", strings.Join(names, "、"), len(recorded), BytesToGB(remaining))}); e != nil {
				if boundary(e) {
					return 0, e
				}
				p.h.Log(ctx, "warning", "全局动态删种发送汇总通知失败："+e.Error())
			}
		}
	}
	return len(recorded), nil
}
