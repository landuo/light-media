package sites

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/logging"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

var ErrMessageReadUnsupported = errors.New("site_selective_message_read_unsupported")

var errMessageMailbox = errors.New("site_message_mailbox_unrecognized")
var errMessagePagination = errors.New("site_message_pagination_incomplete")
var errMessageMarkUnconfirmed = errors.New("site_message_mark_read_unconfirmed")

type MessageReadResult struct {
	Scanned int
	Matched int
	Marked  int
	Failed  int
}

type unreadMessage struct {
	id, title string
}

// MarkMatchingMessagesRead collects subjects from mailbox lists before opening
// any message. Opening an individual NexusPHP detail page marks it as read.
func (c *Client) MarkMatchingMessagesRead(ctx context.Context, matchesTitle func(string) bool) (MessageReadResult, error) {
	var result MessageReadResult
	if matchesTitle == nil {
		return result, nil
	}
	ctx = logging.SiteContext(ctx, c.config.ID)
	var unread []unreadMessage
	var err error
	switch c.config.Kind {
	case "mteam":
		unread, err = c.listUnreadMTeamMessages(ctx)
	case "nexusphp", "hhanclub", "ubits", "jiayuan", "1ptba":
		unread, err = c.listUnreadNexusMessages(ctx)
	default:
		return result, ErrMessageReadUnsupported
	}
	if err != nil {
		return result, err
	}
	result.Scanned = len(unread)
	matched := slices.DeleteFunc(unread, func(message unreadMessage) bool {
		return message.title == "" || !matchesTitle(message.title)
	})
	result.Matched = len(matched)
	for i, message := range matched {
		markErr := ctx.Err()
		if markErr == nil {
			if c.config.Kind == "mteam" {
				// The installed adapter used msgId; current API docs use
				// msgIds. Both fields address the same single message.
				_, markErr = c.messageForm(ctx, "api/msg/markRead", url.Values{
					"msgId": {message.id}, "msgIds": {message.id},
				})
			} else {
				markErr = c.readNexusMessage(ctx, message)
			}
		}
		if markErr == nil {
			result.Marked++
			continue
		}
		if stopMessageMarking(markErr) {
			result.Failed += len(matched) - i
			return result, markErr
		}
		result.Failed++
		if err == nil {
			err = markErr
		}
	}
	return result, err
}

func stopMessageMarking(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch err.Error() {
	case "site_rate_limited", "site_authentication_failed", "site_forbidden", "site_forbidden_or_challenge",
		"site_login_challenge", "site_authentication_or_template_changed", "site_redirect_blocked":
		return true
	}
	return false
}

func (c *Client) listUnreadMTeamMessages(ctx context.Context) ([]unreadMessage, error) {
	var unread []unreadMessage
	seen := map[string]bool{}
	for page := 1; page <= 50; page++ {
		data, err := c.messageForm(ctx, "api/msg/search", url.Values{
			"keyword": {""}, "box": {"-2"}, "type": {"pageNumber"},
			"pageSize": {"100"}, "pageNumber": {strconv.Itoa(page)}, "unread": {"true"},
		})
		if err != nil {
			return nil, err
		}
		rows, ok := data["data"].([]any)
		if !ok || len(rows) > 100 {
			return nil, errMessageMailbox
		}
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			isUnread, valid := row["unread"].(bool)
			id := stringValue(row["id"])
			title, titled := row["title"].(string)
			if !ok || !valid || !validMessageID(id) || seen[id] || isUnread && !titled {
				return nil, errMessageMailbox
			}
			seen[id] = true
			if isUnread {
				unread = append(unread, unreadMessage{id, strings.TrimSpace(title)})
			}
		}
		perPage := int64(100)
		if value, exists := data["pageSize"]; exists {
			size, ok := integer(value)
			if !ok || size < 1 || size > 100 {
				return nil, errMessageMailbox
			}
			perPage = size
		}
		if value, exists := data["pageNumber"]; exists {
			number, ok := integer(value)
			if !ok || number != int64(page) {
				return nil, errMessagePagination
			}
		}
		if value, exists := data["total"]; exists {
			total, ok := integer(value)
			if !ok {
				return nil, errMessageMailbox
			}
			if len(rows) == 0 && (int64(page)-1)*perPage < total {
				return nil, errMessagePagination
			}
			if int64(page)*perPage >= total {
				return unread, nil
			}
		} else if value, exists := data["totalPages"]; exists {
			pages, ok := integer(value)
			if !ok {
				return nil, errMessageMailbox
			}
			if int64(page) >= pages {
				return unread, nil
			}
		} else if int64(len(rows)) < perPage {
			return unread, nil
		}
	}
	return nil, errMessagePagination
}

