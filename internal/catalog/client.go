package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/logging"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	apiHost      = "api.themoviedb.org"
	maxBody      = 4 << 20
	requestLimit = 12 * time.Second
	maxID        = int64(1<<31 - 1)
)

var (
	keyPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	imagePattern = regexp.MustCompile(`^/[A-Za-z0-9_-]+\.(jpg|jpeg|png|webp)$`)
)

// Client uses only the official HTTPS API. It is safe for concurrent use.
// Redirects are never followed, including redirects to the same host.
type Client struct {
	credentials Credentials
	http        *http.Client
	configError *Error
	interval    time.Duration
	inflight    chan struct{}
	mu          sync.Mutex
	next        time.Time
	blocked     time.Time
}

func (Credentials) String() string   { return "TMDB credentials [redacted]" }
func (Credentials) GoString() string { return "catalog.Credentials{[redacted]}" }
func (*Client) String() string       { return "TMDB catalog client" }
func (*Client) GoString() string     { return "catalog.Client{[redacted]}" }

// New holds the supplied credential in memory, never reads environment/files,
// and performs no network request. Credential errors are returned by operations.
func New(credentials Credentials) *Client {
	return newClient(credentials, http.DefaultTransport, 250*time.Millisecond)
}

// The private transport seam lets package tests use httptest while production
// callers cannot configure another origin or disable TLS verification.
func newClient(credentials Credentials, transport http.RoundTripper, interval time.Duration) *Client {
	c := &Client{credentials: credentials, interval: interval, inflight: make(chan struct{}, 4)}
	c.http = &http.Client{
		Timeout:   requestLimit,
		Transport: logging.Transport(transport),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	switch {
	case credentials.APIKey == "" && credentials.AccessToken == "":
		c.configError = &Error{Code: "not_configured"}
	case credentials.APIKey != "" && credentials.AccessToken != "":
		c.configError = &Error{Code: "invalid_input"}
	case credentials.APIKey != "" && !keyPattern.MatchString(credentials.APIKey):
		c.configError = &Error{Code: "invalid_input"}
	case credentials.AccessToken != "" && (len(credentials.AccessToken) > 8192 || !tokenPattern.MatchString(credentials.AccessToken)):
		c.configError = &Error{Code: "invalid_input"}
	}
	return c
}

func (c *Client) wait(ctx context.Context) error {
	if c == nil || c.configError != nil {
		if c != nil && c.configError != nil {
			copy := *c.configError
			return &copy
		}
		return &Error{Code: "not_configured"}
	}
	if ctx == nil {
		return &Error{Code: "invalid_input"}
	}
	if ctx.Err() != nil {
		return &Error{Code: "canceled"}
	}
	c.mu.Lock()
	now := time.Now()
	if c.blocked.After(now) {
		after := c.blocked.Sub(now)
		c.mu.Unlock()
		return &Error{Code: "rate_limited", StatusCode: http.StatusTooManyRequests, RetryAfter: after}
	}
	start := now
	if c.next.After(start) {
		start = c.next
	}
	c.next = start.Add(c.interval)
	c.mu.Unlock()
	if delay := time.Until(start); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return &Error{Code: "canceled"}
		case <-timer.C:
		}
	}
	if ctx.Err() != nil {
		return &Error{Code: "canceled"}
	}
	// A concurrent request may have received 429 during the wait.
	c.mu.Lock()
	remaining := time.Until(c.blocked)
	c.mu.Unlock()
	if remaining > 0 {
		return &Error{Code: "rate_limited", StatusCode: http.StatusTooManyRequests, RetryAfter: remaining}
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, result any) error {
	if ctx == nil {
		return &Error{Code: "invalid_input"}
	}
	ctx, cancel := context.WithTimeout(ctx, requestLimit)
	defer cancel()
	if c == nil {
		return &Error{Code: "not_configured"}
	}
	select {
	case c.inflight <- struct{}{}:
		defer func() { <-c.inflight }()
	case <-ctx.Done():
		return &Error{Code: "canceled"}
	}
	if err := c.wait(ctx); err != nil {
		return err
	}
	values := url.Values{}
	for key, value := range query {
		values[key] = append([]string(nil), value...)
	}
	if values.Get("language") == "" {
		values.Set("language", "zh-CN")
	}
	if c.credentials.APIKey != "" {
		values.Set("api_key", c.credentials.APIKey)
	}
	ctx, _ = logging.Target(ctx, "metadata")
	u := url.URL{Scheme: "https", Host: apiHost, Path: "/3/" + path, RawQuery: values.Encode()}
	request, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "metadata", u.String()), http.MethodGet, u.String(), nil)
	if err != nil {
		return &Error{Code: "invalid_input"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "light-media/0.1")
	if c.credentials.AccessToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.credentials.AccessToken)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return &Error{Code: "canceled"}
		}
		return &Error{Code: "unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return c.responseError(response.StatusCode, response.Header.Get("Retry-After"))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return &Error{Code: "canceled"}
		}
		return &Error{Code: "unavailable"}
	}
	if len(body) > maxBody {
		return &Error{Code: "invalid_response"}
	}
	var status struct {
		Success    *bool `json:"success"`
		StatusCode int   `json:"status_code"`
	}
	if decode(body, &status) != nil {
		return &Error{Code: "invalid_response"}
	}
	if status.Success != nil && !*status.Success {
		switch status.StatusCode {
		case 3, 7, 10, 14:
			return &Error{Code: "unauthorized"}
		case 6, 34:
			return &Error{Code: "not_found"}
		case 25:
			return c.responseError(http.StatusTooManyRequests, "")
		default:
			return &Error{Code: "upstream"}
		}
	}
	if decode(body, result) != nil {
		return &Error{Code: "invalid_response"}
	}
	return nil
}

