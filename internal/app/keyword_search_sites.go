package app

import (
	"context"
	"errors"
	"slices"
	"time"
)

type keywordSiteUpdate struct {
	index      int
	candidates []Candidate
	count      int // after the optional additional filter, before priority rules
	status     SiteStatus
	err        error
}

// Each page is evaluated by its own site worker. The collector only merges
// ready results and is the sole publisher. Workers never access the response.
func (a *App) searchKeywordSites(ctx context.Context, in SearchRequest, out SearchOutput, run func(context.Context, SiteConfig, func([]Candidate)) siteSearchOutput) (SearchOutput, error) {
	settings := a.Settings()
	configs := selectedSearchSites(in.SiteIDs, settings)
	if len(configs) == 0 {
		return out, bad("NO_SITES", "请先添加并启用资源站点")
	}
	filter, err := a.preparePriorityFilter(ctx, in, settings)
	if err != nil {
		return out, err
	}
	var groups []originalPluginModuleGroup
	if filter.engine != nil {
		groups, err = a.originalPluginModulesForMethod(ctx, "", "filter_torrents")
		if err != nil {
			return out, err
		}
	}
	// Native priority rules evaluate each torrent independently. A plugin can
	// change the order or membership of the entire input list, so give it the
	// collected batch once and only publish its results after the call.
	batchFilter := originalModuleGroupsHaveMethod(groups, "filter_torrents")
	out.keywordFilterPending = batchFilter
	for _, config := range configs {
		out.Sites = append(out.Sites, SiteStatus{ID: config.ID, Status: "searching"})
	}
	// Merging allocates a new result slice; earlier progress frames only need
	// their own copy of the site statuses the collector updates in place.
	progress, _ := ctx.Value(searchProgressKey{}).(func(SearchOutput))
	publish := func() {
		if progress != nil {
			snapshot := out
			snapshot.Sites = slices.Clone(out.Sites)
			progress(snapshot)
		}
	}
	publish()
	requestCtx := ctx
	if deadline, ok := ctx.Deadline(); batchFilter && ok {
		reserve := min(time.Second, time.Until(deadline)/3)
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-reserve))
		defer cancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if in.resourceBatch != nil {
		in.resourceBatch.attach(ctx, configs)
	}
	additional := snapshotAdditionalFilter(in.Policy.FilterParams)
	updates := make(chan keywordSiteUpdate, len(configs)*2)
	workflowID := out.WorkflowID
	for index, config := range configs {
		go func() {
			start := time.Now()
			prepare := func(candidates []Candidate, status SiteStatus) {
				if ctx.Err() != nil {
					return
				}
				candidates, err := additional.apply(ctx, candidates)
				count := len(candidates)
				if err == nil && !batchFilter {
					prepared, failure := filter.applyNative(ctx, a, SearchOutput{Candidates: candidates, WorkflowID: workflowID})
					candidates, err = prepared.Candidates, failure
					sortSearchCandidates(candidates, in.Policy)
				}
				if ctx.Err() != nil {
					return
				}
				select {
				case updates <- keywordSiteUpdate{index, candidates, count, status, err}:
				case <-ctx.Done():
				}
			}
			result := run(ctx, config, func(candidates []Candidate) { prepare(candidates, SiteStatus{}) })
			result.status.ID = config.ID
			result.status.ElapsedMS = time.Since(start).Milliseconds()
			prepare(result.candidates, result.status)
		}()
	}

	var pending []Candidate
	var pluginPages [][]Candidate
	if batchFilter {
		pluginPages = make([][]Candidate, len(configs))
	}
	counts := make([]int, len(configs))
	done := 0
	apply := func(update keywordSiteUpdate) error {
		if update.err != nil {
			out.Candidates = []Candidate{}
			return update.err
		}
		counts[update.index] += update.count
		status := &out.Sites[update.index]
		if update.status.ID != "" {
			done++
			*status = update.status
			if in.Policy.FilterParams != nil {
				status.Count = counts[update.index]
			}
			a.logSite(*status, out.WorkflowID)
		} else {
			status.Count = counts[update.index]
		}
		if batchFilter {
			pluginPages[update.index] = append(pluginPages[update.index], update.candidates...)
			if update.status.ID != "" {
				pending = append(pending, pluginPages[update.index]...)
				pluginPages[update.index] = nil
			}
		} else {
			out.Candidates = mergeKeywordCandidates(out.Candidates, update.candidates)
		}
		return nil
	}
	for done < len(configs) {
		select {
		case update := <-updates:
			if err := apply(update); err != nil {
				return out, err
			}
			publish()
		case <-ctx.Done():
			// Keep every prepared page that reached the queue before the clock
			// expired. Canceled workers own their data and may finish unwinding
			// independently without touching the collector or HTTP stream.
			for n := len(updates); n > 0; n-- {
				if err := apply(<-updates); err != nil {
					return out, err
				}
			}
			for i, status := range out.Sites {
				if status.Status == "searching" {
					out.Sites[i] = a.searchStatus(ctx, status.ID, status.Count, ctx.Err(), 0)
				}
			}
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return out, ctx.Err()
			}
			done = len(configs)
		}
	}
	for _, remaining := range pluginPages {
		pending = append(pending, remaining...)
	}
	if batchFilter && len(pending) != 0 {
		input := out
		input.Candidates = pending
		result, err := a.titleFilterWithGroups(requestCtx, filter, input, groups)
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return out, bad("SEARCH_FILTER_TIMEOUT", "搜索结果筛选未在时限内完成")
		}
		if err != nil {
			return out, err
		}
		out.Candidates = result.Candidates
		sortSearchCandidates(out.Candidates, in.Policy)
	}
	out.keywordFilterPending = false
	return out, nil
}

// Both inputs are already ranked. On ties keep earlier arrivals first, matching
// the stable final ordering without re-ranking old pages for every site.
func mergeKeywordCandidates(previous, page []Candidate) []Candidate {
	if len(page) == 0 {
		return previous
	}
	if len(previous) == 0 {
		return page
	}
	merged := make([]Candidate, 0, len(previous)+len(page))
	for len(previous) != 0 && len(page) != 0 {
		if searchCandidateBefore(&page[0], &previous[0]) {
			merged = append(merged, page[0])
			page = page[1:]
		} else {
			merged = append(merged, previous[0])
			previous = previous[1:]
		}
	}
	merged = append(merged, previous...)
	return append(merged, page...)
}
