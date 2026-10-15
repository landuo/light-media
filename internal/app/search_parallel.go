package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"light-media/internal/sites"
)

const concurrentSearchSites = 5
const siteSearchTimeout = 45 * time.Second

type siteSearchOutput struct {
	status     SiteStatus
	candidates []Candidate
}

// Sites run independently, while withSite still serializes every request for a
// particular configured site and enforces its shared budget and cooldown.
// Only the collector calls the progress callback / writes the HTTP stream.
func (a *App) searchSites(ctx context.Context, in SearchRequest, out SearchOutput, run func(context.Context, SiteConfig) siteSearchOutput) (SearchOutput, error) {
	selected := map[string]bool{}
	for _, key := range in.SiteIDs {
		selected[key] = true
	}
	configs := []SiteConfig{}
	for _, config := range a.Settings().Sites {
		if config.Enabled && (len(selected) == 0 || selected[config.ID]) {
			configs = append(configs, config)
			out.Sites = append(out.Sites, SiteStatus{ID: config.ID, Status: "queued"})
		}
	}
	if len(configs) == 0 {
		return out, bad("NO_SITES", "请先添加并启用资源站点")
	}
	emitSearchProgress(ctx, out)
	type event struct {
		index   int
		started bool
		result  siteSearchOutput
	}
	updates := make(chan event, 2*len(configs))
	slots := make(chan struct{}, concurrentSearchSites)
	var workers sync.WaitGroup
	defer workers.Wait()
	for i, config := range configs {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if ctx.Err() != nil {
				return
			}
			updates <- event{index: i, started: true}
			start := time.Now()
			siteCtx, cancel := context.WithTimeout(ctx, siteSearchTimeout)
			defer cancel()
			result := run(siteCtx, config)
			result.status.ID = config.ID
			result.status.ElapsedMS = time.Since(start).Milliseconds()
			updates <- event{index: i, result: result}
		}()
	}
	for completed := 0; completed < len(configs); {
		select {
		case <-ctx.Done():
			for i := range out.Sites {
				if out.Sites[i].Status == "queued" || out.Sites[i].Status == "searching" {
					out.Sites[i].Status, out.Sites[i].Message = "cancelled", "搜索已停止，已完成的结果保留"
					if errors.Is(ctx.Err(), context.DeadlineExceeded) {
						out.Sites[i].Status, out.Sites[i].Message = "timeout", "搜索超时，已完成的结果保留"
					}
				}
			}
			emitSearchProgress(ctx, out)
			return out, ctx.Err()
		case update := <-updates:
			if update.started {
				out.Sites[update.index].Status = "searching"
			} else {
				completed++
				out.Sites[update.index] = update.result.status
				out.Candidates = append(out.Candidates, update.result.candidates...)
				a.logSite(update.result.status, out.WorkflowID)
			}
			emitSearchProgress(ctx, out)
		}
	}
	return out, ctx.Err()
}

func (a *App) searchStatus(ctx context.Context, key string, count int, err error, successfulQueries int) SiteStatus {
	status := SiteStatus{ID: key, Status: "ok", Count: count}
	if err == nil {
		if count == 0 {
			status.Status = "empty"
		}
		return status
	}
	status.Status, status.Message = "failed", a.clean(err.Error())
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		status.Status, status.Message = "timeout", "站点搜索超时，已返回的结果保留"
	} else if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		status.Status, status.Message = "cancelled", "搜索已停止，已返回的结果保留"
	}
	if count > 0 || successfulQueries > 0 {
		status.Status = "partial"
	}
	return status
}

// A listing may contain hundreds of rows. Persist its preview lookup hints in
// one transaction instead of a synchronous SQLite commit for every resource.
func (a *App) cacheSearchResources(ctx context.Context, resources []sites.Resource, queries map[string]string, page int, identity string) error {
	if len(resources) == 0 {
		return nil
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	fetchedAt := now()
	for _, resource := range resources {
		value := map[string]any{"resource": resource, "query": queries[resource.ID], "page": page, "fetched_at": fetchedAt, "media_key": identity}
		if err = txPut(ctx, tx, "resource", resource.SiteID+":"+resource.ID, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
