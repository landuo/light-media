// Package fnos implements a bounded, read-only compatibility probe for the
// private fnOS Media API. The signature format is version dependent.
package fnos

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// These are public client protocol identifiers from the reference client, not
// user credentials. A successful probe does not make this an official API.
const clientID = "16CCEB3D-AB42-077D-36A1-F355324E4237"
const signingPrefix = "NDzZTVxnRKP8Z0jXg1VAMonaG8akvh"

type Config struct {
	Host         string   `json:"host"`
	Username     string   `json:"username"`
	Password     string   `json:"password"`
	AccessCode   string   `json:"access_code"`
	SampleTitles []string `json:"sample_titles,omitempty"`
}

type Sample struct {
	Type       string   `json:"type"`
	Title      string   `json:"title"`
	SourceID   string   `json:"source_id,omitempty"`
	Season     *int     `json:"season,omitempty"`
	Episode    *int     `json:"episode,omitempty"`
	DetailKeys []string `json:"detail_keys,omitempty"`
}
type Library struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Directories []string `json:"directories,omitempty"`
	Samples     []Sample `json:"samples,omitempty"`
}
type SearchSample struct {
	Query   string   `json:"query"`
	Status  string   `json:"status"`
	Samples []Sample `json:"samples"`
}
type Result struct {
	Status                string         `json:"status"`
	Code                  string         `json:"code,omitempty"`
	Frontend              string         `json:"frontend,omitempty"`
	Backend               string         `json:"backend,omitempty"`
	Authenticated         bool           `json:"authenticated"`
	Admin                 bool           `json:"admin"`
	Libraries             []Library      `json:"libraries,omitempty"`
	SearchSamples         []SearchSample `json:"search_samples,omitempty"`
	Requests              int            `json:"requests"`
	WriteOperationsTested bool           `json:"write_operations_tested"`
	NFOImportTested       bool           `json:"nfo_import_tested"`
	Truncated             bool           `json:"truncated"`
}

type envelope struct {
	Code *int            `json:"code"`
	Data json.RawMessage `json:"data"`
}
type client struct {
	host, token string
	http        *http.Client
	requests    int
}

func digest(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }
func Signature(path, payload, nonce, timestamp string) string {
	return "nonce=" + nonce + "&timestamp=" + timestamp + "&sign=" + digest(strings.Join([]string{signingPrefix, path, nonce, timestamp, digest(payload), clientID}, "_"))
}

func (c *client) request(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.requestQuery(ctx, path, body, "")
}

// The wire query is percent-encoded, but authx hashes the decoded q=value text.
func (c *client) requestQuery(ctx context.Context, path string, body any, query string) (json.RawMessage, error) {
	if c.requests >= 20 {
		return nil, fmt.Errorf("REQUEST_LIMIT")
	}
	method := http.MethodGet
	var payload []byte
	var err error
	if body != nil {
		method = http.MethodPost
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("INVALID_BODY")
		}
	}
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return nil, fmt.Errorf("NONCE_FAILED")
	}
	apiPath := "/v/api/v1" + path
	wireURL := c.host + "/api/v1" + path
	signingPayload := string(payload)
	if query != "" {
		if body != nil {
			return nil, fmt.Errorf("INVALID_BODY")
		}
		wireURL += "?" + url.Values{"q": {query}}.Encode()
		signingPayload = "q=" + query
	}
	r, err := http.NewRequestWithContext(ctx, method, wireURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("INVALID_REQUEST")
	}
	r.Header.Set("Accept", "application/json")
	r.Header.Set("User-Agent", "light-media-m0/0.1")
	r.Header.Set("Referer", c.host)
	r.Header.Set("authx", Signature(apiPath, signingPayload, fmt.Sprint(n.Int64()+100000), fmt.Sprint(time.Now().UnixMilli())))
	if c.token != "" {
		r.Header.Set("Authorization", c.token)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	c.requests++
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("TRANSPORT_FAILED")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP_%d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil {
		return nil, fmt.Errorf("BODY_FAILED")
	}
	if len(data) > 2*1024*1024 {
		return nil, fmt.Errorf("BODY_LIMIT")
	}
	var env envelope
	if json.Unmarshal(data, &env) != nil || env.Code == nil {
		return nil, fmt.Errorf("INVALID_ENVELOPE")
	}
	if *env.Code != 0 {
		return nil, fmt.Errorf("BUSINESS_%d", *env.Code)
	}
	if len(env.Data) == 0 || bytes.Equal(bytes.TrimSpace(env.Data), []byte("null")) {
		return nil, fmt.Errorf("INVALID_DATA")
	}
	return env.Data, nil
}

