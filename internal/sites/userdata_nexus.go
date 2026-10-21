package sites

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"light-media/internal/releasemeta/legacyanime"
)

type sourceNexusReader struct {
	c                                                      *Client
	ctx                                                    context.Context
	d                                                      *SourceUserData
	s                                                      *UserStats
	err, primaryError                                      error
	requests                                               int
	now                                                    time.Time
	id                                                     *string
	detail, seed                                           string
	seedParams                                             url.Values
	observed                                               map[string]bool
	seedComplete, seedShape, seedInfoKnown, seedSizesKnown bool
}

func (r *sourceNexusReader) x(doc *html.Node, expr string) []*html.Node {
	if r.err != nil || doc == nil {
		return nil
	}
	nodes, e := htmlquery.QueryAll(doc, expr)
	if e != nil {
		r.err = errors.New("site_source_xpath_invalid")
	}
	return nodes
}
func (r *sourceNexusReader) m(pattern, raw string, ignoreCase bool) []string {
	if r.err != nil {
		return nil
	}
	m, e := sourceNexusMatch(pattern, raw, ignoreCase)
	if e != nil {
		r.err = e
	}
	return m
}
func (r *sourceNexusReader) doc(raw []byte) *html.Node {
	if r.err != nil {
		return nil
	}
	doc, e := sourceNexusDOM(string(raw))
	if e != nil {
		r.err = e
	}
	return doc
}
func (r *sourceNexusReader) prepared(raw string) string {
	value, e := sourceNexusPrepared(raw)
	if e != nil {
		r.err = e
	}
	return value
}
func (r *sourceNexusReader) size(raw string) (int64, bool) {
	n, valid, e := sourceNexusFileSize(raw)
	if e != nil {
		r.err = e
	}
	return n, valid
}

func (r *sourceNexusReader) origin() *url.URL {
	u := *r.c.base
	u.Path = "/"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return &u
}
func (r *sourceNexusReader) resolved(base *url.URL, ref string) *url.URL {
	if r.err != nil || base == nil {
		return nil
	}
	u, e := url.Parse(ref)
	if e != nil {
		r.err = errors.New("site_source_link_invalid")
		return nil
	}
	u = base.ResolveReference(u)
	if !sameOrigin(r.c.base, u) {
		r.err = errors.New("site_credential_origin_mismatch")
		return nil
	}
	return u
}

func (r *sourceNexusReader) page(u *url.URL, params url.Values) []byte {
	if r.err != nil || u == nil {
		return nil
	}
	if e := r.ctx.Err(); e != nil {
		r.err = e
		return nil
	}
	method := http.MethodGet
	var body []byte
	headers := r.c.auth()
	if r.c.config.UserAgent != "" {
		headers["Content-Type"] = "application/x-www-form-urlencoded; charset=UTF-8"
	}
	if len(params) > 0 {
		method = http.MethodPost
		body = []byte(params.Encode())
		if headers["Content-Type"] == "" {
			headers["Content-Type"] = "application/x-www-form-urlencoded"
		}
	}
	r.requests++
	raw, _, e := r.c.request(r.ctx, method, u, body, headers, htmlLimit)
	if e != nil {
		if r.ctx.Err() != nil {
			r.err = r.ctx.Err()
			return nil
		}
		if t := sourceStatsTrace(r.ctx); t != nil && t.guardError != nil {
			r.err = t.guardError
			return nil
		}
		if e.Error() == "site_body_limit" || e.Error() == "site_credential_origin_mismatch" {
			r.err = e
			return nil
		}
		if r.requests == 1 {
			r.primaryError = e
		}
		r.s.Warnings = append(r.s.Warnings, e.Error())
		return nil
	}
	contentType := ""
	if t := sourceStatsTrace(r.ctx); t != nil {
		contentType = t.contentType
		if t.status != 200 && t.status != 403 && t.status != 500 {
			if r.requests == 1 {
				r.primaryError = errors.New("site_http_error")
			}
			return nil
		}
	}
	if len(raw) == 0 {
		return nil
	}
	decoder, e := charset.NewReader(bytes.NewReader(raw), contentType)
	if e != nil {
		r.err = errors.New("site_source_encoding_invalid")
		return nil
	}
	raw, e = io.ReadAll(io.LimitReader(decoder, htmlLimit+1))
	if e != nil || int64(len(raw)) > htmlLimit {
		r.err = errors.New("site_body_limit")
		return nil
	}
	if sourceAccountChallenge(raw) {
		if r.requests == 1 {
			r.primaryError = errors.New("site_login_challenge")
		}
		return nil
	}
	return raw
}

