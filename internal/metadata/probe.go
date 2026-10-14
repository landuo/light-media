// Package metadata implements a fixed-sample, read-only M0 TMDB API probe.
package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"light-media/internal/logging"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxBody = 1 << 20

type Credentials struct {
	APIKey      string `json:"api_key,omitempty"`
	AccessToken string `json:"access_token,omitempty"`
}
type Result struct {
	Sample            string `json:"sample"`
	Type              string `json:"type"`
	RequestedSourceID int64  `json:"requested_source_id"`
	Status            string `json:"status"`
	Reason            string `json:"reason,omitempty"`
	Source            string `json:"source"`
	SourceID          int64  `json:"source_id,omitempty"`
	ParentSourceID    int64  `json:"parent_source_id,omitempty"`
	ParentContext     string `json:"parent_context,omitempty"`
	Title             string `json:"title,omitempty"`
	Year              int    `json:"year,omitempty"`
	Date              string `json:"date,omitempty"`
	Season            int    `json:"season,omitempty"`
	Episode           int    `json:"episode,omitempty"`
}
type Report struct {
	TraceTargetID string   `json:"trace_target_id,omitempty"`
	Status        string   `json:"status"`
	Evidence      string   `json:"evidence"`
	Requests      int      `json:"requests"`
	Results       []Result `json:"results"`
}

type sample struct {
	name, kind, path, expectedTitle string
	id                              int64
	expectedYear, season, episode   int
}

var samples = []sample{
	{"movie-nightking", "movie", "movie/1602727", "夜王", 1602727, 2026, 0, 0},
	{"series-bigbang", "tv", "tv/1418", "生活大爆炸", 1418, 0, 0, 0},
	{"episode-bigbang", "episode", "tv/1418/season/7/episode/17", "", 1418, 0, 7, 17},
	{"series-zhuxian", "tv", "tv/206484", "诛仙", 206484, 0, 0, 0},
	{"episode-zhuxian", "episode", "tv/206484/season/4/episode/5", "", 206484, 0, 4, 5},
}
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Run never accepts a host, path, arbitrary ID or custom HTTP client.
func Run(ctx context.Context, credentials Credentials) Report { return run(ctx, credentials, nil) }
func run(ctx context.Context, c Credentials, syntheticClient *http.Client) Report {
	r := Report{Status: "unverified", Evidence: "not_requested", Results: make([]Result, len(samples))}
	ctx, r.TraceTargetID = logging.Target(ctx, "metadata")
	for i, s := range samples {
		r.Results[i] = Result{Sample: s.name, Type: s.kind, RequestedSourceID: s.id, Status: "unverified", Reason: "credential_not_configured", Source: "tmdb"}
	}
	if c.APIKey == "" && c.AccessToken == "" {
		return r
	}
	if (c.APIKey != "" && c.AccessToken != "") || (c.APIKey != "" && !keyPattern.MatchString(c.APIKey)) || (c.AccessToken != "" && (len(c.AccessToken) > 8192 || !tokenPattern.MatchString(c.AccessToken))) {
		r.Status = "invalid_input"
		for i := range r.Results {
			r.Results[i].Status, r.Results[i].Reason = "invalid_input", "choose_one_valid_credential"
		}
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if syntheticClient != nil {
		client.Transport = syntheticClient.Transport
		r.Evidence = "synthetic_http_roundtrip"
	} else {
		r.Evidence = "live_request"
	}
	client.Transport = logging.Transport(client.Transport)
	verifiedSeries := map[int64]bool{}
	r.Status = "ok"
	for i, s := range samples {
		result := &r.Results[i]
		result.Reason = ""
		u := url.URL{Scheme: "https", Host: "api.themoviedb.org", Path: "/3/" + s.path}
		query := url.Values{"language": {"zh-CN"}}
		if c.APIKey != "" {
			query.Set("api_key", c.APIKey)
		}
		u.RawQuery = query.Encode()
		req, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "metadata", u.String()), http.MethodGet, u.String(), nil)
		if err != nil {
			result.Status = "invalid_input"
			r.Status = "incomplete"
			continue
		}
		if c.AccessToken != "" {
			req.Header.Set("Authorization", "Bearer "+c.AccessToken)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "light-media-M0/0.1")
		r.Requests++
		response, err := client.Do(req)
		if err != nil {
			result.Status = "transport_error"
			r.Status = "incomplete"
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, MaxBody+1))
		response.Body.Close()
		if readErr != nil {
			result.Status = "transport_error"
			r.Status = "incomplete"
			continue
		}
		if len(body) > MaxBody {
			result.Status = "body_limit"
			r.Status = "incomplete"
			continue
		}
		switch {
		case response.StatusCode == 401:
			result.Status = "authentication_failed"
		case response.StatusCode == 403:
			result.Status = "forbidden"
		case response.StatusCode == 404:
			result.Status = "not_found"
		case response.StatusCode == 429:
			result.Status = "rate_limited"
		case response.StatusCode >= 300 && response.StatusCode < 400:
			result.Status = "redirect_blocked"
		case response.StatusCode < 200 || response.StatusCode >= 300:
			result.Status = "http_error"
		default:
			result.Status, result.Reason = validate(body, s, verifiedSeries, c, result)
			if result.Status == "ok" && s.kind == "tv" {
				verifiedSeries[s.id] = true
			}
		}
		if result.Status != "ok" {
			r.Status = "incomplete"
		}
		// An invalid credential or rate limit should not trigger four more calls.
		if result.Status == "authentication_failed" || result.Status == "forbidden" || result.Status == "rate_limited" {
			for j := i + 1; j < len(samples); j++ {
				r.Results[j].Reason = "not_requested_after_" + result.Status
			}
			break
		}
	}
	return r
}

