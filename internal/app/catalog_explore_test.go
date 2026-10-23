package app

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"light-media/internal/catalog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCatalogExploreCachesEachFilterAndKeepsSourceNamespace(t *testing.T) {
	a, _, _ := importTestApp(t)
	for _, sort := range []string{"U", "S"} {
		query := url.Values{"source": {"douban"}, "type": {"movies"}, "sort": {sort}, "page": {"2"}}
		parsed, err := catalog.ParseExploreQuery(query)
		if err != nil {
			t.Fatal(err)
		}
		p := catalog.ExplorePage{Results: []catalog.ExploreMedia{{Media: catalog.Media{Title: sort, MediaType: "movie"}, Source: "douban", SourceID: "123"}}, Page: 2, HasMore: true}
		var cached catalog.ExplorePage
		if _, err := a.cached(t.Context(), parsed.CacheKey(), time.Hour, &cached, func() (any, error) { return p, nil }); err != nil {
			t.Fatal(err)
		}
	}
	for _, sort := range []string{"U", "S"} {
		r := httptest.NewRequest("GET", "/api/catalog/explore?source=douban&type=movies&sort="+sort+"&page=2", nil)
		value, err := a.catalogExploreAPI(r, "explore")
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			catalog.ExplorePage
			CachedAt time.Time `json:"cached_at"`
		}
		if err = json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Results) != 1 || response.Results[0].Title != sort || response.Results[0].ID != 0 || response.Results[0].SourceID != "123" || response.Page != 2 || !response.HasMore || response.CachedAt.IsZero() {
			t.Fatalf("invalid response: %s", data)
		}
	}
	for _, target := range []string{"/api/catalog/explore?source=unknown", "/api/catalog/explore?source=douban&page=-1"} {
		if _, err := a.catalogExploreAPI(httptest.NewRequest("GET", target, nil), "explore"); err == nil {
			t.Fatal("invalid explore query accepted")
		}
	}
}

func TestCatalogExploreDetailDoesNotRequireTMDBIdentity(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := catalog.ExploreMedia{Media: catalog.Media{Title: "来源标题", MediaType: "tv", Year: 2025, Overview: "真实源简介"}, Source: "bangumi", SourceID: "326"}
	var cached catalog.ExploreMedia
	if _, err := a.cached(t.Context(), "explore-detail:v1:bangumi:tv:326", time.Hour, &cached, func() (any, error) { return m, nil }); err != nil {
		t.Fatal(err)
	}
	value, err := a.catalogExploreAPI(httptest.NewRequest("GET", "/api/catalog/explore/detail?source=bangumi&kind=tv&source_id=326", nil), "explore/detail")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := value.(catalog.ExploreMedia)
	if !ok || got.ID != 0 || got.SourceID != "326" || got.Title != m.Title || got.Overview != m.Overview {
		t.Fatalf("unexpected detail %+v", value)
	}
}

func TestCatalogExploreImageAllowlistAndCache(t *testing.T) {
	a, _, _ := importTestApp(t)
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	prior := assetImageClient
	t.Cleanup(func() { assetImageClient = prior })
	calls := 0
	assetImageClient = &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "img3.doubanio.com" || r.URL.Path != "/view/p.jpg" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "https://movie.douban.com/" {
			t.Fatal("incorrect image origin or headers")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/jpeg"}}, Body: io.NopCloser(bytes.NewReader(data.Bytes()))}, nil
	})}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/catalog/explore/image?source=douban&url="+url.QueryEscape("https://img3.doubanio.com/view/p.jpg"), nil)
		if err := a.catalogExploreImage(w, r); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(w.Body.Bytes(), data.Bytes()) || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/jpeg") {
			t.Fatal("source image not preserved")
		}
	}
	for _, raw := range []string{"https://127.0.0.1/x.jpg", "https://img3.doubanio.com.attacker.invalid/x.jpg", "http://img3.doubanio.com/x.jpg"} {
		r := httptest.NewRequest("GET", "/api/catalog/explore/image?source=douban&url="+url.QueryEscape(raw), nil)
		if err := a.catalogExploreImage(httptest.NewRecorder(), r); err == nil {
			t.Fatal("arbitrary image URL accepted")
		}
	}
	if calls != 1 {
		t.Fatalf("expected cached request only, got %d", calls)
	}
}

func TestCatalogExploreImageRejectsOversizedUnknownLengthBody(t *testing.T) {
	a, _, _ := importTestApp(t)
	data := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{'x'}, (8<<20)+100)...)
	body := bytes.NewReader(data)
	prior := assetImageClient
	t.Cleanup(func() { assetImageClient = prior })
	assetImageClient = &http.Client{Transport: imageTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: -1, Header: http.Header{"Content-Type": {"image/jpeg"}}, Body: io.NopCloser(body)}, nil
	})}
	r := httptest.NewRequest("GET", "/api/catalog/explore/image?source=bangumi&url="+url.QueryEscape("https://lain.bgm.tv/pic/cover/l/large.jpg"), nil)
	if err := a.catalogExploreImage(httptest.NewRecorder(), r); err == nil {
		t.Fatal("oversized source image accepted")
	}
	if consumed := len(data) - body.Len(); consumed != (8<<20)+1 {
		t.Fatalf("image read escaped the 8 MiB + 1 byte bound: %d", consumed)
	}
	entries, err := os.ReadDir(filepath.Join(a.Dir, "catalog-explore-images"))
	if err != nil && !os.IsNotExist(err) || len(entries) != 0 {
		t.Fatal("oversized source image was cached")
	}
}
