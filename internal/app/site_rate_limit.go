package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// withSite serializes requests for one site, including account-data and torrent
// requests. The persisted usage ledger also carries the limits across restarts.
func (a *App) siteRequestDelay(ctx context.Context, site SiteConfig, at time.Time) (time.Duration, error) {
	var waitUntil time.Time
	if site.LimitSeconds > 0 {
		var latest sql.NullInt64
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT MAX(ts) FROM site_usage WHERE site_id=?`, site.ID).Scan(&latest); err != nil {
			return 0, err
		}
		if latest.Valid {
			// The existing ledger stores whole seconds. Round up so its precision
			// cannot allow a request before the configured minimum interval.
			waitUntil = time.Unix(latest.Int64+int64(site.LimitSeconds)+1, 0)
		}
	}
	if site.LimitInterval > 0 && site.LimitCount > 0 {
		var used int
		var oldest sql.NullInt64
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*),MIN(ts) FROM site_usage WHERE site_id=? AND ts>?`, site.ID, at.Unix()-int64(site.LimitInterval)).Scan(&used, &oldest); err != nil {
			return 0, err
		}
		if used >= site.LimitCount && oldest.Valid {
			until := time.Unix(oldest.Int64+int64(site.LimitInterval)+1, 0)
			if until.After(waitUntil) {
				waitUntil = until
			}
		}
	}
	if waitUntil.After(at) {
		return waitUntil.Sub(at), nil
	}
	return 0, nil
}

func (a *App) waitSiteRequestLimits(ctx context.Context, site SiteConfig) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		delay, err := a.siteRequestDelay(ctx, site, time.Now())
		if err != nil || delay <= 0 {
			return err
		}
		// Short gaps can be observed inline. Longer windows return a useful
		// retry hint instead of keeping the site lock and UI request pending.
		if delay > 5*time.Second {
			seconds := int64((delay + time.Second - 1) / time.Second)
			return &APIError{"SITE_REQUEST_LIMIT", fmt.Sprintf("站点访问频率已达限制，约%d秒后可重试", seconds), 429}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		current, err := a.siteForRequest(ctx, site.ID)
		if err != nil {
			return err
		}
		if a.managedSiteKey(current) != a.managedSiteKey(site) {
			return bad("SITE_CHANGED", "站点设置已变化，请重新发起请求")
		}
		site = current
	}
}
