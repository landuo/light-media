package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"light-media/internal/plugins/statistic"
)

func siteStatisticDomain(site SiteConfig) string {
	if site.Domain != "" {
		return site.Domain
	}
	u, err := url.Parse(site.BaseURL)
	if err == nil && u.Hostname() != "" {
		return strings.ToLower(u.Hostname())
	}
	return site.ID
}

func statisticData(snapshot siteSnapshot, site SiteConfig) (statistic.Data, error) {
	data := statistic.Data{SiteID: site.ID, Domain: snapshot.Domain, Name: snapshot.Name, UpdatedDay: snapshot.Day}
	if snapshot.Stats == nil {
		return data, bad("SITE_STATISTICS_CORRUPT", "站点统计快照缺少有效数据")
	}
	if data.Domain == "" {
		data.Domain = siteStatisticDomain(site)
	}
	if data.Name == "" {
		data.Name = site.Name
	}
	if data.UpdatedDay == "" {
		at, err := time.Parse(time.RFC3339Nano, snapshot.Stats.CapturedAt)
		if err != nil {
			return data, bad("SITE_STATISTICS_CORRUPT", "站点统计快照日期无效")
		}
		data.UpdatedDay = at.In(shanghai()).Format("2006-01-02")
	}
	convert := func(value *int64) *float64 {
		if value == nil {
			return nil
		}
		v := float64(*value)
		return &v
	}
	stats := snapshot.Stats
	if at, e := time.Parse(time.RFC3339Nano, stats.CapturedAt); e == nil {
		data.UpdatedTime = at.In(shanghai()).Format("15:04:05")
	}
	if stats.Username != "" {
		data.Username = &stats.Username
	}
	if stats.Level != "" {
		data.UserLevel = &stats.Level
	}
	data.Upload, data.Download = convert(stats.Uploaded), convert(stats.Downloaded)
	data.Ratio, data.Bonus = stats.Ratio, stats.Bonus
	data.Seeding, data.SeedingSize = convert(stats.Seeding), convert(stats.SeedingBytes)
	return data, nil
}

func (a *App) siteStatisticsSnapshot(ctx context.Context) (statistic.Snapshot, error) {
	snapshot := statistic.Snapshot{Latest: []statistic.Data{}, History: []statistic.Data{}, ActiveDomains: []string{}, Now: time.Now().In(shanghai())}
	config := a.Settings()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return snapshot, err
	}
	defer tx.Rollback()
	for _, site := range config.Sites {
		if !site.Enabled {
			continue
		}
		snapshot.ActiveDomains = append(snapshot.ActiveDomains, siteStatisticDomain(site))
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='site_stats_latest' AND id=?`, site.ID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return snapshot, err
		}
		var latest siteSnapshot
		if json.Unmarshal(raw, &latest) != nil {
			return snapshot, bad("SITE_STATISTICS_CORRUPT", "站点统计快照无法读取")
		}
		if latest.ConfigKey != a.managedSiteKey(site) {
			continue
		}
		data, err := statisticData(latest, site)
		if err != nil {
			return snapshot, err
		}
		snapshot.Latest = append(snapshot.Latest, data)
		day, err := time.Parse("2006-01-02", data.UpdatedDay)
		if err != nil {
			return snapshot, bad("SITE_STATISTICS_CORRUPT", "站点统计快照日期无效")
		}
		rows, err := tx.QueryContext(ctx, `SELECT payload FROM records WHERE kind='site_stats_history' AND id>=? AND id<? ORDER BY id`, site.ID+":"+day.AddDate(0, 0, -7).Format("2006-01-02"), site.ID+":"+data.UpdatedDay)
		if err != nil {
			return snapshot, err
		}
		for rows.Next() {
			var raw []byte
			var history siteSnapshot
			if err = rows.Scan(&raw); err != nil {
				break
			}
			if json.Unmarshal(raw, &history) != nil {
				err = bad("SITE_STATISTICS_CORRUPT", "站点统计历史无法读取")
				break
			}
			if history.ConfigKey != a.managedSiteKey(site) {
				continue
			}
			previous, e := statisticData(history, site)
			if e != nil {
				err = e
				break
			}
			snapshot.History = append(snapshot.History, previous)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return snapshot, err
		}
	}
	if e := a.mergeSourceAccountStatisticsTx(ctx, tx, &snapshot); e != nil {
		return snapshot, e
	}
	return snapshot, tx.Commit()
}

// A full refresh is a host job, as in SiteChain.refresh_userdatas. The plugin's
// three-second date callback only submits it; slow sites do not hold a plugin
// process or block all other plugin configuration for minutes.
func enqueueSiteRefreshTx(ctx context.Context, tx *sql.Tx) (Job, error) {
	job, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='site_refresh' AND state IN ('queued','running','retry_wait','waiting_paused') ORDER BY created_at LIMIT 1`))
	if err == nil {
		return job, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return job, err
	}
	return enqueueTx(ctx, tx, "site_refresh", map[string]string{"id": "site_refresh"}, "")
}

