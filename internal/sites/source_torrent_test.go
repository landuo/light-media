package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sourceComparable(t *testing.T, value any) any {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e = d.Decode(&v); e != nil {
		t.Fatal(e)
	}
	var norm func(any) any
	norm = func(v any) any {
		switch x := v.(type) {
		case json.Number:
			r, ok := new(big.Rat).SetString(string(x))
			if !ok {
				t.Fatal("invalid number")
			}
			return r.RatString()
		case map[string]any:
			for k, a := range x {
				x[k] = norm(a)
			}
		case []any:
			for i, a := range x {
				x[i] = norm(a)
			}
		}
		return v
	}
	return norm(v)
}
func TestSourceMTeamActualInstalledAST(t *testing.T) {
	raw, e := os.ReadFile("testdata/source-mteam-original.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Case struct {
				Name         string
				Row, Indexer map[string]any
				Proxy        any
				Domain       string
			}
			Result map[string]any
			Error  string
			Print  int `json:"source_print_count"`
		}
	}
	if !decodeJSON(raw, &fixture) {
		t.Fatal("fixture decode")
	}
	for _, r := range fixture.Cases {
		t.Run(r.Case.Name, func(t *testing.T) {
			got, diagnostics, e := BuildSourceMTeamTorrent(r.Case.Row, r.Case.Indexer, r.Case.Domain, r.Case.Proxy, true, time.UTC)
			if r.Error != "" {
				if e == nil || e.Error() != r.Error {
					t.Fatalf("source error class: got %v want %s", e, r.Error)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				if !reflect.DeepEqual(sourceComparable(t, got.Fields), sourceComparable(t, r.Result)) {
					t.Errorf("full source attributes differ: got %#v; want %#v", got.Fields, r.Result)
				}
			}
			if len(diagnostics) != r.Print {
				t.Errorf("caught diagnostics: got %v want count %d", diagnostics, r.Print)
			}
		})
	}
}

func TestSourceMTeamFreshOwnedSearchPrivateSnapshot(t *testing.T) {
	const key = "owned-api-secret"
	const cookie = "owned-cookie-secret"
	row := `{"id":"17","name":"Owned.Movie.2026.1080p","smallDescr":null,"category":"401","size":"1048576","createdDate":"1700000000","imdb":"https://owned.invalid/tt12345","labelsNew":["国配"],"status":{"seeders":"1","leechers":"2","timesCompleted":"3","discount":"_2X_FREE"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/torrent/search" || r.Method != "POST" || r.Header.Get("x-api-key") != key {
			t.Error("wrong owned request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"data":[` + row + `]}}`))
	}))
	defer srv.Close()
	c, e := New(Config{ID: "owned", Kind: "mteam", BaseURL: "https://api.m-team.cc", APIKey: key, Cookie: cookie, UserAgent: "owned-UA"})
	if e != nil {
		t.Fatal(e)
	}
	// Preserve the native configured origin/guard while directing this fixture's
	// transport to the owned HTTP server. No official host is resolved/contacted.
	owned := srv.Client().Transport
	if owned == nil {
		owned = http.DefaultTransport
	}
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return owned.RoundTrip(r)
	})
	result, e := c.Search(context.Background(), "Owned", 1)
	if e != nil || len(result.Resources) != 1 {
		t.Fatalf("search %v", e)
	}
	r := result.Resources[0]
	indexer := map[string]any{"id": json.Number("23"), "domain": "https://owned.invalid/", "name": "Owned", "cookie": cookie, "ua": "owned-UA", "apikey": key, "proxy": false, "pri": 7, "downloader": nil}
	source, _, e := c.SourceMTeamTorrent(r, 23, indexer, "owned.invalid", nil, false, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	if source.Get("description") != nil || source.Get("grabs") != json.Number("3") || source.Get("category") != "电影" || source.Get("pubdate") != "2023-11-14 22:13:20" || source.Get("site") != json.Number("23") {
		t.Fatalf("raw source lost: %#v", source.Fields)
	}
	if r.SourceSubtitle == nil || *r.SourceSubtitle != "" || r.Subtitle == "" {
		t.Fatal("public UI projection should remain independently augmented")
	}
	public, e := json.Marshal(result)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(public, []byte(key)) || bytes.Contains(public, []byte(cookie)) || bytes.Contains(public, []byte("enclosure")) || bytes.Contains(public, []byte("sourceRecord")) {
		t.Fatal("private source escaped public JSON")
	}
	var cached SearchResult
	if json.Unmarshal(public, &cached) != nil {
		t.Fatal("cache decode")
	}
	if _, _, e = c.SourceMTeamTorrent(cached.Resources[0], 23, indexer, "owned.invalid", nil, true, time.UTC); !errors.Is(e, ErrSourceTorrentDependency) {
		t.Fatal("public cache forged private source")
	}
	changed := r
	changed.Title = "Changed"
	if _, _, e = c.SourceMTeamTorrent(changed, 23, indexer, "owned.invalid", nil, true, time.UTC); !errors.Is(e, ErrSourceTorrentDependency) {
		t.Fatal("changed resource accepted")
	}
	previous := c.config.APIKey
	c.config.APIKey = "other-owned-key"
	if _, _, e = c.SourceMTeamTorrent(r, 23, indexer, "owned.invalid", nil, true, time.UTC); !errors.Is(e, ErrSourceTorrentDependency) {
		t.Fatal("changed config accepted")
	}
	c.config.APIKey = previous
	for _, kind := range []string{"proxy", "timezone", "domain"} {
		t.Run(kind, func(t *testing.T) {
			cfg := map[string]any{}
			for k, v := range indexer {
				cfg[k] = v
			}
			domain := "owned.invalid"
			var loc *time.Location = time.UTC
			if kind == "proxy" {
				cfg["proxy"] = true
			}
			if kind == "domain" {
				domain = ""
			}
			if kind == "timezone" {
				loc = nil
			}
			if _, _, e = c.SourceMTeamTorrent(r, 23, cfg, domain, nil, false, loc); !errors.Is(e, ErrSourceTorrentDependency) {
				t.Fatal("missing provenance silently defaulted")
			}
		})
	}
}

