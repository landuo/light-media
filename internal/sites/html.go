package sites

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

func attribute(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, v := range strings.Fields(attribute(n, "class")) {
		if v == class {
			return true
		}
	}
	return false
}

func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		walk(ch, fn)
	}
}

func find(n *html.Node, fn func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	walk(n, func(ch *html.Node) {
		if ch.Type == html.ElementNode && fn(ch) {
			out = append(out, ch)
		}
	})
	return out
}

func text(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(ch *html.Node) {
		if ch.Type == html.ElementNode && (ch.Data == "script" || ch.Data == "style") {
			return
		}
		if ch.Type == html.TextNode {
			b.WriteString(ch.Data)
			b.WriteByte(' ')
		}
		for c := ch.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func firstClass(n *html.Node, class string) *html.Node {
	all := find(n, func(ch *html.Node) bool { return hasClass(ch, class) })
	if len(all) == 0 {
		return nil
	}
	return all[0]
}

func hasLoginChallenge(b []byte) bool {
	s := strings.ToLower(string(b))
	// The passive Cloudflare JS Detection asset is also injected into valid,
	// authenticated pages (including PTTime). It is not a challenge form.
	// Ignore only that asset path; interactive challenge markers still fail.
	s = strings.ReplaceAll(s, "/cdn-cgi/challenge-platform/scripts/jsd/main.js", "")
	for _, marker := range []string{"cf-chl-", "challenge-platform", "just a moment...", "g-recaptcha", "h-captcha", "turnstile"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func pageDOM(b []byte) (*html.Node, error) {
	if hasLoginChallenge(b) {
		return nil, errors.New("site_login_challenge")
	}
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("site_invalid_html")
	}
	if len(find(doc, func(n *html.Node) bool {
		return n.Data == "input" && strings.EqualFold(attribute(n, "type"), "password")
	})) > 0 {
		return nil, errors.New("site_authentication_failed")
	}
	loggedIn := false
	for _, a := range find(doc, func(n *html.Node) bool { return n.Data == "a" }) {
		u, err := url.Parse(attribute(a, "href"))
		if err == nil && (strings.HasSuffix(u.Path, "/logout.php") || u.Path == "logout.php" || u.Path == "logout") {
			loggedIn = true
		}
	}
	if !loggedIn {
		return nil, errors.New("site_authentication_or_template_changed")
	}
	return doc, nil
}

func (c *Client) parseHTML(b []byte, pageURL *url.URL, page int) (SearchResult, error) {
	out := SearchResult{Resources: []Resource{}, Page: page}
	doc, err := pageDOM(b)
	if err != nil {
		return out, err
	}
	var grid *html.Node
	var rows []*html.Node
	if c.config.Kind == "hhanclub" {
		inputs := find(doc, func(n *html.Node) bool {
			return n.Data == "input" && attribute(n, "id") == "searchinput" && attribute(n, "name") == "keyword"
		})
		grids := find(doc, func(n *html.Node) bool { return n.Data == "div" && hasClass(n, "torrents-table") })
		if len(inputs) != 1 || len(grids) != 1 {
			return out, errors.New("site_protocol_changed")
		}
		grid = grids[0]
		rows = find(grid, func(n *html.Node) bool { return n.Data == "div" && hasClass(n, "torrent-table-sub-info") })
	} else if host := c.base.Hostname(); c.config.Kind == "nexusphp" && (host == "cspt.cc" || host == "cspt.top") {
		// 财神 uses div.torrents > div.torrent-table-for-spider for its listing.
		lists := find(doc, func(n *html.Node) bool {
			return n.Data == "div" && hasClass(n, "torrent-table-for-spider") &&
				n.Parent != nil && n.Parent.Data == "div" && hasClass(n.Parent, "torrents")
		})
		if len(lists) > 1 {
			return out, errors.New("site_protocol_changed")
		}
		if len(lists) == 1 {
			list := lists[0]
			grid = list.Parent
			rows = find(list, func(n *html.Node) bool {
				return n.Parent == list && n.Data == "div" && hasClass(n, "torrent-table-sub-info")
			})
		}
	}
	if grid == nil {
		grids := find(doc, func(n *html.Node) bool {
			return n.Data == "table" && (hasClass(n, "torrents") || attribute(n, "id") == "torrents")
		})
		if len(grids) == 0 && c.confirmedNexusEmpty(doc, pageURL) {
			return out, nil
		}
		if len(grids) != 1 {
			return out, errors.New("site_protocol_changed")
		}
		grid = grids[0]
		rows = find(grid, func(n *html.Node) bool {
			if n.Data != "tr" {
				return false
			}
			for p := n.Parent; p != nil; p = p.Parent {
				if p.Data == "table" {
					return p == grid
				}
			}
			return false
		})
	}
	seen := map[string]bool{}
	var rowError error
	var columns *nexusColumns
	if c.config.Kind == "nexusphp" && grid.Data == "table" {
		columns, err = nexusSearchColumns(rows)
		if err != nil {
			return out, err
		}
	}
	for _, row := range rows {
		r, isResult, err := c.parseRow(row, pageURL, columns)
		if err != nil {
			if rowError == nil {
				rowError = err
			}
			continue
		}
		if !isResult {
			continue
		}
		if seen[r.ID] {
			if rowError == nil {
				rowError = errors.New("site_duplicate_result")
			}
			continue
		}
		seen[r.ID] = true
		out.Resources = append(out.Resources, r)
		if len(out.Resources) > 300 {
			return out, errors.New("site_result_limit")
		}
	}
	if len(out.Resources) == 0 {
		if rowError != nil {
			return out, rowError
		}
		lower := strings.ToLower(text(grid))
		empty := false
		for _, marker := range []string{"没有找到", "沒有找到", "没有种子", "沒有種子", "no torrents found", "nothing found", "未找到"} {
			if strings.Contains(lower, marker) {
				empty = true
			}
		}
		if !empty {
			return out, errors.New("site_protocol_changed")
		}
	}
	// Follow only a proven same-origin next-page link. HTML page sizes vary by
	// user preference, so a full row count alone cannot establish another page.
	for _, a := range find(doc, func(n *html.Node) bool { return n.Data == "a" }) {
		u, err := url.Parse(attribute(a, "href"))
		if err != nil {
			continue
		}
		u = pageURL.ResolveReference(u)
		if !sameOrigin(u, c.base) || u.Path != pageURL.Path {
			continue
		}
		q := u.Query()
		next, err := strconv.Atoi(q.Get("page"))
		if err != nil || next != page {
			continue
		}
		if q.Get("search") == pageURL.Query().Get("search") {
			out.HasMore = true
		}
	}
	return out, rowError
}

// The observed Nexus empty page omits the torrent grid entirely. Recognize
// only its authenticated search form and exact message cell shape; an arbitrary
// logged-in page without rows is still a protocol error.
func (c *Client) confirmedNexusEmpty(doc *html.Node, pageURL *url.URL) bool {
	if !sameOrigin(pageURL, c.base) || path.Base(pageURL.Path) != "torrents.php" || !pageURL.Query().Has("search") {
		return false
	}
	searchboxes := find(doc, func(n *html.Node) bool { return n.Data == "table" && hasClass(n, "searchbox") })
	if len(searchboxes) != 1 {
		return false
	}
	for _, cell := range find(doc, func(n *html.Node) bool { return n.Data == "td" && hasClass(n, "text") }) {
		message := text(cell)
		empty := message == "没有种子。请用准确的关键字重试。"
		if c.config.Kind == "nexusphp" {
			empty = empty || message == "沒有種子。請用準確的關鍵字重試。" || message == "No torrents found. Try again with a refined search string." || message == "Nothing found!" || message == "No torrents found."
		}
		if !empty {
			continue
		}
		p := cell.Parent
		if p == nil || p.Data != "tr" {
			continue
		}
		p = p.Parent
		if p != nil && p.Data == "tbody" {
			p = p.Parent
		}
		if p != nil && p.Data == "table" {
			return true
		}
	}
	return false
}

func (c *Client) parseRow(row *html.Node, pageURL *url.URL, columns *nexusColumns) (Resource, bool, error) {
	r := Resource{SiteID: c.config.ID, DownloadFactor: 1, UploadFactor: 1}
	var titleNode *html.Node
	for _, a := range find(row, func(n *html.Node) bool { return n.Data == "a" }) {
		u, err := url.Parse(attribute(a, "href"))
		if err != nil {
			continue
		}
		u = pageURL.ResolveReference(u)
		if path.Base(u.Path) != "details.php" {
			continue
		}
		if c.config.Kind == "nexusphp" && (u.Fragment == "seeders" || u.Fragment == "leechers") {
			continue
		}
		// Peer-count links point at the same item with #seeders/#leechers.
		// Fragments are local anchors and are removed from the public URL.
		u.Fragment = ""
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil || !sameOrigin(u, c.base) || len(q["id"]) != 1 || !numericID.MatchString(q.Get("id")) {
			return r, false, errors.New("site_invalid_detail_link")
		}
		if r.ID != "" && r.ID != q.Get("id") {
			return r, false, errors.New("site_ambiguous_result")
		}
		r.ID = q.Get("id")
		if titleNode == nil || attribute(a, "title") != "" && attribute(titleNode, "title") == "" || attribute(a, "title") == "" && attribute(titleNode, "title") == "" && len(text(a)) > len(text(titleNode)) {
			titleNode = a
		}
	}
	if r.ID == "" {
		if row.Data == "div" || c.config.Kind == "nexusphp" && unrecognizedNexusRow(row) || len(find(row, func(n *html.Node) bool { return n.Data == "table" && hasClass(n, "torrentname") })) > 0 {
			return r, false, errors.New("site_protocol_changed")
		}
		return r, false, nil
	}
	r.Title = strings.TrimSpace(attribute(titleNode, "title"))
	if r.Title == "" {
		r.Title = text(titleNode)
	}
	if r.Title == "" || len(r.Title) > 4096 {
		return r, false, errors.New("site_invalid_title")
	}
	// Reconstruct the public details URL, removing any extra page query values.
	u := c.endpoint("details.php")
	u.RawQuery = url.Values{"id": {r.ID}}.Encode()
	r.DetailsURL = u.String()
	var sizeNode, seedNode, leechNode, dateNode *html.Node
	if row.Data == "div" {
		if c.config.Kind == "hhanclub" && len(find(row, func(n *html.Node) bool { return hasClass(n, "torrent-table-for-spider-info") })) != 1 {
			return r, false, errors.New("site_protocol_changed")
		}
		sizeNode, seedNode, leechNode, dateNode = firstClass(row, "torrent-info-text-size"), firstClass(row, "torrent-info-text-seeders"), firstClass(row, "torrent-info-text-leechers"), firstClass(row, "torrent-info-text-added")
		if n := firstClass(row, "torrent-info-text-small_name"); n != nil {
			r.Subtitle = text(n)
		}
	} else {
		var cols []*html.Node
		for n := row.FirstChild; n != nil; n = n.NextSibling {
			if n.Type == html.ElementNode && n.Data == "td" {
				cols = append(cols, n)
			}
		}
		if c.config.Kind == "nexusphp" {
			var e error
			dateNode, sizeNode, seedNode, leechNode, e = nexusRowColumns(cols, columns)
			if e != nil {
				return r, false, e
			}
			for p := titleNode; p != nil && p != row; p = p.Parent {
				if p.Parent == row {
					r.Subtitle = description(p)
					break
				}
			}
		} else if len(cols) < 7 {
			return r, false, errors.New("site_protocol_changed")
		} else {
			dateNode, sizeNode, seedNode, leechNode = cols[3], cols[4], cols[5], cols[6]
			// The second cell contains a nested torrent-name table. Preserve its
			// plain descriptive text without title, icon, promotion or tag labels.
			r.Subtitle = description(cols[1])
		}
	}
	if sizeNode == nil || seedNode == nil || leechNode == nil {
		return r, false, errors.New("site_protocol_changed")
	}
	var ok bool
	r.Size, ok = parseSize(text(sizeNode))
	if !ok {
		return r, false, errors.New("site_invalid_size")
	}
	if r.Seeders, ok = parseCount(text(seedNode)); !ok {
		return r, false, errors.New("site_invalid_seeders")
	}
	if r.Leechers, ok = parseCount(text(leechNode)); !ok {
		return r, false, errors.New("site_invalid_leechers")
	}
	if dateNode != nil {
		for _, n := range find(dateNode, func(n *html.Node) bool { return attribute(n, "title") != "" }) {
			if date := dateValue(attribute(n, "title")); date != "" {
				r.PublishedAt = date
				break
			}
		}
		if r.PublishedAt == "" {
			r.PublishedAt = dateValue(text(dateNode))
		}
	}
	for _, n := range find(row, func(n *html.Node) bool { return n.Data == "img" || n.Data == "span" }) {
		if strings.EqualFold(attribute(n, "alt"), "Hit and Run") || strings.EqualFold(attribute(n, "title"), "Hit and Run") || strings.EqualFold(attribute(n, "alt"), "H&R") || strings.Contains(strings.ToLower(attribute(n, "src")), "hitandrun") || hasClass(n, "hitandrun") {
			r.HitAndRun = true
		}
		for _, class := range strings.Fields(attribute(n, "class")) {
			promo := strings.TrimPrefix(strings.TrimPrefix(class, "pro_"), "promotion-tag-")
			switch promo {
			case "free", "free2up":
				r.DownloadFactor = 0
			case "50pctdown", "50pctdown2up":
				if r.DownloadFactor != 0 {
					r.DownloadFactor = math.Min(r.DownloadFactor, .5)
				}
			case "30pctdown":
				if r.DownloadFactor != 0 {
					r.DownloadFactor = math.Min(r.DownloadFactor, .3)
				}
			}
			switch promo {
			case "free2up", "50pctdown2up", "2up":
				r.UploadFactor = 2
			}
		}
	}
	// Promotion expiration is distinct from the upload date. Only accept an
	// actual titled date outside the date column under a promotion wrapper.
	for _, n := range find(row, func(n *html.Node) bool { return n.Data == "span" && attribute(n, "title") != "" }) {
		for p := n.Parent; p != nil && p != row; p = p.Parent {
			if p == dateNode {
				break
			}
			if p.Data == "font" && attribute(p, "color") != "" || c.config.Kind == "hhanclub" && p.Data == "span" && hasClass(p, "flex") {
				if date := dateValue(attribute(n, "title")); date != "" {
					r.PromotionEnd = date
				}
				break
			}
		}
	}
	r.Free = r.DownloadFactor == 0
	var labels []string
	for _, n := range find(row, func(n *html.Node) bool {
		return hasClass(n, "tags") || hasClass(n, "tag") || hasClass(n, "torrents-tag")
	}) {
		if label := text(n); label != "" && len(label) <= 128 && len(labels) < 32 {
			labels = append(labels, label)
		}
	}
	sourceSubtitle := r.Subtitle
	r.SourceSubtitle = &sourceSubtitle
	r.Labels = append([]string{}, labels...)
	r.Subtitle = subtitleWithLabels(r.Subtitle, labels)
	classify(&r)
	return r, true, nil
}

func subtitleWithLabels(subtitle string, labels []string) string {
	seen := map[string]bool{}
	var unique []string
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label != "" && !seen[label] && !strings.Contains(subtitle, label) {
			seen[label] = true
			unique = append(unique, label)
		}
	}
	if len(unique) > 0 {
		subtitle = strings.TrimSpace(subtitle) + " [标签：" + strings.Join(unique, " ") + "]"
	}
	if len(subtitle) > 8192 {
		subtitle = subtitle[:8192]
		for !utf8.ValidString(subtitle) {
			subtitle = subtitle[:len(subtitle)-1]
		}
		return strings.Clone(subtitle)
	}
	return strings.TrimSpace(subtitle)
}

func description(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(ch *html.Node) {
		if ch.Type == html.ElementNode {
			switch ch.Data {
			case "a", "img", "font", "b", "script", "style":
				return
			}
			if ch.Data == "span" && (attribute(ch, "class") != "" || attribute(ch, "title") != "") {
				return
			}
		}
		if ch.Type == html.TextNode {
			b.WriteString(ch.Data)
			b.WriteByte(' ')
		}
		for c := ch.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

var sizePattern = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*([KMGTPE]?I?B)\s*$`)

func parseSize(s string) (int64, bool) {
	m := sizePattern.FindStringSubmatch(strings.ReplaceAll(s, ",", ""))
	if len(m) == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	unit := strings.ToUpper(m[2])
	power := strings.IndexByte("BKMGTPE", unit[0])
	if power < 0 {
		return 0, false
	}
	n *= math.Pow(1024, float64(power))
	return int64(math.Round(n)), !math.IsInf(n, 0) && n >= 0 && n < math.MaxInt64
}

func parseCount(s string) (int, bool) {
	s = strings.TrimSpace(strings.Split(s, "/")[0])
	n, err := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 32)
	return int(n), err == nil && n >= 0
}

func (c *Client) hhanTorrentURL(ctx context.Context, id string) (*url.URL, error) {
	u := c.endpoint("details.php")
	u.RawQuery = url.Values{"id": {id}}.Encode()
	b, _, err := c.request(ctx, http.MethodGet, u, nil, c.auth(), htmlLimit)
	if err != nil {
		return nil, err
	}
	doc, err := pageDOM(b)
	if err != nil {
		return nil, err
	}
	for _, a := range find(doc, func(n *html.Node) bool { return n.Data == "a" }) {
		d, err := url.Parse(attribute(a, "href"))
		if err != nil {
			continue
		}
		d = u.ResolveReference(d)
		if !strings.HasSuffix(d.Path, "/download.php") {
			continue
		}
		q, err := url.ParseQuery(d.RawQuery)
		if err != nil || q.Get("id") != id {
			continue
		}
		// The detail page also contains ordinary cookie-based download links.
		// Continue to the dedicated passkey link rather than rejecting the page
		// because the first ordinary link has no passkey.
		if sameOrigin(d, c.base) && len(q) == 1 && len(q["id"]) == 1 {
			continue
		}
		if !sameOrigin(d, c.base) || len(q["id"]) != 1 || len(q["passkey"]) != 1 || len(q) != 2 || !passkeyPattern.MatchString(q.Get("passkey")) {
			return nil, errors.New("site_unsafe_download_link")
		}
		return d, nil
	}
	return nil, errors.New("site_download_link_missing")
}

var passkeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
var resolutionPattern = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(4320p|2160p|1080p|1080i|720p|576p|480p|4k|8k)(?:[^A-Za-z0-9]|$)`)
var codecPattern = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(x265|h[ ._-]?265|hevc|x264|h[ ._-]?264|avc|av1|vc[ ._-]?1)(?:[^A-Za-z0-9]|$)`)
var sourcePattern = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(web[ ._-]?dl|webrip|blu[ ._-]?ray|bdrip|hdtv|remux|dvd)(?:[^A-Za-z0-9]|$)`)

func classify(r *Resource) {
	s := r.Title + " " + r.Subtitle
	if m := resolutionPattern.FindStringSubmatch(s); len(m) > 1 {
		r.Resolution = strings.ToLower(m[1])
		if r.Resolution == "4k" {
			r.Resolution = "2160p"
		}
		if r.Resolution == "8k" {
			r.Resolution = "4320p"
		}
	}
	if m := codecPattern.FindStringSubmatch(s); len(m) > 1 {
		r.Codec = strings.NewReplacer(".", "", "_", "", "-", "", " ", "").Replace(strings.ToLower(m[1]))
		switch r.Codec {
		case "x265", "h265", "hevc":
			r.Codec = "HEVC"
		case "x264", "h264", "avc":
			r.Codec = "AVC"
		default:
			r.Codec = strings.ToUpper(r.Codec)
		}
	}
	if m := sourcePattern.FindStringSubmatch(s); len(m) > 1 {
		r.Source = strings.NewReplacer(".", "", "_", "", "-", "", " ", "").Replace(strings.ToUpper(m[1]))
		if r.Source == "WEBDL" {
			r.Source = "WEB-DL"
		}
	}
}
