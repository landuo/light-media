package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"light-media/internal/plugins"
)

func ownedCoreSiteDeletedFixture(t *testing.T) (*App, *http.Cookie) {
	t.Helper()
	a, cookie := installOwnedInputFixture(t)
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	peer := func() *httptest.Server {
		p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("reference cleanup performed unrelated site network I/O")
			w.WriteHeader(500)
		}))
		t.Cleanup(p.Close)
		return p
	}
	c := a.Settings()
	c.Sites = []SiteConfig{parallelSearchSite("owned-a", peer().URL), parallelSearchSite("owned-b", peer().URL)}
	c.IndexerSites = []string{"owned-a", "owned-b"}
	c.RSSSites = []string{"owned-a", "owned-b"}
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if identityRequire(t, a, "owned-a").SourceID != 1 || identityRequire(t, a, "owned-b").SourceID != 2 {
		t.Fatal("owned source numeric identity setup")
	}
	for _, sub := range []Subscription{{ID: "owned-1", Revision: 3, SiteIDs: []string{"owned-a", "owned-b"}}, {ID: "owned-2", Revision: 3, SiteIDs: []string{"owned-b"}}, {ID: "owned-3", Revision: 3}} {
		if err := a.Store.Put(context.Background(), "subscription", sub.ID, sub); err != nil {
			t.Fatal(err)
		}
	}
	return a, cookie
}

func TestOriginalCoreSiteDeletedActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/site-deleted-consumer-oracle.json")
	var oracle struct {
		Cases []struct {
			Site          json.RawMessage `json:"site_id"`
			Settings      map[string][]int
			Subscriptions []struct {
				ID    int
				Sites []int
			}
			Updates []struct{ ID int }
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 8 {
		t.Fatal("fixed site core consumer oracle", err)
	}
	for index, sample := range oracle.Cases {
		t.Run(string(sample.Site), func(t *testing.T) {
			a, cookie := ownedCoreSiteDeletedFixture(t)
			input, _ := json.Marshal(plugins.EventSendRequest{Kind: plugins.BroadcastEventKind, Type: "site.deleted", Data: append(append([]byte(`{"site_id":`), sample.Site...), '}')})
			ownedSDKEventCall(t, a, cookie, "event.send", string(input))
			a.wg.Wait()
			mapSites := func(ids []int) []string {
				if ids == nil {
					return nil
				}
				out := []string{}
				for _, id := range ids {
					if id == 1 {
						out = append(out, "owned-a")
					} else if id == 2 {
						out = append(out, "owned-b")
					} else {
						t.Fatal("unmapped source ID")
					}
				}
				return out
			}
			if !reflect.DeepEqual(a.Settings().IndexerSites, mapSites(sample.Settings["indexer"])) || !reflect.DeepEqual(a.Settings().RSSSites, mapSites(sample.Settings["rss"])) || len(a.Settings().Sites) != 2 {
				t.Fatal("source core site settings effect", index, a.Settings().IndexerSites, a.Settings().RSSSites)
			}
			for _, row := range sample.Subscriptions {
				var sub Subscription
				id := []string{"owned-1", "owned-2", "owned-3"}[row.ID-1]
				if err := a.Store.Get(context.Background(), "subscription", id, &sub); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(sub.SiteIDs, mapSites(row.Sites)) {
					t.Fatal("source core subscription effect", index, id, sub.SiteIDs, row.Sites)
				}
				revision := 3
				for _, update := range sample.Updates {
					if update.ID == row.ID {
						revision++
					}
				}
				if sub.Revision != revision {
					t.Fatal("revision did not invalidate exactly changed scope", sub.Revision, revision)
				}
			}
			for _, id := range []string{"owned-a", "owned-b"} {
				if identityRequire(t, a, id).State != "active" {
					t.Fatal("event deleted configured site")
				}
			}
		})
	}
}

func TestOriginalCoreSiteDeletedKeepsEarlierSourceCommitsOnFailure(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/site-deleted-consumer-oracle.json")
	var oracle struct {
		Failure struct {
			Site          string `json:"site_id"`
			Exception     string
			Settings      map[string][]int
			Subscriptions []struct {
				ID    int
				Sites []int
			}
		} `json:"failure_case"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Failure.Site != "*" || oracle.Failure.Exception != "RuntimeError" || len(oracle.Failure.Settings["indexer"]) != 0 || len(oracle.Failure.Settings["rss"]) != 0 || len(oracle.Failure.Subscriptions) != 3 || len(oracle.Failure.Subscriptions[0].Sites) != 0 || !reflect.DeepEqual(oracle.Failure.Subscriptions[1].Sites, []int{2}) {
		t.Fatal("fixed partial failure source replay", err)
	}
	a, cookie := ownedCoreSiteDeletedFixture(t)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_site_ref_fail BEFORE UPDATE ON records WHEN NEW.kind='subscription' AND NEW.id='owned-2' BEGIN SELECT RAISE(ABORT,'owned reference failure'); END`); err != nil {
		t.Fatal(err)
	}
	ownedSDKEventCall(t, a, cookie, "event.send", `{"kind":"broadcast","type":"site.deleted","data":{"site_id":"*"}}`)
	a.wg.Wait()
	var first, second Subscription
	a.Store.Get(context.Background(), "subscription", "owned-1", &first)
	a.Store.Get(context.Background(), "subscription", "owned-2", &second)
	if len(a.Settings().IndexerSites) != 0 || len(a.Settings().RSSSites) != 0 || len(first.SiteIDs) != 0 || !reflect.DeepEqual(second.SiteIDs, []string{"owned-b"}) || first.Revision != 4 || second.Revision != 3 || len(a.Settings().Sites) != 2 {
		t.Fatal("later failure rolled back earlier source effects", a.Settings().IndexerSites, a.Settings().RSSSites, first, second)
	}
	found := false
	for _, raw := range ownedInputEvents(t, a, cookie, "OwnedInput") {
		var event struct{ Type string }
		if json.Unmarshal(raw, &event) == nil && event.Type == "system.error" {
			found = true
		}
	}
	if !found {
		t.Fatal("failed concrete consumer did not publish ordinary event error")
	}
}
