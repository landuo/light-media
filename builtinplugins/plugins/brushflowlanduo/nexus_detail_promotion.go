package brushflowlanduo

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func scopedNexusDetail(page *url.URL) bool {
	if page == nil || page.Scheme != "https" || page.Path != "/details.php" || page.User != nil {
		return false
	}
	switch strings.TrimPrefix(strings.ToLower(page.Hostname()), "www.") {
	case "ubits.club", "hdhome.org":
		ids := page.Query()["id"]
		return len(ids) == 1 && ids[0] != "" && len(ids[0]) <= 20 && strings.Trim(ids[0], "0123456789") == ""
	}
	return false
}

func nexusAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func nexusText(n *html.Node) string {
	var text strings.Builder
	for child := range n.Descendants() {
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
		}
	}
	return strings.TrimSpace(text.String())
}

func nexusPageLink(n *html.Node, page *url.URL, path string) *url.URL {
	if n.DataAtom != atom.A || nexusAttr(n, "href") == "" {
		return nil
	}
	href, err := page.Parse(nexusAttr(n, "href"))
	if err != nil || href.User != nil || href.Scheme != page.Scheme || !strings.EqualFold(href.Host, page.Host) || href.Path != path {
		return nil
	}
	return href
}

// Both tracker templates put the release's badge in the title. The approval
// badge ("通过") can use the same CSS as a promotion on these sites.
func nexusHeading(n *html.Node) (freeFilter string, paid bool, until string) {
	for child := range n.Descendants() {
		switch child.DataAtom {
		case atom.Font, atom.Span, atom.Img:
			for _, class := range strings.Fields(strings.ToLower(nexusAttr(child, "class"))) {
				switch class {
				case "free", "twoupfree", "pro_free", "pro_free2up", "promotion-tag-free", "promotion-tag-2xfree":
					label := strings.ToLower(strings.Join(strings.Fields(nexusText(child)+nexusAttr(child, "alt")), ""))
					switch label {
					case "免费", "free":
						freeFilter = "2"
					case "2x免费", "2xfree", "2倍免费":
						freeFilter = "4"
					}
				case "halfdown", "thirtypercent", "pro_50pctdown", "pro_30pctdown", "twoup", "pro_2up":
					label := strings.TrimSpace(nexusText(child) + nexusAttr(child, "alt"))
					switch strings.ToUpper(label) {
					case "50%", "30%", "2X", "2倍上传":
						paid = true
					}
				}
			}
			if child.DataAtom == atom.Span && child.PrevSibling != nil &&
				strings.HasSuffix(strings.TrimRight(child.PrevSibling.Data, " ：:\t\r\n"), "剩余时间") {
				until = nexusAttr(child, "title")
			}
		}
	}
	return
}

var hdHomeWindow = regexp.MustCompile(`优惠(开始|截止)[：:]\s*(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\s*\(GMT([+-]\d{2}:\d{2})\)`)

func hdHomeFreeUntil(n *html.Node, page *url.URL, now time.Time) time.Time {
	if n.Parent == nil || n.Parent.DataAtom != atom.Td || nexusAttr(n.Parent, "id") != "outer" {
		return time.Time{}
	}
	var active, plan bool
	var start, end time.Time
	for child := range n.ChildNodes() {
		switch {
		case child.DataAtom == atom.B:
			label := strings.ReplaceAll(nexusText(child), "：", ":")
			active = strings.Join(strings.Fields(label), "") == "全局优惠生效:FREE"
		case nexusPageLink(child, page, "/free.php") != nil:
			plan = true
		case child.Type == html.TextNode:
			for _, m := range hdHomeWindow.FindAllStringSubmatch(child.Data, -1) {
				at, err := time.Parse("2006-01-02 15:04:05 -07:00", m[2]+" "+m[3])
				if err != nil {
					return time.Time{}
				}
				if m[1] == "开始" {
					start = at
				} else {
					end = at
				}
			}
		}
	}
	if active && plan && !start.IsZero() && !now.Before(start) && now.Before(end) {
		return end
	}
	return time.Time{}
}

