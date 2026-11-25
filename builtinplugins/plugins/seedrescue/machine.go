package seedrescue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const JobsKey = "jobs"
const RSSSnapshotsKey = "rss_snapshots"
const ScanStatusKey = "scan_status"
const Category = "seed-rescue"
const GlobalTag = "seed-rescue"

var ErrScanInterrupted = errors.New("SeedRescue ScanInterrupted")
var ErrDownloadSubmissionUncertain = errors.New("SeedRescue DownloadSubmissionUncertain")
var activeStates = []string{"SOURCE_SUBMITTING", "TARGET_SEEDING", "TARGET_COMPLETE", "TARGET_PAUSED", "TARGET_ERROR", "SOURCE_DOWNLOADING", "TARGET_DOWNLOADING", "WAITING_IYUU", "WAITING_IYUU_RETRY", "TARGET_RECHECKING", "AWAITING_SELECTION"}
var downloadStartedStates = []string{"SOURCE_SUBMITTING", "TARGET_COMPLETE", "TARGET_PAUSED", "TARGET_ERROR", "SOURCE_DOWNLOADING", "TARGET_DOWNLOADING", "WAITING_IYUU", "TARGET_RECHECKING", "TARGET_SEEDING"}
var targetSelectableStates = []string{"WAITING_SOURCE_RETRY", "FAILED_MATCHING", "AWAITING_SELECTION", "WAITING_CAPACITY", "WAITING_IYUU_RETRY", "SKIPPED_NO_IYUU_MATCH", "SKIPPED_NO_HEALTHY_SOURCE", "FAILED_TARGET_TORRENT", "FAILED_SOURCE_DOWNLOAD"}
var terminalStates = []string{"FAILED_MATCHING", "TARGET_SEEDING", "SKIPPED_NO_IYUU_MATCH", "SKIPPED_NO_HEALTHY_SOURCE", "FAILED_TARGET_TORRENT", "FAILED_SOURCE_DOWNLOAD"}

type Machine struct {
	host        Host
	mu          sync.RWMutex
	config      Configuration
	gate        chan struct{}
	statusMu    sync.RWMutex
	status      Object
	stopped     atomic.Bool
	scanActive  atomic.Bool
	scanJobs    *jobsSet
	workerMu    sync.Mutex
	workerDone  chan struct{}
	pendingScan uint64
	sitesMu     sync.Mutex
	siteGates   map[string]chan struct{}
	sourceSlots chan struct{}
}

func newMachine(host Host) *Machine {
	return &Machine{host: host, config: ParseConfig(Object{}), gate: make(chan struct{}, 1), siteGates: map[string]chan struct{}{}, sourceSlots: make(chan struct{}, 6), status: Object{"state": "idle"}}
}

