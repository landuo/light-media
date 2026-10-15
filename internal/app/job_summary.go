package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"native_download": "提交资源下载", "download": "提交影视下载",
	"import": "整理文件", "reorganize_cleanup": "清理旧整理文件",
	"scrape": "刮削元数据", "fnos_sync": "飞牛入库核对",
	"subscription": "检查订阅", "library_scan": "核对媒体目录", "asset_check": "检查媒体文件",
}

func (a *App) jobViews(ctx context.Context, jobs []Job) ([]jobView, error) {
	result := make([]jobView, 0, len(jobs))
	for _, job := range jobs {
		view, err := a.jobView(ctx, job)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	return result, nil
}

func (a *App) jobView(ctx context.Context, job Job) (jobView, error) {
	s := jobSummary{TypeLabel: jobTypeLabels[job.Kind]}
	if s.TypeLabel == "" {
		s.TypeLabel = "后台任务"
	}
	var payload struct {
		ID      string   `json:"id"`
		RootID  string   `json:"root_id"`
		FileIDs []string `json:"file_ids"`
	}
	settings := a.Settings()
	read := func(kind, key string, out any) error {
		if key == "" {
			return nil
		}
		err := a.Store.Get(ctx, kind, key, out)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // Old jobs remain readable after their subject is removed.
		}
		return err
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
		case "download":
			var item Intent
			if err := read("intent", payload.ID, &item); err != nil {
				return jobView{}, err
			}
			s.Subject = jobMediaSubject(item.Media, item.Season, item.Episodes)
			if s.Subject == "" {
				s.Subject = item.ResourceTitle
			}
			s.Target, s.Downloader = item.DownloadPath, downloader(item.DownloaderID)
		case "native_download":
			var item NativeDownload
			if err := read("native_download", payload.ID, &item); err != nil {
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
			if err := read("subscription", payload.ID, &item); err != nil {
				return jobView{}, err
			}
			// LastReason and NextCheck belong to the latest subscription revision,
			// not necessarily this historical execution.
			s.Subject = jobMediaSubject(item.Media, item.Season, nil)
			s.Target, s.Downloader = item.DownloadPath, downloader(item.DownloaderID)
		case "import":
			var item ImportPlan
			if err := read("import_plan", payload.ID, &item); err != nil {
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
			if err := read("asset", payload.ID, &item); err != nil {
				return jobView{}, err
			}
			s.Subject = jobMediaSubject(item.Media, item.Season, item.Episodes)
			if s.Subject == "" && item.Path != "" {
				s.Subject = filepath.Base(item.Path)
			}
			s.Source = item.Path
		case "reorganize_cleanup":
			var item reorganizeCleanup
			if err := read("reorganize_cleanup", payload.ID, &item); err != nil {
				return jobView{}, err
			}
			var old Asset
			if err := read("asset", item.OldAssetID, &old); err != nil {
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
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT message,time FROM events WHERE job_id=? ORDER BY seq DESC LIMIT 1`, job.ID).Scan(&s.Activity, &s.ActivityAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return jobView{}, err
	}
	// All display strings share the application's URL/credential redaction.
	for _, value := range []*string{&s.Subject, &s.Source, &s.Target, &s.Downloader, &s.Activity} {
		*value = boundedLogString(cleanWithSettings(*value, settings), 2048)
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
	case "retry_wait":
		return "等待重试"
	case "waiting_paused":
		return "自动化已暂停，等待恢复"
	case "cancelled":
		return "任务已取消"
	case "needs_review":
		return "需要人工核对"
	case "failed":
		return verb + "失败"
	case "succeeded":
		if j.Kind == "download" || j.Kind == "native_download" {
			return "已提交下载器"
		}
		return verb + "完成"
	default:
		return "任务状态待核对"
	}
}
