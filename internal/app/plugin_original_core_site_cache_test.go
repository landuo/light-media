package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestOriginalSiteCacheDomainAndClearFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/site-updated-consumer-oracle.json")
	var oracle struct {
		Domains []struct {
			Input, Host string
			Error       *string `json:"host_error"`
		} `json:"domain_cases"`
		Clear []struct {
			Domain    string
			Fail      *string
			Deleted   []string
			Exception *string
		} `json:"clear_cases"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Domains) != 7 || len(oracle.Clear) != 5 {
		t.Fatal("fixed cache clear/domain source", err)
	}
	for _, sample := range oracle.Domains {
		value, err := standardSiteEventHost(sample.Input)
		if value != sample.Host || (err != nil) != (sample.Error != nil) {
			t.Fatal("fixed get_url_host", sample.Input, value, err, sample.Host, sample.Error)
		}
	}
	for _, sample := range oracle.Clear {
		t.Run(sample.Domain+"/"+strings.Join(sample.Deleted, ","), func(t *testing.T) {
			a := newTestApp(t)
			c := a.Settings()
			origins := []string{"https://a.foo.org", "https://a.foobar.org", "https://a.other.org", "http://127.0.0.1:43100"}
			ids := []string{"cache-foo", "cache-foobar", "cache-other", "cache-ip"}
			keys := []string{"site.foo.token", "site.foobar.token", "site.other.token", "site.0.token"}
			for i, origin := range origins {
				site := parallelSearchSite(ids[i], origin)
				site.Kind = "ubits" // existing allowed loopback adapter for the IP fixture
				c.Sites = append(c.Sites, site)
			}
			if err := a.saveSettings(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			for _, id := range ids {
				if err := a.Store.Put(context.Background(), "site_cooldown", id, map[string]int64{"until": 4102444800}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Store.Put(context.Background(), "site_stats_history", "must-stay", map[string]bool{"kept": true}); err != nil {
				t.Fatal(err)
			}
			if sample.Fail != nil {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_clear_failure BEFORE DELETE ON records WHEN OLD.kind='site_cooldown' AND OLD.id='cache-foobar' BEGIN SELECT RAISE(ABORT,'owned second cache failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := json.Marshal(map[string]string{"domain": sample.Domain})
			err := a.consumeOriginalCoreSiteDataClear(context.Background(), raw)
			if (err != nil) != (sample.Exception != nil) {
				t.Fatal("fixed per-key failure boundary", err, sample.Exception)
			}
			for i, id := range ids {
				want := 1
				for _, key := range sample.Deleted {
					if key == keys[i] {
						want = 0
					}
				}
				var count int
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='site_cooldown' AND id=?`, id).Scan(&count); err != nil || count != want {
					t.Fatal("source prefix/partial commit cache effect", id, count, want, err)
				}
			}
			var history int
			if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='site_stats_history' AND id='must-stay'`).Scan(&history); err != nil || history != 1 || len(a.Settings().Sites) != 4 {
				t.Fatal("cache clear changed account history or configured sites", err)
			}
		})
	}
}

func TestOriginalSiteFaviconFixedSourceActualHTTPEncryptedCache(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/site-updated-consumer-oracle.json")
	var oracle struct {
		Cases []struct {
			Name, Page, Chosen string
			HomeStatus         int     `json:"home_status"`
			IconStatus         int     `json:"icon_status"`
			Data               *string `json:"data_base64"`
			Requests           []struct{ URL string }
		} `json:"favicon_cases"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 7 {
		t.Fatal("fixed favicon source", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			requests := []string{}
			var mu sync.Mutex
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "owned-site-cookie" || r.Header.Get("User-Agent") != originalPluginServerUserAgent() {
					t.Error("source icon cookie/UA missing")
				}
				mu.Lock()
				requests = append(requests, r.URL.Path)
				mu.Unlock()
				if r.URL.Path == "/" {
					w.WriteHeader(sample.HomeStatus)
					_, _ = w.Write([]byte(sample.Page))
					return
				}
				w.WriteHeader(sample.IconStatus)
				_, _ = w.Write([]byte("owned-icon"))
			}))
			defer peer.Close()
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			site := installResourceBrowseSite(t, a, peer.URL)
			raw, _ := json.Marshal(map[string]string{"domain": peer.URL})
			if err := a.consumeOriginalCoreSiteIcon(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			requests = append([]string(nil), requests...)
			mu.Unlock()
			wantRequests := len(sample.Requests)
			if sample.Name == "foreign_icon" {
				wantRequests = 1
			} // explicit credential origin adaptation
			if len(requests) != wantRequests {
				t.Fatal("source favicon request count", requests, wantRequests)
			}
			for i, path := range requests {
				expected, err := url.Parse(sample.Requests[i].URL)
				expectedPath := expected.Path
				if expectedPath == "" {
					expectedPath = "/"
				}
				if err != nil || expectedPath != path {
					t.Fatal("source favicon first-head/request order", requests, sample.Requests)
				}
			}
			var record struct{ Sealed []byte }
			if err := a.Store.Get(context.Background(), "site_icon", site.ID, &record); err != nil {
				t.Fatal(err)
			}
			var icon originalSiteIcon
			if err := a.Store.Unseal(record.Sealed, &icon); err != nil || strings.ReplaceAll(icon.URL, peer.URL, "http://127.0.0.1:43100") != sample.Chosen {
				t.Fatal("chosen source favicon URL was not retained", err)
			}
			want := []byte(nil)
			if sample.Data != nil && sample.Name != "foreign_icon" {
				want, _ = base64.StdEncoding.DecodeString(*sample.Data)
			}
			if !bytes.Equal(icon.Data, want) {
				t.Fatal("source cached favicon bytes", icon.Data, want)
			}
			response := callTest(t, a.Handler(), "GET", "/api/sites/manage/"+site.ID+"/icon", "", cookie, "")
			status := 200
			if len(want) == 0 {
				status = 404
			}
			if response.Code != status || status == 200 && !bytes.Equal(response.Body.Bytes(), want) {
				t.Fatal("actual authenticated cached icon response", response.Code, status)
			}
			anonymous := callTest(t, a.Handler(), "GET", "/api/sites/manage/"+site.ID+"/icon", "", nil, "")
			if anonymous.Code != 401 {
				t.Fatal("private cached icon is public")
			}
			var public []byte
			if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_icon' AND id=?`, site.ID).Scan(&public); err != nil || bytes.Contains(public, []byte("owned-icon")) || bytes.Contains(public, []byte(peer.URL)) {
				t.Fatal("private chosen icon URL/content exposed in stored cache", err)
			}
		})
	}
}
