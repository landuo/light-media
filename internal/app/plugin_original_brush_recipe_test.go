package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/sites"
)

func TestBrushFlowGuardedSourceRecipeSameOrigin(t *testing.T) {
	a := newTestApp(t)
	torrent, expectedHash := workflowTorrent("owned-recipe.mkv")
	var crossed atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { crossed.Add(1) }))
	t.Cleanup(foreign.Close)
	var mu sync.Mutex
	var requests []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		wantRecipeHeaders := r.URL.Path == "/token" || r.URL.Path == "/token-result" || r.URL.Path == "/cross-origin"
		if wantRecipeHeaders {
			if r.Header.Get("Cookie") != "owned=fixture" || r.Header.Get("Authorization") != "Bearer owned-fixture" {
				t.Error("same-origin recipe request lost its selected credentials")
			}
		} else if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Brush-Fixture") != "" {
			t.Error("final material request retained recipe credentials or headers")
		}
		switch r.URL.Path {
		case "/token":
			body, err := io.ReadAll(r.Body)
			if err != nil || len(body) != 0 {
				t.Error("source POST params belong in the query")
			}
			w.Header().Set("Location", "/token-result?step=2")
			w.WriteHeader(http.StatusFound)
		case "/token-result":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true,"nested":{"token":"x + y"}}`)
		case "/fixture/content":
			w.Header().Set("Location", "/owned-torrent")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case "/owned-torrent", "/literal":
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(torrent)
		case "/cross-origin":
			w.Header().Set("Location", foreign.URL+"/unexpected")
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)
	settings := a.Settings()
	site := SiteConfig{Config: sites.Config{ID: "owned-recipe-site", Kind: "hhanclub", Name: "Owned recipe", BaseURL: peer.URL, Cookie: "owned=fixture"}, Enabled: true, DailyBudget: 50}
	settings.Sites = []SiteConfig{site}
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	scope := originalBaseCallScope{validate: func(ctx context.Context) error { return ctx.Err() }}
	candidate := originalBrushSourceCandidate{site: site.ID, siteKey: a.managedSiteKey(site)}
	encodeRecipe := func(recipe any, address string) string {
		raw, err := json.Marshal(recipe)
		if err != nil {
			t.Fatal(err)
		}
		return "[" + base64.StdEncoding.EncodeToString(raw) + "]" + address
	}
	headers := map[string]string{"Authorization": "Bearer owned-fixture", "X-Brush-Fixture": "selected"}
	candidate.enclosure = encodeRecipe(map[string]any{
		"method": "post", "cookie": true, "header": headers, "params": json.RawMessage(`{"z":"last","a":["one","two"],"omit":null}`),
		"result": "nested.token", "success": "ok", "result_path": "/content", "result_query_param": "ticket", "result_base_url": peer.URL + "/fixture",
	}, peer.URL+"/token?existing=1")
	checkMaterial := func(skipTips bool) {
		t.Helper()
		content, magnet, hash, err := a.originalBrushMaterial(t.Context(), scope, candidate, skipTips)
		if err != nil || magnet != "" || hash != expectedHash || string(content) != string(torrent) {
			t.Fatalf("material was not resolved and parsed, bytes=%d validHash=%t err=%v", len(content), hash == expectedHash, err)
		}
	}
	checkMaterial(true)
	candidate.enclosure = "[]" + peer.URL + "/literal"
	checkMaterial(false)
	candidate.enclosure = encodeRecipe(map[string]any{"method": "get", "cookie": true, "header": headers}, peer.URL+"/cross-origin")
	if _, _, _, err := a.originalBrushMaterial(t.Context(), scope, candidate, false); err == nil || !strings.Contains(err.Error(), "site_material_origin_mismatch") || crossed.Load() != 0 {
		t.Fatalf("cross-origin recipe was not blocked before transport, foreign=%d err=%v", crossed.Load(), err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"POST /token?existing=1&z=last&a=one&a=two", "GET /token-result?step=2", "GET /fixture/content?ticket=x+%2B+y&letdown=1", "GET /owned-torrent", "GET /literal", "GET /cross-origin"}
	if !slices.Equal(requests, want) {
		t.Errorf("source recipe wire sequence differs: got %v, want %v", requests, want)
	}
}
