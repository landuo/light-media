package seedrescue

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

type StatusFilter struct {
	View                                 string
	Page, PageSize                       int
	State, Promotion, SourcePromotion    string
	SourceSiteID, MinSeeders, MaxSeeders *int
}

func JobSummary(jobs []Object) Object {
	counts := map[string]int{}
	for _, j := range jobs {
		counts[pyString(j["state"])]++
	}
	failed := 0
	for state, n := range counts {
		if strings.HasPrefix(state, "FAILED") {
			failed += n
		}
	}
	return Object{"total": len(jobs), "awaiting_selection": counts["AWAITING_SELECTION"], "downloading": counts["SOURCE_DOWNLOADING"] + counts["TARGET_DOWNLOADING"], "waiting_iyuu": counts["WAITING_IYUU"], "target_seeding": counts["TARGET_SEEDING"], "failed": failed}
}
func CandidateRecord(c Candidate) Object {
	return Object{"candidate_id": candidateID(c), "iyuu_sid": c.Match.SID, "site_id": c.SiteID, "site_name": c.SiteName, "title": c.Torrent["title"], "page_url": nullable(SafePageURL(strOr(c.Torrent["page_url"], ""))), "torrent_id": c.Match.TorrentID, "expected_hash": c.Match.InfoHash, "seeders": deref(c.Seeders), "leechers": c.Torrent["peers"], "grabs": c.Torrent["grabs"], "size": c.Torrent["size"], "download_factor": deref(c.DownloadFactor), "promotion": PromotionKind(deref(c.DownloadFactor)), "priority": c.Priority, "metadata_status": c.MetadataStatus, "metadata_error": deref(c.MetadataError), "lookup_method": deref(c.LookupMethod)}
}
func DisplayCandidates(job Object) []Object {
	out := []Object{}
	seen := map[string]bool{}
	for _, record := range objects(job["candidates"]) {
		c := clone(record)
		out = append(out, c)
		id := strOr(c["torrent_id"], ExtractTorrentID(strOr(c["page_url"], "")))
		seen[pyString(c["site_id"])+":"+id] = true
	}
	for _, p := range objects(job["probes"]) {
		id := strOr(p["torrent_id"], ExtractTorrentID(strOr(p["page_url"], "")))
		key := pyString(p["site_id"]) + ":" + id
		if id == "" || p["site_id"] == nil || seen[key] {
			continue
		}
		seen[key] = true
		status := strOr(p["metadata_status"], "unavailable")
		reason := "旧记录未取得详情，请重新匹配按 ID 查询"
		if truth(p["matched"]) {
			status = strOr(p["metadata_status"], "ready")
			reason = strOr(job["reason"], "旧记录未保存下载资格，请重新匹配确认")
		}
		out = append(out, Object{"candidate_id": "probe:" + key, "site_id": p["site_id"], "site_name": p["site_name"], "title": p["title"], "seeders": p["seeders"], "promotion": p["promotion"], "torrent_id": id, "page_url": nullable(SafePageURL(strOr(p["page_url"], ""))), "eligible": false, "metadata_status": status, "exclusion_reason": reason})
	}
	return out
}

var jobViewFields = []string{"target_hash", "target_site_id", "target_site_name", "target_torrent_id", "target_page_url", "target_seeders", "target_peers", "target_grabs", "target_size", "target_download_factor", "target_stats_updated_at", "target_stats_checked_at", "target_stats_error", "target_promotion_updated_at", "title", "state", "created_at", "updated_at", "rss_current", "rss_last_seen_at", "rss_archived_at", "source_hash", "source_site_id", "source_site_name", "source_seeders", "promotion", "reason", "retry_count", "next_retry_at", "probes", "candidates", "selected_candidate_id"}