// The exact download ID binds the parsed page to this record. Neither a
// different release on the page nor the UBits seed-box policy describes this
// torrent's ordinary download factor.
func (p *Core) nexusDetailPromotion(ctx context.Context, siteID int, page *url.URL, body string, offset float64) (until time.Time, ended, known bool) {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return
	}
	now := p.h.Now()
	var title *html.Node
	var global time.Time
	own := false
	hdhome := strings.TrimPrefix(strings.ToLower(page.Hostname()), "www.") == "hdhome.org"
	id := page.Query().Get("id")
	for n := range doc.Descendants() {
		switch n.DataAtom {
		case atom.H1:
			if nexusAttr(n, "id") == "top" {
				if title != nil {
					return time.Time{}, false, false
				}
				title = n
			}
		case atom.Div:
			if hdhome && title == nil {
				if end := hdHomeFreeUntil(n, page, now); end.After(global) {
					global = end
				}
			}
		case atom.A:
			if !own {
				if href := nexusPageLink(n, page, "/download.php"); href != nil && href.Query().Get("id") == id {
					own = true
				}
			}
		}
	}
	if title == nil || !own {
		return
	}
	freeFilter, paid, stamp := nexusHeading(title)
	if freeFilter != "" && !paid {
		if _, _, valid := parseDeadline(stamp); valid {
			if local := p.expiryAt(stamp, offset); local != nil {
				until = *local
			}
		}
		if global.After(until) {
			until = global
		}
		if until.After(now) {
			return until, false, true
		}
		// A UBits release can still be free after its old countdown disappears.
		// The zero-download filter confirms the present rate, not a new deadline.
		freeLabel := "免费"
		if freeFilter == "4" {
			freeLabel = "2X免费"
		}
		if !hdhome && p.ubitsPromotionListing(ctx, siteID, page, title, freeFilter, freeLabel) {
			return time.Time{}, false, true
		}
		return time.Time{}, false, false
	}
	if paid && freeFilter == "" && global.IsZero() {
		return time.Time{}, true, true
	}
	// UBits may render an ordinary release with no promotion badge at all.
	// Its own normal-only search must positively return this exact item.
	if !hdhome && freeFilter == "" && !paid && stamp == "" && p.ubitsPromotionListing(ctx, siteID, page, title, "1", "普通") {
		return time.Time{}, true, true
	}
	return
}

func (p *Core) ubitsPromotionListing(ctx context.Context, siteID int, page *url.URL, title *html.Node, filter, label string) bool {
	var query string
	for child := range title.ChildNodes() {
		if child.Type == html.TextNode {
			words := strings.Fields(child.Data)
			if len(words) > 0 {
				query = strings.Join(words[:min(8, len(words))], " ")
				break
			}
		}
	}
	if query == "" || len(query) > 512 {
		return false
	}
	list := page.ResolveReference(&url.URL{Path: "/torrents.php", RawQuery: url.Values{
		"search": {query}, "spstate": {filter}, "ubits_ui": {"classic"},
	}.Encode()})
	resp, err := p.h.SiteRequest(ctx, siteID, "GET", list.String(), nil)
	if err != nil || !truth(resp["ok"]) {
		return false
	}
	doc, err := html.Parse(strings.NewReader(text(resp["text"])))
	if err != nil {
		return false
	}
	selected, found := false, false
	for n := range doc.Descendants() {
		switch n.DataAtom {
		case atom.Option:
			if n.Parent == nil || n.Parent.DataAtom != atom.Select || nexusAttr(n.Parent, "name") != "spstate" {
				continue
			}
			for _, attr := range n.Attr {
				if attr.Key == "selected" {
					if nexusAttr(n, "value") != filter || nexusText(n) != label {
						return false
					}
					selected = true
				}
			}
		case atom.A:
			href := nexusPageLink(n, list, "/details.php")
			if href == nil || href.Query().Get("id") != page.Query().Get("id") {
				continue
			}
			for parent := range n.Ancestors() {
				if parent.DataAtom == atom.Table && slices.Contains(strings.Fields(nexusAttr(parent, "class")), "torrents") {
					found = true
					break
				}
			}
		}
	}
	return selected && found
}

func (p *Core) nexusProbeDue(t Task, r Row) bool {
	last := number(parseNumber(fallback(r["promotion_nexus_probe_at"], 0)))
	now := p.h.Now()
	if last == 0 || float64(now.UnixNano())/1e9-last >= promotionProbe {
		return true
	}
	if end, _ := p.resolveExpiry(t, r); end != nil {
		// Refresh on entering the deletion window and again after the real
		// deadline. A tracker may change from timed free to undated free.
		for _, boundary := range []time.Time{end.Add(-promotionStopMargin), *end} {
			if !now.Before(boundary) && last < float64(boundary.UnixNano())/1e9 {
				return true
			}
		}
	}
	return false
}
