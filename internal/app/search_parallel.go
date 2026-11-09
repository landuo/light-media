package app

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/decision"
	"light-media/internal/priorityrules"
	"light-media/internal/sites"
)

const concurrentBackgroundSearchSites = 5
const siteSearchTimeout = 45 * time.Second

type siteSearchOutput struct {
	status     SiteStatus
	candidates []Candidate
}

// Sites run independently, while withSite still serializes every request for a
// particular configured site and enforces its shared budget and cooldown.
// Only the collector calls the progress callback / writes the HTTP stream.
func (a *App) searchSites(ctx context.Context, in SearchRequest, out SearchOutput, auto bool, run func(context.Context, SiteConfig) siteSearchOutput) (SearchOutput, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	settings := a.Settings()
	filter, err := a.preparePriorityFilter(ctx, in, settings)
	if err != nil {
		return out, err
	}
	sorter, err := a.preparePrioritySorter(ctx, in, settings)
	if err != nil {
		return out, err
	}
	additional := snapshotAdditionalFilter(in.Policy.FilterParams)
	selected := map[string]bool{}
	selectedIDs := in.SiteIDs
	if len(selectedIDs) == 0 {
		selectedIDs = settings.IndexerSites
	}
	for _, key := range selectedIDs {
		selected[key] = true
	}
	configs := []SiteConfig{}
	for _, config := range settings.Sites {
		if config.Enabled && (len(selected) == 0 || selected[config.ID]) {
			configs = append(configs, config)
		}
	}
	sort.SliceStable(configs, func(i, j int) bool { return configs[i].Priority < configs[j].Priority })
	status := "searching"
	var slots chan struct{}
	if auto {
		status = "queued"
		slots = make(chan struct{}, concurrentBackgroundSearchSites)
	}
	for _, config := range configs {
		out.Sites = append(out.Sites, SiteStatus{ID: config.ID, Status: status})
	}
	if len(configs) == 0 {
		return out, bad("NO_SITES", "请先添加并启用资源站点")
	}
	emitSearchProgress(ctx, out)
	type event struct {
		index   int
		started bool
		partial bool
		result  siteSearchOutput
	}
	updates := make(chan event, 2*len(configs))
	if _, streaming := ctx.Value(searchProgressKey{}).(func(SearchOutput)); streaming {
		indexes := map[string]int{}
		for index, config := range configs {
			indexes[config.ID] = index
		}
		progressCtx := ctx
		ctx = context.WithValue(ctx, originalResourcePageProgressKey{}, originalResourcePageProgress(func(site SiteConfig, result sites.SearchResult, finished bool) {
			if in.mediaModuleSearch != nil {
				previous := in.mediaModuleSearch.results[site.ID].result.Resources
				result.Resources = append(append([]sites.Resource{}, previous...), result.Resources...)
			}
			status := a.searchStatus(progressCtx, site.ID, len(result.Resources), nil, 1)
			if !finished {
				status.Status = "searching"
			}
			found := siteSearchOutput{status: status, candidates: []Candidate{}}
			for _, resource := range result.Resources {
				var candidate Candidate
				if in.priorityTarget != nil {
					choice := a.evaluateResourceContext(progressCtx, resource, *in.priorityTarget, decision.NativePolicy(decisionPolicy(in.Policy)))
					candidate = Candidate{Resource: resource, Accepted: choice.Accepted, NeedsConfirmation: choice.NeedsConfirmation, Reasons: choice.Reasons, Season: choice.Season, Episodes: choice.Episodes}
				} else {
					choice := decision.EvaluateResourceContext(progressCtx, resource, decision.NativePolicy(decisionPolicy(in.Policy)))
					if choice.Accepted {
						choice.Reasons = append(choice.Reasons, "media_identity_required")
					}
					season, episodes := a.resourceNumberingSuggestionContext(progressCtx, resource.Title)
					candidate = Candidate{Resource: resource, Accepted: choice.Accepted, NeedsConfirmation: choice.Accepted, Reasons: choice.Reasons, Season: season, Episodes: episodes}
				}
				found.candidates = append(found.candidates, candidate)
			}
			select {
			case updates <- event{index: indexes[site.ID], partial: true, result: found}:
			case <-progressCtx.Done():
			}
		}))
	}
	if in.resourceBatch != nil {
		in.resourceBatch.attach(ctx, configs)
	}
	if in.mediaModuleSearch != nil {
		in.mediaModuleSearch.ctx, in.mediaModuleSearch.configs = ctx, append([]SiteConfig(nil), configs...)
	}
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	for i, config := range configs {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if slots != nil {
				select {
				case slots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-slots }()
				select {
				case updates <- event{index: i, started: true}:
				case <-ctx.Done():
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
			start := time.Now()
			siteCtx, cancel := context.WithTimeout(ctx, siteSearchTimeout)
			defer cancel()
			result := run(siteCtx, config)
			result.status.ID = config.ID
			result.status.ElapsedMS = time.Since(start).Milliseconds()
			select {
			case updates <- event{index: i, result: result}:
			case <-ctx.Done():
			}
		}()
	}
	var collected []Candidate
	partial := map[int][]Candidate{}
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
				if out.Sites[update.index].Status == "queued" {
					out.Sites[update.index].Status = "searching"
				}
			} else {
				if !update.partial {
					completed++
				}
				filtered, filterErr := additional.apply(ctx, update.result.candidates)
				if filterErr != nil {
					out.Candidates = []Candidate{}
					a.log("ERROR", "rules", "subscription_filter_failed", filterErr.Error(), out.WorkflowID, "")
					emitSearchProgress(ctx, out)
					return out, filterErr
				}
				update.result.candidates = filtered
				if in.Policy.FilterParams != nil {
					update.result.status.Count = len(filtered)
				}
				out.Sites[update.index] = update.result.status
				if update.partial {
					partial[update.index] = update.result.candidates
				} else {
					delete(partial, update.index)
					collected = append(collected, update.result.candidates...)
				}
				out.Candidates = append([]Candidate{}, collected...)
				for index := range configs {
					out.Candidates = append(out.Candidates, partial[index]...)
				}
				sortSearchCandidates(out.Candidates, in.Policy)
				if !update.partial {
					a.logSite(update.result.status, out.WorkflowID)
				}
			}
			emitSearchProgress(ctx, out)
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Candidates = append([]Candidate{}, collected...)
	out, err = filter.apply(ctx, a, out)
	if err != nil {
		emitSearchProgress(ctx, out)
		return out, err
	}
	sorter.sort(a, out.Candidates, in.Policy)
	if in.mediaModuleSearch != nil {
		out, err = originalMediaResourceDeduplicate(out, configs)
		if err != nil {
			return out, err
		}
	}
	emitSearchProgress(ctx, out)
	return out, nil
}

