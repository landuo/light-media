package brushflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Boundary errors stop subsequent external calls. The App creates them from
// its private dispatch/owner receipts, never from HTTP wording or worker data.
type effectBoundary interface{ StopExternalEffects() bool }

func boundary(e error) bool { var b effectBoundary; return errors.As(e, &b) && b.StopExternalEffects() }
func (p *Core) Check(ctx context.Context, id string, wait bool) error {
	t, ok := p.task(id)
	if !ok || !p.Enabled() || !t.B("enabled") {
		return nil
	}
	lock := p.taskLock(id)
	if wait || t.B("del_no_free") {
		lock.Lock()
	} else if !lock.TryLock() {
		p.h.Log(ctx, "info", fmt.Sprintf("刷流任务 [%s] 已有操作执行中，本轮检查跳过", t.Name()))
		return nil
	}
	r := p.newReport("check")
	p.SetRuntime(id, Row{"state": "running", "operation": "check", "last_error": nil})
	err := p.runCheck(ctx, t, r)
	if err != nil {
		r["success"] = false
		r["error"] = err.Error()
		p.SetRuntime(id, Row{"last_error": err.Error()})
		p.h.Log(ctx, "error", fmt.Sprintf("刷流任务 [%s] 检查失败：%s", t.Name(), err))
	} else {
		r["success"] = r["result"] != "downloader_unavailable" && r["result"] != "downloader_error"
	}
	lock.Unlock()
	if p.Config().GlobalDynamic() && !boundary(err) {
		n, e := p.GlobalDelete(ctx)
		if e != nil {
			r["success"] = false
			r["error"] = e.Error()
			p.SetRuntime(id, Row{"last_error": e.Error()})
			p.h.Log(ctx, "error", "全局动态删种失败："+e.Error())
			if err == nil {
				err = e
			}
		} else {
			r["global_deleted_count"] = n
			r["deleted_count"] = number(r["deleted_count"]) + float64(n)
		}
	}
	r["finished_at"] = p.h.Now().Format(time.RFC3339)
	saveErr := p.appendRun(ctx, id, r)
	p.SetRuntime(id, Row{"state": "idle", "operation": nil})
	if err != nil {
		return err
	}
	return saveErr
}
func (p *Core) syncTags(ctx context.Context, t Task, ts, unmanaged *RecordSet, torrents []DownloadState) error {
	ds, e := p.service(ctx, t)
	if e != nil {
		return e
	}
	if ds == nil || ds.Kind != "qbittorrent" {
		return nil
	}
	added, removed, reset := []Row{}, []Row{}, []Row{}
	for _, d := range torrents {
		hash := torrentHash(d)
		tags := torrentLabels(d)
		r, exists := ts.Values[hash]
		hasUnique, hasGlobal := contains(tags, t.Tag()), contains(tags, GlobalTag)
		primary := false
		if hasGlobal && !exists {
			site, _, e := p.siteByTorrent(ctx, d)
			if e != nil {
				return e
			}
			if site == int(t.N("site_id")) {
				for _, v := range p.Config().Tasks {
					if int(v.N("site_id")) == site {
						primary = v.ID() == t.ID()
						break
					}
				}
			}
		}
		managed := hasUnique || hasGlobal && exists || hasGlobal && !exists && primary
		if managed {
			if !exists {
				r = unmanaged.Del(hash)
				if r == nil {
					r, e = p.convertRecord(ctx, t, d)
					if e != nil {
						return e
					}
				}
				r["task_id"] = t.ID()
				r["task_name"] = t.Name()
				ts.Set(hash, r)
				added = append(added, r)
			} else if truth(r["deleted"]) {
				r["deleted"] = false
				delete(r, "deleted_time")
				reset = append(reset, r)
			}
		} else if exists {
			unmanaged.Set(hash, ts.Del(hash))
			removed = append(removed, unmanaged.Values[hash])
		}
	}
	if e = p.put(ctx, t.ID(), "torrents", ts); e != nil {
		return e
	}
	if e = p.put(ctx, t.ID(), "unmanaged", unmanaged); e != nil {
		return e
	}
	for _, v := range []struct {
		title, status, reason string
		rs                    []Row
	}{{"【刷流任务种子加入】", "纳入刷流管理", "刷流任务标签匹配", added}, {"【刷流任务种子移除】", "移除刷流管理", "刷流任务标签移除", removed}, {"【刷流任务状态更新】", "恢复为正常", "下载器中仍存在对应种子", reset}} {
		if e = p.syncNotice(ctx, t, v.title, v.status, v.reason, v.rs); e != nil {
			return e
		}
	}
	return nil
}
func (p *Core) missing(ctx context.Context, t Task, ts *RecordSet, check []string, ds []DownloadState) error {
	exists := map[string]bool{}
	for _, d := range ds {
		exists[torrentHash(d)] = true
	}
	removed := []Row{}
	for _, k := range check {
		r := ts.Values[k]
		if r != nil && !exists[k] && !truth(r["deleted"]) {
			r["deleted"] = true
			r["deleted_time"] = float64(p.h.Now().UnixNano()) / 1e9
			removed = append(removed, r)
		}
	}
	return p.syncNotice(ctx, t, "【刷流任务状态更新】", "更新为已删除", "下载器中找不到对应种子", removed)
}
func (p *Core) runCheck(ctx context.Context, t Task, r Row) error {
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
	ts, e := p.records(ctx, t.ID(), "torrents")
	if e != nil {
		return e
	}
	unmanaged, e := p.records(ctx, t.ID(), "unmanaged")
	if e != nil {
		return e
	}
	torrents, e := p.h.Torrents(ctx, t.S("downloader"))
	if e != nil {
		r["result"] = "downloader_error"
		return configError("连接下载器出错")
	}
	if e = p.syncTags(ctx, t, ts, unmanaged, torrents); e != nil {
		return e
	}
	hashes := append([]string{}, ts.Order...)
	if len(hashes) == 0 {
		if e = p.CleanupTaskTag(ctx, t, torrents); e != nil {
			return e
		}
		r["result"] = "no_managed_torrents"
		r["active_count"] = 0
		_, e = p.recalculate(ctx, t.ID())
		return e
	}
	byHash := map[string]DownloadState{}
	for _, d := range torrents {
		byHash[torrentHash(d)] = d
	}
	check := []DownloadState{}
	for _, hash := range hashes {
		if d, ok := byHash[hash]; ok {
			check = append(check, d)
		}
	}
	updateRecords(p, check, ts)
	if e = p.missing(ctx, t, ts, hashes, torrents); e != nil {
		return e
	}
	promotionDelete, promotionPause, e := p.protectPromotion(ctx, t, check, ts)
	if e != nil {
		return e
	}
	filtered := []DownloadState{}
	for _, d := range check {
		if !contains(promotionDelete, torrentHash(d)) {
			filtered = append(filtered, d)
		}
	}
	filtered = filterTags(filtered, t.S("delete_except_tags"))
	deletes := []string{}
	if !p.Config().GlobalDynamic() {
		if t.B("proxy_delete") && t.S("delete_size_range") != "" {
			deletes, e = p.taskDynamicDelete(ctx, t, filtered, ts)
		} else {
			deletes, e = p.conditionDeletes(ctx, t, filtered, ts, false)
		}
		if e != nil {
			return e
		}
	}
	deletes = unique(append(promotionDelete, deletes...))
	deleted := false
	if len(deletes) > 0 {
		if ds.Kind == "qbittorrent" {
			reannounce := []string{}
			protected := append(append([]string{}, promotionDelete...), promotionPause...)
			for _, h := range deletes {
				if !contains(protected, h) {
					reannounce = append(reannounce, h)
				}
			}
			if len(reannounce) > 0 {
				if e = p.h.Reannounce(ctx, t.S("downloader"), reannounce); e != nil {
					if boundary(e) {
						return e
					}
					p.h.Log(ctx, "error", "强制重新汇报 Tracker 失败："+e.Error())
				}
			}
		}
		ok, e := p.h.Delete(ctx, t.S("downloader"), deletes, true)
		if e != nil {
			return e
		}
		if ok {
			deleted = true
			for _, h := range deletes {
				if v := ts.Values[h]; v != nil {
					v["deleted"] = true
					v["deleted_time"] = float64(p.h.Now().UnixNano()) / 1e9
				}
			}
		} else if len(promotionDelete) > 0 {
			return configError("促销已结束或状态无法核实的种子未能删除，请立即检查下载器")
		}
	}
	if e = p.archive(ctx, t, ts); e != nil {
		return e
	}
	cleanupTorrents := torrents
	if deleted {
		cleanupTorrents = nil
	}
	if e = p.CleanupTaskTag(ctx, t, cleanupTorrents); e != nil {
		return e
	}
	if e = p.put(ctx, t.ID(), "torrents", ts); e != nil {
		return e
	}
	if _, e = p.recalculate(ctx, t.ID()); e != nil {
		return e
	}
	active := 0
	for _, v := range ts.Values {
		if !truth(v["deleted"]) {
			active++
		}
	}
	n := 0
	if deleted {
		n = len(deletes)
	}
	r["result"] = "completed"
	r["deleted_count"] = n
	r["paused_count"] = len(promotionPause)
	r["active_count"] = active
	return nil
}
func (p *Core) protectPromotion(ctx context.Context, t Task, torrents []DownloadState, ts *RecordSet) ([]string, []string, error) {
	del, pause := []string{}, []string{}
	if !t.B("del_no_free") {
		return del, pause, nil
	}
	for _, d := range torrents {
		hash := torrentHash(d)
		record := ts.Values[hash]
		if record == nil {
			continue
		}
		info := p.torrentInfo(d)
		if complete(info) {
			continue
		}
		expired, why, e := p.promotionExpired(ctx, t, info, record)
		if e != nil {
			return nil, nil, e
		}
		if expired {
			del = append(del, hash)
			if e = p.deleteNotice(ctx, t, record, why); e != nil {
				return nil, nil, e
			}
			p.h.Log(ctx, "info", "刷流任务删除种子："+text(record["title"])+"，原因："+why)
			continue
		}
		verified, last := parseNumber(fallback(record["promotion_verified_at"], 0)), parseNumber(fallback(record["promotion_probe_at"], 0))
		if verified == nil || last == nil {
			verified, last = float64(0), float64(0)
		}
		if !addedFree(record) || number(verified) == 0 || number(last) > number(verified) {
			pause = append(pause, hash)
		}
	}
	stop := unique(append(append([]string{}, del...), pause...))
	stopped := true
	var e error
	if len(stop) > 0 {
		stopped, e = p.h.Pause(ctx, t.S("downloader"), stop)
		if e != nil {
			if boundary(e) {
				return nil, nil, e
			}
			p.h.Log(ctx, "error", "刷流任务促销安全暂停接口异常："+e.Error())
			stopped = false
		}
	}
	if len(stop) > 0 && !stopped {
		p.h.Log(ctx, "error", "刷流任务促销安全暂停失败，改为删除受影响的未完成种子")
		del = stop
		pause = []string{}
	} else {
		for _, h := range pause {
			r := ts.Values[h]
			if !truth(r["promotion_paused_at"]) {
				p.h.Log(ctx, "warning", "刷流任务暂停促销状态无法确认的种子："+text(r["title"])+"，需确认优惠后手动恢复")
				r["promotion_paused_at"] = float64(p.h.Now().UnixNano()) / 1e9
			}
		}
	}
	return del, pause, nil
}
func (p *Core) deleteCondition(ctx context.Context, t Task, info, r Row) (bool, string, error) {
	expired, why, e := p.promotionExpired(ctx, t, info, r)
	if e != nil || expired {
		return expired, why, e
	}
	if truth(r["hit_and_run"]) && (t.N("hr_seed_time") != 0 || t.N("seed_ratio") != 0) {
		if t.N("hr_seed_time") != 0 && number(info["seeding_time"]) >= t.N("hr_seed_time")*3600 {
			return true, "H&R 做种时间达到 " + text(t.Row["hr_seed_time"]) + " 小时", nil
		}
		if t.N("seed_ratio") != 0 && number(info["ratio"]) >= t.N("seed_ratio") {
			return true, "H&R 分享率达到 " + text(t.Row["seed_ratio"]), nil
		}
		return false, "H&R 种子尚未满足删除条件", nil
	}
	checks := []struct {
		key, field string
		factor     float64
		reason     string
		gate       bool
	}{{"seed_time", "seeding_time", 3600, "做种时间达到 %s 小时", true}, {"seed_ratio", "ratio", 1, "分享率达到 %s", true}, {"seed_size", "uploaded", 1024 * 1024 * 1024, "上传量达到 %s GB", true}, {"download_time", "dltime", 3600, "下载耗时达到 %s 小时", !complete(info)}}
	for _, c := range checks {
		if t.N(c.key) != 0 && c.gate && number(info[c.field]) >= t.N(c.key)*c.factor {
			return true, fmt.Sprintf(c.reason, text(t.Row[c.key])), nil
		}
	}
	if t.N("seed_avgspeed") != 0 && number(info["avg_upspeed"]) <= t.N("seed_avgspeed")*1024 && number(info["seeding_time"]) >= 1800 {
		return true, "平均上传速度低于 " + text(t.Row["seed_avgspeed"]) + " KB/s", nil
	}
	if t.N("seed_inactivetime") != 0 && number(info["iatime"]) >= t.N("seed_inactivetime")*60 {
		return true, "未活动时间达到 " + text(t.Row["seed_inactivetime"]) + " 分钟", nil
	}
	return false, "尚未满足删除条件", nil
}
func (p *Core) conditionDeletes(ctx context.Context, t Task, ds []DownloadState, rs *RecordSet, dynamic bool) ([]string, error) {
	out := []string{}
	for _, d := range ds {
		hash := torrentHash(d)
		r := rs.Values[hash]
		if r == nil {
			continue
		}
		ok, why, e := p.deleteCondition(ctx, t, p.torrentInfo(d), r)
		if e != nil {
			return nil, e
		}
		if !ok {
			continue
		}
		out = append(out, hash)
		if dynamic {
			why = "触发动态删除阈值，" + why
		}
		if e = p.deleteNotice(ctx, t, r, why); e != nil {
			return nil, e
		}
		p.h.Log(ctx, "info", "刷流任务删除种子："+text(r["title"])+"，原因："+why)
	}
	return out, nil
}
func (p *Core) dynamicPreDelete(ctx context.Context, t Task, ds []DownloadState, rs *RecordSet) ([]string, error) {
	out := []string{}
	for _, d := range ds {
		hash := torrentHash(d)
		r := rs.Values[hash]
		if r == nil || truth(r["hit_and_run"]) {
			continue
		}
		info := p.torrentInfo(d)
		expired, why, e := p.promotionExpired(ctx, t, info, r)
		if e != nil {
			return nil, e
		}
		timeout := t.N("download_time") != 0 && !complete(info) && number(info["dltime"]) >= t.N("download_time")*3600
		if !expired && !timeout {
			continue
		}
		if timeout && why == "" {
			why = "下载耗时达到 " + text(t.Row["download_time"]) + " 小时"
		}
		out = append(out, hash)
		if e = p.deleteNotice(ctx, t, r, why); e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (p *Core) taskDynamicDelete(ctx context.Context, t Task, ds []DownloadState, rs *RecordSet) ([]string, error) {
	if !t.B("proxy_delete") || t.S("delete_size_range") == "" {
		return []string{}, nil
	}
	infos := map[string]Row{}
	for _, d := range ds {
		infos[torrentHash(d)] = p.torrentInfo(d)
	}
	size := seedingSize(rs)
	pre, e := p.dynamicPreDelete(ctx, t, ds, rs)
	if e != nil {
		return nil, e
	}
	for _, h := range pre {
		size -= number(infos[h]["total_size"])
	}
	remaining := []DownloadState{}
	for _, d := range ds {
		if !contains(pre, torrentHash(d)) {
			remaining = append(remaining, d)
		}
	}
	limits := ranges(t.S("delete_size_range"), 1024*1024*1024)
	min, max := limits[0], limits[0]
	if len(limits) > 1 {
		max = limits[1]
	}
	if size < max {
		return pre, nil
	}
	del := append([]string{}, pre...)
	conditional, e := p.conditionDeletes(ctx, t, remaining, rs, true)
	if e != nil {
		return nil, e
	}
	del = append(del, conditional...)
	for _, h := range conditional {
		size -= number(infos[h]["total_size"])
	}
	if size > min {
		hashes := []string{}
		for _, d := range remaining {
			h := torrentHash(d)
			if !contains(del, h) {
				hashes = append(hashes, h)
			}
		}
		done, e := p.h.Completed(ctx, t.S("downloader"), hashes)
		if e != nil {
			return nil, e
		}
		candidates := []struct {
			hash string
			info Row
		}{}
		for _, d := range done {
			hash := torrentHash(d)
			if truth(rs.Values[hash]["hit_and_run"]) {
				continue
			}
			info := infos[hash]
			if info == nil {
				info = p.torrentInfo(d)
			}
			candidates = append(candidates, struct {
				hash string
				info Row
			}{hash, info})
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			return number(candidates[i].info["seeding_time"]) > number(candidates[j].info["seeding_time"])
		})
		for _, c := range candidates {
			if size <= min {
				break
			}
			del = append(del, c.hash)
			size -= number(c.info["total_size"])
			if e = p.deleteNotice(ctx, t, rs.Values[c.hash], "触发动态删除阈值，系统按做种时间清理"); e != nil {
				return nil, e
			}
		}
	}
	if len(limits) > 1 && len(del) > 0 && t.B("notify") {
		e = p.h.Message(ctx, Notice{Title: "【刷流任务动态删除】", Text: fmt.Sprintf("任务：%s\n删除：%d 个种子\n当前做种：%.1f GB", t.Name(), len(del), BytesToGB(size))})
		if e != nil {
			return nil, e
		}
	}
	return del, nil
}
func (p *Core) archive(ctx context.Context, t Task, rs *RecordSet) error {
	if t.N("auto_archive_days") <= 0 {
		return nil
	}
	arch, e := p.records(ctx, t.ID(), "archived")
	if e != nil {
		return e
	}
	now := float64(p.h.Now().UnixNano()) / 1e9
	for _, h := range append([]string{}, rs.Order...) {
		r := rs.Values[h]
		if !truth(r["deleted"]) {
			continue
		}
		v := r["deleted_time"]
		numeric := false
		switch v.(type) {
		case int, int64, float64, json.Number:
			numeric = true
		}
		if v == nil || numeric && now-number(v) > t.N("auto_archive_days")*86400 {
			arch.Set(h, rs.Del(h))
		}
	}
	return p.put(ctx, t.ID(), "archived", arch)
}
func (p *Core) CleanupTaskTag(ctx context.Context, t Task, ds []DownloadState) error {
	if t.S("downloader") == "" {
		return nil
	}
	services, e := p.h.Downloaders(ctx)
	if e != nil {
		return e
	}
	kind := ""
	for _, d := range services {
		if d.Name == t.S("downloader") {
			kind = d.Kind
		}
	}
	if kind != "qbittorrent" {
		return nil
	}
	if ds == nil {
		ds, e = p.h.Torrents(ctx, t.S("downloader"))
		if e != nil {
			p.h.Log(ctx, "warning", fmt.Sprintf("清理刷流任务 [%s] 标签时获取下载器种子失败", t.Name()))
			return nil
		}
	}
	for _, d := range ds {
		if contains(torrentLabels(d), t.Tag()) {
			return nil
		}
	}
	ok, e := p.h.DeleteTags(ctx, t.S("downloader"), []string{t.Tag()})
	if e != nil {
		if boundary(e) {
			return e
		}
		p.h.Log(ctx, "warning", fmt.Sprintf("清理刷流任务 [%s] 标签失败：%s", t.Name(), e))
		return nil
	}
	if ok {
		p.h.Log(ctx, "info", fmt.Sprintf("清理刷流任务 [%s] 未使用标签：%s", t.Name(), t.Tag()))
	}
	return nil
}
func (p *Core) CleanupTags(ctx context.Context) error {
	services, e := p.h.Downloaders(ctx)
	if e != nil {
		return e
	}
	for _, d := range services {
		if d.Kind != "qbittorrent" {
			continue
		}
		tags, e := p.h.Tags(ctx, d.Name)
		if e != nil {
			if boundary(e) {
				return e
			}
			p.h.Log(ctx, "warning", "扫描清理历史刷流标签失败："+e.Error())
			return nil
		}
		candidates := []string{}
		for _, tag := range tags {
			tag = strings.TrimSpace(tag)
			if strings.HasPrefix(tag, "刷流-") {
				candidates = append(candidates, tag)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		ts, e := p.h.Torrents(ctx, d.Name)
		if e != nil {
			p.h.Log(ctx, "warning", fmt.Sprintf("扫描下载器 [%s] 刷流标签时获取种子失败", d.Name))
			continue
		}
		used := []string{}
		for _, t := range ts {
			used = append(used, torrentLabels(t)...)
		}
		unused := []string{}
		for _, tag := range candidates {
			if !contains(used, tag) {
				unused = append(unused, tag)
			}
		}
		if len(unused) > 0 {
			ok, e := p.h.DeleteTags(ctx, d.Name, unused)
			if e != nil {
				if boundary(e) {
					return e
				}
				p.h.Log(ctx, "warning", "扫描清理历史刷流标签失败："+e.Error())
				return nil
			}
			if ok {
				p.h.Log(ctx, "info", fmt.Sprintf("清理下载器 [%s] 未使用刷流标签：%s", d.Name, strings.Join(unused, ",")))
			}
		}
	}
	return nil
}
