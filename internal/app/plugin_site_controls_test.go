package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestSubscriptionSiteControlsOriginalFallbackAndDeletion(t *testing.T) {
	for _, sample := range []struct{ defaults, selected, want []string }{{nil, nil, nil}, {[]string{"a"}, nil, []string{"a"}}, {nil, []string{"b"}, []string{"b"}}, {[]string{"a", "c"}, []string{"b", "c"}, []string{"c"}}, {[]string{"a"}, []string{"b"}, []string{"a"}}} {
		if got := subscriptionSiteControls(sample.defaults, sample.selected); !reflect.DeepEqual(got, sample.want) {
			t.Fatal("original subscription fallback differs", got, sample.want)
		}
	}
	a := retainedCoreSiteControlsFixture(t)
	ctx := context.Background()
	c := a.Settings()
	c.IndexerSites = []string{"s1"}
	c.RSSSites = []string{"s1", "s2"}
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "subscription", "owned-sub", Subscription{ID: "owned-sub", SiteIDs: []string{"s1"}, Revision: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.deleteManagedSite(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	var sub Subscription
	a.Store.Get(ctx, "subscription", "owned-sub", &sub)
	if len(sub.SiteIDs) != 0 || sub.Revision != 4 || len(a.Settings().IndexerSites) != 0 || !reflect.DeepEqual(a.Settings().RSSSites, []string{"s2"}) {
		t.Fatal("site deletion retained stale references")
	}
}

func retainedCoreSiteControlsFixture(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	first, second := &atomic.Int32{}, &atomic.Int32{}
	server := func(counter *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			fmt.Fprint(w, workflowHTML("Synthetic.Film.2024.1080p", "", false))
		}))
	}
	one, two := server(first), server(second)
	t.Cleanup(one.Close)
	t.Cleanup(two.Close)
	c := a.Settings()
	c.Sites = []SiteConfig{parallelSearchSite("s1", one.URL), parallelSearchSite("s2", two.URL)}
	c.Sites[1].Enabled = false
	c.Sites[0].Priority = 5
	c.Sites[1].Priority = 1
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return a
}
