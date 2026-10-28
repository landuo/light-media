package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func verifyOwnedProductionCoreSiteDeleted(t *testing.T, dir string, request func(string, string, any, int) []byte, stop, start func()) {
	t.Helper()
	oraclePath := os.Getenv("PLUGIN_PROCESS_SITE_CORE_ORACLE")
	if oraclePath == "" {
		oraclePath = "../../testdata/standard-event-target/site-deleted-consumer-oracle.json"
	}
	raw, err := os.ReadFile(oraclePath)
	type sourceState struct {
		Settings      map[string][]int
		Subscriptions []struct {
			ID    int
			Sites []int
		}
		Updates []struct{ ID int }
	}
	var oracle struct {
		Cases   []sourceState
		Failure sourceState `json:"failure_case"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 8 || len(oracle.Cases[0].Subscriptions) != 3 || len(oracle.Failure.Subscriptions) != 3 {
		t.Fatal("fixed concrete site consumer source", err)
	}
	mapSites := func(source []int) []string {
		if source == nil {
			return nil
		}
		out := []string{}
		for _, id := range source {
			if id == 1 {
				out = append(out, "core-event-a")
			} else if id == 2 {
				out = append(out, "core-event-b")
			} else {
				t.Fatal("unknown source reference identity")
			}
		}
		return out
	}
	peer := func() *httptest.Server {
		p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("site reference event performed unrelated network operation")
			w.WriteHeader(500)
		}))
		return p
	}
	one, two := peer(), peer()
	defer one.Close()
	defer two.Close()
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	c := seed.Settings()
	c.Sites = []SiteConfig{parallelSearchSite("core-event-a", one.URL), parallelSearchSite("core-event-b", two.URL)}
	c.IndexerSites = []string{"core-event-a", "core-event-b"}
	c.RSSSites = []string{"core-event-a", "core-event-b"}
	if err = seed.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	identity, err := seed.Store.nativeSiteByAppID(context.Background(), "core-event-a")
	if err != nil {
		t.Fatal(err)
	}
	for index, sites := range [][]string{{"core-event-a", "core-event-b"}, {"core-event-b"}} {
		id := []string{"owned-core-site-1", "owned-core-site-2"}[index]
		sub := Subscription{ID: id, SiteIDs: sites, Revision: 3, State: "paused", SourceState: "S", Media: catalog.Media{ID: int64(900061 + index), MediaType: "movie", Title: "Owned reference event"}}
		if err = seed.Store.Put(context.Background(), "subscription", id, sub); err != nil {
			t.Fatal(err)
		}
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	send := func(id string, site any) {
		request("POST", "/api/v1/plugin/"+id+"/input", map[string]any{"operation": "event.send", "input": map[string]any{"kind": "broadcast", "type": "site.deleted", "data": map[string]any{"site_id": site}}}, 200)
	}
	read := func() (map[string]Subscription, map[string]any) {
		body := request("GET", "/api/subscriptions", nil, 200)
		var response struct {
			Data struct{ Items []Subscription }
		}
		if json.Unmarshal(body, &response) != nil {
			t.Fatal("actual reference event subscription readback")
		}
		subs := map[string]Subscription{}
		for _, s := range response.Data.Items {
			if s.ID == "owned-core-site-1" || s.ID == "owned-core-site-2" {
				subs[s.ID] = s
			}
		}
		settings := request("GET", "/api/settings", nil, 200)
		var configured struct {
			Data struct {
				Indexer []string `json:"indexer_sites"`
				RSS     []string `json:"rss_sites"`
				Sites   []struct{ ID string }
			}
		}
		if json.Unmarshal(settings, &configured) != nil {
			t.Fatal("actual reference settings readback")
		}
		ids := []string{}
		for _, s := range configured.Data.Sites {
			ids = append(ids, s.ID)
		}
		return subs, map[string]any{"indexer": configured.Data.Indexer, "rss": configured.Data.RSS, "sites": ids}
	}
	wait := func(partial bool) (map[string]Subscription, map[string]any) {
		deadline := time.Now().Add(10 * time.Second)
		for {
			subs, settings := read()
			first, second := subs["owned-core-site-1"], subs["owned-core-site-2"]
			expected := oracle.Cases[0]
			revision := 3 + len(expected.Updates)
			if partial {
				expected = oracle.Failure
				revision += len(expected.Updates)
			}
			if first.Revision == revision && reflect.DeepEqual(first.SiteIDs, mapSites(expected.Subscriptions[0].Sites)) && second.Revision == 3 && reflect.DeepEqual(second.SiteIDs, mapSites(expected.Subscriptions[1].Sites)) {
				if !reflect.DeepEqual(settings["indexer"], mapSites(expected.Settings["indexer"])) || !reflect.DeepEqual(settings["rss"], mapSites(expected.Settings["rss"])) || !reflect.DeepEqual(settings["sites"], []string{"core-event-a", "core-event-b"}) {
					if time.Now().Before(deadline) {
						time.Sleep(20 * time.Millisecond)
						continue
					}
					t.Fatal("actual settings/identity source effect", settings)
				}
				return subs, settings
			}
			if time.Now().After(deadline) {
				t.Fatal("actual core reference consumer missing", partial, first, second)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	send("OwnedInput", identity.publicSourceID())
	normalSubs, normalSettings := wait(false)
	stop()
	inspector, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = inspector.Store.DB.Exec(`CREATE TRIGGER owned_core_site_partial BEFORE UPDATE ON records WHEN NEW.kind='subscription' AND NEW.id='owned-core-site-2' BEGIN SELECT RAISE(ABORT,'owned second update failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = inspector.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	send("OwnedInputservice", "*")
	partialSubs, partialSettings := wait(true)
	deadline := time.Now().Add(10 * time.Second)
	for {
		body := request("GET", "/api/v1/plugin/OwnedInput/events", nil, 200)
		var response struct {
			Events []struct {
				Type string
				Data struct {
					Handle string `json:"event_handle"`
				}
			}
		}
		json.Unmarshal(body, &response)
		found := false
		for _, event := range response.Events {
			found = found || event.Type == "system.error" && event.Data.Handle == "go.core.Subscribe.remove_site"
		}
		if found {
			writeOwnedEventsProcessEvidence(t, "events36-core-site-error.json", body)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual partial failure produced no ordinary event error")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	inspector, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = inspector.Store.DB.Exec(`DROP TRIGGER owned_core_site_partial`); err != nil {
		t.Fatal(err)
	}
	if err = inspector.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	coldSubs, coldSettings := wait(true)
	evidence, _ := json.Marshal(map[string]any{"kind": "actual cmd/server Event.Send consumers; only initial selected refs/subscription rows and failure trigger are owned synthetic inputs", "normal": map[string]any{"subscriptions": normalSubs, "settings": normalSettings}, "partial_failure": map[string]any{"subscriptions": partialSubs, "settings": partialSettings}, "cold": map[string]any{"subscriptions": coldSubs, "settings": coldSettings}})
	writeOwnedEventsProcessEvidence(t, "events36-core-site-states.json", evidence)
	t.Log("actual Linux SDK source numeric SiteDeleted and clone wildcard -> existing search/RSS/subscription reference effects; no configured site/network deletion; second real SQLite update failure retains prior commits and emits ordinary event error; cold retains partial state")
}