func decode(body []byte, result any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(result); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func (c *Client) responseError(status int, retry string) error {
	err := &Error{Code: "upstream", StatusCode: status}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		err.Code = "unauthorized"
	case status == http.StatusNotFound:
		err.Code = "not_found"
	case status == http.StatusTooManyRequests:
		err.Code = "rate_limited"
		err.RetryAfter = 5 * time.Second
		if seconds, parseErr := strconv.ParseInt(retry, 10, 32); parseErr == nil && seconds > 0 {
			err.RetryAfter = time.Duration(seconds) * time.Second
		} else if date, parseErr := http.ParseTime(retry); parseErr == nil && date.After(time.Now()) {
			err.RetryAfter = time.Until(date)
		}
		if err.RetryAfter > 60*time.Second {
			err.RetryAfter = 60 * time.Second
		}
		c.mu.Lock()
		until := time.Now().Add(err.RetryAfter)
		if until.After(c.blocked) {
			c.blocked = until
		}
		c.mu.Unlock()
	case status >= 300 && status < 400:
		err.Code = "redirect_blocked"
	}
	return err
}

func validID(id int64) bool { return id > 0 && id <= maxID }

func validPage(page int) bool { return page >= 1 && page <= 500 }

func kindOK(kind string) bool { return kind == "movie" || kind == "tv" }

func queryOK(query string) bool {
	if !utf8.ValidString(query) || len(query) > 480 || utf8.RuneCountInString(query) > 120 || strings.TrimSpace(query) == "" {
		return false
	}
	for _, r := range query {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (c *Client) text(value string, limit int) string {
	value = strings.ToValidUTF8(value, "")
	for _, secret := range []string{c.credentials.APIKey, c.credentials.AccessToken} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, value)
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func image(value string) string {
	if len(value) <= 128 && imagePattern.MatchString(value) {
		return value
	}
	return ""
}

func date(value string) (string, int, bool) {
	if value == "" {
		return "", 0, true
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil || t.Year() < 1800 || t.Year() > 2199 {
		return "", 0, false
	}
	return value, t.Year(), true
}
