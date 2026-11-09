package sites

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// All domains, cookies and responses below are synthetic. No test resolves or
// contacts a tracker. These fixtures describe a protocol, not live validation.
const genericOrigin = "https://tracker.example.com"
const genericHeaders = `<tr><td class="colhead">名称</td><td class="colhead">优惠</td><td class="colhead"><img class="size" alt="size"></td><td class="colhead"><img class="leechers" alt="leechers"></td><td class="colhead"><img class="seeders" alt="seeders"></td><td class="colhead">上传时间</td><td class="colhead">完成</td></tr>`
const genericRow = `<tr><td><table class="torrentname"><tr><td><a href="details.php?id=17&amp;hit=1&amp;passkey=synthetic-hidden" title="Example.Film.2026.1080p.WEB-DL.H265">Film</a><br>中文说明</td></tr></table></td><td><img class="pro_free2up"></td><td>2.5 GB</td><td>4</td><td><a href="details.php?id=17#seeders">12</a></td><td>2026-10-15 12:34:56</td><td>99</td></tr>`

func genericClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(Config{ID: "generic", Kind: "nexusphp", BaseURL: genericOrigin, Cookie: "synthetic-cookie", UserAgent: "SyntheticBrowser/1.0", Timeout: 17})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNexusPHPOriginAndRequestConfiguration(t *testing.T) {
	for _, raw := range []string{"https://TRACKER.example.com:443", "https://tracker.example.com/"} {
		got, err := NormalizeNexusPHPOrigin(raw)
		if err != nil || got != genericOrigin+"/" {
			t.Fatalf("normalization = %q, %v", got, err)
		}
	}
	for _, raw := range []string{
		"http://tracker.example.com", "https://localhost", "https://127.0.0.1", "https://192.168.1.1", "https://[::1]",
		"https://name:password@tracker.example.com", "https://tracker.example.com?secret=value", "https://tracker.example.com?",
		"https://tracker.example.com/#secret", "https://tracker.example.com:8443", "https://tracker.example.com:bad",
		"https://tracker.example.com/path/", "https://tracker.example.com/%2F", "https://tracker.example.com.",
		"https://tracker.local", "https://tracker.home.arpa", "https://tracker.invalid", "https://tracker.internal",
		"https://2130706433", "https://127.1", "https://-tracker.example.com", "https://track_er.example.com",
	} {
		if _, err := NormalizeNexusPHPOrigin(raw); err == nil {
			t.Errorf("unsafe origin accepted %q", raw)
		}
	}
	c := genericClient(t)
	if c.http.Timeout != 17*time.Second || c.base.String() != genericOrigin+"/" {
		t.Fatal("per-site timeout/origin not applied")
	}
	for _, cfg := range []Config{
		{Cookie: "", UserAgent: "valid"}, {Cookie: "cookie", UserAgent: "bad\r\nheader"},
		{Cookie: "cookie", UserAgent: "bad\x00header"}, {Cookie: "cookie", Timeout: -1}, {Cookie: "cookie", Timeout: 121},
	} {
		cfg.ID, cfg.Kind, cfg.BaseURL = "generic", "nexusphp", genericOrigin
		if _, err := New(cfg); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	defaultClient, err := New(Config{ID: "generic", Kind: "nexusphp", BaseURL: genericOrigin, Cookie: "synthetic"})
	if err != nil || defaultClient.http.Timeout != 25*time.Second {
		t.Fatal("default timeout changed", err)
	}
	if publicSiteTransport() != publicSiteTransport() || nexusHTTPTransport.Proxy != nil || nexusHTTPTransport.TLSClientConfig != nil && nexusHTTPTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("generic transport must share its pool and retain normal TLS verification")
	}
}

