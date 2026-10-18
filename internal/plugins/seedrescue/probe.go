package seedrescue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
)

func sourceDomain(base string) string {
	u, e := url.Parse(base)
	if e != nil {
		return ""
	}
	return u.Hostname()
}
func (m *Machine) candidateExclusion(c Candidate) *string {
	reasons := []string{}
	conf := m.configuration()
	if c.MetadataStatus != "ready" {
		reasons = append(reasons, strOr(deref(c.MetadataError), "IYUU 已收录，详情待获取"))
	}
	if conf.MinimumSeeders != nil {
		if c.Seeders == nil {
			reasons = append(reasons, fmt.Sprintf("做种人数未知，至少需要 %d 人", *conf.MinimumSeeders))
		} else if *c.Seeders < *conf.MinimumSeeders {
			reasons = append(reasons, fmt.Sprintf("做种 %d 人，少于最低 %d 人", *c.Seeders, *conf.MinimumSeeders))
		}
	}
	kind := PromotionKind(deref(c.DownloadFactor))
	if !contains(conf.AllowedPromotions, kind) {
		reasons = append(reasons, promotionLabel(kind)+"，未通过促销规则")
	}
	if len(reasons) == 0 {
		return nil
	}
	return ptr(strings.Join(reasons, "；"))
}
func promotionLabel(kind string) string {
	if kind == Free {
		return "免费"
	}
	if kind == NonFree {
		return "非免费"
	}
	if kind == Unknown {
		return "促销未知"
	}
	return kind
}
func (m *Machine) rejectionReason(cs []Candidate, probes []Object) string {
	if len(cs) == 0 {
		n := 0
		limited := false
		for _, p := range probes {
			if !truth(p["matched"]) {
				n++
			}
			limited = limited || truth(p["search_limited"])
		}
		if n > 0 {
			s := fmt.Sprintf("IYUU 返回了 %d 个来源，但已查搜索范围内未找到对应种子 ID", n)
			if limited {
				s += "（已达分页上限，不代表站点没有资源）"
			}
			return s
		}
		return "IYUU 返回的来源不在已选择的来源站点中"
	}
	conf := m.configuration()
	below := 0
	counts := map[string]int{}
	order := []string{}
	for _, c := range cs {
		if conf.MinimumSeeders != nil && (c.Seeders == nil || *c.Seeders < *conf.MinimumSeeders) {
			below++
		}
		kind := PromotionKind(deref(c.DownloadFactor))
		if !contains(conf.AllowedPromotions, kind) {
			if counts[kind] == 0 {
				order = append(order, kind)
			}
			counts[kind]++
		}
	}
	details := []string{}
	if below > 0 {
		details = append(details, fmt.Sprintf("%d 个未达到最少 %d 人做种", below, *conf.MinimumSeeders))
	}
	if len(counts) > 0 {
		items := []string{}
		allowed := []string{}
		for _, k := range order {
			items = append(items, fmt.Sprintf("%d 个%s", counts[k], promotionLabel(k)))
		}
		for _, k := range []string{Free, NonFree} {
			if contains(conf.AllowedPromotions, k) {
				allowed = append(allowed, promotionLabel(k))
			}
		}
		a := strings.Join(allowed, "、")
		if a == "" {
			a = "无"
		}
		details = append(details, strings.Join(items, "、")+"不在允许范围内（当前允许："+a+"）")
	}
	if len(details) == 0 {
		details = append(details, "均未通过筛选")
	}
	return fmt.Sprintf("已精确匹配 %d 个同源来源，但%s", len(cs), strings.Join(details, "；"))
}
func (m *Machine) applyCandidates(ctx context.Context, job Object, cs []Candidate, probes []Object) error {
	job["probes"] = probes
	conf := m.configuration()
	ranked := RankedSources(cs, conf.MinimumSeeders, conf.AllowedPromotions)
	eligible := map[string]bool{}
	for _, c := range ranked {
		eligible[candidateID(c)] = true
	}
	all := append([]Candidate{}, ranked...)
	for _, c := range cs {
		if !eligible[candidateID(c)] {
			all = append(all, c)
		}
	}
	records := []Object{}
	for _, c := range all {
		r := CandidateRecord(c)
		r["eligible"] = eligible[candidateID(c)]
		r["exclusion_reason"] = deref(m.candidateExclusion(c))
		records = append(records, r)
	}
	job["candidates"] = records
	for _, p := range probes {
		if truth(p["error"]) {
			if len(ranked) == 0 {
				return m.deferRetry(ctx, job, "部分站点查询失败且暂无符合条件的来源，稍后重试", "")
			}
			if e := m.scanIssue(ctx, "部分来源站查询失败，已保留其他站点的可用候选"); e != nil {
				return e
			}
			break
		}
	}
	job["retry_count"] = 0
	job["next_retry_at"] = nil
	if len(ranked) == 0 {
		return m.finish(ctx, job, "SKIPPED_NO_HEALTHY_SOURCE", ptr(m.rejectionReason(cs, probes)))
	}
	if conf.SelectionMode == "manual" || conf.SelectionMode == "hybrid" && len(ranked) > 1 {
		return m.finish(ctx, job, "AWAITING_SELECTION", nil)
	}
	full, e := m.atCapacity(ctx)
	if e != nil {
		return e
	}
	if full {
		return m.finish(ctx, job, "WAITING_CAPACITY", ptr("达到稀缺保种同时下载上限"))
	}
	failures := []string{}
	for _, c := range ranked {
		ok, reason, e := m.startSource(ctx, c, job)
		if e != nil {
			return e
		}
		if ok {
			return nil
		}
		failures = append(failures, c.SiteName+": "+reason)
	}
	reason := strings.Join(failures, "；")
	if reason == "" {
		reason = "来源种子下载失败"
	}
	if e = m.finish(ctx, job, "FAILED_SOURCE_DOWNLOAD", &reason); e != nil {
		return e
	}
	return m.scanIssue(ctx, "部分来源下载失败，请在任务详情检查")
}
func (m *Machine) matchesForJob(ctx context.Context, job Object, payload any, message string) ([]Match, error) {
	if payload == nil && !(strings.Contains(message, "状态码：404") && strings.Contains(message, "未查询到可辅种数据")) {
		return nil, m.deferRetry(ctx, job, "IYUU 请求失败，稍后重试", "WAITING_IYUU_RETRY")
	}
	out := []Match{}
	for _, v := range MatchesForHash(payload, pyString(job["target_hash"])) {
		if v.InfoHash != job["target_hash"] {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		if message == "" {
			message = "IYUU没有返回其他站点同源种子"
		}
		return nil, m.finish(ctx, job, "SKIPPED_NO_IYUU_MATCH", &message)
	}
	return out, nil
}
func (m *Machine) matchingError(ctx context.Context, job Object, err error, reason string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrScanInterrupted) || ctx.Err() != nil {
		return err
	}
	if errors.Is(err, ErrDownloadSubmissionUncertain) || job["state"] == "SOURCE_SUBMITTING" {
		return m.scanIssue(ctx, "部分下载提交结果待确认，未自动重试添加")
	}
	return m.deferRetry(ctx, job, reason, "")
}
func (m *Machine) findAndDownload(ctx context.Context, job Object) (err error) {
	defer func() {
		if e := m.checkpoint(ctx); e != nil && err == nil {
			err = e
		}
		if e := m.scanAdvance(ctx); e != nil && err == nil {
			err = e
		}
	}()
	operation := func() error {
		if e := m.checkStopped(ctx); e != nil {
			return e
		}
		payload, msg, e := m.host.SeedInfo(ctx, []string{pyString(job["target_hash"])})
		if e != nil {
			return e
		}
		matches, e := m.matchesForJob(ctx, job, payload, msg)
		if e != nil || matches == nil {
			return e
		}
		cs, probes, e := m.probeSources(ctx, matches, pyString(job["title"]))
		if e != nil {
			return e
		}
		if e = m.checkStopped(ctx); e != nil {
			return e
		}
		return m.applyCandidates(ctx, job, cs, probes)
	}
	return m.matchingError(ctx, job, operation(), "同源匹配或站点查询异常，稍后重试")
}
func (m *Machine) findAndDownloadJobs(ctx context.Context, jobs []Object) error {
	if len(jobs) == 0 {
		return nil
	}
	if len(jobs) == 1 {
		return m.findAndDownload(ctx, jobs[0])
	}
	if e := m.checkStopped(ctx); e != nil {
		return e
	}
	hashes := []string{}
	for _, j := range jobs {
		hashes = append(hashes, pyString(j["target_hash"]))
	}
	payload, msg, e := m.host.SeedInfo(ctx, hashes)
	if e != nil {
		for _, j := range jobs {
			if e = m.deferRetry(ctx, j, "IYUU 请求异常，稍后重试", "WAITING_IYUU_RETRY"); e != nil {
				return e
			}
			if e = m.scanAdvance(ctx); e != nil {
				return e
			}
		}
		return m.checkpoint(ctx)
	}
	type task struct {
		job     Object
		matches []Match
	}
	pending := []task{}
	for _, j := range jobs {
		ms, e := m.matchesForJob(ctx, j, payload, msg)
		if e != nil {
			return e
		}
		if ms != nil {
			pending = append(pending, task{j, ms})
		} else if e = m.scanAdvance(ctx); e != nil {
			return e
		}
	}
	if e = m.checkpoint(ctx); e != nil {
		return e
	}
	type result struct {
		job    Object
		cs     []Candidate
		probes []Object
		err    error
	}
	out := make(chan result, len(pending))
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, t := range pending {
		wg.Add(1)
		go func(t task) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			cs, p, e := m.probeSources(ctx, t.matches, pyString(t.job["title"]))
			out <- result{t.job, cs, p, e}
		}(t)
	}
	go func() { wg.Wait(); close(out) }()
	var first error
	for r := range out {
		if first != nil {
			continue
		}
		e = r.err
		if e == nil {
			e = m.checkStopped(ctx)
		}
		if e == nil {
			e = m.applyCandidates(ctx, r.job, r.cs, r.probes)
		}
		e = m.matchingError(ctx, r.job, e, "来源站查询异常，稍后重试")
		if e == nil {
			e = m.checkpoint(ctx)
		}
		if e == nil {
			e = m.scanAdvance(ctx)
		}
		if e != nil {
			first = e
		}
	}
	return first
}

