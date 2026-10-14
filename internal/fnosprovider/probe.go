// Package fnosprovider probes the installed client's read-only metadata selector.
// Search results without explicit standard ID fields cannot close episode gates.
package fnosprovider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"light-media/internal/fnos"
	"light-media/internal/logging"
)

type Sample struct {
	label, title, kind, id string
	year, season, episode  int
}

var fixed = []Sample{{"movie-nightking", "夜王", "Movie", "1602727", 2026, 0, 0}, {"series-bigbang", "生活大爆炸", "TV", "1418", 0, 7, 17}, {"series-zhuxian", "诛仙", "TV", "206484", 0, 4, 5}}

type Episode struct {
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	TMDBID       int64  `json:"tmdb_id,omitempty"`
	ParentTMDBID int64  `json:"parent_tmdb_id,omitempty"`
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
	Date         string `json:"date,omitempty"`
	Title        string `json:"title,omitempty"`
}
type Result struct {
	Sample          string   `json:"sample"`
	Status          string   `json:"status"`
	Reason          string   `json:"reason,omitempty"`
	RequestedSource string   `json:"requested_source"`
	RequestedID     string   `json:"requested_id"`
	ResponseCount   int      `json:"response_count"`
	MatchedTitle    bool     `json:"matched_title"`
	Type            string   `json:"type,omitempty"`
	Title           string   `json:"title,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	ProviderIDKind  string   `json:"provider_id_kind,omitempty"`
	SourceID        string   `json:"source_id,omitempty"`
	ExplicitTMDBID  int64    `json:"explicit_tmdb_id,omitempty"`
	Date            string   `json:"date,omitempty"`
	KnownFields     []string `json:"known_fields,omitempty"`
	TargetEpisode   *Episode `json:"target_episode,omitempty"`
}
type Report struct {
	TraceTargetID            string   `json:"trace_target_id,omitempty"`
	Status                   string   `json:"status"`
	Evidence                 string   `json:"evidence"`
	Requests                 int      `json:"requests"`
	Authenticated            bool     `json:"authenticated"`
	WriteOperations          bool     `json:"write_operations"`
	ProviderIdentityComplete bool     `json:"provider_identity_complete"`
	EpisodeIdentityComplete  bool     `json:"episode_identity_complete"`
	Code                     string   `json:"code,omitempty"`
	Results                  []Result `json:"results"`
}
type row struct {
	Source       string            `json:"source"`
	SourceID     json.RawMessage   `json:"source_id"`
	TMDBID       json.RawMessage   `json:"tmdb_id"`
	Type         string            `json:"type"`
	Title        string            `json:"title"`
	AirDate      string            `json:"air_date"`
	ReleaseDate  string            `json:"release_date"`
	FirstAirDate string            `json:"first_air_date"`
	Episodes     []json.RawMessage `json:"episodes"`
	Seasons      []struct {
		Number   int               `json:"season_number"`
		Episodes []json.RawMessage `json:"episodes"`
	} `json:"seasons"`
}
type envelope struct {
	Code *int            `json:"code"`
	Data json.RawMessage `json:"data"`
}

const bodyLimit = 1 << 20

var integerID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var opaqueProviderID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var allowedKnownFields = []string{"source", "source_id", "type", "title", "tmdb_id", "imdb_id", "air_date", "release_date", "first_air_date", "season_number", "episode_number", "seasons", "episodes", "id", "trim_id"}

func Probe(ctx context.Context, cfg fnos.Config) Report { return probe(ctx, cfg, nil) }
func probe(ctx context.Context, cfg fnos.Config, syntheticClient *http.Client) (out Report) {
	out = Report{Status: "unverified", Evidence: "not_requested", Results: []Result{}}
	ctx, out.TraceTargetID = logging.Target(ctx, "fnos")
	for _, s := range fixed {
		r := Result{Sample: s.label, Status: "unverified", Reason: "not_requested", RequestedSource: "tmdb_id", RequestedID: s.id}
		if s.season > 0 {
			r.TargetEpisode = &Episode{Status: "unverified", Reason: "no_readonly_provider_episode_route_observed", Season: s.season, Episode: s.episode}
		}
		out.Results = append(out.Results, r)
	}
	if cfg.Username == "" || cfg.Password == "" {
		out.Code = "CREDENTIALS_REQUIRED"
		return
	}
	u, err := url.Parse(cfg.Host)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		out.Status, out.Code = "failed", "INVALID_HOST"
		return
	}
	if cfg.AccessCode != "" {
		out.Code = "ACCESS_CODE_SESSION_REQUIRED"
		return
	}
	host := strings.TrimRight(cfg.Host, "/")
	if !strings.HasSuffix(host, "/v") {
		host += "/v"
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if syntheticClient != nil {
		client.Transport = syntheticClient.Transport
		out.Evidence = "synthetic_http_roundtrip"
	} else {
		out.Evidence = "live_fnos_provider_query"
	}
	client.Transport = logging.Transport(client.Transport)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	token := ""
	defer func() {
		secrets := []string{cfg.Username, cfg.Password, token}
		for i := range out.Results {
			r := &out.Results[i]
			for _, secret := range secrets {
				if secret == "" {
					continue
				}
				for _, text := range []string{r.Title, r.SourceID, r.Provider} {
					if strings.Contains(text, secret) {
						r.Status, r.Reason = "unverified", "sensitive_response_field"
						r.Title, r.SourceID, r.Provider = "", "", ""
					}
				}
				if r.TargetEpisode != nil && strings.Contains(r.TargetEpisode.Title, secret) {
					r.TargetEpisode.Status, r.TargetEpisode.Reason = "unverified", "sensitive_response_field"
					r.TargetEpisode.Title = ""
				}
			}
		}
		if out.Authenticated {
			out.ProviderIdentityComplete, out.EpisodeIdentityComplete = true, true
			for _, result := range out.Results {
				if result.Status != "ok" {
					out.ProviderIdentityComplete = false
				}
				if result.TargetEpisode != nil && result.TargetEpisode.Status != "ok" {
					out.EpisodeIdentityComplete = false
				}
			}
			out.Status = "partial_evidence"
			if out.ProviderIdentityComplete && out.EpisodeIdentityComplete {
				out.Status = "ok"
			}
		}
	}()
	request := func(path string, body any) (json.RawMessage, string) {
		if out.Requests >= 5 {
			return nil, "REQUEST_LIMIT"
		}
		method := http.MethodGet
		var payload []byte
		if body != nil {
			method = http.MethodPost
			payload, err = json.Marshal(body)
			if err != nil {
				return nil, "INVALID_BODY"
			}
		}
		n, err := rand.Int(rand.Reader, big.NewInt(900000))
		if err != nil {
			return nil, "NONCE_FAILED"
		}
		req, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "fnos", path), method, host+"/api/v1"+path, bytes.NewReader(payload))
		if err != nil {
			return nil, "INVALID_REQUEST"
		}
		req.Header.Set("authx", fnos.Signature("/v/api/v1"+path, string(payload), strconv.FormatInt(n.Int64()+100000, 10), strconv.FormatInt(time.Now().UnixMilli(), 10)))
		req.Header.Set("User-Agent", "light-media-m0/0.1")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Referer", host)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", token)
		}
		out.Requests++
		response, err := client.Do(req)
		if err != nil {
			return nil, "TRANSPORT_FAILED"
		}
		defer response.Body.Close()
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			return nil, "REDIRECT_BLOCKED"
		}
		if response.StatusCode != 200 {
			return nil, fmt.Sprintf("HTTP_%d", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, bodyLimit+1))
		if err != nil {
			return nil, "BODY_FAILED"
		}
		if len(data) > bodyLimit {
			return nil, "BODY_LIMIT"
		}
		var env envelope
		if !decode(data, &env) || env.Code == nil {
			return nil, "INVALID_ENVELOPE"
		}
		if *env.Code != 0 {
			return nil, fmt.Sprintf("BUSINESS_%d", *env.Code)
		}
		if len(env.Data) == 0 || bytes.Equal(bytes.TrimSpace(env.Data), []byte("null")) {
			return nil, "INVALID_DATA"
		}
		return env.Data, ""
	}
	_, status := request("/sys/version", nil)
	if status != "" {
		out.Status, out.Code = "failed", status
		return
	}
	data, status := request("/login", map[string]string{"username": cfg.Username, "password": cfg.Password, "app_name": "trimemedia-web"})
	if status != "" {
		out.Status, out.Code = "failed", status
		return
	}
	var auth struct {
		Token string `json:"token"`
	}
	if !decode(data, &auth) || auth.Token == "" || len(auth.Token) > 8192 || strings.ContainsAny(auth.Token, "\r\n") {
		out.Status, out.Code = "failed", "INVALID_TOKEN"
		return
	}
	token = auth.Token
	out.Authenticated = true
	for i, s := range fixed {
		data, status = request("/scrap/search", map[string]string{"source": "tmdb_id", "source_keyword": s.id, "lan": "zh-CN"})
		if status != "" {
			out.Results[i].Status, out.Results[i].Reason = "failed", status
			continue
		}
		out.Results[i] = inspect(data, s)
	}
	out.ProviderIdentityComplete = true
	out.EpisodeIdentityComplete = true
	for _, r := range out.Results {
		if r.Status != "ok" {
			out.ProviderIdentityComplete = false
		}
		if r.TargetEpisode != nil && r.TargetEpisode.Status != "ok" {
			out.EpisodeIdentityComplete = false
		}
	}
	out.Status = "partial_evidence"
	if out.ProviderIdentityComplete && out.EpisodeIdentityComplete {
		out.Status = "ok"
	}
	return
}

func decode(data []byte, out any) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	return d.Decode(out) == nil && d.Decode(new(any)) == io.EOF
}
func numeric(raw json.RawMessage) int64 {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return 0
		}
		text = n.String()
	}
	if !integerID.MatchString(text) {
		return 0
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0
	}
	return value
}
func providerID(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		if value := numeric(raw); value > 0 {
			return strconv.FormatInt(value, 10)
		}
		return ""
	}
	if !opaqueProviderID.MatchString(text) {
		return ""
	}
	return text
}
func date(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil || t.Year() < 1800 || t.Year() > 2199 {
		return ""
	}
	return t.Format("2006-01-02")
}
func inspect(data []byte, s Sample) Result {
	out := Result{Sample: s.label, Status: "unverified", RequestedSource: "tmdb_id", RequestedID: s.id}
	if s.season > 0 {
		out.TargetEpisode = &Episode{Status: "unverified", Reason: "standard_episode_id_not_returned", Season: s.season, Episode: s.episode}
	}
	var rows []json.RawMessage
	if !decode(data, &rows) || rows == nil || len(rows) > 100 {
		out.Status, out.Reason = "failed", "INVALID_SEARCH_ROWS"
		return out
	}
	out.ResponseCount = len(rows)
	matched := 0
	var candidate row
	var candidateRaw json.RawMessage
	for _, raw := range rows {
		var r row
		if !decode(raw, &r) {
			out.Status, out.Reason = "failed", "INVALID_SEARCH_ROW"
			return out
		}
		if r.Title == s.title && r.Type == s.kind {
			matched++
			candidate, candidateRaw = r, raw
		}
	}
	if matched != 1 {
		out.Reason = "exact_type_title_match_not_unique"
		return out
	}
	out.MatchedTitle, out.Type, out.Title = true, candidate.Type, candidate.Title
	if opaqueProviderID.MatchString(candidate.Source) {
		out.Provider = candidate.Source
	}
	out.SourceID = providerID(candidate.SourceID)
	explicit := numeric(candidate.TMDBID)
	standardSource := strings.EqualFold(candidate.Source, "tmdb") || strings.EqualFold(candidate.Source, "tmdb_id")
	var fields map[string]json.RawMessage
	decode(candidateRaw, &fields)
	for _, field := range allowedKnownFields {
		if _, ok := fields[field]; ok {
			out.KnownFields = append(out.KnownFields, field)
		}
	}
	sort.Strings(out.KnownFields)
	// Validate every supplied standard-ID assertion before choosing one. A
	// malformed or conflicting assertion is not permission to fall back to the
	// other field. A Trim source_id is a different opaque namespace.
	explicitPresent := len(candidate.TMDBID) != 0
	standardSourceIDPresent := standardSource && len(candidate.SourceID) != 0
	standardSourceID := numeric(candidate.SourceID)
	if explicitPresent && explicit == 0 {
		out.ProviderIDKind, out.Reason = "invalid_standard_id", "invalid_explicit_tmdb_id"
		return out
	}
	if standardSourceIDPresent && standardSourceID == 0 {
		out.ProviderIDKind, out.Reason = "invalid_standard_id", "invalid_tmdb_source_id"
		return out
	}
	if explicitPresent && standardSourceIDPresent && explicit != standardSourceID {
		out.ProviderIDKind, out.Reason = "conflicting_standard_ids", "standard_tmdb_id_conflict"
		return out
	}
	if explicitPresent {
		out.ExplicitTMDBID = explicit
		out.ProviderIDKind = "explicit_tmdb_id"
	} else if standardSourceIDPresent {
		explicit = standardSourceID
		out.ExplicitTMDBID = explicit
		out.ProviderIDKind = "explicit_tmdb_source_numeric_id"
	} else {
		out.ProviderIDKind = "opaque_or_unknown_provider_id"
	}
	d := candidate.ReleaseDate
	if s.kind == "TV" {
		d = candidate.FirstAirDate
	}
	if d == "" {
		d = candidate.AirDate
	}
	out.Date = date(d)
	if out.Date == "" {
		out.Reason = "standard_date_not_returned"
		return out
	}
	if s.year > 0 && !strings.HasPrefix(out.Date, strconv.Itoa(s.year)+"-") {
		out.Reason = "movie_year_mismatch"
		return out
	}
	if explicit <= 0 || strconv.FormatInt(explicit, 10) != s.id {
		out.Reason = "standard_tmdb_namespace_id_not_verified"
		return out
	}
	out.Status = "ok"
	if out.TargetEpisode != nil {
		all := append([]json.RawMessage{}, candidate.Episodes...)
		for _, season := range candidate.Seasons {
			if season.Number == s.season {
				all = append(all, season.Episodes...)
			}
		}
		matches := 0
		for _, raw := range all {
			var ep struct {
				TMDBID       json.RawMessage `json:"tmdb_id"`
				ParentTMDBID json.RawMessage `json:"show_id"`
				Season       int             `json:"season_number"`
				Episode      int             `json:"episode_number"`
				AirDate      string          `json:"air_date"`
				Title        string          `json:"title"`
				Name         string          `json:"name"`
			}
			if !decode(raw, &ep) || ep.Season != s.season || ep.Episode != s.episode {
				continue
			}
			matches++
			id, parent, air := numeric(ep.TMDBID), numeric(ep.ParentTMDBID), date(ep.AirDate)
			if id <= 0 || strconv.FormatInt(parent, 10) != s.id || air == "" {
				continue
			}
			title := ep.Title
			if title == "" {
				title = ep.Name
			}
			if title == "" || len(title) > 512 || strings.ContainsAny(title, "\r\n\x00") {
				continue
			}
			out.TargetEpisode = &Episode{Status: "ok", TMDBID: id, ParentTMDBID: parent, Season: s.season, Episode: s.episode, Date: air, Title: title}
		}
		if matches != 1 {
			out.TargetEpisode.Status, out.TargetEpisode.Reason = "unverified", "standard_episode_match_not_unique"
		}
	}
	return out
}