func TestNexusPHPSearchVariableColumnsAndRecentListing(t *testing.T) {
	for _, query := range []string{"合成 & +", "tt0111161", ""} {
		c := genericClient(t)
		c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Scheme != "https" || r.URL.Host != "tracker.example.com" || r.URL.Path != "/torrents.php" || r.Method != "GET" || r.Header.Get("Cookie") != "synthetic-cookie" || r.Header.Get("User-Agent") != "SyntheticBrowser/1.0" || r.Header.Get("x-api-key") != "" {
				t.Error("incorrect origin, endpoint or headers")
			}
			q := r.URL.Query()
			area := "0"
			if query == "tt0111161" {
				area = "4"
			}
			if q.Get("search") != query || !q.Has("search") || q.Get("page") != "0" || q.Get("search_mode") != "0" || q.Get("search_area") != area {
				t.Error("search parameters changed")
			}
			next := *r.URL
			q.Set("page", "1")
			next.RawQuery = q.Encode()
			return response([]byte(authHTML+`<table class="torrents">`+genericHeaders+genericRow+`</table><a href="`+next.String()+`">Next</a>`), 200), nil
		})
		out, err := c.Search(context.Background(), query, 1)
		if err != nil || len(out.Resources) != 1 || !out.HasMore {
			t.Fatalf("generic search %q = %+v, %v", query, out, err)
		}
		r := out.Resources[0]
		if r.ID != "17" || r.SiteID != "generic" || r.Size != 2684354560 || r.Seeders != 12 || r.Leechers != 4 || !r.Free || r.UploadFactor != 2 || r.Subtitle != "中文说明" || r.PublishedAt != "2026-10-15T12:34:56+08:00" || r.DetailsURL != genericOrigin+"/details.php?id=17" {
			t.Fatalf("generic resource = %+v", r)
		}
	}
}

func TestCaishenSearchReadsCardResultsOnBothOrigins(t *testing.T) {
	const cards = `<div class="torrents"><div class="torrent-table-for-spider"><div class="torrent-table-sub-info">
		<div><a href="details.php?id=42" title="Synthetic.Show.S01E02.1080p">Synthetic Show</a><div class="torrent-info-text-small_name">示例说明</div><img class="pro_free2up"></div>
		<div class="torrent-info-text-added"><span title="2026-10-15 12:34:56">1小时前</span></div>
		<div class="torrent-info-text-size">2.5 GB</div>
		<div class="torrent-info-text-seeders"><a href="details.php?id=42#seeders">12</a></div>
		<div class="torrent-info-text-leechers"><a href="details.php?id=42#leechers">4</a></div>
	</div></div></div>`
	for _, origin := range []string{"https://cspt.top", "https://cspt.cc", genericOrigin} {
		t.Run(origin, func(t *testing.T) {
			c, err := New(Config{ID: "synthetic", Kind: "nexusphp", BaseURL: origin, Cookie: "synthetic-cookie"})
			if err != nil {
				t.Fatal(err)
			}
			c.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Scheme+"://"+req.URL.Host+req.URL.Path != origin+"/torrents.php" || req.URL.Query().Get("search") != "示例剧集" || req.Header.Get("Cookie") != "synthetic-cookie" {
					t.Error("search did not use the configured origin and keyword")
				}
				return response([]byte(authHTML+cards), http.StatusOK), nil
			})
			out, err := c.Search(context.Background(), "示例剧集", 1)
			if origin == genericOrigin {
				if err == nil || err.Error() != "site_protocol_changed" || len(out.Resources) != 0 {
					t.Fatalf("unrelated site accepted the card template: %+v, %v", out, err)
				}
				return
			}
			if err != nil || len(out.Resources) != 1 {
				t.Fatalf("Caishen card search: %+v, %v", out, err)
			}
			r := out.Resources[0]
			if r.ID != "42" || r.Title != "Synthetic.Show.S01E02.1080p" || r.Subtitle != "示例说明" || r.DetailsURL != origin+"/details.php?id=42" || r.Size != 2684354560 || r.Seeders != 12 || r.Leechers != 4 || r.PublishedAt != "2026-10-15T12:34:56+08:00" || !r.Free || r.UploadFactor != 2 {
				t.Fatalf("Caishen card resource: %+v", r)
			}
		})
	}
}

func TestNexusPHPSearchTemplateAndAuthenticationFailures(t *testing.T) {
	for _, body := range []string{
		`<input type="password">`, authHTML + `<p>Logged in, but this is not a search result.</p>`,
		authHTML + `<table class="torrents">` + strings.Replace(genericHeaders, `class="seeders" alt="seeders"`, `class="unknown" alt="unknown"`, 1) + genericRow + `</table>`,
		authHTML + `<table class="torrents">` + genericHeaders + strings.Replace(genericRow, "2.5 GB", "unknown", 1) + `</table>`,
		authHTML + `<table class="torrents">` + genericHeaders + strings.ReplaceAll(genericRow, `details.php?`, `https://other.example.com/details.php?`) + `</table>`,
		authHTML + `<table class="torrents"><tr><td>unexpected content</td></tr></table>`,
	} {
		c := genericClient(t)
		c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response([]byte(body), 200), nil })
		if _, err := c.Search(context.Background(), "", 1); err == nil {
			t.Fatal("unsupported/login page became a successful result")
		}
	}
	for _, body := range []string{
		htmlFixture("nexusphp", ""),
		authHTML + `<table class="torrents"><tr><td>No torrents found</td></tr></table>`,
		authHTML + `<table class="searchbox"></table><table><tr><td class="text">No torrents found. Try again with a refined search string.</td></tr></table>`,
	} {
		c := genericClient(t)
		c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response([]byte(body), 200), nil })
		if _, err := c.Search(context.Background(), "", 1); err != nil {
			t.Fatal("recognized NexusPHP template rejected", err)
		}
	}
}

