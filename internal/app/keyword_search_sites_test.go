package app

import (
	"context"
	"strconv"
	"testing"
	"time"

	"light-media/internal/sites"
)

func TestKeywordDeadlineKeepsReceivedPageAndReturnsBeforeWorkerExits(t *testing.T) {
	a := newTestApp(t)
	a.config.Sites = []SiteConfig{parallelSearchSite("ongoing", "https://fixture.example")}
	a.invalidateSettings()
	release, exited := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan SearchOutput, 1)
	go func() {
		result, err := a.searchKeywordSites(ctx, SearchRequest{}, SearchOutput{}, func(_ context.Context, s SiteConfig, emit func([]Candidate)) siteSearchOutput {
			emit([]Candidate{{Resource: sites.Resource{ID: "1", SiteID: s.ID}}})
			<-release // Deliberately slow to notice cancellation after publishing.
			close(exited)
			return siteSearchOutput{status: SiteStatus{Status: "ok", Count: 1}}
		})
		if err != nil {
			t.Errorf("deadline should give partial results: %v", err)
		}
		done <- result
	}()
	defer func() {
		close(release)
		<-exited
	}()
	select {
	case result := <-done:
		if len(result.Candidates) != 1 || len(result.Sites) != 1 || result.Sites[0].Status != "partial" || result.Sites[0].Count != 1 {
			t.Fatalf("lost a received page or reported the worker as complete: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deadline was held open by a worker")
	}
}

func TestKeywordPagesDedupeBeforeEvaluationAndRetainOtherSites(t *testing.T) {
	a := newTestApp(t)
	var early []Candidate
	page := keywordCandidatePage{seen: map[keywordResourceID]struct{}{}, emit: func(rows []Candidate) { early = append(early, rows...) }}
	rows := make([]sites.Resource, 18)
	for i := range rows {
		rows[i] = sites.Resource{ID: strconv.Itoa(i), SiteID: "one", Title: "Demo.2024." + strconv.Itoa(i)}
	}
	first := page.evaluate(context.Background(), a, Policy{}, rows)
	other := rows[0]
	other.SiteID = "two"
	second := page.evaluate(context.Background(), a, Policy{}, append(rows, other))
	if len(early) != 16 || len(first) != 2 || len(second) != 1 || second[0].Resource.SiteID != "two" || page.total != 19 {
		t.Fatalf("early=%d, first=%d, second=%+v, total=%d", len(early), len(first), second, page.total)
	}
}

func TestKeywordRankMergesSingleItemPagesInStableOrder(t *testing.T) {
	var results []Candidate
	for i, title := range []string{"A.1080p", "B.2160p", "C.2160p"} {
		page := []Candidate{{Resource: sites.Resource{ID: strconv.Itoa(i), Title: title}, Accepted: true}}
		sortSearchCandidates(page, Policy{PreferredResolutions: []string{"2160p"}})
		results = mergeKeywordCandidates(results, page)
	}
	if results[0].Resource.ID != "1" || results[1].Resource.ID != "2" || results[2].Resource.ID != "0" {
		t.Fatal("rank changed with page arrival order", results)
	}
}
