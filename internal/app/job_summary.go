package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/catalog"
)

// A presentation-only projection. Never return the related configuration,
// encrypted downloader snapshot, material reference or resource URL here.
type jobSummary struct {
	TypeLabel  string `json:"type_label"`
	Title      string `json:"title"`
	Subject    string `json:"subject"`
	Stage      string `json:"stage"`
	Source     string `json:"source,omitempty"`
	Target     string `json:"target,omitempty"`
	FileCount  int    `json:"file_count,omitempty"`
	Downloader string `json:"downloader,omitempty"`
	Activity   string `json:"activity,omitempty"`
	ActivityAt string `json:"activity_at,omitempty"`
}

type jobView struct {
	Job
	Summary jobSummary `json:"summary"`
}

var jobTypeLabels = map[string]string{
	"native_download": "提交资源下载", "download": "提交影视下载", "lark_download": "提交影视下载",
	"import": "整理文件", "organize_prepare": "准备整理目录", "reorganize_cleanup": "清理旧整理文件", "quality_cleanup": "清理洗版旧文件", "subscription_completion_prepare": "判定订阅完成", "subscription_completion": "发送订阅完成通知", "subscription_stats": "提交订阅统计", "subscription_add": "新增订阅通知与统计",
	"organize_file":                    "识别待整理文件",
	"subscription_download":            "提交订阅资源下载",
	"subscription_acquisition_receipt": "登记已接收下载",
	"scrape":                           "刮削元数据", "fnos_sync": "飞牛入库核对",
	"subscription": "检查订阅", "library_scan": "核对媒体目录", "asset_check": "检查媒体文件",
	"lark_message": "处理飞书消息", "site_refresh": "刷新站点数据", "download_file_deleted": "处理源文件删除通知",
}

type jobSummaryInput struct {
	subject, oldAsset    []byte
	activity, activityAt string
}

const jobSummarySubject = `COALESCE(subject_dl.payload,subject_sub.payload,subject_plan.payload,subject_asset.payload,subject_hist.payload)`

const jobSummaryColumns = jobSummarySubject + `,old_asset.payload,COALESCE(latest.message,''),COALESCE(latest.time,'')`

const jobSummaryReferenceID = `CASE WHEN j.id=j.ref_owner AND json_valid(j.payload) THEN NULLIF(json_extract(j.payload,'$.id'),'') END`

const jobSummaryJoins = `
 LEFT JOIN events latest ON latest.seq=(SELECT seq FROM events WHERE job_id=j.id ORDER BY seq DESC LIMIT 1)
 LEFT JOIN downloadhistory subject_dl ON j.kind IN ('download','lark_download','subscription_download','subscription_acquisition_receipt','native_download')
   AND subject_dl.uid=` + jobSummaryReferenceID + `
 LEFT JOIN subscribe subject_sub ON j.kind='subscription'
   AND subject_sub.uid=` + jobSummaryReferenceID + `
 LEFT JOIN import_plan subject_plan ON j.kind='import'
   AND subject_plan.id=` + jobSummaryReferenceID + `
 LEFT JOIN library_asset subject_asset ON j.kind IN ('scrape','fnos_sync','asset_check')
   AND subject_asset.id=` + jobSummaryReferenceID + `
 LEFT JOIN history_cleanup subject_hist ON j.kind='reorganize_cleanup'
   AND subject_hist.id=` + jobSummaryReferenceID + `
 LEFT JOIN library_asset old_asset ON j.kind='reorganize_cleanup'
   AND old_asset.id=CASE WHEN json_valid(subject_hist.payload) THEN NULLIF(json_extract(subject_hist.payload,'$.old_asset_id'),'') END`

const jobDisplayPriority = `CASE
   WHEN state='running' THEN 0
   WHEN state IN ` + activeOrganizeStates + ` AND kind NOT IN ('import','organize_file') THEN 1
   WHEN state IN ` + activeOrganizeStates + ` THEN 2
   ELSE 3 END`