func (c *Client) listUnreadNexusMessages(ctx context.Context) ([]unreadMessage, error) {
	var unread []unreadMessage
	seen := map[string]string{}
	boxes := []string{"1", "-2"}
	host := strings.ToLower(c.base.Hostname())
	audiences := host == "audiences.me" || host == "www.audiences.me"
	if audiences {
		boxes = boxes[:1]
	}
	for _, box := range boxes {
		for page, fetched := 0, 0; ; fetched++ {
			if fetched == 50 {
				return nil, errMessagePagination
			}
			u := c.nexusMailboxURL(box, page)
			b, contentType, err := c.request(ctx, http.MethodGet, u, nil, c.auth(), htmlLimit)
			if err != nil {
				return nil, err
			}
			doc, err := nexusMessageDOM(b, contentType)
			if err != nil {
				return nil, err
			}
			messages, next, err := nexusMailboxRows(doc, u, box, page)
			if err != nil {
				return nil, err
			}
			newMessages := 0
			for _, message := range messages {
				if title, exists := seen[message.id]; exists {
					if title != message.title {
						return nil, errMessageMailbox
					}
					continue
				}
				seen[message.id] = message.title
				unread = append(unread, message)
				newMessages++
			}
			if next == 0 && audiences && newMessages > 0 {
				next = page + 1
			}
			if next == 0 {
				break
			}
			page = next
		}
	}
	return unread, nil
}

func nexusMessageDOM(b []byte, contentType string) (*html.Node, error) {
	decoded, err := decodeNexusHTML(b, contentType)
	if err != nil {
		return nil, err
	}
	doc, err := parsedSitePage(decoded)
	if err != nil {
		return nil, err
	}
	marker, err := htmlquery.Query(doc, sourceNexusLoginMarkerXPath)
	if err != nil || marker == nil {
		return nil, errors.New("site_authentication_or_template_changed")
	}
	return doc, nil
}

func (c *Client) nexusMailboxURL(box string, page int) *url.URL {
	u := c.endpoint("messages.php")
	query := url.Values{"action": {"viewmailbox"}, "box": {box}, "unread": {"yes"}}
	if page > 0 {
		query.Set("page", strconv.Itoa(page))
	}
	u.RawQuery = query.Encode()
	return u
}

