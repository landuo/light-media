package app

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// withSite serializes requests for one site, including account-data and torrent
// requests. The persisted usage ledger also carries the limits across restarts.
func (a *App) siteRequestDelay(ctx context.Context, site SiteConfig, at time.Time) (time.Duration, error) {
	var waitUntil time.Time
	if site.LimitSeconds > 0 {
		var latest sql.NullInt64
		if err := a.Store.reader().QueryRowContext(ctx, `SELECT MAX(ts) FROM site_usage WHERE site_id=?`, site.ID).Scan(&latest); err != nil {
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
		if err := a.Store.reader().QueryRowContext(ctx, `SELECT COUNT(*),MIN(ts) FROM site_usage WHERE site_id=? AND ts>?`, site.ID, at.Unix()-int64(site.LimitInterval)).Scan(&used, &oldest); err != nil {
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

type keywordSiteUsageKey struct{}

// A keyword search starts independent site workers together. Requests that are
// ready while waiting for SQLite's one writer can share a durable commit.
// There is no batching timer: the first ready site acquires the writer directly.
type keywordSiteUsage struct {
	ctx     context.Context
	mu      sync.Mutex
	pending *siteUsageGroup
}

type siteUsageGroup struct {
	requests []siteUsageRequest
	done     chan struct{}
	err      error
}

type siteUsageRequest struct {
	ctx    context.Context
	siteID string
}

func withKeywordSiteUsage(ctx context.Context) context.Context {
	return context.WithValue(ctx, keywordSiteUsageKey{}, &keywordSiteUsage{ctx: ctx})
}

func (s *Store) recordSiteRequest(ctx context.Context, siteID string) error {
	request := siteUsageRequest{ctx, siteID}
	batch, _ := ctx.Value(keywordSiteUsageKey{}).(*keywordSiteUsage)
	if batch == nil {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		return commitSiteRequests(ctx, tx, []siteUsageRequest{request})
	}

	batch.mu.Lock()
	group := batch.pending
	if group == nil {
		group = &siteUsageGroup{done: make(chan struct{})}
		batch.pending = group
		go batch.commit(s, group)
	}
	group.requests = append(group.requests, request)
	batch.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-group.done:
		if group.err != nil {
			return group.err
		}
		return ctx.Err()
	}
}

func (batch *keywordSiteUsage) commit(s *Store, group *siteUsageGroup) {
	// Use the search's context: one site's cancellation must not cancel
	// unrelated sites in its transaction. The locked sites go on the wire only
	// after the commit, so later changes to their limits see every request.
	tx, err := s.DB.BeginTx(batch.ctx, nil)
	batch.mu.Lock()
	batch.pending = nil
	batch.mu.Unlock()
	if err == nil {
		err = commitSiteRequests(batch.ctx, tx, group.requests)
	}
	group.err = err
	close(group.done)
}

func commitSiteRequests(ctx context.Context, tx *sql.Tx, requests []siteUsageRequest) error {
	defer tx.Rollback()
	at := time.Now()
	for _, request := range requests {
		if request.ctx.Err() != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_usage VALUES(?,?)`, request.siteID, at.Unix()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_usage WHERE ts<?`, at.Add(-48*time.Hour).Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
