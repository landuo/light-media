package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
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

func siteMessagesEnabled(config Settings) bool {
	return config.ReadSiteMessages == nil || *config.ReadSiteMessages
}

func statisticData(snapshot siteSnapshot, site SiteConfig) (statistic.Data, error) {
	data := statistic.Data{SiteID: site.ID, Domain: snapshot.Domain, Name: snapshot.Name, UpdatedDay: snapshot.Day}
	if data.Domain == "" {
		data.Domain = siteStatisticDomain(site)
	}
	if data.Name == "" {
		data.Name = site.Name
	}
	at, err := time.Parse(time.RFC3339Nano, snapshot.Connection.CheckedAt)
	if err != nil && snapshot.Stats != nil {
		at, err = time.Parse(time.RFC3339Nano, snapshot.Stats.CapturedAt)
	}
	if data.UpdatedDay == "" {
		if err != nil {
			return data, invalidSiteAccountSnapshot()
		}
		data.UpdatedDay = at.In(shanghai()).Format("2006-01-02")
	}
	if _, e := time.Parse("2006-01-02", data.UpdatedDay); e != nil {
		return data, invalidSiteAccountSnapshot()
	}
	if err == nil {
		data.UpdatedTime = at.In(shanghai()).Format("15:04:05")
	}
	if dto := snapshot.SourceData; dto != nil {
		// SiteOper updates a model_dump copy. Its database timestamp must not
		// mutate the nullable timestamp on the returned source DTO.
		data.Username, data.UserLevel = dto.Username, dto.UserLevel
		ptr := func(n int64) *float64 { v := float64(n); return &v }
		data.Upload, data.Download = ptr(dto.Upload), ptr(dto.Download)
		data.Ratio, data.Bonus = &dto.Ratio, &dto.Bonus
		data.Seeding, data.SeedingSize = ptr(dto.Seeding), ptr(dto.SeedingSize)
		for key, field := range map[string]**float64{"upload": &data.Upload, "download": &data.Download, "ratio": &data.Ratio, "bonus": &data.Bonus, "seeding": &data.Seeding, "seeding_size": &data.SeedingSize} {
			if dto.SourceFieldNull(key) {
				*field = nil
			}
		}
		if dto.Error != nil {
			data.Error = *dto.Error
		}
		return data, nil
	}
	if snapshot.Connection.Status == "failed" {
		data.Error = snapshot.Connection.Error
		if data.Error == "" {
			data.Error = "site_user_stats_refresh_failed"
		}
		return data, nil
	}
	if snapshot.Stats == nil {
		return data, invalidSiteAccountSnapshot()
	}
	convert := func(value *int64) *float64 {
		if value == nil {
			return nil
		}
		v := float64(*value)
		return &v
	}
	stats := snapshot.Stats
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
	view, err := a.siteAccountViewTx(ctx, tx, config.Sites)
	if err != nil {
		return snapshot, err
	}
	latest := []accountObservation{}
	wantedDays := map[string]bool{}
	for _, site := range config.Sites {
		if !site.Enabled {
			continue
		}
		snapshot.ActiveDomains = append(snapshot.ActiveDomains, siteStatisticDomain(site))
		rows := view.latest(site.ID)
		latest = append(latest, rows...)
		if len(rows) == 0 {
			continue
		}
		day, _ := time.Parse("2006-01-02", rows[0].Header.Day)
		for back := 1; back <= 7; back++ {
			wantedDays[day.AddDate(0, 0, -back).Format("2006-01-02")] = true
		}
	}
	sort.SliceStable(latest, func(i, j int) bool { return accountObservationLess(latest[i], latest[j], view.Rank) })
	for _, o := range latest {
		d, e := a.siteAccountStatisticTx(ctx, tx, view, o)
		if e != nil {
			return snapshot, e
		}
		snapshot.Latest = append(snapshot.Latest, d)
	}
	for _, o := range view.Rows {
		if !wantedDays[o.Header.Day] {
			continue
		}
		// Keep every original history row, including removed sites, for the
		// original name-based fallback. Native disabled instances stay excluded.
		if o.Native != nil && !view.Sites[o.SiteID].Enabled {
			continue
		}
		d, e := a.siteAccountStatisticTx(ctx, tx, view, o)
		if e != nil {
			return snapshot, e
		}
		snapshot.History = append(snapshot.History, d)
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
	anyData := false
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
		snapshot, err := a.collectManagedSite(ctx, site.ID, job.ID+":"+site.ID, &siteCollectionHooks{Origin: siteCollectionOrigin{Kind: "job", ID: job.ID, Generation: int64(job.Generation)}, Auto: true, RequestGuard: func(requestCtx context.Context) error { return a.jobCancelled(requestCtx, job) }, Guard: func(tx *sql.Tx) error {
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
			var post *siteMessagePostError
			if errors.As(err, &post) {
				return err
			}
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
		if err == nil && snapshot.SourceData != nil {
			anyData = true
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
	newStandardRefresh := false
	if anyData {
		newStandardRefresh, err = standardBusinessFactIsNewTx(ctx, tx, domainEventID("site.refreshed", job.ID+":all"))
		if err != nil {
			return err
		}
		if err = recordDomainEventTx(ctx, tx, DomainEvent{EventID: domainEventID("site.refreshed", job.ID+":all"), Type: "site.refreshed", SiteID: "*", JobID: job.ID}); err != nil {
			return err
		}
	}
	if err = txPut(ctx, tx, "site_refresh_result", job.ID, map[string]any{"finished": done, "total": total, "any_updated": anyUpdated, "any_data": anyData}); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if newStandardRefresh {
		a.emitStandardSiteRefreshed(ctx, "*")
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