func (a *App) jobQueue(ctx context.Context) ([]jobView, error) {
	// Limit before reading payloads. Running and other pending work stay
	// visible when a directory adds hundreds of recent file jobs.
	tx, err := a.Store.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var version int64
	if err = tx.QueryRowContext(ctx, "SELECT version FROM navigation_list_versions WHERE name='jobs'").Scan(&version); err != nil {
		return nil, err
	}
	settings := a.settingsSnapshot()
	cached := a.jobDisplayCache.Load()
	if cached != nil && cached.settings == settings && cached.version == version {
		return cached.rows, nil
	}
	reuse, order, dirty, err := incrementalJobQueue(ctx, tx, cached, settings, version)
	if err != nil {
		return nil, err
	}
	selection := ""
	var args []any
	if order != nil {
		selection = " WHERE id IN (SELECT value FROM json_each(?))"
		ids, _ := json.Marshal(dirty)
		args = []any{string(ids)}
	}
	rows, err := tx.QueryContext(ctx, `WITH selected AS MATERIALIZED (
 SELECT id,`+jobDisplayPriority+` AS priority FROM jobs`+selection+` ORDER BY priority,created_at DESC,id LIMIT 100
), page AS (SELECT `+jobColumns+`,selected.priority FROM selected JOIN jobs USING(id)),
 refs AS (SELECT page.*,min(id) OVER (PARTITION BY kind,CASE WHEN json_valid(payload) THEN NULLIF(json_extract(payload,'$.id'),'') END) AS ref_owner FROM page)
 SELECT j.*,`+jobSummaryColumns+` FROM refs j`+jobSummaryJoins+` ORDER BY j.priority,j.created_at DESC,j.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type entry struct {
		job      Job
		input    jobSummaryInput
		refOwner string
	}
	var entries []entry
	// Jobs on one page often reference the same subscription. Transfer and
	// retain its blob once, while keeping every job's own latest activity.
	refs := make(map[string]jobSummaryInput)
	for rows.Next() {
		var item entry
		var priority int
		input := &item.input
		item.job, err = scanJobRow(rows, &priority, &item.refOwner, &input.subject, &input.oldAsset, &input.activity, &input.activityAt)
		if err != nil {
			return nil, err
		}
		if item.job.ID == item.refOwner {
			refs[item.refOwner] = item.input
		}
		entries = append(entries, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// Formatting and redaction should not keep the database connection busy.
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	secrets := settingsRedactions(*settings)
	bytes := 0
	result := make([]jobView, 0, len(entries))
	for _, item := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		readJobTrace(&item.job)
		shared := refs[item.refOwner]
		item.input.subject, item.input.oldAsset = shared.subject, shared.oldAsset
		view, err := summarizeJob(item.job, item.input, *settings, secrets)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
		if order != nil {
			reuse[view.ID] = view
		}
	}
	if order != nil {
		result = make([]jobView, 0, len(order))
		for _, id := range order {
			result = append(result, reuse[id])
		}
	}
	for _, view := range result {
		bytes += len(view.Payload) + len(view.ErrorMessage) + len(view.ID) + len(view.WorkflowID) + len(view.ParentJobID) + len(view.RequestID) + len(view.ErrorCode) + len(view.Kind) + len(view.State) + len(view.CreatedAt) + len(view.UpdatedAt) + 512
		summary := view.Summary
		bytes += len(summary.Title) + len(summary.Subject) + len(summary.Source) + len(summary.Target) + len(summary.Downloader) + len(summary.Activity) + len(summary.ActivityAt) + len(summary.Stage)
	}
	if bytes <= 512<<10 {
		a.jobDisplayCache.Store(&jobQueueDisplay{version: version, settings: settings, rows: result})
	}
	return result, nil
}

func (a *App) jobView(ctx context.Context, job Job) (jobView, error) {
	var input jobSummaryInput
	err := a.Store.reader().QueryRowContext(ctx, `SELECT `+jobSummaryColumns+` FROM (SELECT ?1 AS id,?2 AS kind,?3 AS payload,?1 AS ref_owner) j`+jobSummaryJoins,
		job.ID, job.Kind, []byte(job.Payload)).Scan(&input.subject, &input.oldAsset, &input.activity, &input.activityAt)
	if err != nil {
		return jobView{}, err
	}
	settings := a.Settings()
	return summarizeJob(job, input, settings, settingsRedactions(settings))
}

func decodeJobSummaryRecord(raw []byte, out any) error {
	if raw == nil {
		return nil // Old jobs remain readable after their subject is removed.
	}
	return json.Unmarshal(raw, out)
}

func summarizeJob(job Job, input jobSummaryInput, settings Settings, secrets []string) (jobView, error) {
	s := jobSummary{TypeLabel: jobTypeLabels[job.Kind], Activity: input.activity, ActivityAt: input.activityAt}
	if s.TypeLabel == "" {
		s.TypeLabel = "后台任务"
	}
	var payload struct {
		ID      string   `json:"id"` // Keep payload type validation shared with the detail view.
		RootID  string   `json:"root_id"`
		FileIDs []string `json:"file_ids"`
	}
	downloader := func(key string) string {
		for _, d := range settings.Downloaders {
			if d.ID == key {
				if d.Name != "" {
					return d.Name
				}
				return d.Kind
			}
		}
		return ""
	}
	if json.Unmarshal(job.Payload, &payload) == nil {
		switch job.Kind {
		case "download_file_deleted":
			s.Subject = "核对下载任务并请求默认下载器保留数据删种"
		case "site_refresh":
			s.Subject = "所有启用站点"
		case "organize_prepare":
			var item organizePreparationPayload
			if json.Unmarshal(job.Payload, &item) != nil {
				break
			}
			if len(item.Request.HistoryIDs) > 0 {
				s.TypeLabel = "准备重新整理"
				s.Subject = fmt.Sprintf("%d 条整理记录", len(item.Request.HistoryIDs))
				s.FileCount = len(item.Request.HistoryIDs)
			} else if len(item.Request.Paths) > 0 {
				s.Source = item.Request.Paths[0]
				s.Subject = filepath.Base(s.Source)
				if len(item.Request.Paths) > 1 {
					s.Subject += fmt.Sprintf(" 等 %d 项", len(item.Request.Paths))
				}
			}
		case "organize_file":
			var item organizeFilePayload
			if json.Unmarshal(job.Payload, &item) == nil {
				s.Source, s.Subject = item.File.Path, item.Title
				if s.Subject == "" {
					s.Subject = filepath.Base(s.Source)
				}
			}
		case "download", "lark_download", "subscription_download", "subscription_acquisition_receipt":
			var item Intent
			if err := decodeJobSummaryRecord(input.subject, &item); err != nil {
				return jobView{}, err
			}
			s.Subject = jobMediaSubject(item.Media, item.Season, item.Episodes)
			if s.Subject == "" {
				s.Subject = item.ResourceTitle
			}
			s.Target, s.Downloader = item.DownloadPath, downloader(item.DownloaderID)
		case "native_download":
			var item NativeDownload
			if err := decodeJobSummaryRecord(input.subject, &item); err != nil {
				return jobView{}, err
			}
			if item.Media != nil {
				s.Subject = jobMediaSubject(*item.Media, 0, nil)
			}
			if s.Subject == "" {
				s.Subject = item.ResourceTitle
			}
			s.Target, s.Downloader = item.DownloadPath, downloader(item.DownloaderID)
		case "subscription":
			var item Subscription
			if err := decodeJobSummaryRecord(input.subject, &item); err != nil {
				return jobView{}, err
			}
			// LastReason and NextCheck belong to the latest subscription revision,
			// not necessarily this historical execution.
			s.Subject = jobMediaSubject(item.Media, item.Season, nil)
			s.Target, s.Downloader = item.DownloadPath, downloader(item.DownloaderID)
		case "import":
			var item ImportPlan
			if err := decodeJobSummaryRecord(input.subject, &item); err != nil {
				return jobView{}, err
			}
			files := []PlanFile{}
			for _, file := range item.Files {
				if len(payload.FileIDs) > 0 && !slices.Contains(payload.FileIDs, file.ID) || len(payload.FileIDs) == 0 && file.Status == "blocked" {
					continue
				}
				files = append(files, file)
			}
			s.FileCount = len(files)
			if len(files) > 0 {
				season, episodes := files[0].Season, []int{}
				for _, file := range files {
					if file.Season != season {
						season, episodes = -1, nil
						break
					}
					episodes = append(episodes, file.Episodes...)
				}
				s.Subject = jobMediaSubject(item.Media, season, episodes)
				if s.Subject == "" {
					s.Subject = filepath.Base(files[0].Source)
				}
				if len(files) == 1 {
					s.Source, s.Target = files[0].Source, files[0].Target
				} else {
					s.Subject += fmt.Sprintf(" · %d 个文件", len(files))
				}
			}
		case "scrape", "fnos_sync", "asset_check":
			var item Asset
			if err := decodeJobSummaryRecord(input.subject, &item); err != nil {
				return jobView{}, err
			}
			s.Subject = jobMediaSubject(item.Media, item.Season, item.Episodes)
			if s.Subject == "" && item.Path != "" {
				s.Subject = filepath.Base(item.Path)
			}
			s.Source = item.Path
		case "reorganize_cleanup":
			var item reorganizeCleanup
			if err := decodeJobSummaryRecord(input.subject, &item); err != nil {
				return jobView{}, err
			}
			var old Asset
			if err := decodeJobSummaryRecord(input.oldAsset, &old); err != nil {
				return jobView{}, err
			}
			s.Subject = jobMediaSubject(old.Media, old.Season, old.Episodes)
			s.Source, s.Target = old.Path, item.NewPath
			if s.Subject == "" && item.NewPath != "" {
				s.Subject = filepath.Base(item.NewPath)
			}
		case "library_scan":
			for _, root := range settings.Roots {
				if root.ID == payload.RootID {
					s.Subject, s.Source = root.Name, root.Path
					if s.Subject == "" {
						s.Subject = root.Path
					}
					break
				}
			}
		}
	}
	if strings.TrimSpace(s.Subject) == "" {
		s.Subject = "关联资料不可用"
	}
	s.Stage = jobSummaryStage(job)
	// All display strings share the application's URL/credential redaction.
	for _, value := range []*string{&s.Subject, &s.Source, &s.Target, &s.Downloader, &s.Activity} {
		*value = boundedLogString(redactSettingsValues(*value, secrets), 2048)
	}
	s.Title = s.TypeLabel + " · " + s.Subject
	return jobView{Job: job, Summary: s}, nil
}

func jobMediaSubject(m catalog.Media, season int, episodes []int) string {
	title := strings.TrimSpace(m.Title)
	if title == "" {
		title = strings.TrimSpace(m.OriginalTitle)
	}
	if title == "" {
		return ""
	}
	if m.Year > 0 {
		title += fmt.Sprintf(" (%d)", m.Year)
	}
	if m.MediaType != "tv" || season < 0 {
		return title
	}
	if season > 0 || len(episodes) > 0 {
		title += fmt.Sprintf(" 第%d季", season)
	}
	if len(episodes) == 0 {
		return title
	}
	ordered := slices.Clone(episodes)
	slices.Sort(ordered)
	ordered = slices.Compact(ordered)
	ranges := []string{}
	for i := 0; i < len(ordered); i++ {
		if ordered[i] < 1 {
			continue
		}
		first, last := ordered[i], ordered[i]
		for i+1 < len(ordered) && ordered[i+1] == last+1 {
			i++
			last = ordered[i]
		}
		text := strconv.Itoa(first)
		if last != first {
			text += "–" + strconv.Itoa(last)
		}
		ranges = append(ranges, text)
		if len(ranges) == 6 && i+1 < len(ordered) {
			ranges = append(ranges, "…")
			break
		}
	}
	if len(ranges) > 0 {
		title += " 第" + strings.Join(ranges, "、") + "集"
	}
	return title
}

func jobSummaryStage(j Job) string {
	verb := jobTypeLabels[j.Kind]
	if verb == "" {
		verb = "后台任务"
	}
	switch j.State {
	case "queued":
		return "等待" + verb
	case "running":
		return "正在" + verb
	case "cancelled":
		return "任务已取消"
	case "needs_review":
		return "需要人工核对"
	case "failed":
		return verb + "失败"
	case "succeeded":
		if j.Kind == "download" || j.Kind == "native_download" || j.Kind == "lark_download" || j.Kind == "subscription_download" {
			return "已提交下载器"
		}
		return verb + "完成"
	default:
		return "任务状态待核对"
	}
}