func TestNexusPHPPartialRowsKeepValidatedResultsAndReportFailure(t *testing.T) {
	c := genericClient(t)
	broken := `<tr><td>Changed torrent title cell</td><td>normal</td><td>1 GB</td><td>2</td><td>3</td><td>2026-10-15 12:00:00</td><td>4</td></tr>`
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response([]byte(authHTML+`<table class="torrents">`+genericHeaders+genericRow+broken+`</table>`), 200), nil
	})
	out, err := c.Search(context.Background(), "", 1)
	if err == nil || err.Error() != "site_protocol_changed" || len(out.Resources) != 1 || out.Resources[0].ID != "17" {
		t.Fatal("malformed row was silently lost or accepted", out, err)
	}
}

func TestNexusPHPTorrentMaterialAndCredentialBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     []byte
		location string
		wantErr  string
	}{
		{"torrent", 200, validTorrent(), "", ""},
		{"login", 200, []byte(`<input type="password">`), "", "site_invalid_metainfo"},
		{"redirect-other", 302, nil, "https://other.example.com/download.php?id=17&passkey=synthetic", "site_redirect_blocked"},
		{"redirect-same", 302, nil, "/download.php?id=17&passkey=synthetic", "site_redirect_blocked"},
		{"auth", 403, []byte("synthetic secret in upstream response"), "", "site_forbidden_or_challenge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := genericClient(t)
			requests := 0
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.String() != genericOrigin+"/download.php?id=17" || r.Header.Get("Cookie") != "synthetic-cookie" || r.Header.Get("User-Agent") != "SyntheticBrowser/1.0" {
					t.Error("material request escaped origin, ID or authentication")
				}
				res := response(tc.body, tc.status)
				res.Header.Set("Location", tc.location)
				return res, nil
			})
			got, err := c.Torrent(context.Background(), "17")
			if tc.wantErr == "" {
				if err != nil || !bytes.Equal(got, validTorrent()) {
					t.Fatal("valid material rejected", err)
				}
			} else if err == nil || err.Error() != tc.wantErr {
				t.Fatal("wrong sanitized error", err)
			}
			if requests != 1 {
				t.Fatal("redirect followed")
			}
			if _, err := c.Torrent(context.Background(), "https://other.example.com/download.php?id=17"); err == nil || requests != 1 {
				t.Fatal("caller URL accepted")
			}
			foreign := *c.base
			foreign.Host = "other.example.com"
			if _, _, err := c.request(context.Background(), "GET", &foreign, nil, c.auth(), htmlLimit); err == nil || requests != 1 {
				t.Fatal("credentials could leave their configured origin")
			}
		})
	}
}

func TestNexusPHPUserStatsAndSeedingPagination(t *testing.T) {
	c := genericClient(t)
	requests := 0
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Cookie") != "synthetic-cookie" || r.Header.Get("User-Agent") != "SyntheticBrowser/1.0" || r.URL.Host != "tracker.example.com" {
			t.Error("statistics credential scope changed")
		}
		body := ""
		switch r.URL.Path {
		case "/index.php":
			body = `<a href="logout.php">Logout</a><a href="https://other.example.com/userdetails.php?id=99">Other</a><a href="userdetails.php?id=5">Owner</a><div>Uploaded: 10 GB Downloaded: 5 GB Torrents leeching: 0</div><a href="mybonus.php">Bonus: 123.5</a>`
		case "/userdetails.php":
			if r.URL.Query().Get("id") != "5" {
				t.Error("wrong account")
			}
			body = `<a href="logout.php">Logout</a><table><tr><td>Class</td><td>Power User</td></tr><tr><td>Join date</td><td>2024-01-01</td></tr></table><div>Torrents seeding: 2</div><a href="getusertorrentlist.php?type=seeding&amp;userid=5">Seeding</a>`
		case "/getusertorrentlist.php":
			if r.URL.Query().Get("userid") != "5" || r.URL.Query().Get("type") != "seeding" {
				t.Error("wrong seeding account")
			}
			id := "11"
			if r.URL.Query().Get("page") == "1" {
				id = "12"
			}
			body = `<table><tr><th>Name</th><th>Size</th></tr><tr><td><a href="details.php?id=` + id + `">Owned torrent</a></td><td>1.5 GiB</td></tr></table>`
			if id == "11" {
				body += `<a href="getusertorrentlist.php?page=1">Next</a>`
			}
		default:
			t.Errorf("unexpected endpoint %q", r.URL.Path)
		}
		return response([]byte(body), 200), nil
	})
	s, err := c.UserStats(context.Background())
	if err != nil || requests != 4 || s.UserID != "5" || s.Username != "Owner" || s.Level != "Power User" || s.JoinedAt != "2024-01-01" || s.Uploaded == nil || *s.Uploaded != 10737418240 || s.Downloaded == nil || *s.Downloaded != 5368709120 || s.Ratio == nil || *s.Ratio != 2 || s.Bonus == nil || *s.Bonus != 123.5 || s.Seeding == nil || *s.Seeding != 2 || s.SeedingBytes == nil || *s.SeedingBytes != 3221225472 || s.Leeching == nil || *s.Leeching != 0 || len(s.Warnings) != 0 {
		t.Fatalf("account values were lost or guessed: %+v, error %v, requests %d", s, err, requests)
	}
}