func nexusMailboxRows(doc *html.Node, mailbox *url.URL, box string, page int) ([]unreadMessage, int, error) {
	recognized := findFirst(doc, func(n *html.Node) bool {
		if hasClass(n, "pm-list") {
			return true
		}
		child := n.FirstChild
		if child == nil || child != n.LastChild || child.Type != html.TextNode {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(child.Data)) {
		case "no messages.", "no messages", "没有短讯", "沒有短訊", "没有消息", "沒有消息", "暂无消息", "暫無消息", "暂无短讯", "暫無短訊":
			return true
		case "invalid mailbox", "无效的短讯箱", "無效的短訊箱":
			return box == "-2" && page == 0
		}
		return false
	}) != nil
	var messages []unreadMessage
	for _, row := range find(doc, func(n *html.Node) bool { return n.Data == "tr" || hasClass(n, "pm-item-row") }) {
		modern := hasClass(row, "pm-item-row")
		links := find(row, func(n *html.Node) bool {
			if n.Data != "a" || !strings.Contains(attribute(n, "href"), "viewmessage") {
				return false
			}
			if modern {
				return hasClass(n, "pm-item__subject")
			}
			return n.Parent != nil && n.Parent.Data == "td" && n.Parent.Parent == row
		})
		if len(links) == 0 {
			continue
		}
		unread, read := nexusMessageRowStatus(row)
		if unread && read || unread && len(links) != 1 {
			return nil, 0, errMessageMailbox
		}
		recognized = recognized || unread || read
		if !unread {
			continue
		}
		query, valid := selectiveMailboxQuery(mailbox, attribute(links[0], "href"))
		if !valid || query.Get("action") != "viewmessage" || !validMessageID(query.Get("id")) {
			return nil, 0, errMessageMailbox
		}
		for key, values := range query {
			if len(values) != 1 || (key != "action" && key != "id" && (key != "box" || values[0] != box)) {
				return nil, 0, errMessageMailbox
			}
		}
		messages = append(messages, unreadMessage{query.Get("id"), strings.TrimSpace(messageNodeString(links[0]))})
	}
	if !recognized {
		return nil, 0, errMessageMailbox
	}
	next := 0
	current := mailbox.Query()
	for _, link := range find(doc, func(n *html.Node) bool {
		if n.Data != "a" && n.Data != "link" {
			return false
		}
		label := strings.ToLower(strings.Trim(messageNodeString(n), " \t\r\n\u00a0>›»→"))
		return strings.EqualFold(attribute(n, "rel"), "next") || label == "下一页" || label == "下一頁" || label == "next" || label == "next page"
	}) {
		query, valid := selectiveMailboxQuery(mailbox, attribute(link, "href"))
		n, parseErr := strconv.Atoi(query.Get("page"))
		if !valid || parseErr != nil || n <= page || (next != 0 && next != n) {
			return nil, 0, errMessagePagination
		}
		for key, values := range query {
			if len(values) != 1 || (key != "page" && (!current.Has(key) || current.Get(key) != values[0])) {
				return nil, 0, errMessagePagination
			}
		}
		next = n
	}
	return messages, next, nil
}

func nexusMessageRowStatus(row *html.Node) (unread, read bool) {
	unread = hasClass(row, "is-unread")
	for _, node := range find(row, func(n *html.Node) bool {
		return (n.Data == "img" || hasClass(n, "pm-item__status") ||
			hasClass(n, "pm-item__status--unread") || hasClass(n, "pm-item__status--read")) &&
			insideMessageRow(n, row)
	}) {
		alt := strings.ToLower(attribute(node, "alt"))
		title := strings.ToLower(attribute(node, "title"))
		unread = unread || hasClass(node, "unreadpm") || hasClass(node, "pm-item__status--unread") ||
			alt == "unread" || title == "unread" || title == "未读" || title == "未讀"
		read = read || hasClass(node, "readpm") || hasClass(node, "pm-item__status--read") ||
			alt == "read" || title == "read" || title == "已读" || title == "已讀"
	}
	return unread, read
}

func insideMessageRow(n, row *html.Node) bool {
	for parent := n.Parent; parent != nil; parent = parent.Parent {
		if parent == row {
			return true
		}
		if parent.Data == "tr" || hasClass(parent, "pm-item-row") {
			return false
		}
	}
	return false
}

func selectiveMailboxQuery(mailbox *url.URL, raw string) (url.Values, bool) {
	link, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, false
	}
	link = mailbox.ResolveReference(link)
	if !sameOrigin(link, mailbox) || link.EscapedPath() != mailbox.EscapedPath() {
		return nil, false
	}
	query, err := url.ParseQuery(link.RawQuery)
	return query, err == nil
}

func validMessageID(id string) bool {
	// NexusPHP casts the string to a signed 64-bit PHP integer. Reject an
	// overflowing ID because the cast could otherwise address a different row.
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}

func (c *Client) readNexusMessage(ctx context.Context, message unreadMessage) error {
	u := c.endpoint("messages.php")
	u.RawQuery = url.Values{"action": {"viewmessage"}, "id": {message.id}}.Encode()
	b, contentType, err := c.request(ctx, http.MethodGet, u, nil, c.auth(), htmlLimit)
	if err != nil {
		return err
	}
	doc, err := nexusMessageDOM(b, contentType)
	if err != nil {
		return err
	}
	title := firstClass(doc, "pm-hero__title")
	body := firstClass(doc, "pm-view__body")
	if title != nil && body != nil && strings.TrimSpace(messageNodeString(title)) == message.title {
		return nil
	}
	legacy := nexusMessageContent(doc)
	if legacy[0] == nil || legacy[1] == nil || legacy[2] == nil || *legacy[0] != message.title {
		return errMessageMarkUnconfirmed
	}
	return nil
}