func (a *App) runSiteRefresh(ctx context.Context, job Job) error {
	anyUpdated := false
	config := a.Settings()
	total := 0
	for _, site := range config.Sites {
		if site.Enabled {
			total++
		}
	}
	done := 0
	for _, site := range config.Sites {
		if !site.Enabled {
			continue
		}
		if err := a.jobCancelled(ctx, job); err != nil {
			return err
		}
		a.event(ctx, job, "INFO", "site_refresh", "progress", fmt.Sprintf("正在刷新站点数据（%d/%d）%s", done+1, total, site.Name))
		snapshot, err := a.collectManagedSite(ctx, site.ID, job.ID+":"+site.ID, &siteCollectionHooks{Auto: true, Guard: func(tx *sql.Tx) error {
			var valid int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE id=? AND generation=? AND state='running'`, job.ID, job.Generation).Scan(&valid); err != nil {
				return err
			}
			if valid != 1 {
				return bad("JOB_CANCELLED", "站点刷新任务已取消")
			}
			return nil
		}})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			var api *APIError
			if errors.As(err, &api) && (api.Code == "JOB_CANCELLED" || api.Code == "AUTOMATION_PAUSED") {
				return err
			}
			a.event(ctx, job, "WARN", "site_refresh", "site_failed", "站点 "+site.Name+" 刷新失败，请在站点管理查看连接状态")
		} else if snapshot.Connection.Status == "ok" {
			anyUpdated = true
		} else {
			a.event(ctx, job, "WARN", "site_refresh", "site_failed", "站点 "+site.Name+" 刷新失败，请在站点管理查看连接状态")
		}
		done++
	}
	if err := a.jobCancelled(ctx, job); err != nil {
		return err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE id=? AND generation=? AND state='running'`, job.ID, job.Generation).Scan(&valid); err != nil {
		return err
	}
	if valid != 1 {
		return bad("JOB_CANCELLED", "站点刷新任务已取消")
	}
	if anyUpdated {
		if err = recordDomainEventTx(ctx, tx, DomainEvent{EventID: domainEventID("site.refreshed", job.ID+":all"), Type: "site.refreshed", SiteID: "*", JobID: job.ID}); err != nil {
			return err
		}
	}
	if err = txPut(ctx, tx, "site_refresh_result", job.ID, map[string]any{"finished": done, "total": total, "any_updated": anyUpdated}); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.event(ctx, job, "INFO", "site_refresh", "progress", fmt.Sprintf("站点数据刷新完成（%d/%d）", done, total))
	if total > 0 && !anyUpdated {
		return bad("SITE_STATISTICS_REFRESH_FAILED", "所有站点的数据刷新失败，请在站点管理查看连接状态")
	}
	return nil
}

func siteDataRefreshHours(config Settings) int {
	if config.SiteDataRefreshHours == nil {
		return 6
	}
	return *config.SiteDataRefreshHours
}
func (a *App) scheduleSiteDataRefresh(ctx context.Context, at time.Time) error {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	hours := siteDataRefreshHours(a.Settings())
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	var clock struct {
		Hours int       `json:"hours"`
		Next  time.Time `json:"next"`
	}
	err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='site_refresh_clock' AND id='main'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && json.Unmarshal(raw, &clock) != nil {
		return bad("SITE_REFRESH_CLOCK_CORRUPT", "站点数据刷新时间无法读取")
	}
	interval := time.Duration(hours) * time.Hour
	previousClock := clock
	if hours == 0 {
		clock.Hours = 0
		clock.Next = time.Time{}
	} else if clock.Hours != hours || clock.Next.IsZero() {
		clock.Hours, clock.Next = hours, at.Add(interval)
	} else if !clock.Next.After(at) {
		if _, err = enqueueSiteRefreshTx(ctx, tx); err != nil {
			return err
		}
		clock.Next = clock.Next.Add((at.Sub(clock.Next)/interval + 1) * interval)
	}
	if clock == previousClock {
		return tx.Commit()
	}
	if err = txPut(ctx, tx, "site_refresh_clock", "main", clock); err != nil {
		return err
	}
	return tx.Commit()
}
