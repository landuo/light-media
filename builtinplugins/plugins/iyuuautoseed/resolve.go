package iyuuautoseed

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"light-media/builtinplugins/internal/encodingdetector"
	"light-media/builtinplugins/plugins/autosignin/pyregex"
	regexengine "light-media/builtinplugins/plugins/autosignin/regexengine"
	"light-media/builtinplugins/plugins/iyuuautoseed/compat/htmlextract"
)

type PageKind string

const (
	DetailsPage PageKind = "details"
	GPWPage     PageKind = "gpw"
)

type PageResponse struct {
	Status int
	Text   string
}

// SiteHTTP selects only fixed original page/API routes using an opaque host
// site ref and an ID. Worker-origin URLs, cookies, API keys and proxy settings
// cannot enter this interface. Text must carry the source response's decoding.
type SiteHTTP interface {
	MTeamToken(context.Context, string, any) (*APIResponse, error)
	Page(context.Context, string, PageKind, any) (*PageResponse, error)
}
type Resolver struct{ HTTP SiteHTTP }

var removeUID = regexp.MustCompile(`[&?][uU][iIİı][dD]=`)
var removePasskey = regexp.MustCompile(`[&?][pP][aA][sSſ][sSſ][kKK][eE][yY]=`)

// Resolve follows IYUU 2.22 __init__.py:1311–1542 using the pinned 2.19 resolver.
func (r Resolver) Resolve(ctx context.Context, seed map[string]any, site Site, template string) (string, error) {
	siteURL, ok := site.Fields["url"].(string)
	if !ok {
		return "", sourceFault("TypeError", "site.url contains")
	}
	fallback := func(reason string) (string, error) {
		sourceLog(ctx, 1495, logString(site.Fields["name"])+" Url转换失败，"+reason+"：site_url="+siteURL+"，base_url="+template+", seed="+logString(seed))
		return r.page(ctx, seed, site, false)
	}
	if strings.Contains(siteURL, "m-team.") {
		if !truth(site.Fields["apikey"]) {
			sourceLog(ctx, 1339, "m-team站点的apikey未配置")
			return "", nil
		}
		if r.HTTP == nil {
			return "", errors.New("site HTTP dependency required")
		}
		res, e := r.HTTP.MTeamToken(ctx, site.Ref, seed["torrent_id"])
		if e != nil {
			return fallback(e.Error())
		}
		if res == nil || res.Status >= 400 {
			sourceLog(ctx, 1358, "m-team 获取种子下载链接失败："+pyString(seed["torrent_id"]))
			return "", nil
		}
		value, e := decodeJSON(res.Body)
		if e != nil {
			return fallback("JSONDecodeError")
		}
		m, ok := value.(map[string]any)
		if !ok {
			return fallback("AttributeError: response has no get")
		}
		if !truth(m["data"]) {
			return "", nil
		}
		valueURL, ok := m["data"].(string)
		if !ok {
			return "", errors.New("unsupported non-string M-Team download URL")
		}
		return valueURL, nil
	}
	if strings.Contains(siteURL, "monikadesign.") {
		if !truth(site.Fields["rss"]) {
			sourceLog(ctx, 1367, "Monika站点的rss链接未配置")
			return "", nil
		}
		rss, ok := site.Fields["rss"].(string)
		if !ok {
			return fallback("TypeError: rss must be a string")
		}
		pattern, err := pyregex.Compile(`/rss/\d+\.(\w+)`, false)
		if err != nil {
			return "", err
		}
		match, err := pattern.FindStringMatch(rss)
		if err != nil {
			return fallback(err.Error())
		}
		if match == nil {
			return fallback("AttributeError: no RSS match")
		}
		return siteURL + "torrents/download/" + pyString(seed["torrent_id"]) + "." + match.GroupByNumber(1).String(), nil
	}
	if strings.Contains(siteURL, "greatposterwall.") {
		return r.page(ctx, seed, site, true)
	}
	special := strings.Contains(template, "hash=") || strings.Contains(template, "authkey=")
	for _, domain := range []string{"hdchina.org", "hdsky.me", "hdcity.in", "totheglory.im"} {
		special = special || strings.Contains(siteURL, domain)
	}
	if special {
		return r.page(ctx, seed, site, false)
	}
	input := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(template, "id={}", "id={id}"), "/{}", "/{id}"), "/{torrent_key}", "")
	fields := map[string]any{"id": seed["torrent_id"], "passkey": or(site.Fields["passkey"], ""), "uid": or(site.Fields["uid"], "")}
	value, e := sourceFormat(input, fields)
	if e != nil {
		return fallback(e.Error())
	}
	if strings.Contains(value, "{") {
		sourceLog(ctx, 1486, "当前不支持该站点的辅助任务，Url转换失败："+logString(seed))
		return "", nil
	}
	return siteURL + removePasskey.ReplaceAllString(removeUID.ReplaceAllString(value, ""), ""), nil
}

