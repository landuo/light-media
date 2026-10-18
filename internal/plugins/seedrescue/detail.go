package seedrescue

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

var mteamHost = regexp.MustCompile(`^(?:[a-z0-9-]+\.)?m-team\.(?:cc|io)$`)

func SiteBase(site Object) string { return SafePageURL(strOr(site["url"], strOr(site["domain"], ""))) }
func IsMTeam(site Object) bool {
	return mteamHost.MatchString(strings.ToLower(sourceDomain(SiteBase(site))))
}
func DetailURL(site Object, tid string) string {
	base := SiteBase(site)
	if base == "" || tid == "" {
		return ""
	}
	for _, r := range tid {
		if !unicode.IsDigit(r) {
			return ""
		}
	}
	if IsMTeam(site) {
		parts := strings.Split(sourceDomain(base), ".")
		return "https://kp.m-team." + parts[len(parts)-1] + "/detail/" + tid
	}
	if truth(site["parser"]) || !(truth(site["_seedrescue_nexus"]) || strings.HasPrefix(strings.ToLower(strOr(site["schema"], "")), "nexus")) {
		return ""
	}
	return joinURL(base, "details.php?id="+tid)
}
func joinURL(base, ref string) string {
	b, e := url.Parse(base)
	if e != nil {
		return ref
	}
	r, e := url.Parse(ref)
	if e != nil {
		return ""
	}
	return b.ResolveReference(r).String()
}
func detailNumber(v any) *int {
	if v == nil {
		return nil
	}
	s := strings.ReplaceAll(strings.TrimSpace(pyString(v)), ",", "")
	if s == "" {
		return nil
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return nil
		}
	}
	n, e := strconv.Atoi(s)
	if e != nil {
		return nil
	}
	return &n
}
func ParseMTeam(payload any, tid string) Object {
	p, ok := payload.(map[string]any)
	if !ok || pyString(p["code"]) != "0" {
		return nil
	}
	data, ok := p["data"].(map[string]any)
	if !ok || pyString(data["id"]) != tid {
		return nil
	}
	status, ok := data["status"].(map[string]any)
	if !ok && truth(data["status"]) {
		return nil
	}
	if status["banned"] == true {
		return nil
	}
	discount := strOr(status["discount"], "")
	if rule, ok := status["promotionRule"].(map[string]any); ok && len(rule) > 0 {
		discount = strOr(rule["discount"], "")
	}
	if personal, ok := status["mallSingleFree"].(map[string]any); ok && personal["status"] == "ONGOING" {
		discount = "FREE"
	}
	factors := map[string]float64{"NORMAL": 1, "_2X": 1, "FREE": 0, "_2X_FREE": 0, "PERCENT_50": .5, "_2X_PERCENT_50": .5, "PERCENT_70": .3}
	var factor any
	if f, ok := factors[discount]; ok {
		factor = f
	}
	return Object{"title": data["name"], "size": deref(detailNumber(data["size"])), "seeders": deref(detailNumber(status["seeders"])), "peers": deref(detailNumber(status["leechers"])), "grabs": deref(detailNumber(status["timesCompleted"])), "downloadvolumefactor": factor}
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func nodes(root *html.Node, pred func(*html.Node) bool) []*html.Node {
	out := []*html.Node{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if pred(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	return out
}
func textNodes(n *html.Node) []string {
	out := []string{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			out = append(out, n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return out
}
func textContent(n *html.Node) string { return strings.Join(textNodes(n), "") }

var labelFree = regexp.MustCompile(`(?i)(?:\[|【)\s*(?:\d+\s*[xX]\s*)?(?:free|免费|免費)\s*(?:\]|】)`)
var labelHalf = regexp.MustCompile(`(?i)(?:\[|【)\s*(?:\d+\s*[xX]\s*)?50%\s*(?:\]|】)`)
var labelThirty = regexp.MustCompile(`(?:\[|【)\s*30%\s*(?:\]|】)`)
var labelUnknown = regexp.MustCompile(`(?i)free|免费|免費|\d+%|促销|促銷`)
var detailCounts = regexp.MustCompile(`\d[\d,]*`)
var sizeLabel = regexp.MustCompile(`(?i)^\s*(?:大小|體積|体积|Size)\s*[:：]?\s*$`)
var sizeValue = regexp.MustCompile(`(?i)([\d,.]+)\s*([KMGT]i?B|bytes|字节)`)

// ParseNexus implements the source's consumed heading/statistics selectors.
// x/net/html uses HTML5 repair; malformed HTML repaired differently by libxml2
// remains an explicit adapter compatibility boundary (not an XPath port).
func ParseNexus(text, page, tid string) (Object, error) {
	doc, e := html.Parse(strings.NewReader(text))
	if e != nil {
		return nil, e
	}
	headings := nodes(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "h1" && attr(n, "id") == "top" })
	if len(headings) == 0 {
		headings = nodes(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "h1" })
	}
	if len(headings) == 0 || len(nodes(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "input" && attr(n, "type") == "password"
	})) > 0 {
		return nil, nil
	}
	links := nodes(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "a" && attr(n, "href") != "" })
	enclosure := ""
	pageURL, e := url.Parse(page)
	if e != nil {
		return nil, e
	}
	for _, link := range links {
		raw := joinURL(page, attr(link, "href"))
		u, e := url.Parse(raw)
		if e == nil && strings.HasSuffix(u.Path, "/download.php") && u.Host == pageURL.Host && ExtractTorrentID(raw) == tid {
			enclosure = raw
			break
		}
	}
	if enclosure == "" {
		return nil, nil
	}
	heading := headings[0]
	texts := textNodes(heading)
	labels := ""
	if len(texts) > 1 {
		labels = strings.Join(texts[1:], " ")
	}
	var factor any = float64(1)
	if labelFree.MatchString(labels) {
		factor = float64(0)
	} else if labelHalf.MatchString(labels) {
		factor = .5
	} else if labelThirty.MatchString(labels) {
		factor = .3
	} else if labelUnknown.MatchString(labels) {
		factor = nil
	}
	counts := []string{}
	peers := nodes(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && attr(n, "id") == "peercount" })
	if len(peers) > 0 {
		counts = detailCounts.FindAllString(textContent(peers[0]), -1)
	}
	var seeders, leechers, grabs, size any
	if len(counts) > 0 {
		seeders = deref(detailNumber(counts[0]))
	}
	if len(counts) > 1 {
		leechers = deref(detailNumber(counts[1]))
	}
	for _, link := range links {
		href := attr(link, "href")
		if strings.Contains(href, "viewsnatches.php") && ExtractTorrentID(href) == tid {
			grabs = deref(detailNumber(detailCounts.FindString(textContent(link))))
			break
		}
	}
	labelsNodes := nodes(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "b" {
			return false
		}
		for p := n.Parent; p != nil; p = p.Parent {
			if p.Type == html.ElementNode && p.Data == "td" {
				return true
			}
		}
		return false
	})
	for _, label := range labelsNodes {
		if !sizeLabel.MatchString(textContent(label)) {
			continue
		}
		tail := ""
		for s := label.NextSibling; s != nil && s.Type == html.TextNode; s = s.NextSibling {
			tail += s.Data
		}
		v := sizeValue.FindStringSubmatch(tail)
		if len(v) > 2 {
			n, e := strconv.ParseFloat(strings.ReplaceAll(v[1], ",", ""), 64)
			if e != nil {
				return nil, e
			}
			unit := strings.ToUpper(v[2])
			power := 0
			if i := strings.Index("KMGT", unit[:1]); i >= 0 {
				power = i + 1
			}
			size = int64(n * math.Pow(1024, float64(power)))
			break
		}
	}
	title := ""
	if heading.FirstChild != nil && heading.FirstChild.Type == html.TextNode {
		title = heading.FirstChild.Data
	}
	if title == "" {
		title = textContent(heading)
	}
	return Object{"title": strings.TrimSpace(title), "page_url": page, "enclosure": enclosure, "seeders": seeders, "peers": leechers, "grabs": grabs, "size": size, "downloadvolumefactor": factor}, nil
}

