package sites

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Cloudflare injects this passive detection script into authenticated PTTime
// pages as well as pages that do not require any interactive verification.
const passiveJSD = `<script>(function(){var s=document.createElement('script');s.src='/cdn-cgi/challenge-platform/scripts/jsd/main.js';document.head.appendChild(s)})();</script>`

func TestNexusPassiveJSDAccountSearchAndSeeding(t *testing.T) {
	c := genericClient(t)
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.Path {
		case "/index.php":
			body = authHTML + `<a href="userdetails.php?id=1">synthetic-user</a><p>上传：2 GB 下载：1 GB</p>`
		case "/userdetails.php":
			body = authHTML + `<p>等级：Power User</p>`
		case "/getusertorrentlistajax.php":
			body = `<table><tr><th>名称</th><th>大小</th><th>做种者</th></tr><tr><td><a href="details.php?id=9">synthetic</a></td><td>1 GB</td><td>7</td></tr></table>`
		case "/torrents.php":
			body = htmlFixture("nexusphp", "")
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		return response([]byte(body+passiveJSD), 200), nil
	})
	s, err := c.UserStats(context.Background())
	if err != nil || s.UserID != "1" || s.Uploaded == nil || s.Downloaded == nil || s.SeedingBytes == nil || *s.SeedingBytes != 1024*1024*1024 || len(s.Warnings) != 0 {
		t.Fatalf("passive detection rejected valid account data: %+v %v", s, err)
	}
	result, err := c.Search(context.Background(), "", 1)
	if err != nil || len(result.Resources) == 0 {
		t.Fatalf("passive detection rejected valid search: %+v %v", result, err)
	}
}

func TestNexusPassiveJSDDoesNotAuthenticateOrHideActualChallenges(t *testing.T) {
	for _, body := range []string{
		passiveJSD,
		passiveJSD + `<input type="password">`,
		passiveJSD + `<table><tr><td>not an authenticated site</td></tr></table>`,
	} {
		if _, err := pageDOM([]byte(body)); err == nil {
			t.Fatal("passive script authenticated a page")
		}
	}
	for _, challenge := range []string{
		`<div id="cf-chl-widget">verify</div>`,
		`<script src="/cdn-cgi/challenge-platform/h/g/orchestrate/chl_page/v1"></script>`,
		`<title>Just a moment...</title>`,
		`<div class="g-recaptcha"></div>`,
		`<div class="h-captcha"></div>`,
		`<div class="cf-turnstile"></div>`,
	} {
		body := authHTML + passiveJSD + challenge
		if _, err := pageDOM([]byte(body)); err == nil || err.Error() != "site_login_challenge" {
			t.Fatal("active challenge was accepted", challenge, err)
		}
		c := genericClient(t)
		c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response([]byte(body), 200), nil
		})
		stats := UserStats{UserID: "1", Sources: map[string]string{}}
		profile, parseErr := pageDOM([]byte(authHTML))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if err := c.nexusSeedingStats(context.Background(), &stats, profile); err == nil || !strings.Contains(err.Error(), "challenge") {
			t.Fatal("seeding response accepted active challenge", err)
		}
	}
}

func TestNexusPTTimeExplicitEmptySeedingResponse(t *testing.T) {
	for _, tc := range []struct {
		body string
		err  string
	}{
		{"没有记录", ""},
		{"", "site_seeding_table_unrecognized"},
		{"<p>没有记录</p><p>请求失败</p>", "site_seeding_table_unrecognized"},
		{`<input type="password">没有记录`, "site_authentication_failed"},
		{`<div class="cf-turnstile">没有记录</div>`, "site_login_challenge"},
	} {
		c := genericClient(t)
		c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response([]byte(tc.body), 200), nil
		})
		profile, err := pageDOM([]byte(authHTML))
		if err != nil {
			t.Fatal(err)
		}
		stats := UserStats{UserID: "1", Sources: map[string]string{}}
		err = c.nexusSeedingStats(context.Background(), &stats, profile)
		if tc.err == "" {
			if err != nil || stats.Seeding == nil || *stats.Seeding != 0 || stats.SeedingBytes == nil || *stats.SeedingBytes != 0 {
				t.Fatalf("explicit empty response was not recognized: %+v %v", stats, err)
			}
		} else if err == nil || err.Error() != tc.err || stats.Seeding != nil || stats.SeedingBytes != nil {
			t.Fatalf("unknown/auth response became zero: %+v %v", stats, err)
		}
	}
}
