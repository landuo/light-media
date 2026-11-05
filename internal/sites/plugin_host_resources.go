package sites

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"light-media/internal/logging"
	"light-media/internal/metainfo"
)

// ConfiguredRSS is called only with the host's saved RSS URL. The ordinary
// RssHelper request does not send the site's login cookie or API key. Preserve
// that separation, while retaining the existing client guard/proxy/limits.
func (c *Client) ConfiguredRSS(ctx context.Context, configuredURL string) ([]byte, string, error) {
	u, err := url.Parse(configuredURL)
	if err != nil || u.User != nil || u.Fragment != "" || !sameOrigin(u, c.base) {
		return nil, "", errors.New("site_rss_origin_mismatch")
	}
	req, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "site", u.String()), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", errors.New("site_invalid_request")
	}
	ua := c.config.UserAgent
	if ua == "" {
		ua = "light-media/0.1"
	}
	req.Header.Set("User-Agent", ua)
	if c.requestGuard != nil {
		if err = c.requestGuard(ctx); err != nil {
			return nil, "", err
		}
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", errors.New("site_transport_error")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", errors.New("site_http_error")
	}
	const limit = 50 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", errors.New("site_read_error")
	}
	if len(data) > limit {
		return nil, "", errors.New("site_body_limit")
	}
	return data, response.Header.Get("Content-Type"), nil
}

// SearchWithCategoryParams changes only source-defined category query fields.
// Credentials, proxy, request guard and parsing remain the existing site host.
func (c *Client) SearchWithCategoryParams(ctx context.Context, query string, page int, category url.Values) (SearchResult, error) {
	if len(category) == 0 || c.config.Kind == "mteam" {
		return c.Search(ctx, query, page)
	}
	if c.config.Kind == "rousi" {
		return c.searchRousiWithCategory(ctx, query, page, category.Get("category"))
	}
	if len(query) > 512 || page > 10000 {
		return SearchResult{}, errors.New("site_invalid_search")
	}
	if page <= 0 {
		page = 1
	}
	u := c.endpoint("torrents.php")
	params := url.Values{"search": {query}, "page": {strconv.Itoa(page - 1)}, "search_area": {"0"}, "search_mode": {"0"}}
	if imdbID.MatchString(query) {
		params.Set("search_area", "4")
	}
	for key, values := range category {
		params[key] = append([]string{}, values...)
	}
	u.RawQuery = params.Encode()
	body, _, err := c.request(ctx, http.MethodGet, u, nil, c.auth(), htmlLimit)
	if err != nil {
		return SearchResult{}, err
	}
	return c.parseHTML(body, u, page)
}

// ConfiguredMaterial receives an enclosure previously collected by the host,
// never a URL passed through a plugin RPC. An RSS token is its own credential;
// do not add the saved login cookie/API key or follow it onto another origin.
func (c *Client) ConfiguredMaterial(ctx context.Context, enclosure string) ([]byte, error) {
	u, err := url.Parse(enclosure)
	if err != nil || u.User != nil || u.Fragment != "" || !sameOrigin(u, c.base) {
		return nil, errors.New("site_material_origin_mismatch")
	}
	data, _, err := c.request(ctx, http.MethodGet, u, nil, nil, torrentLimit)
	if err != nil {
		return nil, err
	}
	if _, err = metainfo.ProductInspect(data); err != nil {
		return nil, errors.New("site_invalid_metainfo")
	}
	return data, nil
}