type probeEntry struct {
	index int
	match Match
	site  Object
}
type probeResult struct {
	index     int
	candidate Candidate
	probe     Object
}

func (m *Machine) pendingCandidate(match Match, site Object, title string, priorities map[int]int, err *string) Candidate {
	return Candidate{Match: match, SiteID: asInt(site["id"], 0), SiteName: strOr(site["name"], "来源站"), Priority: priorities[asInt(site["id"], 0)], Torrent: Object{"title": title, "page_url": nullable(DetailURL(site, match.TorrentID))}, MetadataStatus: "unavailable", MetadataError: err}
}
func (m *Machine) siteGate(domain string) chan struct{} {
	m.sitesMu.Lock()
	defer m.sitesMu.Unlock()
	g := m.siteGates[domain]
	if g == nil {
		g = make(chan struct{}, 1)
		m.siteGates[domain] = g
	}
	return g
}
func (m *Machine) probeSources(ctx context.Context, matches []Match, title string) ([]Candidate, []Object, error) {
	conf := m.configuration()
	priorities := map[int]int{}
	for i, id := range conf.SourceSiteIDs {
		priorities[id] = i
	}
	groups := map[string][]probeEntry{}
	domains := []string{}
	for i, match := range matches {
		base, page, e := m.host.TorrentURL(ctx, match.SID)
		if e != nil {
			return nil, nil, e
		}
		domain := sourceDomain(base)
		if domain == "" {
			continue
		}
		site, e := m.host.Indexer(ctx, domain)
		if e != nil {
			return nil, nil, e
		}
		if len(site) == 0 {
			continue
		}
		if _, ok := priorities[asInt(site["id"], 0)]; !ok {
			continue
		}
		site = clone(site)
		site["_seedrescue_nexus"] = strings.Contains(page, "download.php?")
		if _, ok := groups[domain]; !ok {
			domains = append(domains, domain)
		}
		groups[domain] = append(groups[domain], probeEntry{i, match, site})
	}
	type done struct {
		domain  string
		results []probeResult
		err     error
	}
	out := make(chan done, len(domains))
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, d := range domains {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			rs, e := m.probeDomain(ctx, d, groups[d], title, priorities)
			out <- done{d, rs, e}
		}(d)
	}
	go func() { wg.Wait(); close(out) }()
	ordered := []probeResult{}
	var first error
	for r := range out {
		if errors.Is(r.err, ErrScanInterrupted) || ctx.Err() != nil {
			if first == nil {
				first = r.err
			}
			continue
		}
		if r.err != nil {
			for _, entry := range groups[r.domain] {
				c := m.pendingCandidate(entry.match, entry.site, title, priorities, ptr("站点查询异常"))
				ordered = append(ordered, probeResult{entry.index, c, Object{"site_id": entry.site["id"], "site_name": entry.site["name"], "matched": true, "metadata_status": "unavailable", "torrent_id": entry.match.TorrentID, "page_url": nullable(DetailURL(entry.site, entry.match.TorrentID)), "error": "站点查询异常", "promotion": Unknown}})
			}
		} else {
			ordered = append(ordered, r.results...)
		}
	}
	if first != nil {
		return nil, nil, first
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].index < ordered[j].index })
	cs := []Candidate{}
	ps := []Object{}
	for _, r := range ordered {
		cs = append(cs, r.candidate)
		ps = append(ps, r.probe)
	}
	return cs, ps, nil
}
func (m *Machine) probeDomain(ctx context.Context, domain string, entries []probeEntry, title string, priorities map[int]int) ([]probeResult, error) {
	gate := m.siteGate(domain)
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-gate }()
	select {
	case m.sourceSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-m.sourceSlots }()
	torrents := map[string]Object{}
	details := map[string]DetailResult{}
	methods := map[string]string{}
	for _, entry := range entries {
		match, site := entry.match, entry.site
		if _, ok := details[match.TorrentID]; ok {
			continue
		}
		if e := m.checkStopped(ctx); e != nil {
			return nil, e
		}
		r, e := m.host.Detail(ctx, domain, site, match.TorrentID)
		if e != nil {
			return nil, e
		}
		details[match.TorrentID] = r
		if len(r.Torrent) > 0 {
			t := clone(r.Torrent)
			t["site"] = site["id"]
			t["site_name"] = site["name"]
			t["site_cookie"] = site["cookie"]
			t["site_ua"] = site["ua"]
			t["site_proxy"] = truth(site["proxy"])
			torrents[match.TorrentID] = t
			methods[match.TorrentID] = "detail"
		}
	}
	keywords := SearchKeywords(title)
	stable := keywords
	if len(keywords) > 1 {
		stable = keywords[:len(keywords)-1]
	}
	type query struct {
		keyword string
		page    int
	}
	queries := []query{}
	for page := 0; page < 3; page++ {
		for _, kw := range stable {
			queries = append(queries, query{kw, page})
		}
	}
	if len(keywords) > 0 && !contains(stable, keywords[len(keywords)-1]) {
		queries = append(queries, query{keywords[len(keywords)-1], 0})
	}
	fallback := map[string]bool{}
	for tid, r := range details {
		if r.Status == "unsupported" || r.Status == "partial" {
			fallback[tid] = true
		}
	}
	found := map[string]bool{}
	exhausted := map[string]bool{}
	seen := map[string]map[string]bool{}
	searched := map[string]int{}
	count := 0
	searchError := ""
	for _, q := range queries {
		if len(found) == len(fallback) {
			break
		}
		if exhausted[q.keyword] {
			continue
		}
		if count >= 8 {
			break
		}
		if e := m.checkStopped(ctx); e != nil {
			return nil, e
		}
		count++
		rows, e := m.host.Search(ctx, domain, q.keyword, q.page)
		if errors.Is(e, ErrScanInterrupted) || ctx.Err() != nil {
			return nil, e
		}
		if e != nil {
			searchError = "站点查询异常"
			break
		}
		searched[q.keyword] = q.page
		fingerprints := []string{}
		for _, row := range rows {
			fingerprints = append(fingerprints, strOr(row["page_url"], strOr(row["enclosure"], "")))
		}
		fingerprints = uniqueStrings(fingerprints)
		sort.Strings(fingerprints)
		b, _ := json.Marshal(fingerprints)
		fingerprint := string(b)
		if seen[q.keyword] == nil {
			seen[q.keyword] = map[string]bool{}
		}
		if len(rows) == 0 || seen[q.keyword][fingerprint] {
			exhausted[q.keyword] = true
			continue
		}
		seen[q.keyword][fingerprint] = true
		for _, row := range rows {
			tid := ExtractTorrentID(strOr(row["page_url"], strOr(row["enclosure"], "")))
			if fallback[tid] && !found[tid] {
				torrents[tid] = row
				methods[tid] = "search"
				found[tid] = true
			}
		}
	}
	limited := count >= 8
	for kw, page := range searched {
		if page >= 2 && !exhausted[kw] {
			limited = true
		}
	}
	results := []probeResult{}
	conf := m.configuration()
	for _, entry := range entries {
		match, site := entry.match, entry.site
		t := torrents[match.TorrentID]
		d := details[match.TorrentID]
		ready := methods[match.TorrentID] == "search" || d.Status == "ready" || d.Status == "partial" && conf.MinimumSeeders == nil && t["downloadvolumefactor"] != nil
		var msg *string
		if !ready {
			s := searchError
			if s == "" {
				if count > 0 {
					s = "详情及兜底搜索未取得对应 ID 的完整信息，可重新匹配重试"
				} else {
					s = strOr(deref(d.Error), "详情获取失败，可重新匹配重试")
				}
			}
			msg = &s
		}
		page := nullable(DetailURL(site, match.TorrentID))
		if len(t) > 0 {
			page = nullable(SafePageURL(strOr(t["page_url"], "")))
		}
		status := "unavailable"
		if ready {
			status = "ready"
		}
		method := nullable(methods[match.TorrentID])
		p := Object{"site_id": site["id"], "site_name": site["name"], "title": t["title"], "page_url": page, "seeders": t["seeders"], "promotion": PromotionKind(t["downloadvolumefactor"]), "matched": true, "metadata_status": status, "lookup_method": method, "searches": count, "torrent_id": match.TorrentID, "search_limited": !ready && limited}
		if msg != nil {
			p["error"] = *msg
		}
		c := m.pendingCandidate(match, site, title, priorities, msg)
		if len(t) > 0 {
			c = Candidate{match, asInt(site["id"], 0), strOr(site["name"], domain), intPtr(t["seeders"]), floatPtr(t["downloadvolumefactor"]), priorities[asInt(site["id"], 0)], t, status, msg, nil}
			if methods[match.TorrentID] != "" {
				c.LookupMethod = ptr(methods[match.TorrentID])
			}
		}
		results = append(results, probeResult{entry.index, c, p})
	}
	return results, nil
}