func TestSourceMTeamSourceIdentityAndLosslessJSON(t *testing.T) {
	c, e := New(Config{ID: "owned", Kind: "mteam", BaseURL: "https://api.m-team.cc", APIKey: "owned-key"})
	if e != nil {
		t.Fatal(e)
	}
	body := func(title string) []byte {
		return []byte(`{"code":0,"data":{"data":[{"id":"1","name":` + title + `,"size":"1","status":{}}]}}`)
	}
	indexer := map[string]any{"id": json.Number("23"), "apikey": "owned-key", "domain": "https://owned.invalid/"}
	for _, title := range []string{`"Owned"`, `"Owned\ud83d\ude00"`, `"Owned\\ud800"`} {
		out, e := c.parseMTeam(body(title), 1)
		if e != nil {
			t.Fatal(e)
		}
		r := out.Resources[0]
		if _, _, e = c.SourceMTeamTorrent(r, 23, indexer, "owned.invalid", nil, true, time.UTC); e != nil {
			t.Fatalf("valid source: %v", e)
		}
		for _, id := range []int64{0, -1, 24} {
			if _, _, e = c.SourceMTeamTorrent(r, id, indexer, "owned.invalid", nil, true, time.UTC); !errors.Is(e, ErrSourceTorrentDependency) {
				t.Fatal("unbound source ID accepted")
			}
		}
	}
	for _, title := range []string{`"Owned\ud800"`, `"Owned\udc00"`, `"Owned\ud800x"`, `"Owned` + string([]byte{0xff}) + `"`} {
		out, e := c.parseMTeam(body(title), 1)
		if e != nil {
			t.Fatalf("ordinary UI decode changed: %v", e)
		}
		if _, _, e = c.SourceMTeamTorrent(out.Resources[0], 23, indexer, "owned.invalid", nil, true, time.UTC); !errors.Is(e, ErrSourceTorrentDependency) {
			t.Fatal("lossy raw source claimed exact")
		}
	}
}