func JobView(job Object) Object {
	out := Object{}
	for _, key := range jobViewFields {
		if v, exists := job[key]; exists {
			out[key] = clone(v)
		}
	}
	out["candidates"] = DisplayCandidates(job)
	if truth(job["target_site_id"]) {
		promotion := PromotionKind(job["target_download_factor"])
		updated := job["target_promotion_updated_at"]
		if !truth(updated) && promotion != Unknown {
			updated = job["target_stats_updated_at"]
		}
		note := job["target_stats_error"]
		if !truth(note) && promotion == Unknown {
			note = "待补查原站促销"
			if !truth(job["target_torrent_id"]) {
				note = "旧记录缺少原站种子 ID，等待 RSS 再次发现"
			}
		}
		out["target_candidate"] = Object{"candidate_id": targetCandidateID(job), "is_target": true, "selectable": contains(targetSelectableStates, pyString(job["state"])), "site_id": job["target_site_id"], "site_name": strOr(job["target_site_name"], "保种站点"), "title": job["title"], "page_url": nullable(SafePageURL(strOr(job["target_page_url"], ""))), "seeders": job["target_seeders"], "leechers": job["target_peers"], "grabs": job["target_grabs"], "size": job["target_size"], "download_factor": job["target_download_factor"], "promotion": promotion, "promotion_updated_at": updated, "promotion_note": note}
	}
	return out
}
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (m *Machine) GetJob(ctx context.Context, hash string) (Response, error) {
	jobs, e := m.loadJobs(ctx)
	if e != nil {
		return Response{}, e
	}
	job := jobs.items[strings.ToLower(hash)]
	if len(job) == 0 {
		return rejected("保种任务不存在"), nil
	}
	return success(JobView(job)), nil
}
func (m *Machine) rssSourceKeys() []string {
	c := m.configuration()
	out := []string{}
	for _, url := range c.RSSURLs {
		out = append(out, strconv.Itoa(c.TargetSiteID)+":"+FeedKey(url, "", ""))
	}
	return out
}
func (m *Machine) GetStatus(ctx context.Context, f StatusFilter) (Response, error) {
	if f.View == "" {
		f.View = "current"
	}
	if f.View != "current" && f.View != "history" {
		return rejected("无效的 RSS 列表类型"), nil
	}
	if f.Promotion != "" && !contains([]string{Free, NonFree, Unknown}, f.Promotion) {
		return rejected("无效的原站促销条件"), nil
	}
	if f.SourcePromotion != "" && !contains([]string{Free, NonFree, Unknown}, f.SourcePromotion) {
		return rejected("无效的跨站促销条件"), nil
	}
	if f.MinSeeders != nil && *f.MinSeeders < 0 || f.MaxSeeders != nil && *f.MaxSeeders < 0 {
		return rejected("做种人数不能为负数"), nil
	}
	if f.MinSeeders != nil && f.MaxSeeders != nil && *f.MinSeeders > *f.MaxSeeders {
		return rejected("最低做种人数不能大于最高做种人数"), nil
	}
	jobs, e := m.loadJobs(ctx)
	if e != nil {
		return Response{}, e
	}
	all := jobs.values()
	current, unclassified := 0, 0
	filtered := []Object{}
	for _, j := range all {
		if j["rss_current"] == true {
			current++
		}
		if _, exists := j["rss_current"]; !exists {
			unclassified++
		}
		if (j["rss_current"] == true) == (f.View == "current") {
			filtered = append(filtered, j)
		}
	}
	viewTotal := len(filtered)
	kept := []Object{}
	for _, j := range filtered {
		if f.State != "" && j["state"] != f.State {
			continue
		}
		if f.Promotion != "" && PromotionKind(j["target_download_factor"]) != f.Promotion {
			continue
		}
		if f.SourceSiteID != nil || f.SourcePromotion != "" {
			matched := false
			for _, c := range DisplayCandidates(j) {
				if f.SourceSiteID != nil && pyString(c["site_id"]) != strconv.Itoa(*f.SourceSiteID) {
					continue
				}
				if f.SourcePromotion != "" && strOr(c["promotion"], PromotionKind(c["download_factor"])) != f.SourcePromotion {
					continue
				}
				matched = true
				break
			}
			if !matched {
				continue
			}
		}
		if f.MinSeeders != nil || f.MaxSeeders != nil {
			n := intPtr(j["target_seeders"])
			if n == nil || f.MinSeeders != nil && *n < *f.MinSeeders || f.MaxSeeders != nil && *n > *f.MaxSeeders {
				continue
			}
		}
		kept = append(kept, j)
	}
	filtered = kept
	ordered := append([]Object{}, filtered...)
	field := "rss_last_seen_at"
	if f.View == "history" {
		field = "rss_archived_at"
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		for _, key := range []string{field, "created_at", "target_hash"} {
			x, y := strOr(a[key], ""), strOr(b[key], "")
			if x != y {
				return x > y
			}
		}
		return false
	})
	size := f.PageSize
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	pages := (len(ordered) + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	page := f.Page
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start, end := (page-1)*size, page*size
	if end > len(ordered) {
		end = len(ordered)
	}
	views := []Object{}
	for _, j := range ordered[start:end] {
		views = append(views, JobView(j))
	}
	snapshots := Object{}
	if e = m.load(ctx, RSSSnapshotsKey, &snapshots); e != nil {
		return Response{}, e
	}
	failed := 0
	attempt, successful := "", ""
	keys := m.rssSourceKeys()
	for _, k := range keys {
		s, _ := snapshots[k].(map[string]any)
		if truth(s["error"]) {
			failed++
		}
		if x := strOr(s["last_attempt_at"], ""); x > attempt {
			attempt = x
		}
		if x := strOr(s["last_success_at"], ""); x > successful {
			successful = x
		}
	}
	siteNames := map[string]string{}
	siteIDs := []string{}
	for _, j := range all {
		for _, c := range DisplayCandidates(j) {
			if c["site_id"] == nil {
				continue
			}
			id := pyString(c["site_id"])
			if _, exists := siteNames[id]; !exists {
				siteIDs = append(siteIDs, id)
			}
			siteNames[id] = strOr(c["site_name"], id)
		}
	}
	sort.SliceStable(siteIDs, func(i, j int) bool { return siteNames[siteIDs[i]] < siteNames[siteIDs[j]] })
	siteOptions := []Object{}
	for _, id := range siteIDs {
		siteOptions = append(siteOptions, Object{"title": siteNames[id], "value": id})
	}
	options, e := m.host.Options(ctx)
	if e != nil {
		return Response{}, e
	}
	summary := JobSummary(all)
	summary["current"] = current
	summary["history"] = len(all) - current
	summary["unclassified"] = unclassified
	c := m.configuration()
	return success(Object{"enabled": c.Enabled, "selection_mode": c.SelectionMode, "confirm_download_risk": c.ConfirmDownloadRisk, "scan_status": m.ScanStatus(), "busy": m.Busy(), "summary": summary, "view": f.View, "view_total": viewTotal, "view_summary": JobSummary(filtered), "filter_options": Object{"source_sites": siteOptions}, "pagination": Object{"page": page, "page_size": size, "total": len(ordered), "pages": pages}, "rss_status": Object{"configured_feeds": len(keys), "failed_feeds": failed, "last_attempt_at": attempt, "last_success_at": successful}, "jobs": views, "options": options}), nil
}
