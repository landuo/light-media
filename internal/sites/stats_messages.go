package sites

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/dlclark/regexp2/v2"
	"golang.org/x/net/html"
)

var nexusUnreadPattern = func() *regexp2.Regexp {
	r := regexp2.MustCompile("[^Date](信息箱\\s*|\\((?![^)]*:)|你有\u00a0)(\\d+)")
	r.MatchTimeout = 100 * time.Millisecond
	return r
}()

func parseNexusUnread(s *UserStats, doc *html.Node) {
	anchors := find(doc, func(n *html.Node) bool { return n.Data == "a" && attribute(n, "href") == "messages.php" })
	if len(anchors) == 0 {
		anchors = find(doc, func(n *html.Node) bool {
			return n.Data == "a" && strings.Contains(attribute(n, "href"), "messages.php")
		})
	}
	if len(anchors) == 0 {
		return
	}
	value := messageNodeString(anchors[0].Parent)
	digits := value != ""
	for _, r := range value {
		if !unicode.IsDigit(r) {
			digits = false
			break
		}
	}
	if n, ok := statsInt(value); ok && digits {
		s.MessageUnread = n
		s.Sources["message_unread"] = "index.php:messages link"
		return
	}
	var candidate *int64
	match, err := nexusUnreadPattern.FindStringMatch(value)
	for err == nil && match != nil {
		if n, ok := statsInt(match.GroupByNumber(2).String()); ok {
			candidate = n
		}
		match, err = nexusUnreadPattern.FindNextMatch(match)
	}
	if err == nil && candidate != nil {
		s.MessageUnread = candidate
		s.Sources["message_unread"] = "index.php:messages link"
	}
}

func (c *Client) messageForm(ctx context.Context, endpoint string, fields url.Values) (map[string]any, error) {
	h := c.auth()
	h["Content-Type"] = "application/x-www-form-urlencoded"
	b, _, e := c.request(ctx, http.MethodPost, c.endpoint(endpoint), []byte(fields.Encode()), h, htmlLimit)
	if e != nil {
		return nil, e
	}
	data, e := mteamEnvelope(b, endpoint == "api/msg/markRead")
	if e != nil {
		return nil, e
	}
	if endpoint == "api/msg/markRead" {
		return map[string]any{}, nil
	}
	var result map[string]any
	if !decodeJSON(data, &result) || result == nil {
		return nil, errors.New("site_message_protocol_changed")
	}
	return result, nil
}

// The installed MTorrent parser reads one 100-message search page and marks
// only unread messages with all three content fields present as read.
func (c *Client) mteamMessages(ctx context.Context, s *UserStats) error {
	data, e := c.messageForm(ctx, "api/msg/search", url.Values{"keyword": {""}, "box": {"-2"}, "type": {"pageNumber"}, "pageSize": {"100"}})
	if e != nil {
		return errors.New("site_message_fetch_failed")
	}
	rows, ok := data["data"].([]any)
	if !ok || len(rows) > 100 {
		return errors.New("site_message_protocol_changed")
	}
	messages := [][3]*string{}
	for _, v := range rows {
		row, ok := v.(map[string]any)
		if !ok {
			return errors.New("site_message_protocol_changed")
		}
		unread, ok := row["unread"].(bool)
		if !ok {
			return errors.New("site_message_protocol_changed")
		}
		if !unread {
			continue
		}
		title, tok := row["title"].(string)
		date, dok := row["createdDate"].(string)
		body, bok := row["context"].(string)
		if !tok || !dok || !bok || title == "" || date == "" || body == "" {
			continue
		}
		id := stringValue(row["id"])
		if !numericID.MatchString(id) {
			return errors.New("site_message_protocol_changed")
		}
		messages = append(messages, [3]*string{&title, &date, &body})
		// Retain already observed contents even if the server refuses the mark.
		s.Messages = messages
		s.Sources["message_unread_contents"] = "api/msg/search:unread title,createdDate,context"
		if _, e = c.messageForm(ctx, "api/msg/markRead", url.Values{"msgId": {id}}); e != nil {
			return errors.New("site_message_mark_read_failed")
		}
	}
	s.Messages = messages
	s.Sources["message_unread_contents"] = "api/msg/search:unread title,createdDate,context"
	return nil
}

