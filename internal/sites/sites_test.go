package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

const authHTML = `<a href="logout.php">退出</a><a href="userdetails.php?id=1">用户</a>`
const nexusRow = `<tr><td>类别</td><td><table class="torrentname"><tr><td class="embedded"><a title="Example.Show.S01E02.2160p.WEB-DL.H.265-TEST" href="details.php?id=101&amp;hit=1">short title</a><br><span style="color:gray">中文副标题</span><img class="pro_free2up"><font color="red"><span title="2026-10-20 12:00:00">free</span></font></td></tr></table></td><td>1</td><td><span title="2026-10-15 18:30:00">1小时前</span></td><td>1.5<br>GiB</td><td><a href="details.php?id=101&amp;hit=1#seeders">12</a></td><td>2</td><td>99</td></tr>`
const hhanRow = `<div class="torrent-table-sub-info"><div class="torrent-table-for-spider-info"><a title="Example.S04E05.1080p.BluRay.x264-TEST" href="details.php?id=202">short</a><a href="details.php?id=202">详情</a><a href="download.php?id=202&amp;passkey=synthetic-only-123456">下载</a><div class="torrent-info-text-small_name">中文说明</div><span class="promotion-tag-50pctdown2up">50%</span><span class="flex"><span title="2026-10-20 12:00:00">促销</span></span></div><div class="torrent-info-text-added"><span title="2026-10-15 18:30:00">1小时前</span></div><div class="torrent-info-text-size">2 GB</div><div class="torrent-info-text-seeders"><a href="details.php?id=202#seeders">23</a></div><div class="torrent-info-text-leechers"><a href="details.php?id=202#leechers">4</a></div></div>`

func htmlFixture(kind, query string) string {
	if kind == "hhanclub" {
		return authHTML + `<input id="searchinput" name="keyword"><div class="torrents-table">` + hhanRow + `</div><a href="torrents.php?search=` + url.QueryEscape(query) + `&amp;page=1">next</a>`
	}
	return authHTML + `<table class="torrents"><tr><th>Title</th></tr>` + nexusRow + `</table><a href="torrents.php?search=` + url.QueryEscape(query) + `&amp;page=1">next</a>`
}

