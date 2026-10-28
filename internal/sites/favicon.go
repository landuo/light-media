package sites

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// ReadFavicon follows the pinned homepage -> first head link containing "icon"
// -> favicon request order. Credential origin guards and the existing bounded
// HTTP reader also apply to icons; a foreign link is never sent site cookies.
func (c *Client) ReadFavicon(ctx context.Context, userAgent string) (string, []byte, error) {
	fallback := c.base.ResolveReference(&url.URL{Path: "favicon.ico"})
	client, transport := *c, *c.http
	client.http = &transport
	client.config.UserAgent = userAgent
	transport.Timeout = 30 * time.Second
	body, _, err := client.request(ctx, http.MethodGet, c.base, nil, c.auth(), htmlLimit)
	if err != nil {
		return fallback.String(), nil, err
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return fallback.String(), nil, err
	}
	icon := fallback
	var inspect func(*html.Node, bool) bool
	inspect = func(node *html.Node, head bool) bool {
		head = head || node.Type == html.ElementNode && node.Data == "head"
		if head && node.Type == html.ElementNode && node.Data == "link" && strings.Contains(attribute(node, "rel"), "icon") {
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					if value, parseErr := url.Parse(attr.Val); parseErr == nil {
						icon = c.base.ResolveReference(value)
					}
					return true
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if inspect(child, head) {
				return true
			}
		}
		return false
	}
	inspect(doc, false)
	transport.Timeout = 15 * time.Second
	content, _, err := client.request(ctx, http.MethodGet, icon, nil, c.auth(), 1<<20)
	return icon.String(), content, err
}