func (m *Machine) restore(ctx context.Context) error {
	var status Object
	if e := m.load(ctx, ScanStatusKey, &status); e != nil {
		return e
	}
	if len(status) > 0 {
		m.statusMu.Lock()
		m.status = status
		m.statusMu.Unlock()
	}
	if m.ScanStatus()["state"] == "running" {
		return m.scanUpdate(ctx, Object{"state": "interrupted", "finished_at": m.now(), "message": "上次扫描被中断，未完成的匹配将在下次扫描恢复"})
	}
	return nil
}
func (m *Machine) Init(ctx context.Context, value Object) error {
	if e := m.guard(ctx); e != nil {
		return e
	}
	m.mu.Lock()
	m.config = ParseConfig(value)
	config := m.config
	m.mu.Unlock()
	if config.RunOnce {
		return m.host.UpdateConfig(ctx, config.Current())
	}
	return nil
}
func (m *Machine) Config() Object               { return clone(m.configuration().Current()) }
func (m *Machine) configuration() Configuration { m.mu.RLock(); defer m.mu.RUnlock(); return m.config }
func (m *Machine) State() bool                  { return m.configuration().Enabled }
func (m *Machine) Sidebar() []Object {
	if !m.State() {
		return []Object{}
	}
	return []Object{{"nav_key": "main", "title": "稀缺保种", "icon": "mdi-seed-outline", "section": "organize", "permission": "manage", "order": 46}}
}
func (m *Machine) now() string { return m.host.Now().Format("2006-01-02T15:04:05-07:00") }
func (m *Machine) guard(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	return m.host.Guard(ctx)
}
func (m *Machine) load(ctx context.Context, key string, out any) error {
	if e := m.guard(ctx); e != nil {
		return e
	}
	raw, e := m.host.LoadData(ctx, key)
	if e != nil {
		return e
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}
func (m *Machine) save(ctx context.Context, key string, value any) error {
	if e := m.guard(ctx); e != nil {
		return e
	}
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	return m.host.SaveData(ctx, key, b)
}
func (m *Machine) tryLock() bool {
	select {
	case m.gate <- struct{}{}:
		return true
	default:
		return false
	}
}
func (m *Machine) unlock()    { <-m.gate }
func (m *Machine) Busy() bool { return len(m.gate) > 0 }
func (m *Machine) ScanStatus() Object {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	return clone(m.status)
}
func (m *Machine) scanUpdate(ctx context.Context, fields Object) error {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if m.stopped.Load() {
		return nil
	}
	return m.saveScanStatus(ctx, fields)
}

func (m *Machine) saveScanStatus(ctx context.Context, fields Object) error {
	for k, v := range fields {
		m.status[k] = v
	}
	return m.save(ctx, ScanStatusKey, m.status)
}
func (m *Machine) beginScan(ctx context.Context) error {
	m.statusMu.Lock()
	m.status = Object{}
	m.statusMu.Unlock()
	return m.scanUpdate(ctx, Object{"state": "running", "stage": "preparing", "completed": 0, "total": 0, "started_at": m.now(), "finished_at": nil, "errors": 0, "issues": []any{}, "message": "正在准备扫描"})
}
func (m *Machine) scanStage(ctx context.Context, stage string, total int) error {
	if !m.scanActive.Load() {
		return nil
	}
	return m.scanUpdate(ctx, Object{"stage": stage, "completed": 0, "total": total})
}
func (m *Machine) scanAdvance(ctx context.Context) error {
	if !m.scanActive.Load() {
		return nil
	}
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if m.stopped.Load() {
		return nil
	}
	m.status["completed"] = asInt(m.status["completed"], 0) + 1
	return m.save(ctx, ScanStatusKey, m.status)
}
func (m *Machine) scanIssue(ctx context.Context, message string) error {
	if !m.scanActive.Load() {
		return nil
	}
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if m.stopped.Load() {
		return nil
	}
	issues := []string{}
	for _, v := range array(m.status["issues"]) {
		issues = append(issues, pyString(v))
	}
	issues = uniqueStrings(append(issues, message))
	if len(issues) > 5 {
		issues = issues[:5]
	}
	m.status["errors"] = asInt(m.status["errors"], 0) + 1
	m.status["issues"] = issues
	return m.save(ctx, ScanStatusKey, m.status)
}
func (m *Machine) checkStopped(ctx context.Context) error {
	if e := m.guard(ctx); e != nil {
		return e
	}
	if m.scanActive.Load() && m.stopped.Load() {
		return ErrScanInterrupted
	}
	return nil
}
func (m *Machine) checkpoint(ctx context.Context) error {
	if !m.scanActive.Load() {
		return nil
	}
	if e := m.checkStopped(ctx); e != nil {
		return e
	}
	return m.saveJobs(ctx, m.scanJobs)
}
func (m *Machine) persistJob(ctx context.Context, job Object) error {
	if e := m.checkStopped(ctx); e != nil {
		return e
	}
	jobs := m.scanJobs
	if jobs == nil {
		var e error
		jobs, e = m.loadJobs(ctx)
		if e != nil {
			return e
		}
	}
	jobs.set(pyString(job["target_hash"]), job)
	return m.saveJobs(ctx, jobs)
}
func (m *Machine) ValidConfig(ctx context.Context, log bool) (bool, error) {
	c := m.configuration()
	checks := []struct {
		ok  bool
		msg string
	}{{m.host.IYUUAvailable(ctx) && c.Token != "", "请安装并配置 IYUU自动辅种插件，同时填写 IYUU Token"}, {m.host.IYUUEnabled(ctx), "请启用并正确配置 IYUU自动辅种插件"}, {c.TargetSiteID > 0, "请选择目标站点"}, {len(c.RSSURLs) > 0, "请填写至少一个目标站 RSS"}, {len(c.SourceSiteIDs) > 0, "请选择至少一个来源站点"}, {len(c.AllowedPromotions) > 0, "免费和非免费至少选择一项"}, {c.Downloader != "", "请选择 qBittorrent 下载器"}}
	for _, check := range checks {
		if !check.ok {
			if log {
				m.host.Log(ctx, "error", "稀缺保种配置无效："+check.msg)
			}
			return false, nil
		}
	}
	service, e := m.host.Service(ctx, c.Downloader)
	if e != nil {
		return false, e
	}
	if service == nil || service.Kind != "qbittorrent" {
		if log {
			m.host.Log(ctx, "error", "稀缺保种配置无效：当前版本仅支持已连接的 qBittorrent")
		}
		return false, nil
	}
	if !service.Connected {
		if log {
			m.host.Log(ctx, "error", "稀缺保种配置无效：qBittorrent 当前未连接")
		}
		return false, nil
	}
	return true, nil
}
func (m *Machine) Services(ctx context.Context) ([]Schedule, error) {
	c := m.configuration()
	valid, e := m.ValidConfig(ctx, c.Enabled || c.RunOnce)
	if e != nil {
		return nil, e
	}
	out := []Schedule{}
	if c.Enabled && valid {
		if e = m.host.ValidateCron(c.Cron); e != nil {
			m.host.Log(ctx, "error", "稀缺保种 CRON 无效："+e.Error())
		} else {
			out = append(out, Schedule{ID: "SeedRescue.Scan", Name: "稀缺保种RSS扫描", Trigger: "cron", Cron: c.Cron, Method: "scan", Kwargs: Object{}})
		}
	}
	if c.RunOnce && valid {
		out = append(out, Schedule{ID: "SeedRescue.RunOnce", Name: "稀缺保种立即扫描", Trigger: "date", RunAt: m.host.Now().Add(3 * time.Second), Method: "scan", Kwargs: Object{}})
	}
	return out, nil
}
func (m *Machine) Stop(ctx context.Context) error {
	m.workerMu.Lock()
	m.statusMu.Lock()
	var updateErr error
	if !m.stopped.Load() && m.status["state"] == "running" {
		updateErr = m.saveScanStatus(ctx, Object{"state": "interrupted", "finished_at": m.now(), "message": "扫描已停止，未完成的匹配将在下次扫描恢复"})
	}
	m.stopped.Store(true)
	m.statusMu.Unlock()
	done := m.workerDone
	if m.pendingScan != 0 {
		m.pendingScan = 0
		m.scanActive.Store(false)
		m.unlock()
		close(done)
	}
	m.workerMu.Unlock()
	if done == nil {
		return updateErr
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
		return errors.Join(updateErr, ctx.Err())
	}
	return updateErr
}
func (m *Machine) finish(ctx context.Context, job Object, state string, reason *string) error {
	changed := job["state"] != state || !sameOptionalString(job["reason"], reason)
	job["state"] = state
	job["reason"] = deref(reason)
	job["updated_at"] = m.now()
	if changed && contains(terminalStates, state) && m.configuration().Notify {
		return m.notifyJob(ctx, job)
	}
	return nil
}
func (m *Machine) notifyJob(ctx context.Context, job Object) error {
	state := pyString(job["state"])
	title := "【稀缺保种失败】"
	if state == "TARGET_SEEDING" {
		title = "【稀缺保种成功】"
	} else if strings.HasPrefix(state, "SKIPPED") {
		title = "【稀缺保种跳过】"
	}
	return m.host.PostMessage(ctx, Object{"mtype": "SiteMessage", "title": title, "text": fmt.Sprintf("种子：%s\n状态：%s\n来源：%s\n原因：%s", pyString(job["title"]), state, strOr(job["source_site_name"], "无"), strOr(job["reason"], "无"))})
}
func sameOptionalString(v any, s *string) bool {
	if s == nil {
		return v == nil
	}
	x, ok := v.(string)
	return ok && x == *s
}
func (m *Machine) getTorrent(ctx context.Context, hash string) (Object, error) {
	if hash == "" {
		return nil, nil
	}
	c := m.configuration()
	service, e := m.host.Service(ctx, c.Downloader)
	if e != nil {
		return nil, e
	}
	if service == nil {
		return nil, nil
	}
	rows, e := m.host.QueryTorrents(ctx, c.Downloader, []string{hash})
	if e != nil || len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}
func (m *Machine) atCapacity(ctx context.Context) (bool, error) {
	c := m.configuration()
	if c.MaxDownloadCount == nil {
		return false, nil
	}
	service, e := m.host.Service(ctx, c.Downloader)
	if e != nil {
		return false, e
	}
	if service == nil {
		return true, nil
	}
	rows, e := m.host.DownloadingTorrents(ctx, c.Downloader, GlobalTag)
	if e != nil {
		return false, e
	}
	return rows == nil || len(rows) >= *c.MaxDownloadCount, nil
}
func (m *Machine) deferRetry(ctx context.Context, job Object, reason, state string) error {
	if state == "" {
		state = "WAITING_SOURCE_RETRY"
	}
	attempts := asInt(job["retry_count"], 0) + 1
	job["retry_count"] = attempts
	job["next_retry_at"] = nil
	if attempts >= 5 {
		state = "FAILED_MATCHING"
		reason += "；已达到自动重试上限，可手动重新匹配"
	} else {
		job["next_retry_at"] = m.host.Now().Add(time.Duration(5*(1<<(attempts-1))) * time.Minute).Format(time.RFC3339)
	}
	if e := m.finish(ctx, job, state, &reason); e != nil {
		return e
	}
	return m.scanIssue(ctx, strings.TrimSuffix(reason, "；已达到自动重试上限，可手动重新匹配"))
}
func RetryableIYUUError(message string) bool {
	return strings.Contains(message, "请求IYUU失败") && !(strings.Contains(message, "状态码：404") && strings.Contains(message, "未查询到可辅种数据"))
}

// jobsSet preserves Python dict insertion order across encrypted-host KV round
// trips. Source iteration order affects retry/capacity/retention decisions.
type jobsSet struct {
	order []string
	items map[string]Object
}

func newJobs() *jobsSet { return &jobsSet{order: []string{}, items: map[string]Object{}} }
func (j *jobsSet) set(key string, value Object) {
	if _, ok := j.items[key]; !ok {
		j.order = append(j.order, key)
	}
	j.items[key] = value
}
func (j *jobsSet) values() []Object {
	out := make([]Object, 0, len(j.order))
	for _, k := range j.order {
		if v, exists := j.items[k]; exists {
			out = append(out, v)
		}
	}
	return out
}
func (j *jobsSet) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range j.order {
		if i > 0 {
			b.WriteByte(',')
		}
		key, e := json.Marshal(k)
		if e != nil {
			return nil, e
		}
		v, e := json.Marshal(j.items[k])
		if e != nil {
			return nil, e
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
func (m *Machine) loadJobs(ctx context.Context) (*jobsSet, error) {
	raw, e := m.host.LoadData(ctx, JobsKey)
	if e != nil {
		return nil, e
	}
	out := newJobs()
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return out, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, fault("TypeError", "jobs", "任务数据必须为对象")
	}
	for d.More() {
		k, e := d.Token()
		if e != nil {
			return nil, e
		}
		key, ok := k.(string)
		if !ok {
			return nil, fault("TypeError", "jobs", "任务标识必须为字符串")
		}
		var job Object
		if e = d.Decode(&job); e != nil {
			return nil, e
		}
		out.set(key, job)
	}
	_, e = d.Token()
	return out, e
}
func (m *Machine) saveJobs(ctx context.Context, jobs *jobsSet) error {
	if jobs == nil {
		return nil
	}
	if m.scanJobs == jobs && m.stopped.Load() {
		return nil
	}
	save := jobs
	if len(jobs.order) > 1000 {
		save = newJobs()
		history := []string{}
		for _, k := range jobs.order {
			job := jobs.items[k]
			s := pyString(job["state"])
			if job["rss_current"] == true || (contains(downloadStartedStates, s) && s != "TARGET_SEEDING") || contains([]string{"MATCHING", "WAITING_IYUU_RETRY", "WAITING_SOURCE_RETRY", "WAITING_CAPACITY"}, s) {
				save.set(k, job)
			} else {
				history = append(history, k)
			}
		}
		sort.SliceStable(history, func(i, j int) bool {
			a, b := jobs.items[history[i]], jobs.items[history[j]]
			return strOr(a["rss_archived_at"], strOr(a["created_at"], "")) > strOr(b["rss_archived_at"], strOr(b["created_at"], ""))
		})
		if len(history) > 1000 {
			history = history[:1000]
		}
		for _, k := range history {
			save.set(k, jobs.items[k])
		}
	}
	return m.save(ctx, JobsKey, save)
}
