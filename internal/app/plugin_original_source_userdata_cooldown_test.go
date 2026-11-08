package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/sites"
)

// The actual synchronous source userdata chain retries the next refresh after
// a logged-out DTO; the ordinary search/connection cooldown is not its policy.
func TestSourceUserdataNativeRecoveryKeepsOrdinaryCooldownSeparate(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	var normal atomic.Bool
	var hits atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if normal.Load() {
			io.WriteString(w, `<a href="logout.php">out</a><div>上传：8 GB 下载：3 GB</div>`)
		} else {
			io.WriteString(w, `<input type="password">`)
		}
	}))
	defer peer.Close()
	site := SiteConfig{Config: sites.Config{ID: "owned-source-cooldown", Name: "Owned", Kind: "jiayuan", BaseURL: peer.URL, Cookie: "owned-fixture-cookie"}, Enabled: true}
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{site}
	a.cfgMu.Unlock()
	if err := a.bootstrapNativeSiteIdentities(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := a.originalSourceSiteRefresh(ctx, sourceSitesScope(), site.ID)
	if err != nil || !bytes.Contains(current, []byte(`"err_msg":"未检测到已登陆`)) || hits.Load() == 0 {
		t.Fatal("source logged-out DTO must be current", string(current), err, hits.Load())
	}
	var cooldown struct {
		Until int64 `json:"until"`
	}
	if err := a.Store.Get(ctx, "site_cooldown", site.ID, &cooldown); err == nil {
		t.Fatal("source userdata created ordinary cooldown", cooldown)
	}
	until := time.Now().Add(time.Hour).Unix()
	if err := a.Store.Put(ctx, "site_cooldown", site.ID, map[string]int64{"until": until}); err != nil {
		t.Fatal(err)
	}
	normal.Store(true)
	before := hits.Load()
	current, err = a.originalSourceSiteRefresh(ctx, sourceSitesScope(), site.ID)
	if err != nil || !bytes.Contains(current, []byte(`"upload":8589934592`)) || hits.Load() <= before {
		t.Fatal("recovered source refresh was replaced by cooldown/old data", string(current), err, hits.Load(), before)
	}
	if err := a.Store.Get(ctx, "site_cooldown", site.ID, &cooldown); err != nil || cooldown.Until != until {
		t.Fatal("source refresh changed ordinary cooldown", cooldown, err)
	}
	called := false
	err = a.withSite(ctx, site, false, func(*sites.Client) error { called = true; return nil })
	var api *APIError
	if !errors.As(err, &api) || api.Code != "SITE_COOLDOWN" || called {
		t.Fatal("ordinary request lost its unchanged cooldown", called, err)
	}
}
