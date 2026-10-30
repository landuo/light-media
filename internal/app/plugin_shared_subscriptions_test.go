package app

import (
	"context"
	"encoding/json"
	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"testing"
)

var sharedSubscriptionsAll = []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsList, plugins.SharedSubscriptionsListByTMDBID, plugins.SharedSubscriptionsUpdate}

func seedSharedSubscription(t *testing.T, a *App, id string) Subscription {
	t.Helper()
	s := Subscription{ID: id, Media: catalog.Media{ID: 7001, MediaType: "tv", Title: "Owned S05", Year: 2026}, Season: 1, TotalEpisodes: 12, StartEpisode: 1, SourceState: "N", State: "paused", Revision: 1, Schedule: Schedule{Mode: "manual"}, CreatedAt: now()}
	if e := a.Store.Put(context.Background(), "subscription", id, s); e != nil {
		t.Fatal(e)
	}
	return s
}
func decodeSharedSubscription(t *testing.T, b json.RawMessage) plugins.SharedSubscriptionV1 {
	t.Helper()
	var s plugins.SharedSubscriptionV1
	if e := json.Unmarshal(b, &s); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestSharedSubscriptionsActualWorkerSmoke(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, sharedSubscriptionsAll)
	seedSharedSubscription(t, a, "owned-s05-smoke")
	for _, q := range []struct {
		method string
		input  any
	}{
		{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-s05-smoke"}},
		{plugins.SharedSubscriptionsList, plugins.SharedSubscriptionListV1{}},
		{plugins.SharedSubscriptionsListByTMDBID, plugins.SharedSubscriptionTMDBV1{TMDBID: json.RawMessage(`7001`), Season: json.RawMessage(`1`)}},
	} {
		b, e := sharedSubscriptionsCall(t, p, q.method, q.input)
		if e != nil {
			t.Fatalf("%s: %v", q.method, e)
		}
		t.Logf("%s %s", q.method, b)
	}
	b, e := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-s05-smoke", Patch: json.RawMessage(`{"keyword":"worker-persisted","note":{"ordinary":true},"state":"CUSTOM","tmdbid":-1}`)})
	if e != nil {
		t.Fatal(e)
	}
	got := decodeSharedSubscription(t, b)
	if got.Revision != 2 {
		t.Fatalf("revision %d", got.Revision)
	}
	var native Subscription
	if e = a.Store.Get(context.Background(), "subscription", "owned-s05-smoke", &native); e != nil {
		t.Fatal(e)
	}
	if native.Keyword != "worker-persisted" || native.SourceState != "CUSTOM" || native.Media.ID != -1 {
		t.Fatalf("native not updated: %#v", native)
	}
	t.Logf("updated fresh projection %s", b)
	snap := sharedSubscriptionsSnapshot(t, p)
	t.Logf("actual worker snapshot %s", mustCleanSeedJSON(snap))
}