func (r Resolver) page(ctx context.Context, seed map[string]any, site Site, gpw bool) (string, error) {
	siteURL, ok := site.Fields["url"].(string)
	if !ok || siteURL == "" {
		line := 1504
		if gpw {
			line = 1379
		}
		sourceLog(ctx, line, "站点 "+logString(site.Fields["name"])+" 未获取站点地址，无法获取种子下载链接")
		return "", nil
	}
	if r.HTTP == nil {
		return "", errors.New("site HTTP dependency required")
	}
	kind := DetailsPage
	pageURL := siteURL + "details.php?id=" + pyString(seed["torrent_id"]) + "&hit=1"
	startLine, failureLine, emptyLine, exceptLine := 1508, 1538, 1520, 1541
	if gpw {
		kind = GPWPage
		pageURL = siteURL + "torrents.php?torrentid=" + pyString(seed["torrent_id"]) + "&hit=1"
		startLine, failureLine, emptyLine, exceptLine = 1384, 1394, 1403, 1434
	}
	sourceLog(ctx, startLine, "正在获取种子下载链接："+pageURL+" ...")
	res, e := r.HTTP.Page(ctx, site.Ref, kind, seed["torrent_id"])
	if e != nil {
		sourceLog(ctx, exceptLine, "获取种子下载链接失败："+e.Error())
		if encodingdetector.ErrorClass(e) != "" {
			return "", nil
		}
		return "", e
	}
	if res == nil || (res.Status != 200 && res.Status != 500) {
		status := ""
		if res != nil && res.Status < 400 {
			status = itoa(res.Status)
		}
		sourceLog(ctx, failureLine, "获取种子下载链接失败，请求失败："+pageURL+"，"+status)
		return "", nil
	}
	if res.Text == "" {
		sourceLog(ctx, emptyLine, "获取种子下载链接失败，页面内容为空："+pageURL)
		return "", nil
	}
	links, e := htmlLinks(res.Text)
	if e != nil {
		sourceLog(ctx, exceptLine, "获取种子下载链接失败："+e.Error())
		return "", nil
	}
	if !links.document {
		if gpw {
			sourceLog(ctx, 1408, "解析页面失败："+pageURL)
		} else {
			sourceLog(ctx, 1541, "获取种子下载链接失败：'NoneType' object has no attribute 'xpath'")
		}
		return "", nil
	}
	if gpw {
		tid := pyString(seed["torrent_id"])
		var pattern *regexengine.Regexp
		candidates := 0
		for _, a := range links.anchors {
			if !strings.Contains(a.href, "torrents.php?action=download") {
				continue
			}
			candidates++
			if pattern == nil {
				pattern, e = pyregex.Compile("id="+tid+"(?:&|$)", false)
				if e != nil {
					sourceLog(ctx, 1434, "获取种子下载链接失败："+e.Error())
					return "", nil
				}
			}
			matched, e := pattern.MatchString(a.href)
			if e != nil {
				sourceLog(ctx, 1434, "获取种子下载链接失败："+e.Error())
				return "", nil
			}
			if matched {
				joined, e := sourceURLJoin(siteURL, a.href)
				if e != nil {
					sourceLog(ctx, 1434, "获取种子下载链接失败："+e.Error())
					return "", nil
				}
				sourceLog(ctx, 1431, "获取种子下载链接成功："+joined)
				return joined, nil
			}
		}
		if candidates == 0 {
			sourceLog(ctx, 1415, "获取种子下载链接失败，未找到下载链接："+pageURL)
		} else {
			sourceLog(ctx, 1426, "未找到与 torrent_id="+tid+" 对应的下载链接")
		}
		return "", nil
	}
	groups := [][]string{links.forms, {}, {}, {}}
	for _, a := range links.anchors {
		if strings.Contains(a.href, "download.php?hash=") {
			groups[1] = append(groups[1], a.href)
		}
		if strings.Contains(a.href, "download.php?id=") {
			groups[2] = append(groups[2], a.href)
		}
		if a.class == "index" && strings.Contains(a.href, "/dl/") {
			groups[3] = append(groups[3], a.href)
		}
	}
	for _, values := range groups {
		if len(values) > 0 {
			href := values[0]
			sourceLog(ctx, 1528, "获取种子下载链接成功："+href)
			if !strings.HasPrefix(href, "http") {
				href = siteURL + strings.TrimPrefix(href, "/")
			}
			return href, nil
		}
	}
	sourceLog(ctx, 1535, "获取种子下载链接失败，未找到下载链接："+pageURL)
	return "", nil
}

type linkAnchor struct{ href, class string }
type links struct {
	document bool
	forms    []string
	anchors  []linkAnchor
}

func htmlLinks(text string) (links, error) {
	source, e := htmlextract.Extract(text)
	if e != nil {
		return links{}, sourceFault("UnicodeEncodeError", "lxml HTML input")
	}
	out := links{document: source.Document, forms: source.Forms}
	for _, a := range source.Anchors {
		out.anchors = append(out.anchors, linkAnchor{a.Href, a.Class})
	}
	return out, nil
}