func messageURL(base *url.URL, raw, action, box string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return nil, errors.New("site_message_link_invalid")
	}
	u = base.ResolveReference(u)
	if !sameOrigin(base, u) || u.User != nil || path.Base(u.Path) != "messages.php" || u.Query().Get("action") != action {
		return nil, errors.New("site_message_link_invalid")
	}
	if action == "viewmessage" && !numericID.MatchString(u.Query().Get("id")) {
		return nil, errors.New("site_message_link_invalid")
	}
	if action == "viewmailbox" && u.Query().Get("box") != box {
		return nil, errors.New("site_message_link_invalid")
	}
	u.Fragment = ""
	return u, nil
}

func (c *Client) nexusMessages(ctx context.Context, s *UserStats) error {
	if s.MessageUnread == nil {
		return nil
	}
	if *s.MessageUnread == 0 {
		s.Messages = [][3]*string{}
		s.Sources["message_unread_contents"] = "index.php:observed no unread messages"
		return nil
	}
	links := []*url.URL{}
	for _, box := range []string{"1", "-2"} {
		u := c.endpoint("messages.php")
		u.RawQuery = url.Values{"action": {"viewmailbox"}, "box": {box}, "unread": {"yes"}}.Encode()
		seenPages := map[string]bool{}
		for page := 0; ; page++ {
			if page >= 50 || seenPages[u.String()] {
				return errors.New("site_message_pagination_incomplete")
			}
			seenPages[u.String()] = true
			b, _, e := c.request(ctx, http.MethodGet, u, nil, c.auth(), htmlLimit)
			if e != nil {
				return errors.New("site_message_fetch_failed")
			}
			doc, e := pageDOM(b)
			if e != nil {
				return errors.New("site_message_page_unrecognized")
			}
			rawLinks, rawNext := nexusUnreadLinks(doc)
			for _, raw := range rawLinks {
				link, e := messageURL(u, raw, "viewmessage", "")
				if e != nil {
					return e
				}
				links = append(links, link)
			}
			var next *url.URL
			if rawNext != "" {
				next, e = messageURL(u, rawNext, "viewmailbox", box)
				if e != nil {
					return e
				}
			}
			if next == nil {
				break
			}
			u = next
		}
	}
	messages := [][3]*string{}
	size := 0
	for _, u := range links {
		b, _, e := c.request(ctx, http.MethodGet, u, nil, c.auth(), htmlLimit)
		if e != nil {
			return errors.New("site_message_fetch_failed")
		}
		doc, e := pageDOM(b)
		if e != nil {
			return errors.New("site_message_page_unrecognized")
		}
		message := nexusMessageContent(doc)
		encoded, e := json.Marshal(message)
		if e != nil {
			return errors.New("site_message_protocol_changed")
		}
		size += len(encoded)
		if size > htmlLimit {
			return errors.New("site_message_contents_incomplete")
		}
		messages = append(messages, message)
		s.Messages = messages
		s.Sources["message_unread_contents"] = "messages.php:unread title,date,content"
	}
	s.Messages = messages
	s.Sources["message_unread_contents"] = "messages.php:unread title,date,content"
	return nil
}

func messageText(n *html.Node) *string {
	if n == nil {
		return nil
	}
	value := strings.TrimSpace(messageNodeString(n))
	return &value
}

