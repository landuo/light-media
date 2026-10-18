// Package sites implements native PT adapters and the standard NexusPHP protocol.
// Secret-bearing torrent URLs are resolved only inside Torrent and never appear
// in Resource or errors.
package sites

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"light-media/internal/logging"
	"light-media/internal/metainfo"
	"light-media/internal/networkproxy"
)

type Config struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Cookie    string `json:"cookie,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
	UserAgent string `json:"ua,omitempty"`
	Timeout   int    `json:"timeout,omitempty"`
}

type Resource struct {
	HitAndRun      bool     `json:"hit_and_run"`
	Labels         []string `json:"labels,omitempty"`
	SourceSubtitle *string  `json:"source_subtitle,omitempty"`
	PriorityOrder  int      `json:"pri_order,omitempty"`
	ID             string   `json:"id"`
	SiteID         string   `json:"site_id"`
	Title          string   `json:"title"`
	Subtitle       string   `json:"subtitle"`
	Size           int64    `json:"size"`
	Seeders        int      `json:"seeders"`
	Leechers       int      `json:"leechers"`
	PublishedAt    string   `json:"published_at"`
	DownloadFactor float64  `json:"download_factor"`
	UploadFactor   float64  `json:"upload_factor"`
	PromotionEnd   string   `json:"promotion_end"`
	Free           bool     `json:"free"`
	DetailsURL     string   `json:"details_url"`
	Hash           string   `json:"hash,omitempty"`
	Resolution     string   `json:"resolution"`
	Codec          string   `json:"codec"`
	Source         string   `json:"source"`
}

type SearchResult struct {
	Resources []Resource `json:"resources"`
	Page      int        `json:"page"`
	HasMore   bool       `json:"has_more"`
}

type Client struct {
	closeProxyTransport func()
	requestGuard        func(context.Context) error
	config              Config
	base                *url.URL
	http                *http.Client
}

var numericID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var imdbID = regexp.MustCompile(`^tt[0-9]+$`)
var configID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

const htmlLimit = 8 << 20
const torrentLimit = 32 << 20

func NewWithProxy(c Config, proxy networkproxy.Config) (*Client, error) {
	client, err := New(c)
	if err != nil {
		return nil, err
	}
	if proxy.HTTP == "" && proxy.HTTPS == "" {
		return client, nil
	}
	transport, err := proxy.Transport(nil)
	if err != nil {
		return nil, err
	}
	client.http.Transport = logging.Transport(transport)
	client.closeProxyTransport = transport.(interface{ CloseIdleConnections() }).CloseIdleConnections
	return client, nil
}

func (c *Client) CloseIdleConnections() {
	if c.closeProxyTransport != nil {
		c.closeProxyTransport()
	}
}

func New(c Config) (*Client, error) {
	switch c.Kind {
	case "mteam", "hhanclub", "ubits", "jiayuan", "1ptba", "nexusphp":
	default:
		return nil, errors.New("site_unsupported_kind")
	}
	if !configID.MatchString(c.ID) || strings.ContainsAny(c.Cookie+c.APIKey, "\r\n") || !validUserAgent(c.UserAgent) || c.Timeout < 0 || c.Timeout > 120 {
		return nil, errors.New("site_invalid_config")
	}
	if c.Kind == "nexusphp" {
		origin, err := NormalizeNexusPHPOrigin(c.BaseURL)
		if err != nil {
			return nil, err
		}
		c.BaseURL = origin
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("site_invalid_origin")
	}
	// Plain HTTP is available solely for local test fixtures. Production PT
	// authentication must not travel over an unencrypted connection.
	if u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return nil, errors.New("site_https_required")
	}
	if c.Kind == "mteam" {
		if c.APIKey == "" {
			return nil, errors.New("site_api_key_required")
		}
		if !mteamAPI(u) {
			return nil, errors.New("site_mteam_origin_not_allowed")
		}
	} else if c.Cookie == "" {
		return nil, errors.New("site_cookie_required")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/"
	timeout := 25 * time.Second
	if c.Timeout > 0 {
		timeout = time.Duration(c.Timeout) * time.Second
	}
	var transport http.RoundTripper
	if c.Kind == "nexusphp" {
		transport = publicSiteTransport()
	}
	return &Client{config: c, base: u, http: &http.Client{
		Timeout:       timeout,
		Transport:     logging.Transport(transport),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Search accepts a one-based page. Zero selects the first page.
func (c *Client) Search(ctx context.Context, query string, page int) (SearchResult, error) {
	ctx = logging.SiteContext(ctx, c.config.ID)
	if len(query) > 512 || page > 10000 {
		return SearchResult{}, errors.New("site_invalid_search")
	}
	if page <= 0 {
		page = 1
	}
	ctx, _ = logging.Target(ctx, "site")
	if c.config.Kind == "mteam" {
		return c.searchMTeam(ctx, query, page)
	}
	u := c.endpoint("torrents.php")
	q := url.Values{"search": {query}, "page": {strconv.Itoa(page - 1)}, "search_area": {"0"}, "search_mode": {"0"}}
	// Nexus' IMDb search area is only valid for an exact IMDb identifier.
	if imdbID.MatchString(query) {
		q.Set("search_area", "4")
	}
	u.RawQuery = q.Encode()
	b, _, err := c.request(ctx, http.MethodGet, u, nil, c.auth(), htmlLimit)
	if err != nil {
		return SearchResult{}, err
	}
	return c.parseHTML(b, u, page)
}

// Torrent takes a stable numeric site ID, never a caller supplied URL. The
// tracker passkey remains in metainfo and is delivered only to the downloader.
func (c *Client) Torrent(ctx context.Context, resourceID string) ([]byte, error) {
	ctx = logging.SiteContext(ctx, c.config.ID)
	if !numericID.MatchString(resourceID) {
		return nil, errors.New("site_invalid_resource_id")
	}
	ctx, _ = logging.Target(ctx, "material")
	var u *url.URL
	headers := c.auth()
	var err error
	if c.config.Kind == "mteam" {
		u, err = c.mteamTorrentURL(ctx, resourceID)
		headers = nil // Token URLs and the CDN must never receive API credentials.
	} else if c.config.Kind == "hhanclub" {
		u, err = c.hhanTorrentURL(ctx, resourceID)
	} else {
		u = c.endpoint("download.php")
		u.RawQuery = url.Values{"id": {resourceID}}.Encode()
	}
	if err != nil {
		return nil, err
	}
	b, redirect, err := c.request(ctx, http.MethodGet, u, nil, headers, torrentLimit)
	if err != nil && redirect != "" && c.config.Kind == "mteam" {
		next, e := url.Parse(redirect)
		if e != nil || len(redirect) > 8192 {
			return nil, errors.New("site_invalid_material_redirect")
		}
		next = u.ResolveReference(next)
		if !c.allowedMaterial(next) {
			return nil, errors.New("site_material_redirect_blocked")
		}
		b, _, err = c.request(ctx, http.MethodGet, next, nil, nil, torrentLimit)
	}
	if err != nil {
		return nil, err
	}
	if _, err = metainfo.ProductInspect(b); err != nil {
		return nil, errors.New("site_invalid_metainfo")
	}
	return b, nil
}

func (c *Client) endpoint(path string) *url.URL {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/" + path
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	return &u
}

func (c *Client) auth() map[string]string {
	if c.config.Kind == "mteam" {
		return map[string]string{"x-api-key": c.config.APIKey, "Content-Type": "application/json"}
	}
	return map[string]string{"Cookie": c.config.Cookie}
}

func (c *Client) request(ctx context.Context, method string, u *url.URL, body []byte, headers map[string]string, limit int64) ([]byte, string, error) {
	if (len(headers) > 0 || c.config.Kind == "nexusphp") && !sameOrigin(u, c.base) {
		return nil, "", errors.New("site_credential_origin_mismatch")
	}
	req, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "material", u.String()), method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, "", errors.New("site_invalid_request")
	}
	userAgent := c.config.UserAgent
	if userAgent == "" {
		userAgent = "light-media/0.1"
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.requestGuard != nil {
		if err = c.requestGuard(ctx); err != nil {
			return nil, "", err
		}
	}
	res, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, "", context.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			return nil, "", context.Canceled
		}
		return nil, "", errors.New("site_transport_error")
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == 401:
		return nil, "", errors.New("site_authentication_failed")
	case res.StatusCode == 403:
		return nil, "", errors.New("site_forbidden_or_challenge")
	case res.StatusCode == 429:
		return nil, "", errors.New("site_rate_limited")
	case res.StatusCode >= 300 && res.StatusCode < 400:
		return nil, res.Header.Get("Location"), errors.New("site_redirect_blocked")
	case res.StatusCode < 200 || res.StatusCode >= 300:
		return nil, "", errors.New("site_http_error")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, "", context.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			return nil, "", context.Canceled
		}
		return nil, "", errors.New("site_read_error")
	}
	if int64(len(b)) > limit {
		return nil, "", errors.New("site_body_limit")
	}
	return b, "", nil
}

func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && a.User == nil && b.User == nil && a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && originPort(a) == originPort(b) && a.Fragment == ""
}

func originPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func mteamAPI(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && (u.Port() == "" || u.Port() == "443") && u.User == nil && (strings.EqualFold(u.Hostname(), "api.m-team.cc") || strings.EqualFold(u.Hostname(), "api.m-team.io") || strings.EqualFold(u.Hostname(), "test2.m-team.cc"))
}

func mteamMaterial(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	return strings.EqualFold(u.Hostname(), "fr1.halomt.com") || mteamAPI(u)
}

func (c *Client) allowedMaterial(u *url.URL) bool {
	return mteamMaterial(u) && (sameOrigin(u, c.base) || strings.EqualFold(u.Hostname(), "fr1.halomt.com"))
}

// SetRequestGuard installs the owning application admission/quota check. Configure before use.
func (c *Client) SetRequestGuard(guard func(context.Context) error) { c.requestGuard = guard }