func Probe(ctx context.Context, cfg Config) (out Result) {
	out.Status = "failed"
	if len(cfg.SampleTitles) > 3 {
		out.Code = "SAMPLE_TITLE_LIMIT"
		return
	}
	for _, title := range cfg.SampleTitles {
		if strings.TrimSpace(title) == "" || len(title) > 1024 || len([]rune(title)) > 256 {
			out.Code = "INVALID_SAMPLE_TITLE"
			return
		}
	}
	u, err := url.Parse(cfg.Host)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		out.Code = "INVALID_HOST"
		return
	}
	if cfg.AccessCode != "" {
		out.Status = "unverified"
		out.Code = "ACCESS_CODE_SESSION_REQUIRED"
		return
	}
	host := strings.TrimRight(cfg.Host, "/")
	if !strings.HasSuffix(host, "/v") {
		host += "/v"
	}
	c := client{host: host, http: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	defer func() { out.Requests = c.requests; sanitizeResult(&out, []string{cfg.Username, cfg.Password, c.token}) }()
	step := func(path string, body any, dst any) bool {
		data, e := c.request(ctx, path, body)
		if e != nil {
			out.Code = e.Error()
			return false
		}
		if json.Unmarshal(data, dst) != nil {
			out.Code = "INVALID_DATA"
			return false
		}
		return true
	}
	var ver struct {
		Version string `json:"version"`
		Backend string `json:"mediasrvVersion"`
	}
	if !step("/sys/version", nil, &ver) {
		return
	}
	if !versionLike.MatchString(ver.Version) || !versionLike.MatchString(ver.Backend) {
		out.Code = "INVALID_VERSION"
		return
	}
	out.Frontend = ver.Version
	out.Backend = ver.Backend
	if cfg.Username == "" || cfg.Password == "" {
		out.Status = "unverified"
		out.Code = "CREDENTIALS_REQUIRED"
		return
	}
	var auth struct {
		Token string `json:"token"`
	}
	if !step("/login", map[string]string{"username": cfg.Username, "password": cfg.Password, "app_name": "trimemedia-web"}, &auth) {
		return
	}
	if auth.Token == "" || len(auth.Token) > 8192 || strings.ContainsAny(auth.Token, "\r\n") {
		out.Code = "EMPTY_TOKEN"
		return
	}
	c.token = auth.Token
	var user struct {
		Admin *int `json:"is_admin"`
	}
	if !step("/user/info", nil, &user) {
		return
	}
	if user.Admin == nil || (*user.Admin != 0 && *user.Admin != 1) {
		out.Code = "INVALID_USER"
		return
	}
	out.Authenticated = true
	out.Admin = *user.Admin == 1
	var libs []struct {
		GUID     string   `json:"guid"`
		Name     string   `json:"name"`
		Title    string   `json:"title"`
		Category string   `json:"category"`
		Dirs     []string `json:"dir_list"`
	}
	endpoint := "/mediadb/list"
	if out.Admin {
		endpoint = "/mdb/list"
	}
	if !step(endpoint, nil, &libs) {
		return
	}
	if len(libs) > 8 {
		out.Truncated = true
		libs = libs[:8]
	}
	for _, l := range libs {
		name := l.Name
		if name == "" {
			name = l.Title
		}
		if l.GUID == "" || len(l.GUID) > 256 || name == "" || l.Category == "" {
			out.Code = "INVALID_LIBRARY"
			return
		}
		if len(l.Dirs) > 16 {
			l.Dirs = l.Dirs[:16]
			out.Truncated = true
		}
		lib := Library{Name: name, Category: l.Category, Directories: l.Dirs}
		var page struct {
			List []struct {
				Type     string `json:"type"`
				Title    string `json:"title"`
				SourceID string `json:"trim_id"`
				Season   *int   `json:"season_number"`
				Episode  *int   `json:"episode_number"`
			} `json:"list"`
		}
		if !step("/item/list", map[string]any{"ancestor_guid": l.GUID, "tags": map[string]any{"type": []string{"Movie", "TV"}}, "sort_type": "DESC", "sort_column": "create_time", "page": 1, "page_size": 1, "exclude_grouped_video": 1}, &page) {
			out.Libraries = append(out.Libraries, lib)
			return
		}
		if page.List == nil {
			out.Code = "INVALID_ITEMS"
			return
		}
		if len(page.List) > 1 {
			page.List = page.List[:1]
			out.Truncated = true
		}
		for _, item := range page.List {
			if (item.Type != "Movie" && item.Type != "TV") || item.Title == "" || !validNumber(item.Season) || !validNumber(item.Episode) {
				out.Code = "INVALID_ITEM"
				return
			}
			lib.Samples = append(lib.Samples, Sample{Type: item.Type, Title: item.Title, SourceID: item.SourceID, Season: item.Season, Episode: item.Episode})
		}
		out.Libraries = append(out.Libraries, lib)
	}
	for _, title := range cfg.SampleTitles {
		data, e := c.requestQuery(ctx, "/search/list", nil, title)
		if e != nil {
			out.Code = e.Error()
			return
		}
		var items []struct {
			Type     string `json:"type"`
			Title    string `json:"title"`
			SourceID string `json:"trim_id"`
			Season   *int   `json:"season_number"`
			Episode  *int   `json:"episode_number"`
		}
		if json.Unmarshal(data, &items) != nil || items == nil {
			out.Code = "INVALID_SEARCH_DATA"
			return
		}
		search := SearchSample{Query: title, Status: "ok", Samples: []Sample{}}
		if len(items) > 3 {
			items = items[:3]
			out.Truncated = true
		}
		if len(items) == 0 {
			search.Status = "empty_result"
		}
		for _, item := range items {
			if !sampleType.MatchString(item.Type) || item.Title == "" || !validNumber(item.Season) || !validNumber(item.Episode) {
				out.Code = "INVALID_SEARCH_ITEM"
				return
			}
			search.Samples = append(search.Samples, Sample{Type: item.Type, Title: item.Title, SourceID: item.SourceID, Season: item.Season, Episode: item.Episode})
		}
		out.SearchSamples = append(out.SearchSamples, search)
	}
	out.Status = "passed"
	return
}

