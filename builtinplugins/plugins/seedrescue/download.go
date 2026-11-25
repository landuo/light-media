package seedrescue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (m *Machine) setSourceJob(job Object, c Candidate) {
	id := candidateID(c)
	if c.Match.InfoHash == job["target_hash"] && c.SiteID == asInt(job["target_site_id"], 0) {
		id = targetCandidateID(job)
	}
	for k, v := range (Object{"selected_candidate_id": id, "source_hash": c.Match.InfoHash, "source_site_id": c.SiteID, "source_site_name": c.SiteName, "source_seeders": deref(c.Seeders), "promotion": PromotionKind(deref(c.DownloadFactor))}) {
		job[k] = v
	}
}
func (m *Machine) startSource(ctx context.Context, c Candidate, job Object) (bool, string, error) {
	if e := m.checkStopped(ctx); e != nil {
		return false, "", e
	}
	target := c.Match.InfoHash == job["target_hash"] && c.SiteID == asInt(job["target_site_id"], 0)
	state := "SOURCE_DOWNLOADING"
	if target {
		state = "TARGET_DOWNLOADING"
	}
	existing, e := m.getTorrent(ctx, c.Match.InfoHash)
	if e != nil {
		return false, "", e
	}
	if len(existing) > 0 {
		m.setSourceJob(job, c)
		reason := ptr("来源种子已存在于下载器")
		if target {
			state, reason = TargetTorrentStatus(existing)
		} else if TorrentIsComplete(existing) {
			state = "WAITING_IYUU"
		}
		return true, "", m.finish(ctx, job, state, reason)
	}
	if !truth(c.Torrent["enclosure"]) {
		return false, "来源种子缺少下载链接", nil
	}
	content, msg, e := m.host.DownloadMetadata(ctx, c.Torrent)
	if e != nil {
		return false, "", e
	}
	if content == nil {
		if msg == "" {
			msg = "未取得种子文件"
		}
		return false, msg, nil
	}
	actual, e := InfoHashFromTorrent(content)
	if e != nil {
		return false, e.Error(), nil
	}
	if !strings.EqualFold(actual, c.Match.InfoHash) {
		if target {
			return false, "下载种子Hash与保种原种不一致", nil
		}
		return false, "下载种子Hash与IYUU映射不一致", nil
	}
	conf := m.configuration()
	service, e := m.host.Service(ctx, conf.Downloader)
	if e != nil {
		return false, "", e
	}
	if service == nil {
		return false, "下载器不可用", nil
	}
	if e = m.checkStopped(ctx); e != nil {
		return false, "", e
	}
	m.setSourceJob(job, c)
	if e = m.finish(ctx, job, "SOURCE_SUBMITTING", ptr("下载提交待确认")); e != nil {
		return false, "", e
	}
	if e = m.persistJob(ctx, job); e != nil {
		return false, "", e
	}
	added, callErr := m.host.AddTorrent(ctx, AddRequest{conf.Downloader, content, conf.SavePath, Category, []string{GlobalTag}})
	if callErr != nil || !added {
		job["reason"] = "下载器未确认添加结果，后续扫描将核对同一 Hash；请先检查 qBittorrent，勿重复添加"
		if callErr != nil {
			job["reason"] = "下载提交结果未知，后续扫描将核对同一 Hash；请先检查 qBittorrent，勿重复添加"
		}
		if e = m.persistJob(ctx, job); e != nil {
			return false, "", e
		}
		return false, "", ErrDownloadSubmissionUncertain
	}
	m.setSourceJob(job, c)
	if e = m.finish(ctx, job, state, nil); e != nil {
		return false, "", e
	}
	m.host.Log(ctx, "info", fmt.Sprintf("稀缺保种从 %s 下载：%s，做种人数 %s，促销 %s", c.SiteName, pyString(job["title"]), pyString(deref(c.Seeders)), PromotionKind(deref(c.DownloadFactor))))
	return true, "", nil
}
func (m *Machine) DownloadCandidate(ctx context.Context, hash string, p Selection) (Response, error) {
	if !m.tryLock() {
		return rejected("插件正在执行其他操作，请稍后重试"), nil
	}
	defer m.unlock()
	jobs, e := m.loadJobs(ctx)
	if e != nil {
		return Response{}, e
	}
	job := jobs.items[strings.ToLower(hash)]
	if len(job) == 0 {
		return rejected("保种任务不存在"), nil
	}
	if contains(downloadStartedStates, pyString(job["state"])) {
		if job["state"] == "SOURCE_SUBMITTING" {
			return rejected("下载提交结果待确认，请先检查 qBittorrent，勿重复添加"), nil
		}
		if p.CandidateID == job["selected_candidate_id"] {
			return success(JobView(job)), nil
		}
		return rejected("任务已经选择了其他来源"), nil
	}
	target := p.CandidateID == targetCandidateID(job)
	var candidate *Candidate
	if target && truth(job["target_site_id"]) && contains(targetSelectableStates, pyString(job["state"])) {
		full, e := m.atCapacity(ctx)
		if e != nil {
			return Response{}, e
		}
		if full {
			existing, e := m.getTorrent(ctx, pyString(job["target_hash"]))
			if e != nil {
				return Response{}, e
			}
			if len(existing) == 0 {
				return rejected("达到稀缺保种同时下载上限"), nil
			}
		}
		var reason string
		candidate, reason, e = m.targetSource(ctx, job)
		if e != nil {
			return Response{}, e
		}
		if candidate == nil {
			return rejected(reason), nil
		}
	} else if job["state"] != "AWAITING_SELECTION" || target {
		return rejected("当前任务不允许选择来源"), nil
	} else {
		var record Object
		for _, r := range objects(job["candidates"]) {
			if r["candidate_id"] == p.CandidateID {
				record = r
				break
			}
		}
		if record == nil {
			return rejected("候选来源不存在或已经过期"), nil
		}
		if record["eligible"] == false {
			return rejected("该来源未通过下载规则，请调整配置后重新匹配"), nil
		}
		full, e := m.atCapacity(ctx)
		if e != nil {
			return Response{}, e
		}
		if full {
			return rejected("达到稀缺保种同时下载上限"), nil
		}
		cs, _, e := m.probeSources(ctx, []Match{{record["iyuu_sid"], strOr(record["torrent_id"], ""), strOr(record["expected_hash"], "")}}, strOr(job["title"], ""))
		if e != nil {
			return Response{}, e
		}
		conf := m.configuration()
		for _, c := range RankedSources(cs, conf.MinimumSeeders, conf.AllowedPromotions) {
			if candidateID(c) == p.CandidateID {
				candidate = ptr(c)
				break
			}
		}
		if candidate == nil {
			return rejected("来源状态已变化，请重新匹配后再试"), nil
		}
	}
	kind := PromotionKind(deref(candidate.DownloadFactor))
	confirmed := kind == Unknown && p.ConfirmUnknown || kind == NonFree && p.ConfirmedDownloadFactor != nil && candidate.DownloadFactor != nil && *p.ConfirmedDownloadFactor == *candidate.DownloadFactor
	conf := m.configuration()
	if conf.ConfirmDownloadRisk && kind != Free && !confirmed {
		existing, e := m.getTorrent(ctx, candidate.Match.InfoHash)
		if e != nil {
			return Response{}, e
		}
		if len(existing) == 0 {
			if e = m.saveJobs(ctx, jobs); e != nil {
				return Response{}, e
			}
			r := rejected("所选种子非免费或促销未知，请确认后下载")
			var factor any
			if kind == NonFree {
				factor = deref(candidate.DownloadFactor)
			}
			r.Data = Object{"confirmation_required": true, "candidate_id": p.CandidateID, "title": job["title"], "site_name": candidate.SiteName, "promotion": kind, "download_factor": factor}
			return r, nil
		}
	}
	if p.SkipFutureRiskConfirmation && confirmed && conf.ConfirmDownloadRisk {
		config := conf.Current()
		config["confirm_download_risk"] = false
		if e = m.host.UpdateConfig(ctx, config); e != nil {
			return Response{}, e
		}
		m.mu.Lock()
		m.config.ConfirmDownloadRisk = false
		m.mu.Unlock()
	}
	job["selected_candidate_id"] = p.CandidateID
	ok, reason, e := m.startSource(ctx, *candidate, job)
	if errors.Is(e, ErrDownloadSubmissionUncertain) {
		if e = m.saveJobs(ctx, jobs); e != nil {
			return Response{}, e
		}
		return rejected(pyString(job["reason"])), nil
	}
	if e != nil {
		return Response{}, e
	}
	if !ok {
		job["selected_candidate_id"] = nil
		state := "FAILED_SOURCE_DOWNLOAD"
		if target {
			state = "FAILED_TARGET_TORRENT"
		}
		if e = m.finish(ctx, job, state, &reason); e != nil {
			return Response{}, e
		}
		if e = m.saveJobs(ctx, jobs); e != nil {
			return Response{}, e
		}
		return rejected(reason), nil
	}
	if e = m.saveJobs(ctx, jobs); e != nil {
		return Response{}, e
	}
	return success(JobView(job)), nil
}
func (m *Machine) RetryJob(ctx context.Context, hash string) (Response, error) {
	if !m.tryLock() {
		return rejected("插件正在执行其他操作，请稍后重试"), nil
	}
	defer m.unlock()
	jobs, e := m.loadJobs(ctx)
	if e != nil {
		return Response{}, e
	}
	job := jobs.items[strings.ToLower(hash)]
	if len(job) == 0 {
		return rejected("保种任务不存在"), nil
	}
	if contains(downloadStartedStates, pyString(job["state"])) {
		return rejected("当前任务正在下载或已经进入辅种流程"), nil
	}
	for k, v := range (Object{"state": "MATCHING", "reason": nil, "candidates": []Object{}, "selected_candidate_id": nil, "retry_count": 0, "next_retry_at": nil}) {
		job[k] = v
	}
	if e = m.saveJobs(ctx, jobs); e != nil {
		return Response{}, e
	}
	if e = m.findAndDownload(ctx, job); e != nil {
		return Response{}, e
	}
	if e = m.saveJobs(ctx, jobs); e != nil {
		return Response{}, e
	}
	failed := contains([]string{"WAITING_IYUU_RETRY", "WAITING_SOURCE_RETRY", "FAILED_MATCHING", "FAILED_SOURCE_DOWNLOAD", "SOURCE_SUBMITTING"}, pyString(job["state"]))
	r := Response{Success: !failed, Data: JobView(job)}
	if failed && job["reason"] != nil {
		r.Message = ptr(pyString(job["reason"]))
	}
	return r, nil
}
func (m *Machine) IgnoreJob(ctx context.Context, hash string) (Response, error) {
	if !m.tryLock() {
		return rejected("插件正在执行其他操作，请稍后重试"), nil
	}
	defer m.unlock()
	jobs, e := m.loadJobs(ctx)
	if e != nil {
		return Response{}, e
	}
	job := jobs.items[strings.ToLower(hash)]
	if len(job) == 0 {
		return rejected("保种任务不存在"), nil
	}
	if contains(downloadStartedStates, pyString(job["state"])) {
		return rejected("当前任务正在下载或已经进入辅种流程"), nil
	}
	if e = m.finish(ctx, job, "IGNORED", ptr("用户忽略")); e != nil {
		return Response{}, e
	}
	if e = m.saveJobs(ctx, jobs); e != nil {
		return Response{}, e
	}
	return success(JobView(job)), nil
}
func (m *Machine) reconcileJobs(ctx context.Context, jobs *jobsSet) error {
	for _, j := range jobs.values() {
		if !contains(activeStates, pyString(j["state"])) {
			continue
		}
		t, e := m.getTorrent(ctx, strOr(j["target_hash"], ""))
		if e != nil {
			return e
		}
		if len(t) > 0 {
			state, reason := TargetTorrentStatus(t)
			if e = m.finish(ctx, j, state, reason); e != nil {
				return e
			}
			continue
		}
		t, e = m.getTorrent(ctx, strOr(j["source_hash"], ""))
		if e != nil {
			return e
		}
		if len(t) > 0 {
			state := "SOURCE_DOWNLOADING"
			var reason *string
			if TorrentIsComplete(t) {
				state = "WAITING_IYUU"
				reason = ptr("来源下载完成，等待IYUU添加目标站种子")
			}
			if e = m.finish(ctx, j, state, reason); e != nil {
				return e
			}
		}
	}
	return nil
}
func (m *Machine) resumeWaitingJobs(ctx context.Context, jobs *jobsSet) error {
	pending := []Object{}
	for _, j := range jobs.values() {
		if e := m.checkStopped(ctx); e != nil {
			return e
		}
		state := pyString(j["state"])
		if contains([]string{"MATCHING", "WAITING_IYUU_RETRY", "WAITING_SOURCE_RETRY"}, state) || state == "SKIPPED_NO_IYUU_MATCH" && RetryableIYUUError(strOr(j["reason"], "")) {
			if t, e := parseTime(j["next_retry_at"]); e == nil && t.After(m.host.Now()) {
				continue
			}
			pending = append(pending, j)
		} else if state == "WAITING_CAPACITY" {
			full, e := m.atCapacity(ctx)
			if e != nil {
				return e
			}
			if !full {
				pending = append(pending, j)
			}
		}
	}
	if e := m.scanStage(ctx, "recovery", len(pending)); e != nil {
		return e
	}
	for _, j := range pending {
		if j["state"] == "WAITING_CAPACITY" {
			full, e := m.atCapacity(ctx)
			if e != nil {
				return e
			}
			if full {
				continue
			}
		}
		if e := m.findAndDownload(ctx, j); e != nil {
			return e
		}
	}
	return nil
}
func parseTime(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, errors.New("not datetime")
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, e := time.Parse(layout, s); e == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("invalid datetime")
}
