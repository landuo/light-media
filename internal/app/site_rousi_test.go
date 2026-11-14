package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"light-media/internal/sites"
)

func TestManagedRousiImportSecretsAndTokenRevocation(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	report, e := a.importManagedSites(ctx, json.RawMessage(`[{"id":2,"name":"肉丝","domain":"rousi.pro","url":"https://rousi.pro/","apikey":"synthetic-profile-key","token":"synthetic-old-message-token","is_active":true}]`))
	if e != nil || report.Created != 1 || report.Failed != 0 {
		t.Fatal("Rousi API-only import failed", e, report)
	}
	s := a.Settings().Sites[0]
	if s.Kind != "rousi" || s.Cookie != "" || s.Token != "synthetic-old-message-token" || s.APIKey != "synthetic-profile-key" {
		t.Fatal("Rousi credentials not kept independently")
	}
	for _, w := range report.Results[0].Warnings {
		if strings.Contains(w, "登录凭据尚未配置") {
			t.Fatal("API-only site treated as missing cookie")
		}
	}
	v, e := a.managedSites(ctx)
	raw, _ := json.Marshal(v)
	if e != nil || !strings.Contains(string(raw), "synthetic-") {
		t.Fatal("managed site response missing credential", e)
	}
	oldKey := a.managedSiteKey(s)
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	calls := 0
	http.DefaultTransport = siteOutcomeRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "rousi.pro" || r.Header.Get("Cookie") != "" {
			t.Error("credential origin/transport mismatch")
		}
		body := `{"code":0,"data":{"id":7,"username":"owned"}}`
		switch r.URL.Path {
		case "/api/v1/profile":
			if r.Header.Get("Authorization") != "Bearer synthetic-profile-key" {
				t.Error("profile key missing")
			}
		case "/api/messages":
			if r.Header.Get("Authorization") != "Bearer synthetic-old-message-token" {
				t.Error("message token missing")
			}
			c := a.Settings()
			c.Sites[0].Token = "synthetic-new-message-token"
			if e := a.saveSettings(ctx, c); e != nil {
				t.Error(e)
			}
			body = `{"code":0,"data":{"messages":[],"total_pages":1}}`
		default:
			t.Error("old token marked read after revocation")
			return nil, fmt.Errorf("owned unexpected IO")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	var d *sites.SourceUserData
	e = a.withSite(ctx, s, false, func(c *sites.Client) error {
		var inner error
		_, d, inner = c.SourceUserStats(ctx, sites.UserStatsOptions{ReadMessages: true})
		return inner
	})
	if e == nil || !strings.Contains(e.Error(), "站点设置已变化") || d != nil || calls != 2 || a.managedSiteKey(a.Settings().Sites[0]) == oldKey {
		t.Fatal("token edit did not revoke in-flight mark-read", e, calls)
	}
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	if cold.Settings().Sites[0].Token != "synthetic-new-message-token" || cold.Settings().Sites[0].APIKey != s.APIKey {
		t.Fatal("encrypted cold settings lost independent credentials")
	}
}