var versionLike = regexp.MustCompile(`^v?[0-9]+(?:\.[0-9]+){1,3}(?:[-+._][A-Za-z0-9.-]+)?$`)
var remoteURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var sampleType = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

func validNumber(n *int) bool { return n == nil || (*n >= 0 && *n <= 100000) }
func sanitizeResult(out *Result, secrets []string) {
	var variants []string
	for _, s := range secrets {
		if s != "" {
			variants = append(variants, s, url.QueryEscape(s), url.PathEscape(s))
		}
	}
	sort.Slice(variants, func(i, j int) bool { return len(variants[i]) > len(variants[j]) })
	clean := func(s string, limit int) string {
		if len(s) > 8192 {
			out.Truncated = true
			return "[OVERSIZED]"
		}
		for _, secret := range variants {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
		s = remoteURL.ReplaceAllString(s, "[URL REDACTED]")
		s = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return ' '
			}
			return r
		}, s)
		runes := []rune(s)
		if len(runes) > limit {
			out.Truncated = true
			return string(runes[:limit]) + "…"
		}
		return s
	}
	out.Frontend = clean(out.Frontend, 96)
	out.Backend = clean(out.Backend, 96)
	for i := range out.Libraries {
		l := &out.Libraries[i]
		l.Name = clean(l.Name, 256)
		l.Category = clean(l.Category, 64)
		for j := range l.Directories {
			l.Directories[j] = clean(l.Directories[j], 1024)
		}
		for j := range l.Samples {
			s := &l.Samples[j]
			s.Title = clean(s.Title, 256)
			s.SourceID = clean(s.SourceID, 128)
		}
	}
	for i := range out.SearchSamples {
		s := &out.SearchSamples[i]
		s.Query = clean(s.Query, 256)
		for j := range s.Samples {
			item := &s.Samples[j]
			item.Type = clean(item.Type, 32)
			item.Title = clean(item.Title, 256)
			item.SourceID = clean(item.SourceID, 128)
		}
	}
}