func (r *sourceNexusReader) loggedIn(doc *html.Node) bool {
	if doc == nil || len(r.x(doc, `//input[@type="password"]`)) > 0 {
		return false
	}
	return len(r.x(doc, `//a[contains(@href,"logout") or contains(@data-url,"logout") or contains(@href,"mybonus") or contains(@onclick,"logout") or contains(@href,"usercp") or contains(@lay-on,"logout")]|//form[contains(@action,"logout")]|//div[@class="user-info-side"]|//a[@id="myitem"]`)) > 0
}

func (c *Client) sourceNexusUserStats(ctx context.Context, options UserStatsOptions, now time.Time) (s UserStats, dto *SourceUserData, err error) {
	s = UserStats{CapturedAt: now.UTC().Format(time.RFC3339), Sources: map[string]string{}, Warnings: []string{}}
	defer func() {
		if err != nil {
			message := err.Error()
			s.Error = &message
		}
	}()
	client := *c
	httpClient := *c.http
	httpClient.Timeout = 60 * time.Second
	client.http = &httpClient
	dto = emptySourceUserData()
	r := sourceNexusReader{c: &client, ctx: ctx, d: dto, s: &s, detail: "userdetails.php?id=", seed: "getusertorrentlistajax.php?userid=", now: now, observed: map[string]bool{}, seedInfoKnown: true, seedSizesKnown: true}
	home := r.page(c.base, nil)
	doc := r.doc(home)
	if r.err != nil {
		return s, nil, r.err
	}
	if !r.loggedIn(doc) {
		message := "未检测到已登陆，请检查cookies是否过期"
		dto.Error = &message
		if r.primaryError != nil {
			return s, dto, r.primaryError
		}
		return s, dto, errors.New("site_authentication_failed")
	}
	r.sitePage(string(home))
	r.baseInfo(string(home))
	if r.detail != "" {
		r.detailInfo(r.doc(r.page(r.resolved(r.origin(), r.detail), nil)))
	}
	if options.ReadMessages {
		r.messages()
	}
	r.seeding()
	if r.err != nil {
		return s, nil, r.err
	}
	r.native()
	if r.primaryError != nil {
		return s, dto, r.primaryError
	}
	if !numericID.MatchString(s.UserID) || s.Username == "" {
		return s, dto, errors.New("site_user_identity_missing")
	}
	if s.Uploaded == nil && s.Downloaded == nil && s.Bonus == nil && s.Seeding == nil {
		return s, dto, errors.New("site_user_stats_unrecognized")
	}
	return s, dto, nil
}

func (r *sourceNexusReader) sitePage(raw string) {
	prepared := r.prepared(raw)
	m := r.m(`userdetails.php\?id=(\d+)`, prepared, false)
	if len(m) > 1 && legacyanime.Strip(m[0]) != "" {
		r.detail = strings.TrimLeft(legacyanime.Strip(m[0]), "/")
		id := m[1]
		r.id = &id
		r.d.UserID = id
		r.seed = "getusertorrentlistajax.php?userid=" + id + "&type=seeding"
	} else if len(r.m(`(userdetails)`, prepared, false)) > 0 {
		r.detail = "userdetails"
		r.id = nil
		r.seed = ""
	}
}

func (r *sourceNexusReader) baseInfo(raw string) {
	r.traffic(raw)
	doc := r.doc([]byte(raw))
	if r.err != nil {
		return
	}
	probe := UserStats{Sources: map[string]string{}}
	if doc != nil {
		parseNexusUnread(&probe, doc)
	}
	if probe.MessageUnread != nil {
		r.d.MessageUnread = *probe.MessageUnread
		r.observed["message_unread"] = true
	}
	id := "None"
	if r.id != nil {
		id = *r.id
	}
	base := `//a[contains(@href,"userdetails") and contains(@href,"` + id + `")]`
	if n := r.x(doc, base+`//b//text()`); len(n) > 0 {
		value := sourceNexusText(n[0])
		r.d.Username = &value
		return
	}
	if n := r.x(doc, base+`//text()`); len(n) > 0 {
		value := sourceNexusText(n[0])
		r.d.Username = &value
	}
	if n := r.x(doc, `//a[contains(@href,"userdetails")]//strong//text()`); len(n) > 0 {
		value := sourceNexusText(n[0])
		r.d.Username = &value
	}
}

