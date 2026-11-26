package app

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"light-media/internal/sites"
)

func TestKeywordStreamEncoderPreservesSkippedUpdatesAndFinalOrder(t *testing.T) {
	var encoder keywordStreamEncoder
	output := func(rows ...Candidate) SearchOutput {
		return SearchOutput{WorkflowID: "one", CachedAt: "time", Sites: []SiteStatus{{ID: "site"}}, Candidates: rows}
	}
	first := encoder.frame(output())
	if first.Sequence != 1 || len(first.Added) != 0 || first.WorkflowID != "one" || first.CachedAt != "time" || len(first.Sites) != 1 {
		t.Fatalf("initial frame %+v", first)
	}
	rows := []Candidate{{Resource: sites.Resource{ID: "first"}}, {Resource: sites.Resource{ID: "skipped"}}, {Resource: sites.Resource{ID: "latest"}}}
	assignKeywordSlots(rows, 0)
	second := encoder.frame(output(rows[0]))
	if second.Sequence != 2 || len(second.Added) != 1 || second.Added[0].Resource.ID != "first" || !slices.Equal(second.Order, []int{0}) {
		t.Fatalf("first result %+v", second)
	}
	// The collector saw "skipped" earlier, but the connection never emitted its
	// snapshot. The last result sorts ahead of it and only arrives at completion.
	terminal := encoder.frame(output(rows[2], rows[0], rows[1]))
	if terminal.Sequence != 3 || len(terminal.Added) != 2 || terminal.Added[0].Resource.ID != "skipped" || terminal.Added[1].Resource.ID != "latest" || !slices.Equal(terminal.Order, []int{2, 0, 1}) {
		t.Fatalf("cannot reconstruct final result %+v", terminal)
	}
	if !slices.Equal(second.Order, []int{0}) {
		t.Fatal("overwrote a previous frame")
	}
	reordered := encoder.frame(output(rows[0], rows[1], rows[2]))
	if len(reordered.Added) != 0 || !slices.Equal(reordered.Order, []int{0, 1, 2}) {
		t.Fatal("reordered rows should require only slot indexes")
	}
	statusOnly := encoder.frame(output(rows[0], rows[1], rows[2]))
	raw, err := json.Marshal(statusOnly)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil || fields["candidates_added"] != nil || fields["candidate_order"] != nil || string(fields["sequence"]) != "5" {
		t.Fatal("status update repeated result data", string(raw), err)
	}
}

func TestKeywordStreamEncoderKeepsSeparatePluginRowsWithSameID(t *testing.T) {
	var encoder keywordStreamEncoder
	rows := []Candidate{{Resource: sites.Resource{ID: "1", Title: "before"}}, {Resource: sites.Resource{ID: "1", Title: "after"}}}
	assignKeywordSlots(rows, 0)
	frame := encoder.frame(SearchOutput{Candidates: rows})
	if len(frame.Added) != 2 || frame.Added[0].Resource.Title != "before" || frame.Added[1].Resource.Title != "after" || !slices.Equal(frame.Order, []int{0, 1}) {
		t.Fatal("lost distinct enriched rows in a plugin's final batch")
	}
}

func TestKeywordCollectorSlotsAssignedAcrossSortedPages(t *testing.T) {
	a := newTestApp(t)
	a.config.Sites = []SiteConfig{parallelSearchSite("only", "https://fixture.example")}
	a.invalidateSettings()
	ranked := func(id string, seeders int) Candidate {
		return Candidate{Accepted: true, Resource: sites.Resource{ID: id, SiteID: "only", Seeders: seeders}}
	}
	var frames []SearchOutput
	ctx := context.WithValue(context.Background(), searchProgressKey{}, func(snapshot SearchOutput) { frames = append(frames, snapshot) })
	out, err := a.searchKeywordSites(ctx, SearchRequest{}, SearchOutput{}, func(_ context.Context, site SiteConfig, emit func([]Candidate)) siteSearchOutput {
		emit([]Candidate{ranked("earlier", 2)})
		emit([]Candidate{ranked("skipped", 1)})
		return siteSearchOutput{status: SiteStatus{ID: site.ID, Status: "ok", Count: 3}, candidates: []Candidate{ranked("fastest", 3)}}
	})
	if err != nil || len(frames) < 2 {
		t.Fatal("did not collect pages", err)
	}
	var encoder keywordStreamEncoder
	first := encoder.frame(frames[1])
	last := encoder.frame(out)
	if len(first.Added) != 1 || first.Added[0].Resource.ID != "earlier" || len(last.Added) != 2 || last.Added[0].Resource.ID != "skipped" || last.Added[1].Resource.ID != "fastest" || !slices.Equal(last.Order, []int{2, 0, 1}) {
		t.Fatal("collector and wire indexes disagree", first, last)
	}
}
