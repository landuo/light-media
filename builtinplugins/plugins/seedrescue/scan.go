package seedrescue

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ScanNow reserves the scan before submitting it. The manager gives the
// callback a fresh host invocation so the HTTP request can finish first.
func (m *Machine) ScanNow(ctx context.Context, id uint64, submit func() error) (Response, error) {
	valid, e := m.ValidConfig(ctx, false)
	if e != nil {
		return Response{}, e
	}
	if !valid {
		return rejected("插件配置不完整或下载器不可用"), nil
	}
	if !m.tryLock() {
		return rejected("稀缺保种任务正在运行"), nil
	}
	if _, e = m.beginScanRun(ctx, id); e != nil {
		return Response{}, e
	}
	if e = submit(); e != nil {
		m.workerMu.Lock()
		defer m.workerMu.Unlock()
		if m.pendingScan == id {
			m.pendingScan = 0
			m.scanActive.Store(false)
			m.unlock()
			close(m.workerDone)
			if err := m.scanUpdate(ctx, Object{"state": "failed", "message": "后台扫描启动失败", "finished_at": m.now()}); err != nil {
				return Response{}, err
			}
		}
		return rejected("后台扫描启动失败"), nil
	}
	r := success(Object{"scan_status": m.ScanStatus()})
	r.Message = ptr("已提交后台扫描")
	return r, nil
}

func (m *Machine) beginScanRun(ctx context.Context, pending uint64) (chan struct{}, error) {
	m.workerMu.Lock()
	defer m.workerMu.Unlock()
	if m.stopped.Load() {
		m.unlock()
		return nil, ErrScanInterrupted
	}
	if e := m.beginScan(ctx); e != nil {
		m.unlock()
		return nil, e
	}
	done := make(chan struct{})
	m.workerDone = done
	m.pendingScan = pending
	m.scanActive.Store(true)
	return done, nil
}

func (m *Machine) RunSubmittedScan(ctx context.Context, id uint64) error {
	m.workerMu.Lock()
	if id == 0 || m.pendingScan != id {
		m.workerMu.Unlock()
		return nil
	}
	m.pendingScan = 0
	done := m.workerDone
	m.workerMu.Unlock()
	defer close(done)
	return m.runScan(ctx)
}