func TestNativeHTMLSearch(t *testing.T) {
	for _, kind := range []string{"ubits", "jiayuan", "1ptba", "hhanclub"} {
		t.Run(kind, func(t *testing.T) {
			query := "合成 & +"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/torrents.php" || r.Header.Get("Cookie") != "synthetic-cookie" {
					t.Error("wrong authenticated search")
				}
				if r.URL.Query().Get("page") != "0" {
					t.Error("wrong page")
				}
				key := "search"
				if r.URL.Query().Get("search_area") != "0" || r.URL.Query().Get("search_mode") != "0" || r.URL.Query().Has("keyword") {
					t.Error("Nexus title search must preserve the verified search area")
				}
				if r.URL.Query().Get(key) != query {
					t.Error("query not preserved")
				}
				fmt.Fprint(w, htmlFixture(kind, query))
			}))
			defer srv.Close()
			c, err := New(Config{ID: "test", Kind: kind, BaseURL: srv.URL, Cookie: "synthetic-cookie"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := c.Search(context.Background(), query, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Resources) != 1 || !result.HasMore || result.Page != 1 {
				t.Fatalf("%+v", result)
			}
			r := result.Resources[0]
			if r.SiteID != "test" || r.Subtitle == "" || r.PublishedAt != "2026-10-15T18:30:00+08:00" || r.PromotionEnd != "2026-10-20T12:00:00+08:00" {
				t.Fatalf("%+v", r)
			}
			if strings.Contains(r.DetailsURL, "passkey") || strings.Contains(r.DetailsURL, "hit=") {
				t.Fatal("secret or unrelated query in details URL")
			}
			if kind == "hhanclub" {
				if r.ID != "202" || r.Size != 2*1024*1024*1024 || r.Seeders != 23 || r.Leechers != 4 || r.Free || r.DownloadFactor != .5 || r.UploadFactor != 2 || r.Codec != "AVC" {
					t.Fatalf("%+v", r)
				}
			} else if r.ID != "101" || r.Size != 1610612736 || r.Seeders != 12 || r.Leechers != 2 || !r.Free || r.DownloadFactor != 0 || r.UploadFactor != 2 || r.Resolution != "2160p" || r.Codec != "HEVC" || r.Source != "WEB-DL" {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestIMDbSearchUsesExactIdentifierMode(t *testing.T) {
	for _, kind := range []string{"ubits", "jiayuan", "1ptba", "hhanclub"} {
		t.Run(kind, func(t *testing.T) {
			for _, query := range []string{"tt0111161", "tt0111161 title", "Title tt0111161"} {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					wantArea := "0"
					if query == "tt0111161" {
						wantArea = "4"
					}
					if got := r.URL.Query().Get("search_area"); got != wantArea {
						t.Errorf("query %q: area %q, want %q", query, got, wantArea)
					}
					field := "search"
					if r.URL.Query().Get(field) != query {
						t.Error("changed IMDb search identifier")
					}
					fmt.Fprint(w, htmlFixture(kind, query))
				}))
				c, err := New(Config{ID: "test", Kind: kind, BaseURL: srv.URL, Cookie: "synthetic"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = c.Search(context.Background(), query, 1); err != nil {
					t.Fatal(err)
				}
				srv.Close()
			}
		})
	}
}

func TestMTeamMissingPeerCountRemainsUnknown(t *testing.T) {
	c, _ := New(Config{ID: "test", Kind: "mteam", BaseURL: "https://api.m-team.cc", APIKey: "synthetic"})
	for _, status := range []string{`{}`, `{"seeders":0,"leechers":0}`, `{"seeders":"bad"}`, `{"seeders":-1}`} {
		body := `{"code":0,"data":{"data":[{"id":"1","name":"Movie","size":"1","status":` + status + `}]}}`
		out, err := c.parseMTeam([]byte(body), 1)
		if strings.Contains(status, "bad") || strings.Contains(status, "-1") {
			if err == nil {
				t.Fatal("invalid peer count accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		want := -1
		if status != `{}` {
			want = 0
		}
		if out.Resources[0].Seeders != want || out.Resources[0].Leechers != want {
			t.Fatalf("unknown converted to zero: %+v", out.Resources[0])
		}
	}
}

func TestHTMLFailureIsNotEmptySuccess(t *testing.T) {
	c, _ := New(Config{ID: "test", Kind: "ubits", BaseURL: "https://example.invalid", Cookie: "synthetic"})
	u, _ := url.Parse("https://example.invalid/torrents.php?search=test&page=0")
	for _, fixture := range []string{
		`<input type="password">`,
		authHTML + `<div class="new-template">No torrents found</div>`,
		authHTML + `<table class="torrents"></table>`,
		authHTML + `<table class="torrents">` + strings.Replace(nexusRow, "1.5<br>GiB", "unknown", 1) + `</table>`,
		authHTML + `<table class="torrents">` + strings.Replace(nexusRow, "details.php?id=101", "https://evil.invalid/details.php?id=101", 1) + `</table>`,
	} {
		if _, err := c.parseHTML([]byte(fixture), u, 1); err == nil {
			t.Fatal("malformed/auth failure became success")
		}
	}
	for _, kind := range []string{"ubits", "hhanclub"} {
		c.config.Kind = kind
		fixture := authHTML + `<table class="torrents"><tr><td>没有找到种子</td></tr></table>`
		if kind == "hhanclub" {
			fixture = authHTML + `<input id="searchinput" name="keyword"><div class="torrents-table">No torrents found</div>`
		}
		result, err := c.parseHTML([]byte(fixture), u, 1)
		if err != nil || len(result.Resources) != 0 || result.Resources == nil {
			t.Fatalf("empty: %+v %v", result, err)
		}
	}
}

func TestPartialHTMLKeepsOnlyValidatedRowsAndReportsFailure(t *testing.T) {
	c, _ := New(Config{ID: "test", Kind: "ubits", BaseURL: "https://example.invalid", Cookie: "synthetic"})
	u, _ := url.Parse("https://example.invalid/torrents.php")
	bad := strings.ReplaceAll(strings.Replace(nexusRow, "1.5<br>GiB", "invalid", 1), "id=101", "id=102")
	last := strings.ReplaceAll(nexusRow, "id=101", "id=103")
	body := authHTML + `<table class="torrents">` + nexusRow + bad + last + `</table>`
	out, err := c.parseHTML([]byte(body), u, 1)
	if err == nil || len(out.Resources) != 2 || out.Resources[0].ID != "101" || out.Resources[1].ID != "103" {
		t.Fatalf("partial response hidden or invalid row accepted: %+v %v", out, err)
	}
}

// Small, secret-free row captures preserve the actual station templates.
// 1PTBA's final source row is genuinely incomplete (two outer cells).
func TestObservedNexusTemplates(t *testing.T) {
	for _, kind := range []string{"ubits", "jiayuan", "1ptba", "hhanclub"} {
		t.Run(kind, func(t *testing.T) {
			b, err := os.ReadFile("testdata/" + kind + "-search.sanitized.html")
			if err != nil {
				t.Fatal(err)
			}
			c, _ := New(Config{ID: kind, Kind: kind, BaseURL: "https://example.invalid", Cookie: "synthetic"})
			u, _ := url.Parse("https://example.invalid/torrents.php")
			out, err := c.parseHTML(b, u, 1)
			if (kind == "1ptba") != (err != nil) || len(out.Resources) != 2 {
				t.Fatalf("observed template: count=%d error=%v", len(out.Resources), err)
			}
			for _, r := range out.Resources {
				if r.Size <= 0 || r.Seeders < 0 || r.Title == "" || strings.Contains(r.DetailsURL, "passkey") {
					t.Fatalf("invalid captured resource %+v", r)
				}
			}
		})
	}
}

func TestObservedHHanEmptyTemplate(t *testing.T) {
	b, err := os.ReadFile("testdata/hhanclub-empty.sanitized.html")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := New(Config{ID: "hhan", Kind: "hhanclub", BaseURL: "https://example.invalid", Cookie: "synthetic"})
	u, _ := url.Parse("https://example.invalid/torrents.php?search=SYNTHETICNOMATCH")
	out, err := c.parseHTML(b, u, 1)
	if err != nil || out.Resources == nil || len(out.Resources) != 0 {
		t.Fatalf("genuine empty: %+v %v", out, err)
	}
}

func TestObservedNexusEmptyPageWithoutGrid(t *testing.T) {
	b, err := os.ReadFile("testdata/nexus-empty.sanitized.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ubits", "jiayuan", "1ptba"} {
		c, _ := New(Config{ID: kind, Kind: kind, BaseURL: "https://example.invalid", Cookie: "synthetic"})
		u, _ := url.Parse("https://example.invalid/torrents.php?search=SYNTHETICNOMATCH")
		out, err := c.parseHTML(b, u, 1)
		if err != nil || len(out.Resources) != 0 || out.Resources == nil {
			t.Fatalf("observed %s empty rejected: %+v %v", kind, out, err)
		}
		for _, changed := range []string{strings.Replace(string(b), `class="searchbox"`, `class="other"`, 1), strings.Replace(string(b), `class="text"`, `class="other"`, 1), strings.Replace(string(b), "没有种子。请用准确的关键字重试。", "没有种子。", 1), strings.Replace(string(b), `href="logout.php"`, `href="profile.php"`, 1)} {
			if _, err := c.parseHTML([]byte(changed), u, 1); err == nil {
				t.Fatalf("%s arbitrary page recognized as empty", kind)
			}
		}
		wrong, _ := url.Parse("https://example.invalid/profile.php?search=x")
		if _, err := c.parseHTML(b, wrong, 1); err == nil {
			t.Fatal("non-search page became empty success")
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(body []byte, code int) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}
}

func TestMTeamSearchAndPromotion(t *testing.T) {
	c, err := New(Config{ID: "mt", Kind: "mteam", BaseURL: "https://api.m-team.cc", APIKey: "synthetic-api-key"})
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("x-api-key") != "synthetic-api-key" || r.URL.Path != "/api/torrent/search" || r.Method != "POST" {
			t.Error("wrong mteam request")
		}
		var p map[string]any
		if json.NewDecoder(r.Body).Decode(&p) != nil || p["pageNumber"] != float64(2) || p["keyword"] != "https://www.imdb.com/title/tt12345" || p["pageSize"] != float64(100) {
			t.Error("wrong payload")
		}
		return response([]byte(`{"code":"0","data":{"total":"250","data":[{"id":"15","name":"Movie.2026.1080p.WEB-DL.x264","smallDescr":"合成","size":"123456","createdDate":"2026-10-15 12:00:00","status":{"seeders":"12","leechers":3,"discount":"_2X","discountEndTime":"2026-10-16 12:00:00","promotionRule":{"discount":"PERCENT_50","endTime":"2026-10-17 12:00:00"},"mallSingleFree":{"status":"ONGOING","endDate":"2026-10-18 12:00:00"}}}]}}`), 200), nil
	})
	out, err := c.Search(context.Background(), "tt12345", 2)
	if err != nil || len(out.Resources) != 1 || !out.HasMore {
		t.Fatalf("%+v %v", out, err)
	}
	r := out.Resources[0]
	if r.ID != "15" || r.DownloadFactor != 0 || r.UploadFactor != 2 || !r.Free || r.PromotionEnd != "2026-10-18T12:00:00+08:00" || r.Seeders != 12 || r.Size != 123456 {
		t.Fatalf("%+v", r)
	}
	for _, fixture := range []string{`{"code":401,"data":{}}`, `{"code":0,"data":{}}`, `{"code":0,"data":{"data":[{"id":1,"name":"x","size":"invalid","status":{}}]}}`, `{"code":0,"data":{"data":null}}`} {
		if _, err := c.parseMTeam([]byte(fixture), 1); err == nil {
			t.Fatal("malformed mteam accepted")
		}
	}
	if out, err := c.parseMTeam([]byte(`{"code":0,"data":{"data":[],"total":0}}`), 1); err != nil || len(out.Resources) != 0 || out.HasMore {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestSiteLabelsRemainAvailableToQualityFilters(t *testing.T) {
	c, _ := New(Config{ID: "s", Kind: "mteam", BaseURL: "https://api.m-team.cc", APIKey: "synthetic"})
	for _, labels := range []string{`"labelsNew":["国配","中字"]`, `"labels":"6"`} {
		body := `{"code":0,"data":{"data":[{"id":"1","name":"Movie.2026.1080p.WEB-DL.H264","smallDescr":"合成描述","size":"1","status":{},` + labels + `}]}}`
		out, err := c.parseMTeam([]byte(body), 1)
		if err != nil || len(out.Resources) != 1 || !strings.Contains(out.Resources[0].Subtitle, "国配") || !strings.Contains(out.Resources[0].Subtitle, "中字") {
			t.Fatalf("tags unavailable: %+v %v", out, err)
		}
	}
	c, _ = New(Config{ID: "s", Kind: "ubits", BaseURL: "https://example.invalid", Cookie: "synthetic"})
	u, _ := url.Parse("https://example.invalid/torrents.php")
	row := strings.Replace(nexusRow, `<img class="pro_free2up">`, `<span class="tags">国语 / 中字</span><img class="pro_free2up">`, 1)
	out, err := c.parseHTML([]byte(authHTML+`<table class="torrents">`+row+`</table>`), u, 1)
	if err != nil || len(out.Resources) != 1 || !strings.Contains(out.Resources[0].Subtitle, "国语 / 中字") {
		t.Fatalf("nexus tags unavailable: %+v %v", out, err)
	}
	bounded := subtitleWithLabels(strings.Repeat("中", 4000), []string{"标签"})
	if len(bounded) > 8192 || !utf8.ValidString(bounded) {
		t.Fatal("subtitle limit or encoding invalid")
	}
}

func validTorrent() []byte {
	b := []byte("d4:infod6:lengthi1e4:name4:test12:piece lengthi16384e6:pieces20:")
	b = append(b, bytes.Repeat([]byte{'x'}, 20)...)
	return append(b, 'e', 'e')
}

func TestTorrentNexusAndHhan(t *testing.T) {
	for _, kind := range []string{"ubits", "jiayuan", "1ptba", "hhanclub"} {
		t.Run(kind, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Cookie") != "synthetic" || r.URL.Query().Get("id") != "202" {
					t.Error("wrong auth/id")
				}
				if r.URL.Path == "/details.php" {
					fmt.Fprint(w, authHTML+`<a href="download.php?id=202">ordinary download</a><a href="download.php?id=202&amp;passkey=synthetic-only-123456">download</a><a href="download.php?id=202">ordinary download</a>`)
					return
				}
				if r.URL.Path != "/download.php" {
					t.Error("wrong endpoint")
				}
				if kind == "hhanclub" && r.URL.Query().Get("passkey") != "synthetic-only-123456" {
					t.Error("passkey lost")
				}
				w.Write(validTorrent())
			}))
			defer srv.Close()
			c, _ := New(Config{ID: "s", Kind: kind, BaseURL: srv.URL, Cookie: "synthetic"})
			b, err := c.Torrent(context.Background(), "202")
			if err != nil || !bytes.Equal(b, validTorrent()) {
				t.Fatalf("torrent %v", err)
			}
			want := 1
			if kind == "hhanclub" {
				want = 2
			}
			if requests != want {
				t.Fatal("wrong request count")
			}
			if _, err := c.Torrent(context.Background(), "https://evil.invalid/download.php?id=202"); err == nil || requests != want {
				t.Fatal("URL input accepted")
			}
		})
	}
}

func TestMTeamMaterialRedirectDoesNotForwardSecrets(t *testing.T) {
	c, _ := New(Config{ID: "s", Kind: "mteam", BaseURL: "https://api.m-team.cc", APIKey: "synthetic-api-key", Cookie: "synthetic-cookie"})
	requests := 0
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			if r.URL.Path != "/api/torrent/genDlToken" || r.Header.Get("x-api-key") != "synthetic-api-key" || r.Header.Get("Cookie") != "" {
				t.Error("wrong token request")
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "id=202" {
				t.Error("wrong token ID")
			}
			return response([]byte(`{"code":0,"data":"https://api.m-team.cc/api/torrent/downloadmaterial?id=202&token=synthetic"}`), 200), nil
		}
		if r.Header.Get("x-api-key") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
			t.Error("credential forwarded to material URL")
		}
		if requests == 2 {
			res := response(nil, 302)
			res.Header.Set("Location", "https://fr1.halomt.com/material?sign=synthetic")
			res.Header.Set("Set-Cookie", "never-forward=synthetic")
			return res, nil
		}
		if r.URL.Host != "fr1.halomt.com" {
			t.Error("unexpected CDN")
		}
		return response(validTorrent(), 200), nil
	})
	if b, err := c.Torrent(context.Background(), "202"); err != nil || len(b) == 0 || requests != 3 {
		t.Fatalf("%d %v", requests, err)
	}
	for _, raw := range []string{"https://evil.invalid/file", "https://fr2.halomt.com/file", "https://fr1.halomt.com.evil.invalid/file", "http://fr1.halomt.com/file", "https://fr1.halomt.com:8443/file", "https://api.m-team.io/file", "https://attacker@fr1.halomt.com/file"} {
		u, _ := url.Parse(raw)
		if c.allowedMaterial(u) {
			t.Fatalf("unsafe material allowed: %s", raw)
		}
	}
}

func TestMalformedTorrentAndRedirectFailSafely(t *testing.T) {
	for _, scenario := range []string{"login", "redirect", "oversize"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := New(Config{ID: "s", Kind: "ubits", BaseURL: "https://example.invalid", Cookie: "synthetic"})
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch scenario {
				case "redirect":
					res := response(nil, 302)
					res.Header.Set("Location", "https://evil.invalid/?passkey=synthetic-secret")
					return res, nil
				case "oversize":
					return response(bytes.Repeat([]byte{'a'}, torrentLimit+1), 200), nil
				default:
					return response([]byte(`<html>secret authentication page</html>`), 200), nil
				}
			})
			_, err := c.Torrent(context.Background(), "1")
			if err == nil || strings.Contains(err.Error(), "synthetic") || strings.Contains(err.Error(), "passkey") || strings.Contains(err.Error(), "evil") {
				t.Fatalf("unsafe error %v", err)
			}
		})
	}
}
