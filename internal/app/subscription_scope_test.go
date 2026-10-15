package app

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func TestSeasonSubscriptionStartFollowsNewEpisodesWithoutSelectingEach(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	r := subscriptionRequest(t)
	r.TargetScope, r.StartEpisode = "season", 3
	s, err := a.saveSubscription(ctx, "", r)
	if err != nil {
		t.Fatal(err)
	}
	if s.TargetScope != "season" || s.StartEpisode != 3 || len(s.Episodes) != 0 {
		t.Fatalf("not a growing season: %+v", s)
	}
	var persisted Subscription
	if err = a.Store.Get(ctx, "subscription", s.ID, &persisted); err != nil || persisted.StartEpisode != 3 {
		t.Fatal("threshold not persisted", err)
	}
	coverage, err := a.subscriptionCoverage(ctx, persisted, []int{1, 2, 3, 4, 5})
	if err != nil || !reflect.DeepEqual(coverage.Missing, []int{3, 4, 5}) {
		t.Fatalf("wanted wrong range %+v %v", coverage, err)
	}
	// The next provider refresh grows the same subscription, without a user
	// editing a snapshot of the initially known episode list.
	coverage, err = a.subscriptionCoverage(ctx, persisted, []int{1, 2, 3, 4, 5, 6})
	if err != nil || !reflect.DeepEqual(coverage.Missing, []int{3, 4, 5, 6}) {
		t.Fatalf("new episode not tracked %+v %v", coverage, err)
	}
	page, err := a.calendar(ctx, "2026-10-15", "2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range page.(map[string]any)["entries"].([]CalendarEntry) {
		if event.Confirmed && event.Episode < 3 {
			t.Fatalf("calendar included before threshold %+v", event)
		}
	}
	r.State, r.Revision, r.StartEpisode = "paused", s.Revision, 0
	paused, err := a.saveSubscription(ctx, s.ID, r)
	if err != nil || paused.StartEpisode != 3 {
		t.Fatalf("pause lost threshold %+v %v", paused, err)
	}
}

func TestSeasonSubscriptionFutureStartWaitsAndLegacyRangesStayBounded(t *testing.T) {
	a := subscriptionTestApp(t)
	r := subscriptionRequest(t)
	r.TargetScope, r.StartEpisode = "season", 10
	s, err := a.saveSubscription(context.Background(), "", r)
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.subscriptionCoverage(context.Background(), s, []int{1, 2, 3})
	if err != nil || len(c.Missing) != 0 || len(c.Known) != 0 {
		t.Fatalf("fabricated future range %+v %v", c, err)
	}
	s.StartEpisode, s.Episodes, s.TargetScope = 3, []int{1, 2}, "episodes"
	c, err = a.subscriptionCoverage(context.Background(), s, []int{1, 2, 3, 4})
	if err != nil || len(c.Missing) != 0 {
		t.Fatalf("legacy range expanded %+v %v", c, err)
	}
	for _, r := range []SubscriptionRequest{{TargetScope: "season", StartEpisode: -1}, {TargetScope: "season", StartEpisode: 10000}, {TargetScope: "season", Episodes: []int{1}}, {TargetScope: "movie"}} {
		if err := normalizeSubscriptionScope(&r, Subscription{}, "tv"); err == nil {
			t.Fatalf("invalid scope accepted %+v", r)
		}
	}
}

func TestUnifiedCatalogSearchReturnsMixedMediaWithoutTypeSelection(t *testing.T) {
	a := newTestApp(t)
	page := catalog.Page{Page: 1, TotalPages: 2, TotalResults: 30, Results: []catalog.Media{{ID: 11, MediaType: "movie", Title: "共同的名字"}, {ID: 12, MediaType: "tv", Title: "共同的名字"}}}
	if err := a.Store.Put(context.Background(), "cache", "search:::common:1", map[string]any{"at": time.Now(), "data": page}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"q=common", "query=common"} {
		value, err := a.catalogAPI(httptest.NewRequest("GET", "/api/catalog/search?"+query, nil), "search")
		if err != nil {
			t.Fatal(err)
		}
		out := value.(map[string]any)
		items := out["results"].([]catalog.Media)
		if len(items) != 2 || items[0].MediaType != "movie" || items[1].MediaType != "tv" || out["has_more"] != true {
			t.Fatalf("not unified %+v", out)
		}
	}
}