// Scan is the synchronous cron/once consumer. A concurrent scan is skipped.
func (m *Machine) Scan(ctx context.Context) error {
	if !m.tryLock() {
		m.host.Log(ctx, "info", "稀缺保种任务仍在运行，本轮跳过")
		return nil
	}
	done, e := m.beginScanRun(ctx, 0)
	if e != nil {
		return e
	}
	defer close(done)
	return m.runScan(ctx)
}
func (m *Machine) runScan(ctx context.Context) (err error) {
	var jobs *jobsSet
	defer func() {
		if jobs != nil {
			if e := m.saveJobs(ctx, jobs); e != nil {
				m.host.Log(ctx, "error", "稀缺保种任务保存失败："+ErrorClass(e))
				_ = m.scanUpdate(ctx, Object{"state": "failed", "message": "任务保存失败，请检查存储后重试扫描"})
				if err == nil {
					err = e
				}
			}
		}
		if e := m.scanUpdate(ctx, Object{"finished_at": m.now()}); e != nil && err == nil {
			err = e
		}
		m.scanJobs = nil
		m.scanActive.Store(false)
		m.unlock()
	}()
	operation := func() error {
		valid, e := m.ValidConfig(ctx, true)
		if e != nil {
			return e
		}
		if !valid {
			return m.scanUpdate(ctx, Object{"state": "failed", "message": "插件配置不完整或下载器不可用"})
		}
		jobs, e = m.loadJobs(ctx)
		if e != nil {
			return e
		}
		m.scanJobs = jobs
		if e = m.checkStopped(ctx); e != nil {
			return e
		}
		if e = m.scanStage(ctx, "reconcile", 1); e != nil {
			return e
		}
		if e = m.reconcileJobs(ctx, jobs); e != nil {
			return e
		}
		if e = m.scanAdvance(ctx); e != nil {
			return e
		}
		if e = m.checkpoint(ctx); e != nil {
			return e
		}
		if e = m.scanStage(ctx, "recovery", 0); e != nil {
			return e
		}
		if e = m.resumeWaitingJobs(ctx, jobs); e != nil {
			return e
		}
		if e = m.checkpoint(ctx); e != nil {
			return e
		}
		conf := m.configuration()
		site, e := m.host.Site(ctx, conf.TargetSiteID)
		if e != nil {
			return e
		}
		if len(site) == 0 {
			return m.scanUpdate(ctx, Object{"state": "failed", "message": "稀缺保种目标站点不存在"})
		}
		byFeed, byID := map[string]Object{}, map[string]Object{}
		for _, j := range jobs.values() {
			if asInt(j["target_site_id"], 0) != conf.TargetSiteID {
				continue
			}
			if truth(j["feed_key"]) {
				byFeed[pyString(j["feed_key"])] = j
			}
			if truth(j["target_torrent_id"]) {
				byID[pyString(j["target_torrent_id"])] = j
			}
		}
		keys := m.rssSourceKeys()
		old := Object{}
		if e = m.load(ctx, RSSSnapshotsKey, &old); e != nil {
			return e
		}
		snapshots := Object{}
		for _, k := range keys {
			if v, ok := old[k]; ok {
				snapshots[k] = v
			}
		}
		scanAt := m.now()
		newJobs := []Object{}
		failedFeeds := 0
		if e = m.scanStage(ctx, "rss", len(conf.RSSURLs)); e != nil {
			return e
		}
		for i, rssURL := range conf.RSSURLs {
			if e = m.checkStopped(ctx); e != nil {
				return e
			}
			items, ok, fetchErr := m.host.RSS(ctx, rssURL, site)
			if fetchErr != nil || !ok {
				failedFeeds++
				if e = m.scanIssue(ctx, "RSS 拉取失败，保留上次成功记录"); e != nil {
					return e
				}
				previous, _ := snapshots[keys[i]].(map[string]any)
				previous = clone(previous)
				if previous == nil {
					previous = Object{}
				}
				previous["last_attempt_at"] = scanAt
				previous["error"] = "RSS 拉取失败，保留上次成功记录"
				snapshots[keys[i]] = previous
				m.host.Log(ctx, "warning", "稀缺保种 RSS 获取失败，保留上次成功记录")
				if e = m.scanAdvance(ctx); e != nil {
					return e
				}
				continue
			}
			itemKeys, ids := []string{}, []string{}
			for _, item := range items {
				itemKeys = append(itemKeys, itemFeedKey(item))
				if tid := itemTorrentID(item); tid != "" {
					ids = append(ids, tid)
				}
			}
			snapshots[keys[i]] = Object{"last_attempt_at": scanAt, "last_success_at": scanAt, "error": nil, "item_keys": itemKeys, "torrent_ids": ids}
			count := 0
			for _, item := range items {
				if e = m.checkStopped(ctx); e != nil {
					return e
				}
				key, tid := itemFeedKey(item), itemTorrentID(item)
				existing := byFeed[key]
				if len(existing) == 0 {
					existing = byID[tid]
				}
				if len(existing) > 0 {
					m.setTargetIdentity(existing, item, site)
					existing["feed_key"] = key
					byFeed[key] = existing
					continue
				}
				if conf.InitialLimit != nil && count >= *conf.InitialLimit {
					continue
				}
				count++
				job, e := m.createJob(ctx, item, key, site)
				if e != nil {
					return e
				}
				if len(job) == 0 {
					if e = m.scanIssue(ctx, "部分 RSS 种子元数据获取失败，将在后续扫描重试"); e != nil {
						return e
					}
					continue
				}
				hash := pyString(job["target_hash"])
				if existing := jobs.items[hash]; len(existing) > 0 {
					m.setTargetIdentity(existing, item, site)
					job = existing
				} else {
					jobs.set(hash, job)
					newJobs = append(newJobs, job)
				}
				byFeed[key] = job
				job["feed_key"] = key
				if truth(job["target_torrent_id"]) {
					byID[pyString(job["target_torrent_id"])] = job
				}
				if e = m.checkpoint(ctx); e != nil {
					return e
				}
			}
			if e = m.scanAdvance(ctx); e != nil {
				return e
			}
		}
		if e = m.checkStopped(ctx); e != nil {
			return e
		}
		m.updateRSSPresence(jobs, snapshots)
		targetJobs := m.selectTargetRefreshJobs(jobs)
		if e = m.save(ctx, RSSSnapshotsKey, snapshots); e != nil {
			return e
		}
		if e = m.saveJobs(ctx, jobs); e != nil {
			return e
		}
		if e = m.checkStopped(ctx); e != nil {
			return e
		}
		if e = m.scanStage(ctx, "matching", len(newJobs)); e != nil {
			return e
		}
		if e = m.findAndDownloadJobs(ctx, newJobs); e != nil {
			return e
		}
		if e = m.checkpoint(ctx); e != nil {
			return e
		}
		if e = m.checkStopped(ctx); e != nil {
			return e
		}
		if e = m.scanStage(ctx, "target_stats", len(targetJobs)); e != nil {
			return e
		}
		if e = m.refreshTargetJobs(ctx, targetJobs, site); e != nil {
			return e
		}
		if e = m.saveJobs(ctx, jobs); e != nil {
			return e
		}
		if e = m.checkStopped(ctx); e != nil {
			return e
		}
		result, message := "success", "扫描完成"
		if failedFeeds == len(conf.RSSURLs) {
			result, message = "failed", "全部 RSS 拉取失败，已保留上次成功记录"
		} else if truth(m.ScanStatus()["errors"]) {
			result, message = "partial", "扫描完成，部分请求失败，详见提示及任务重试时间"
		}
		return m.scanUpdate(ctx, Object{"state": result, "message": message})
	}
	if e := operation(); e != nil {
		err = e
		if errors.Is(e, ErrScanInterrupted) {
			_ = m.scanUpdate(ctx, Object{"state": "interrupted", "message": "扫描已中断，未完成的匹配将在下次扫描恢复"})
		} else {
			m.host.Log(ctx, "error", "稀缺保种扫描异常："+ErrorClass(e))
			_ = m.scanUpdate(ctx, Object{"state": "failed", "message": "扫描异常，已完成的阶段将保留；未完成的匹配可重试"})
		}
	}
	return err
}
func itemFeedKey(item Object) string {
	if key, ok := item["source_feed_key"].(string); ok && key != "" {
		return key
	}
	raw := item["enclosure"]
	if !truth(raw) {
		raw = item["link"]
	}
	if !truth(raw) {
		raw = item["title"]
	}
	return FeedKey(pyString(raw), "", "")
}
func itemTorrentID(item Object) string {
	if id, ok := item["source_torrent_id"].(string); ok && id != "" {
		return id
	}
	return ExtractTorrentID(strOr(item["link"], strOr(item["enclosure"], "")))
}
func (m *Machine) setTargetIdentity(job, item, site Object) {
	job["target_site_name"] = strOr(site["name"], fmt.Sprint(m.configuration().TargetSiteID))
	job["target_torrent_id"] = nullable(itemTorrentID(item))
	job["target_page_url"] = nullable(SafePageURL(strings.TrimSpace(strOr(item["link"], ""))))
	job["target_size"] = item["size"]
}
func (m *Machine) createJob(ctx context.Context, item Object, key string, site Object) (Object, error) {
	enclosure := strings.TrimSpace(strOr(item["enclosure"], ""))
	if enclosure == "" {
		m.host.Log(ctx, "warning", "RSS 种子缺少下载链接："+strOr(item["title"], "未知标题"))
		return nil, nil
	}
	runtime := Object{"enclosure": enclosure, "site_cookie": site["cookie"], "site_ua": site["ua"], "site_proxy": truth(site["proxy"])}
	content, msg, e := m.host.DownloadMetadata(ctx, runtime)
	if e != nil {
		return nil, e
	}
	if content == nil {
		m.host.Log(ctx, "warning", "目标种子元数据下载失败："+pyString(item["title"])+"，"+strOr(msg, "未知错误"))
		return nil, nil
	}
	hash, e := InfoHashFromTorrent(content)
	if e != nil {
		m.host.Log(ctx, "warning", "目标种子元数据无效："+pyString(item["title"])+"，"+e.Error())
		return nil, nil
	}
	job := Object{"feed_key": key, "target_hash": hash, "target_site_id": m.configuration().TargetSiteID, "title": strOr(item["title"], hash), "state": "MATCHING", "created_at": m.now(), "updated_at": m.now(), "source_hash": nil, "source_site_id": nil, "source_site_name": nil, "source_seeders": nil, "promotion": nil, "reason": nil, "probes": []Object{}, "candidates": []Object{}, "selected_candidate_id": nil}
	m.setTargetIdentity(job, item, site)
	return job, nil
}
func (m *Machine) updateRSSPresence(jobs *jobsSet, snapshots Object) {
	active := []Object{}
	complete := true
	for _, k := range m.rssSourceKeys() {
		s, _ := snapshots[k].(map[string]any)
		active = append(active, s)
		if !truth(s["last_success_at"]) {
			complete = false
		}
	}
	complete = complete && len(active) > 0
	items, ids := map[string]string{}, map[string]string{}
	for _, s := range active {
		seen := strOr(s["last_success_at"], "")
		if seen == "" {
			continue
		}
		for _, x := range array(s["item_keys"]) {
			key := pyString(x)
			if seen > items[key] {
				items[key] = seen
			}
		}
		for _, x := range array(s["torrent_ids"]) {
			key := pyString(x)
			if seen > ids[key] {
				ids[key] = seen
			}
		}
	}
	now := m.now()
	for _, j := range jobs.values() {
		same := asInt(j["target_site_id"], 0) == m.configuration().TargetSiteID
		seen := ""
		if same {
			seen = items[strOr(j["feed_key"], "")]
			if x := ids[strOr(j["target_torrent_id"], "")]; x > seen {
				seen = x
			}
		}
		if seen != "" {
			j["rss_current"] = true
			j["rss_last_seen_at"] = seen
			j["rss_archived_at"] = nil
		} else if complete || !same {
			if j["rss_current"] != false {
				j["rss_archived_at"] = now
			}
			j["rss_current"] = false
		}
	}
}
func (m *Machine) selectTargetRefreshJobs(jobs *jobsSet) []Object {
	out := []Object{}
	now := m.host.Now()
	conf := m.configuration()
	for _, j := range jobs.values() {
		if asInt(j["target_site_id"], 0) != conf.TargetSiteID {
			continue
		}
		if !truth(j["target_torrent_id"]) {
			tid := ExtractTorrentID(strOr(j["target_page_url"], ""))
			if tid == "" {
				continue
			}
			j["target_torrent_id"] = tid
		}
		missing := PromotionKind(j["target_download_factor"]) == Unknown
		last := j["target_stats_checked_at"]
		if !truth(last) && !missing {
			last = j["target_stats_updated_at"]
		}
		if truth(last) {
			if t, e := parseTime(last); e == nil {
				interval := 30 * time.Minute
				if truth(j["target_stats_error"]) {
					interval = time.Hour
				}
				if now.Sub(t) < interval {
					continue
				}
			}
		}
		out = append(out, j)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		first := func(j Object) bool {
			return PromotionKind(j["target_download_factor"]) == Unknown && !truth(j["target_stats_checked_at"])
		}
		if first(a) != first(b) {
			return first(a)
		}
		at, bt := strOr(a["target_stats_checked_at"], strOr(a["target_stats_updated_at"], "")), strOr(b["target_stats_checked_at"], strOr(b["target_stats_updated_at"], ""))
		if at != bt {
			return at < bt
		}
		ac, bc := a["rss_current"] == true, b["rss_current"] == true
		if ac != bc {
			return ac
		}
		return pyString(a["target_hash"]) < pyString(b["target_hash"])
	})
	if conf.InitialLimit != nil && len(out) > *conf.InitialLimit {
		out = out[:*conf.InitialLimit]
	}
	return out
}
func (m *Machine) refreshTargetJob(ctx context.Context, j Object, domain string) (Object, error) {
	tid := strOr(j["target_torrent_id"], "")
	j["target_stats_checked_at"] = m.now()
	j["target_stats_error"] = "原站信息查询失败，稍后重试"
	if tid == "" {
		j["target_stats_error"] = "旧记录缺少原站种子 ID，等待 RSS 再次发现"
		return nil, nil
	}
	var torrent Object
	for _, kw := range SearchKeywords(strOr(j["title"], "")) {
		if e := m.checkStopped(ctx); e != nil {
			return nil, e
		}
		rows, e := m.host.Search(ctx, domain, kw, 0)
		if e != nil {
			return nil, e
		}
		for _, r := range rows {
			if ExtractTorrentID(strOr(r["page_url"], strOr(r["enclosure"], ""))) == tid {
				torrent = r
				break
			}
		}
		if len(torrent) > 0 {
			break
		}
	}
	if len(torrent) == 0 {
		j["target_stats_error"] = "未在原站搜索结果中找到相同种子 ID，稍后重试"
		return nil, nil
	}
	if PromotionKind(j["target_download_factor"]) != Unknown && !truth(j["target_promotion_updated_at"]) {
		j["target_promotion_updated_at"] = j["target_stats_updated_at"]
	}
	if page := SafePageURL(strOr(torrent["page_url"], "")); page != "" {
		j["target_page_url"] = page
	}
	j["target_seeders"] = torrent["seeders"]
	j["target_peers"] = torrent["peers"]
	j["target_grabs"] = torrent["grabs"]
	if truth(torrent["size"]) {
		j["target_size"] = torrent["size"]
	}
	j["target_stats_updated_at"] = m.now()
	factor := torrent["downloadvolumefactor"]
	if PromotionKind(factor) != Unknown {
		j["target_download_factor"] = factor
		j["target_promotion_updated_at"] = m.now()
		j["target_stats_error"] = nil
	} else {
		j["target_stats_error"] = "原站搜索结果未提供有效促销信息，稍后重试"
	}
	return torrent, nil
}
func (m *Machine) refreshTargetJobs(ctx context.Context, jobs []Object, site Object) error {
	domain := strOr(site["domain"], sourceDomain(strOr(site["url"], "")))
	if domain == "" {
		return nil
	}
	type result struct {
		job Object
		err error
	}
	out := make(chan result, len(jobs))
	slots := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for _, j := range jobs {
		if !truth(j["target_torrent_id"]) {
			continue
		}
		wg.Add(1)
		go func(j Object) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			_, e := m.refreshTargetJob(ctx, j, domain)
			out <- result{j, e}
		}(j)
	}
	go func() { wg.Wait(); close(out) }()
	var first error
	for r := range out {
		if errors.Is(r.err, ErrScanInterrupted) {
			if first == nil {
				first = r.err
			}
			continue
		}
		if r.err != nil {
			r.job["target_stats_error"] = "原站信息查询失败，稍后重试"
			m.host.Log(ctx, "warning", "目标站数据刷新失败："+pyString(r.job["title"]))
		}
		if truth(r.job["target_stats_error"]) {
			if e := m.scanIssue(ctx, "部分原站信息查询失败，已保留上次有效数据"); e != nil && first == nil {
				first = e
			}
		}
		if e := m.scanAdvance(ctx); e != nil && first == nil {
			first = e
		}
	}
	return first
}
func (m *Machine) targetSource(ctx context.Context, job Object) (*Candidate, string, error) {
	var t Object
	existing, e := m.getTorrent(ctx, pyString(job["target_hash"]))
	if e != nil {
		return nil, "", e
	}
	if len(existing) == 0 {
		site, e := m.host.Site(ctx, asInt(job["target_site_id"], 0))
		if e != nil {
			return nil, "", e
		}
		if len(site) == 0 {
			return nil, "保种站点不存在，请检查站点配置", nil
		}
		domain := strOr(site["domain"], sourceDomain(strOr(site["url"], "")))
		if domain != "" && truth(job["target_torrent_id"]) {
			t, e = m.refreshTargetJob(ctx, job, domain)
			if e != nil {
				m.host.Log(ctx, "warning", "原种站点搜索失败，尝试从目标 RSS 获取")
			}
		}
		if !truth(t["enclosure"]) && asInt(job["target_site_id"], 0) == m.configuration().TargetSiteID {
			for _, rss := range m.configuration().RSSURLs {
				rows, ok, e := m.host.RSS(ctx, rss, site)
				if e != nil {
					return nil, "", e
				}
				if !ok {
					continue
				}
				for _, item := range rows {
					sameID := truth(job["target_torrent_id"]) && itemTorrentID(item) == pyString(job["target_torrent_id"])
					sameFeed := truth(job["feed_key"]) && job["feed_key"] == itemFeedKey(item)
					if truth(item["enclosure"]) && (sameID || sameFeed) {
						m.setTargetIdentity(job, item, site)
						t = Object{"enclosure": item["enclosure"], "title": job["title"], "site_name": job["target_site_name"], "site_cookie": site["cookie"], "site_ua": site["ua"], "site_proxy": truth(site["proxy"])}
						break
					}
				}
				if truth(t["enclosure"]) {
					break
				}
			}
		}
		if !truth(t["enclosure"]) {
			return nil, "未找到保种站点原种的下载链接，请重新扫描 RSS 或检查站点登录状态", nil
		}
	}
	return &Candidate{Match: Match{nil, strOr(job["target_torrent_id"], ""), pyString(job["target_hash"])}, SiteID: asInt(job["target_site_id"], 0), SiteName: strOr(job["target_site_name"], "保种站点"), Seeders: intPtr(job["target_seeders"]), DownloadFactor: floatPtr(t["downloadvolumefactor"]), Priority: 0, Torrent: t, MetadataStatus: "ready"}, "", nil
}