type DetailRequest struct {
	Method, URL, UserAgent string
	Proxy                  bool
	Timeout                int
	Headers                map[string]string
	Cookie                 any
	Form                   map[string]string
	AllowRedirects         bool
}
type DetailResponse struct {
	Status int
	Text   string
	JSON   any
}
type DetailTransport interface {
	DoDetail(context.Context, DetailRequest) (*DetailResponse, error)
}

var detailChallenge = regexp.MustCompile(`(?i)type\s*=\s*["']password|cf-chl-|challenge-platform`)

func FetchDetail(ctx context.Context, site Object, tid, userAgent string, transport DetailTransport) (result DetailResult) {
	result = DetailResult{Status: "unavailable", Error: ptr("详情获取异常，请检查站点连接或响应格式")}
	// Request/JSON/parser errors use the source's fixed error, never secret text.
	page := DetailURL(site, tid)
	if page == "" {
		return DetailResult{Status: "unsupported", Error: ptr("该站暂未适配按 ID 查详情")}
	}
	ua := strOr(site["ua"], userAgent)
	timeout := asInt(site["timeout"], 15)
	if !truth(site["timeout"]) {
		timeout = 15
	}
	if timeout > 20 {
		timeout = 20
	}
	req := DetailRequest{Method: "GET", URL: page, UserAgent: ua, Proxy: truth(site["proxy"]), Timeout: timeout, Cookie: site["cookie"], Headers: map[string]string{}, AllowRedirects: false}
	api := ""
	if IsMTeam(site) {
		if !truth(site["apikey"]) {
			return DetailResult{Status: "unavailable", Error: ptr("站点未配置 API Key")}
		}
		host := sourceDomain(SiteBase(site))
		if !strings.HasPrefix(host, "api.") && !strings.HasPrefix(host, "api2.") {
			parts := strings.Split(host, ".")
			host = "api.m-team." + parts[len(parts)-1]
		}
		api = "https://" + host + "/api/torrent/"
		req.Method = "POST"
		req.URL = api + "detail"
		req.Cookie = nil
		req.Headers["x-api-key"] = pyString(site["apikey"])
		req.Form = map[string]string{"id": tid}
	}
	response, e := transport.DoDetail(ctx, req)
	if e != nil {
		return result
	}
	if response == nil || response.Status != 200 {
		if response != nil && response.Status == 404 {
			return DetailResult{Status: "unsupported", Error: ptr("详情入口未命中，尝试搜索兜底")}
		}
		return DetailResult{Status: "unavailable", Error: ptr("详情请求失败，请检查登录状态、网络或站点限流")}
	}
	var torrent Object
	if IsMTeam(site) {
		torrent = ParseMTeam(response.JSON, tid)
		if torrent == nil {
			return DetailResult{Status: "unavailable", Error: ptr("详情接口未返回对应 ID，请检查权限或种子状态")}
		}
		params := Object{"method": "post", "cookie": false, "params": Object{"id": tid}, "header": Object{"x-api-key": site["apikey"], "User-Agent": ua}, "proxy": truth(site["proxy"]), "result": "data"}
		b, e := json.Marshal(params)
		if e != nil {
			return result
		}
		torrent["page_url"] = page
		torrent["enclosure"] = "[" + base64.StdEncoding.EncodeToString(b) + "]" + api + "genDlToken"
	} else {
		if detailChallenge.MatchString(response.Text) {
			return DetailResult{Status: "unavailable", Error: ptr("详情页要求登录或验证，请检查站点登录状态")}
		}
		torrent, e = ParseNexus(response.Text, page, tid)
		if e != nil {
			return result
		}
		if torrent == nil {
			return DetailResult{Status: "unsupported", Error: ptr("详情页结构未识别，尝试搜索兜底")}
		}
	}
	if torrent["seeders"] != nil && torrent["downloadvolumefactor"] != nil {
		return DetailResult{Torrent: torrent, Status: "ready"}
	}
	return DetailResult{Torrent: torrent, Status: "partial", Error: ptr("详情未提供完整做种人数或促销")}
}