func sortSearchCandidates(candidates []Candidate, policy Policy) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Accepted != candidates[j].Accepted {
			return candidates[i].Accepted
		}
		if left, right := priorityrules.PriorityKey(candidates[i].Resource.PriorityOrder), priorityrules.PriorityKey(candidates[j].Resource.PriorityOrder); left != right {
			return left > right
		}
		left, right := decision.Rank(candidates[i].Resource, decisionPolicy(policy)), decision.Rank(candidates[j].Resource, decisionPolicy(policy))
		if left != right {
			return left > right
		}
		if candidates[i].Resource.Free != candidates[j].Resource.Free {
			return candidates[i].Resource.Free
		}
		return candidates[i].Resource.Seeders > candidates[j].Resource.Seeders
	})
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
	private := map[string]downloadContextSnapshot{}
	for _, resource := range resources {
		if len(resource.ModuleSourceFields) == 0 {
			continue
		}
		snapshot, e := a.prepareDownloadContext(ctx, "search_resource", resource.SiteID+":"+resource.ID, "", resource)
		if e != nil {
			return e
		}
		if e = a.buildOriginalDownloadContext(ctx, &snapshot, Candidate{Resource: resource}, catalog.Media{}, resource.ModuleMediaFields); e != nil {
			return e
		}
		private[resource.SiteID+":"+resource.ID] = snapshot
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
		if snapshot, ok := private[resource.SiteID+":"+resource.ID]; ok {
			if err = a.saveDownloadContextTx(ctx, tx, snapshot); err != nil {
				return err
			}
		} else {
			// A new native response supersedes a stale plugin-only context.
			if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind='download_context' AND id=?`, "search_resource:"+resource.SiteID+":"+resource.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