func messageNodeString(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(v *html.Node) {
		if v.Type == html.TextNode {
			b.WriteString(v.Data)
		}
		for c := v.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return b.String()
}

func elementPosition(n *html.Node, tag string) int {
	i := 0
	for v := n.PrevSibling; v != nil; v = v.PrevSibling {
		if v.Type == html.ElementNode && v.Data == tag {
			i++
		}
	}
	return i + 1
}

func messageTextNodes(n *html.Node) []*html.Node {
	var nodes []*html.Node
	walk(n, func(v *html.Node) {
		if v.Type == html.TextNode {
			nodes = append(nodes, v)
		}
	})
	return nodes
}

func nexusUnreadLinks(doc *html.Node) ([]string, string) {
	links := []string{}
	next := ""
	for _, row := range find(doc, func(n *html.Node) bool { return n.Data == "tr" }) {
		read := false
		for _, cell := range directCells(row) {
			if cell.Data != "td" {
				continue
			}
			for c := cell.FirstChild; c != nil; c = c.NextSibling {
				if c.Data == "img" && attribute(c, "alt") == "Read" {
					read = true
				}
			}
		}
		if read {
			continue
		}
		for _, cell := range directCells(row) {
			if cell.Data != "td" {
				continue
			}
			for c := cell.FirstChild; c != nil; c = c.NextSibling {
				if c.Data == "a" && strings.Contains(attribute(c, "href"), "viewmessage") {
					links = append(links, attribute(c, "href"))
				}
			}
		}
	}
	for _, a := range find(doc, func(n *html.Node) bool { return n.Data == "a" }) {
		nodes := messageTextNodes(a)
		if len(nodes) > 0 && (strings.Contains(nodes[0].Data, "下一页") || strings.Contains(nodes[0].Data, "下一頁")) {
			next = strings.TrimSpace(attribute(a, "href"))
		}
	}
	return links, next
}

func nexusMessageContent(doc *html.Node) [3]*string {
	var result [3]*string
	rank := map[*html.Node]int{}
	i := 0
	walk(doc, func(n *html.Node) { rank[n] = i; i++ })
	var dates, bodies []*html.Node
	for _, node := range messageTextNodes(doc) {
		p := node.Parent
		if p == nil {
			continue
		}
		heading := p.Data == "h1"
		if p.Data == "span" && elementPosition(p, "span") == 1 && p.Parent != nil && p.Parent.Data == "div" && attribute(p.Parent, "class") == "layui-card-header" {
			heading = true
		}
		if heading {
			value := strings.TrimSpace(node.Data)
			result[0] = &value
		}
	}
	for _, h := range find(doc, func(n *html.Node) bool { return n.Data == "h1" }) {
		for table := h.NextSibling; table != nil; table = table.NextSibling {
			if table.Data != "table" || len(find(table, func(n *html.Node) bool {
				return n.Data == "td" && n.Parent != nil && n.Parent.Data == "tr" && attribute(n, "class") == "colhead"
			})) == 0 {
				continue
			}
			for _, row := range find(table, func(n *html.Node) bool { return n.Data == "tr" }) {
				cells := []*html.Node{}
				for _, cell := range directCells(row) {
					if cell.Data == "td" {
						cells = append(cells, cell)
					}
				}
				position := elementPosition(row, "tr")
				if position == 2 && len(cells) > 1 && cells[1].Data == "td" {
					dates = append(dates, cells[1])
				}
				if position == 3 {
					for _, c := range cells {
						if c.Data == "td" {
							bodies = append(bodies, c)
						}
					}
				}
			}
		}
	}
	for _, header := range find(doc, func(n *html.Node) bool { return n.Data == "div" && attribute(n, "class") == "layui-card-header" }) {
		for span := header.FirstChild; span != nil; span = span.NextSibling {
			if span.Data != "span" || elementPosition(span, "span") != 2 {
				continue
			}
			for child := span.FirstChild; child != nil; child = child.NextSibling {
				if child.Data == "span" && elementPosition(child, "span") == 2 {
					dates = append(dates, child)
				}
			}
		}
	}
	bodies = append(bodies, find(doc, func(n *html.Node) bool {
		return n.Data == "div" && strings.Contains(attribute(n, "class"), "layui-card-body")
	})...)
	for k, nodes := range [][]*html.Node{dates, bodies} {
		var first *html.Node
		for _, n := range nodes {
			if first == nil || rank[n] < rank[first] {
				first = n
			}
		}
		if first != nil {
			result[k+1] = messageText(first)
		}
	}
	return result
}