func TestNexusPHPUserStatsUnsupportedAndPartialPages(t *testing.T) {
	for _, body := range []string{`<input type="password">`, authHTML + `<p>Unknown account layout</p>`} {
		c := genericClient(t)
		c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response([]byte(body), 200), nil })
		if _, err := c.UserStats(context.Background()); err == nil {
			t.Fatal("unknown account page reported success")
		}
	}
	for _, seeding := range []string{
		`<input type="password"><p>No torrents found</p>`,
		`<table><tr><th>Name</th><th>Size</th></tr><tr><td>Changed row</td><td>1 GB</td></tr></table>`,
		`<table><tr><th>Name</th><th>Size</th></tr><tr><td><a href="https://other.example.com/details.php?id=1">Other</a></td><td>1 GB</td></tr></table>`,
		`<a href="https://other.example.com/getusertorrentlist.php?page=1">Next</a><p>No torrents found</p>`,
	} {
		c := genericClient(t)
		c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "tracker.example.com" {
				t.Error("followed untrusted account pagination")
			}
			if r.URL.Path == "/getusertorrentlistajax.php" {
				return response([]byte(seeding), 200), nil
			}
			return response([]byte(authHTML+`<div>Uploaded: 1 GB Downloaded: 0 B</div>`), 200), nil
		})
		s, err := c.UserStats(context.Background())
		if err != nil || s.Uploaded == nil || s.SeedingBytes != nil || s.Seeding != nil || len(s.Warnings) == 0 {
			t.Fatalf("partial statistics became fake zero or lost observed data: %+v, %v", s, err)
		}
	}
}

func TestNexusPHPPublicDialerPinsValidatedAddresses(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "198.18.0.1", "0.0.0.0", "224.0.0.1", "::1", "fc00::1", "fe80::1", "::ffff:192.168.0.1", "64:ff9b::a00:1", "2001:db8::1"} {
		for _, mixed := range []bool{false, true} {
			lookup := func(context.Context, string, string) ([]netip.Addr, error) {
				ips := []netip.Addr{netip.MustParseAddr(address)}
				if mixed {
					ips = append([]netip.Addr{netip.MustParseAddr("8.8.8.8")}, ips...)
				}
				return ips, nil
			}
			_, err := dialPublicSite(context.Background(), "tcp", "tracker.example.com:443", lookup, func(context.Context, string, string) (net.Conn, error) {
				t.Error("unsafe DNS answer was dialed")
				return nil, errors.New("must not dial")
			})
			if err == nil {
				t.Errorf("non-public DNS address accepted %s", address)
			}
		}
	}
	lookups := 0
	lookup := func(_ context.Context, network, host string) ([]netip.Addr, error) {
		lookups++
		if network != "ip" || host != "tracker.example.com" {
			t.Error("lookup did not use configured host")
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("2606:4700:4700::1111")}, nil
	}
	dials := []string{}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	conn, err := dialPublicSite(context.Background(), "tcp", "tracker.example.com:443", lookup, func(_ context.Context, network, address string) (net.Conn, error) {
		dials = append(dials, address)
		if len(dials) == 1 {
			return nil, fmt.Errorf("synthetic connection failure")
		}
		return left, nil
	})
	if err != nil || conn != left || lookups != 1 || strings.Join(dials, ",") != "8.8.8.8:443,[2606:4700:4700::1111]:443" {
		t.Fatalf("dialer did not pin validated addresses: lookups=%d dials=%v err=%v", lookups, dials, err)
	}
}