type details struct {
	ID           int64  `json:"id"`
	ShowID       *int64 `json:"show_id"`
	MediaType    string `json:"media_type"`
	Title        string `json:"title"`
	Name         string `json:"name"`
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
	AirDate      string `json:"air_date"`
	Season       int    `json:"season_number"`
	Episode      int    `json:"episode_number"`
	Success      *bool  `json:"success"`
	StatusCode   int    `json:"status_code"`
}

func validate(body []byte, s sample, series map[int64]bool, c Credentials, result *Result) (string, string) {
	d := json.NewDecoder(bytes.NewReader(body))
	var value details
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
		return "protocol_changed", "invalid_json_schema"
	}
	if value.Success != nil && !*value.Success {
		if value.StatusCode == 3 || value.StatusCode == 7 || value.StatusCode == 10 || value.StatusCode == 14 {
			return "authentication_failed", "business_authentication_failure"
		}
		return "business_error", "provider_business_failure"
	}
	if value.ID <= 0 {
		return "protocol_changed", "missing_stable_source_id"
	}
	title, date := value.Name, value.FirstAirDate
	if s.kind == "movie" {
		title, date = value.Title, value.ReleaseDate
	}
	if s.kind == "episode" {
		date = value.AirDate
	}
	if !safeText(title, c) {
		return "protocol_changed", "invalid_or_sensitive_title"
	}
	if s.expectedTitle != "" && title != s.expectedTitle {
		return "identity_mismatch", "localized_title_mismatch"
	}
	if s.kind != "episode" && value.ID != s.id {
		return "identity_mismatch", "source_id_mismatch"
	}
	if value.MediaType != "" && value.MediaType != s.kind {
		return "identity_mismatch", "media_type_mismatch"
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil || t.Year() < 1800 || t.Year() > 2199 {
		return "metadata_incomplete", "unknown_or_invalid_date"
	}
	if s.expectedYear != 0 && t.Year() != s.expectedYear {
		return "identity_mismatch", "release_year_mismatch"
	}
	if s.kind == "episode" {
		if value.Season <= 0 || value.Episode <= 0 || value.Season != s.season || value.Episode != s.episode {
			return "identity_mismatch", "season_episode_mismatch"
		}
		if !series[s.id] {
			return "unverified", "parent_series_not_verified"
		}
		if value.ShowID != nil && *value.ShowID != s.id {
			return "identity_mismatch", "response_parent_id_mismatch"
		}
		result.ParentSourceID, result.Season, result.Episode = s.id, s.season, s.episode
		result.ParentContext = "verified_series_and_fixed_request_route"
		if value.ShowID != nil {
			result.ParentContext = "verified_response_show_id_and_request_route"
		}
	}
	result.SourceID, result.Title, result.Year, result.Date = value.ID, title, t.Year(), t.Format("2006-01-02")
	return "ok", ""
}

func safeText(text string, c Credentials) bool {
	if text == "" || len(text) > 512 || !utf8.ValidString(text) {
		return false
	}
	if (c.APIKey != "" && strings.Contains(text, c.APIKey)) || (c.AccessToken != "" && strings.Contains(text, c.AccessToken)) {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ParseCredentials is separate so the CLI can reject arbitrary host/config fields.
func ParseCredentials(body []byte) (Credentials, error) {
	var c Credentials
	if len(bytes.TrimSpace(body)) == 0 {
		return c, nil
	}
	if len(body) > 65536 {
		return c, io.ErrShortBuffer
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	if d.Decode(new(any)) != io.EOF {
		return Credentials{}, io.ErrUnexpectedEOF
	}
	return c, nil
}

// SourceKey illustrates type-scoped stable identity; episode IDs use their own type.
func (r Result) SourceKey() string {
	if r.Status != "ok" || r.SourceID <= 0 {
		return ""
	}
	return r.Source + ":" + r.Type + ":" + strconv.FormatInt(r.SourceID, 10)
}
