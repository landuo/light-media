package sites

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"strings"
)

// BrushRecipeResponse is private to the host-bound Brush material call. The
// plugin does not receive recipe URLs, headers, cookies or raw torrent bytes.
type BrushRecipeResponse struct {
	Status                       int
	ContentType, ContentEncoding string
	Content                      []byte
}

func brushRecipeRedirect(status int) bool {
	switch status {
	case 301, 302, 303, 307, 308:
		return true
	}
	return false
}

// BrushRecipeRequest uses the configured site's physical client and request
// guard for every hop. Even a response redirect cannot move recipe credentials
// to a different origin. A final material GET supplies fresh headers.
func (c *Client) BrushRecipeRequest(ctx context.Context, method string, target *url.URL, headers map[string]string, material bool) (BrushRecipeResponse, error) {
	limit := int64(1 << 20)
	if material {
		limit = torrentLimit
	}
	if method != http.MethodGet && (method != http.MethodPost || material) {
		return BrushRecipeResponse{}, errors.New("site_invalid_request")
	}
	for hop := 0; hop <= 30; hop++ {
		var reply BrushRecipeResponse
		if target == nil || target.Opaque != "" || !sameOrigin(target, c.base) || len(target.String()) > 8192 {
			return reply, errors.New("site_material_origin_mismatch")
		}
		body, location, err := c.requestWithBrushReply(ctx, method, target, nil, headers, limit, &reply)
		if !brushRecipeRedirect(reply.Status) || location == "" {
			reply.Content = body
			return reply, err
		}
		next, parseErr := target.Parse(location)
		if parseErr != nil {
			return reply, errors.New("site_material_origin_mismatch")
		}
		target = next
		if reply.Status == 303 || method == http.MethodPost && (reply.Status == 301 || reply.Status == 302) {
			method = http.MethodGet
			headers = maps.Clone(headers)
			for name := range headers {
				if strings.EqualFold(name, "Content-Type") {
					delete(headers, name)
				}
			}
		}
	}
	return BrushRecipeResponse{}, errors.New("site_http_redirect_limit")
}