func (r *sourceNexusReader) traffic(raw string) {
	raw = r.prepared(raw)
	for _, field := range []struct {
		key, pattern string
		dst          *int64
	}{{"uploaded", `[^总]上[传傳]量?[:：_<>/a-zA-Z-="'\s#;]+([\d,.\s]+[KMGTPI]*B)`, &r.d.Upload}, {"downloaded", `[^总子影力]下[载載]量?[:：_<>/a-zA-Z-="'\s#;]+([\d,.\s]+[KMGTPI]*B)`, &r.d.Download}} {
		*field.dst = 0
		r.observed[field.key] = false
		if m := r.m(field.pattern, raw, true); len(m) > 1 {
			value, valid := r.size(legacyanime.Strip(m[1]))
			*field.dst = value
			r.observed[field.key] = valid
		}
	}
	r.ratio(r.m(`分享率[:：_<>/a-zA-Z-="'\s#;]+([\d,.\s]+)`, raw, false))
	r.d.Leeching = 0
	if m := r.m(`(Torrents leeching|下载中)[\u4E00-\u9FA5\D\s]+(\d+)[\s\S]+<`, raw, false); len(m) > 2 {
		value, valid := sourceNexusStringInt(m[2])
		r.d.Leeching = value
		r.observed["leeching"] = valid
	}
	doc := r.doc([]byte(raw))
	r.d.Bonus = 0
	var coin float64
	coinObserved := false
	for _, c := range []struct {
		name   string
		weight float64
	}{{"gold", 10000}, {"silver", 100}, {"copper", 1}} {
		if n := r.x(doc, `//span[@class="ucoin-symbol ucoin-`+c.name+`"]//text()`); len(n) > 0 {
			v, valid := sourceNexusStringFloat(sourceNexusText(n[len(n)-1]))
			if valid {
				coin += v * c.weight
				coinObserved = true
			}
		}
	}
	r.d.Bonus = coin
	r.observed["bonus"] = coinObserved
	if coin == 0 {
		var m []string
		if n := r.x(doc, `//a[contains(@href,"mybonus")]/text()`); len(n) > 0 {
			m = r.m(`([\d,.]+)`, legacyanime.Strip(sourceNexusText(n[0])), false)
		}
		if len(m) < 2 || legacyanime.Strip(m[1]) == "" {
			m = r.m(`mybonus.[\[\]:：<>/a-zA-Z_\-="'\s#;.(使用魔力值豆]+\s*([\d,.]+)[<()&\s]`, raw, false)
		}
		if len(m) < 2 || legacyanime.Strip(m[1]) == "" {
			m = r.m(`[魔力值|\]][\[\]:：<>/a-zA-Z_\-="'\s#;]+\s*([\d,.]+|"[\d,.]+\")[<>()&\s]`, raw, false)
		}
		if len(m) > 1 && legacyanime.Strip(m[1]) != "" {
			v, valid := sourceNexusStringFloat(strings.Trim(m[1], `"`))
			r.d.Bonus = v
			r.observed["bonus"] = valid
		}
	}
	if r.c.config.Kind == "hhanclub" {
		for _, f := range []struct {
			index, key string
			dst        *int64
		}{{"4", "uploaded", &r.d.Upload}, {"5", "downloaded", &r.d.Download}} {
			nodes := r.x(doc, `//*[@id="user-info-panel"]/div[2]/div[2]/div[`+f.index+`]/text()`)
			if len(nodes) == 0 {
				r.err = sourceModuleError(nil)
				return
			}
			*f.dst = 0
			r.observed[f.key] = false
			if m := r.m(`[_<>/a-zA-Z-="'\s#;]+([\d,.\s]+[KMGTPI]*B)`, sourceNexusText(nodes[0]), false); len(m) > 1 {
				n, valid := r.size(legacyanime.Strip(m[1]))
				*f.dst = n
				r.observed[f.key] = valid
			}
		}
		nodes := r.x(doc, `//*[@id="user-info-panel"]/div[2]/div[1]/div[1]/div/text()`)
		if len(nodes) == 0 {
			r.err = sourceModuleError(nil)
			return
		}
		r.ratio(r.m(`分享率][:：_<>/a-zA-Z-="'\s#;]+([\d,.\s]+)`, sourceNexusText(nodes[0]), false))
	}
}

func (r *sourceNexusReader) ratio(match []string) {
	r.d.Ratio = sourceNexusRatio(r.d.Upload, r.d.Download)
	r.observed["ratio"] = r.observed["uploaded"] && r.observed["downloaded"]
	if len(match) > 1 && legacyanime.Strip(match[1]) != "" {
		value, valid := sourceNexusStringFloat(match[1])
		r.d.Ratio = value
		r.observed["ratio"] = valid
	}
}
