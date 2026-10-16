package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/decision"
	"light-media/internal/sites"
	"light-media/internal/torrentfilter"
)

func TestOriginalSubscriptionFiltersPersistAndAffectRealSearchBeforeProgress(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	fragments := []string{}
	for i, title := range []string{"Jade.Dynasty.S04E01.2160p.WEB-DL.HDR10", "Jade.Dynasty.S04E01.1080p.WEB-DL.HDR10", "Jade.Dynasty.S04E01.2160p.BluRay.HDR10", "Jade.Dynasty.S04E01.2160p.WEB-DL.SDR"} {
		fragments = append(fragments, strings.ReplaceAll(workflowHTML(title, "", true), "id=101", "id="+strconv.Itoa(i+1)))
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/torrents.php" {
			t.Error("filter search requested private material", r.URL.Path)
		}
		io.WriteString(w, keywordFixtureListing(fragments...))
	}))
	defer site.Close()
	c := a.Settings()
	c.Sites = []SiteConfig{{Config: sites.Config{ID: "filter-fixture", Kind: "jiayuan", Name: "Filter fixture", BaseURL: site.URL, Cookie: "synthetic-cookie"}, Enabled: true, DailyBudget: 100}}
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	request := subscriptionRequest(t)
	request.Policy = Policy{Resolution: "1080p", Source: "BluRay", HDR: "sdr", FilterParams: &torrentfilter.Parameters{Resolution: priorityText("4K|2160p|x2160"), Quality: priorityText("WEB-?DL|WEB-?RIP"), Effect: priorityText("HDR10|HDR10\\+")}}
	raw, _ := json.Marshal(request)
	w := callTest(t, a.Handler(), "POST", "/api/subscriptions", string(raw), cookie, "")
	if w.Code != 200 {
		t.Fatal("original filter API save failed", w.Code, w.Body.String())
	}
	var response struct {
		Data Subscription `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatal("bad saved subscription response")
	}
	sub := response.Data
	if !reflect.DeepEqual(sub.Policy.FilterParams, request.Policy.FilterParams) {
		t.Fatal("stored filters changed representation")
	}
	var progresses [][]string
	ctx = context.WithValue(ctx, searchProgressKey{}, func(out SearchOutput) {
		ids := priorityCandidateIDs(out)
		if len(ids) > 0 {
			progresses = append(progresses, ids)
		}
	})
	search := SearchRequest{Query: "Jade Dynasty", MediaType: "tv", TMDBID: 206484, Season: 4, Episodes: []int{1}, Policy: sub.Policy}
	for _, identity := range []bool{false, true} {
		var out SearchOutput
		var err error
		if identity {
			out, err = a.search(ctx, search, true)
		} else {
			out, err = a.searchResources(ctx, search)
		}
		if err != nil || !reflect.DeepEqual(priorityCandidateIDs(out), []string{"1"}) || !out.Candidates[0].Accepted {
			t.Fatal("original regex didn't affect actual search", identity, priorityCandidateIDs(out), err)
		}
	}
	for _, ids := range progresses {
		if !reflect.DeepEqual(ids, []string{"1"}) {
			t.Fatal("unfiltered row reached streaming results", ids)
		}
	}
	// Preserve source lazy error semantics: an earlier nonmatch does not
	// evaluate a malformed later quality expression. A visited error clears
	// all published candidates and identifies the field without its pattern.
	search.Policy.FilterParams = &torrentfilter.Parameters{Include: priorityText("NO_MATCH"), Quality: priorityText("private-synthetic-broken(")}
	if out, err := a.searchResources(ctx, search); err != nil || len(out.Candidates) != 0 {
		t.Fatal("unvisited invalid filter evaluated", err)
	}
	search.Policy.FilterParams.Include = nil
	var last SearchOutput
	failureCtx := context.WithValue(context.Background(), searchProgressKey{}, func(out SearchOutput) { last = out })
	if out, err := a.searchResources(failureCtx, search); pluginErrorCode(err) != "SUBSCRIPTION_FILTER_FAILED" || len(out.Candidates) != 0 || len(last.Candidates) != 0 || strings.Contains(err.Error(), "private-synthetic-broken") {
		t.Fatal("filter error published raw rows or exposed expression", err)
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('intent','native_download','material_blob')`).Scan(&count)
	if count != 0 {
		t.Fatal("filter validation created downloads")
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var stored Subscription
	if err = reopened.Store.Get(context.Background(), "subscription", sub.ID, &stored); err != nil || !reflect.DeepEqual(stored.Policy.FilterParams, request.Policy.FilterParams) {
		t.Fatal("original filters not preserved across actual restart", err)
	}
}
func TestAdditionalFilterSnapshotAndErrorsDoNotPublishUnfilteredResults(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		io.WriteString(w, priorityListing())
	}))
	defer site.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	workflowConfigure(t, a, site.URL, "http://127.0.0.1:1")
	p := &torrentfilter.Parameters{Quality: priorityText("WEB-?DL")}
	done := make(chan SearchOutput, 1)
	failure := make(chan error, 1)
	go func() {
		out, err := a.searchResources(ctx, SearchRequest{Query: "Synthetic", Policy: Policy{Source: "BluRay", FilterParams: p}})
		done <- out
		failure <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("search did not enter actual site")
	}
	p.Quality = nil
	p.Effect = priorityText("private-mutated-pattern(")
	close(release)
	select {
	case out := <-done:
		if err := <-failure; err != nil || len(out.Candidates) != 2 {
			t.Fatal("in-flight source filter mutated", priorityCandidateIDs(out), err)
		}
		for _, candidate := range out.Candidates {
			if !candidate.Accepted {
				t.Fatal("in-flight presence/legacy masking changed", candidate.Reasons)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("search did not finish")
	}
	filter := snapshotAdditionalFilter(&torrentfilter.Parameters{Quality: priorityText("private-filter-pattern(")})
	if rows, err := filter.apply(ctx, []Candidate{{Resource: sites.Resource{Title: "Example", Size: 1, Seeders: 1}}}); rows != nil || pluginErrorCode(err) != "SUBSCRIPTION_FILTER_FAILED" || strings.Contains(err.Error(), "private-filter-pattern") {
		t.Fatal("invalid matcher returned rows or exposed expression", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if rows, err := snapshotAdditionalFilter(&torrentfilter.Parameters{}).apply(cancelled, []Candidate{{}}); rows != nil || err == nil {
		t.Fatal("cancelled filter accepted rows")
	}
}
func TestOriginalFiltersUseRawTitleBeforeRecognitionAndKeepLegacyPolicies(t *testing.T) {
	a := newTestApp(t)
	if err := a.saveRecognitionWords(context.Background(), 0, []string{"RAW_SOURCE => Synthetic"}); err != nil {
		t.Fatal(err)
	}
	resource := sites.Resource{Title: "RAW_SOURCE.2026.2160p.WEB-DL.HDR10", Size: 1 << 30, Seeders: 10}
	target := decision.Target{Media: catalog.Media{ID: 123, MediaType: "movie", Title: "Synthetic", OriginalTitle: "Synthetic", Year: 2026}, Auto: true}
	p := decisionPolicy(Policy{FilterParams: &torrentfilter.Parameters{Include: priorityText("RAW_SOURCE"), Resolution: priorityText("2160p")}})
	if choice := a.evaluateResource(resource, target, p); !choice.Accepted {
		t.Fatal("filters ran after name rewriting", choice.Reasons)
	}
	pack := resource
	pack.Title = "RAW_SOURCE.S01.2160p.WEB-DL.HDR10"
	series := decision.Target{Media: catalog.Media{ID: 123, MediaType: "tv", Title: "Synthetic", OriginalTitle: "Synthetic"}, Season: 1, Episodes: []int{1}, KnownEpisodes: []int{1}, AiredEpisodes: []int{1}, Auto: true}
	if !a.eligibleAutomaticSeasonPack(pack, series, Policy{FilterParams: &torrentfilter.Parameters{Include: priorityText("RAW_SOURCE")}}) {
		t.Fatal("season pack filters used rewritten rather than raw title")
	}
	if a.eligibleAutomaticSeasonPack(pack, series, Policy{FilterParams: &torrentfilter.Parameters{Include: priorityText("Synthetic")}}) {
		t.Fatal("season pack bypassed raw include filter")
	}
	if choice := decision.EvaluateResource(resource, decisionPolicy(Policy{Resolution: "1080p"})); choice.Accepted {
		t.Fatal("legacy enum filter silently changed")
	}
	if choice := decision.EvaluateResource(resource, decisionPolicy(Policy{Resolution: "1080p", FilterParams: &torrentfilter.Parameters{Resolution: priorityText("")}})); !choice.Accepted {
		t.Fatal("explicit original empty did not override legacy value", choice.Reasons)
	}
}
