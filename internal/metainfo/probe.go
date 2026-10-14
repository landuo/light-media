package metainfo

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"light-media/internal/integrations"
)

type Result struct {
	ID                         string   `json:"id"`
	Kind                       string   `json:"kind"`
	Evidence                   string   `json:"evidence"`
	Status                     string   `json:"status"`
	Stage                      string   `json:"stage"`
	Reason                     string   `json:"reason,omitempty"`
	Requests                   int      `json:"requests"`
	ControlledRedirects        int      `json:"controlled_redirects"`
	ProtectedMaterialAvailable bool     `json:"protected_material_available"`
	Summary                    *Summary `json:"summary,omitempty"`
}
type Report struct {
	Status  string   `json:"status"`
	Results []Result `json:"results"`
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var numericID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var boundedPasskey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var anchor = regexp.MustCompile(`(?is)<a\b[^>]*[[:space:]]href\s*=\s*(?:"([^"]*)"|'([^']*)')[^>]*>`)
var password = regexp.MustCompile(`(?i)<input\b[^>]*type\s*=\s*["']password["']`)
var logout = regexp.MustCompile(`(?i)href\s*=\s*["'][^"']*logout\.php[^"']*["']`)
var inputTag = regexp.MustCompile(`(?i)<input\b[^>]*>`)
var attr = regexp.MustCompile(`(?i)(?:^|[[:space:]])(id|name|class)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

func Run(ctx context.Context, in integrations.Input) Report {
	out := Report{Status: "ok", Results: []Result{}}
	if len(in.Sites) > 5 {
		out.Status = "invalid_input"
		return out
	}
	seen := map[string]bool{}
	for _, s := range in.Sites {
		if seen[s.ID] || !safeID.MatchString(s.ID) {
			out.Status = "invalid_input"
			return out
		}
		seen[s.ID] = true
	}
	for _, s := range in.Sites {
		r := ProbeSite(ctx, s)
		out.Results = append(out.Results, r)
		if r.Status != "ok" {
			out.Status = "incomplete"
		}
	}
	if len(out.Results) == 0 {
		out.Status = "unverified"
	}
	return out
}

func ProbeSite(ctx context.Context, s integrations.Site) Result { return probe(ctx, s, nil) }

func probe(ctx context.Context, s integrations.Site, syntheticClient *http.Client) (r Result) {
	r = Result{ID: "invalid_id", Kind: "unsupported", Evidence: "live_request", Status: "unverified", Stage: "configuration"}
	if safeID.MatchString(s.ID) {
		r.ID = s.ID
	}
	switch s.Kind {
	case "mteam", "hhanclub", "ubits", "jiayuan", "1ptba":
		r.Kind = s.Kind
	default:
		r.Status = "unsupported_kind"
		return
	}
	if !safeID.MatchString(s.ID) || len(s.Query) > 256 {
		r.Status = "invalid_input"
		return
	}
	if s.Query == "" {
		r.Reason = "search_query_required"
		return
	}
	base, err := url.Parse(s.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		r.Status = "invalid_input"
		return
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if syntheticClient != nil {
		client.Transport = syntheticClient.Transport
	}
	maxRequests := 2
	if s.Kind == "mteam" {
		maxRequests = 4
	}
	lastLocation, lastRedirectStatus := "", 0
	fetch := func(method string, u *url.URL, data []byte, headers map[string]string, limit int) ([]byte, string) {
		lastLocation, lastRedirectStatus = "", 0
		if r.Requests >= maxRequests {
			return nil, "request_limit"
		}
		r.Requests++
		req, e := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(data))
		if e != nil {
			return nil, "invalid_input"
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		req.Header.Set("User-Agent", "light-media-M0/0.1")
		response, e := client.Do(req)
		if e != nil {
			return nil, "transport_error"
		}
		defer response.Body.Close()
		body, e := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
		if e != nil {
			return nil, "transport_error"
		}
		if len(body) > limit {
			return nil, "body_limit"
		}
		status := ""
		switch {
		case response.StatusCode == 401:
			status = "authentication_failed"
		case response.StatusCode == 403:
			status = "forbidden"
		case response.StatusCode == 429:
			status = "rate_limited"
		case response.StatusCode >= 300 && response.StatusCode < 400:
			status = "redirect_blocked"
			lastLocation, lastRedirectStatus = response.Header.Get("Location"), response.StatusCode
		case response.StatusCode < 200 || response.StatusCode >= 300:
			status = "http_error"
		}
		// Do not interpret a valid bencoded filename/tracker as an HTML challenge.
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) > 0 && trimmed[0] == '<' {
			if state := pageState(body); state != "" {
				return nil, state
			}
		}
		return body, status
	}
	endpoint := func(path string) *url.URL {
		u := *base
		u.Path = strings.TrimRight(u.Path, "/") + "/" + path
		return &u
	}
	var downloadURL *url.URL
	var downloadHeaders map[string]string
	r.Stage = "search_page"
	if s.Kind == "mteam" {
		if s.APIKey == "" {
			r.Reason = "api_key_required"
			return
		}
		if !allowedMTeamAPI(base) {
			r.Status = "unverified"
			r.Reason = "api_origin_not_allowlisted"
			return
		}
		headers := map[string]string{"x-api-key": s.APIKey, "Content-Type": "application/json"}
		payload, _ := json.Marshal(map[string]any{"keyword": s.Query, "pageNumber": 1, "pageSize": 10, "categories": []string{}, "visible": 1})
		body, status := fetch(http.MethodPost, endpoint("api/torrent/search"), payload, headers, 1<<20)
		if status != "" {
			r.Status = status
			return
		}
		data, status := envelope(body)
		if status != "" {
			r.Status = status
			return
		}
		var list struct {
			Data []json.RawMessage `json:"data"`
		}
		if !decode(data, &list) || list.Data == nil || len(list.Data) > 10 {
			r.Status = "protocol_changed"
			return
		}
		if len(list.Data) == 0 {
			r.Status = "empty_result"
			r.Reason = "no_candidate_material_unverified"
			return
		}
		var first struct {
			ID json.RawMessage `json:"id"`
		}
		if !decode(list.Data[0], &first) {
			r.Status = "protocol_changed"
			return
		}
		id := jsonID(first.ID)
		if id == "" {
			r.Status = "protocol_changed"
			return
		}
		r.Stage = "download_token"
		headers = map[string]string{"x-api-key": s.APIKey, "Content-Type": "application/x-www-form-urlencoded"}
		body, status = fetch(http.MethodPost, endpoint("api/torrent/genDlToken"), []byte(url.Values{"id": {id}}.Encode()), headers, 1<<20)
		if status != "" {
			r.Status = status
			return
		}
		data, status = envelope(body)
		if status != "" {
			r.Status = status
			return
		}
		var raw string
		if !decode(data, &raw) || len(raw) > 8192 {
			r.Status = "protocol_changed"
			return
		}
		downloadURL, err = url.Parse(raw)
		if err != nil || !allowedMTeamMaterialURL(downloadURL) || downloadURL.Fragment != "" || downloadURL.User != nil {
			r.Status = "unverified"
			r.Reason = "download_origin_not_allowlisted"
			return
		}
		// No jar, API key, Cookie, Authorization or Referer on token URL requests.
		downloadHeaders = nil
	} else {
		if s.Cookie == "" {
			r.Reason = "cookie_required"
			return
		}
		u := endpoint("torrents.php")
		q := url.Values{"search": {s.Query}, "page": {"0"}, "search_area": {"0"}, "search_mode": {"0"}}
		if s.Kind == "hhanclub" {
			q = url.Values{"keyword": {s.Query}, "page": {"0"}}
		}
		u.RawQuery = q.Encode()
		body, status := fetch(http.MethodGet, u, nil, map[string]string{"Cookie": s.Cookie}, 1<<20)
		if status != "" {
			r.Status = status
			return
		}
		if !logout.Match(body) {
			r.Status = "protocol_changed"
			return
		}
		fragment, status := searchFragment(body, s.Kind)
		if status != "" {
			r.Status = status
			return
		}
		downloadURL, status = firstDownload(fragment, base, u, s.Kind)
		if status != "" {
			r.Status = status
			r.Reason = "explicit_safe_first_candidate_download_unavailable"
			return
		}
		downloadHeaders = map[string]string{"Cookie": s.Cookie}
	}
	r.Stage = "metainfo_fetch"
	body, status := fetch(http.MethodGet, downloadURL, nil, downloadHeaders, MaxBody)
	if s.Kind == "mteam" && status == "redirect_blocked" {
		// Exactly one manually validated redirect in the credential-free material
		// stage. The HTTP client still refuses all automatic redirects, including
		// API redirects. Never propagate headers or cookies from either response.
		if lastRedirectStatus != 301 && lastRedirectStatus != 302 && lastRedirectStatus != 303 && lastRedirectStatus != 307 && lastRedirectStatus != 308 {
			r.Status, r.Reason = "redirect_blocked", "unsupported_redirect_status"
			return
		}
		if lastLocation == "" || len(lastLocation) > 8192 {
			r.Status, r.Reason = "redirect_blocked", "invalid_redirect_location"
			return
		}
		reference, parseErr := url.Parse(lastLocation)
		if parseErr != nil {
			r.Status, r.Reason = "redirect_blocked", "invalid_redirect_location"
			return
		}
		next := downloadURL.ResolveReference(reference)
		if !allowedMTeamMaterialURL(next) || next.Fragment != "" {
			r.Status, r.Reason = "redirect_blocked", "redirect_origin_not_allowlisted"
			return
		}
		r.Stage, r.ControlledRedirects = "metainfo_redirect_fetch", 1
		body, status = fetch(http.MethodGet, next, nil, nil, MaxBody)
		if status == "redirect_blocked" {
			r.Reason = "redirect_limit"
		}
	}
	if status != "" {
		r.Status = status
		return
	}
	r.Stage = "bencode_info_validation"
	summary, err := Inspect(body)
	if err != nil {
		r.Status = "invalid_metainfo"
		r.Reason = err.Error()
		return
	}
	r.Summary = &summary
	r.Status, r.Stage, r.ProtectedMaterialAvailable = "ok", "complete", true
	return
}

func allowedMTeamURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.Hostname() == "" || (u.Port() != "" && u.Port() != "443") || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, root := range []string{"m-team.cc", "m-team.io"} {
		if host == root || strings.HasSuffix(host, "."+root) {
			return true
		}
	}
	return false
}
func allowedMTeamAPI(u *url.URL) bool {
	if !allowedMTeamURL(u) {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.m-team.cc" || host == "api.m-team.io" || host == "test2.m-team.cc"
}

// This exact material CDN was supplied by the authorized live MTeam API's
// redirect. It is not a general halomt domain allowance or an API endpoint.
func allowedMTeamMaterialURL(u *url.URL) bool {
	if allowedMTeamURL(u) {
		return true
	}
	return u != nil && u.Scheme == "https" && u.User == nil && (u.Port() == "" || u.Port() == "443") && strings.EqualFold(u.Hostname(), "fr1.halomt.com")
}
func pageState(b []byte) string {
	lower := strings.ToLower(string(b))
	for _, marker := range []string{"cf-chl-", "challenge-platform", "just a moment...", "g-recaptcha", "h-captcha", "turnstile"} {
		if strings.Contains(lower, marker) {
			return "login_challenge"
		}
	}
	if password.Match(b) {
		return "authentication_failed"
	}
	return ""
}
func decode(b []byte, out any) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	return d.Decode(out) == nil && d.Decode(new(any)) == io.EOF
}
func jsonID(b []byte) string {
	var s string
	if json.Unmarshal(b, &s) != nil {
		var n json.Number
		if json.Unmarshal(b, &n) != nil {
			return ""
		}
		s = n.String()
	}
	if !numericID.MatchString(s) {
		return ""
	}
	return s
}
func envelope(b []byte) (json.RawMessage, string) {
	var m map[string]json.RawMessage
	if !decode(b, &m) || m == nil {
		return nil, "protocol_changed"
	}
	var code string
	if json.Unmarshal(m["code"], &code) != nil {
		var number json.Number
		if json.Unmarshal(m["code"], &number) != nil {
			return nil, "protocol_changed"
		}
		code = number.String()
	}
	if code == "" || len(code) > 12 {
		return nil, "protocol_changed"
	}
	switch code {
	case "0":
	case "401", "401000", "-401":
		return nil, "authentication_failed"
	case "403":
		return nil, "forbidden"
	case "429":
		return nil, "rate_limited"
	default:
		return nil, "business_error"
	}
	data, ok := m["data"]
	if !ok || bytes.Equal(data, []byte("null")) {
		return nil, "protocol_changed"
	}
	return data, ""
}

func attributes(tag []byte) map[string]string {
	out := map[string]string{}
	for _, m := range attr.FindAllSubmatch(tag, -1) {
		v := m[2]
		if v == nil {
			v = m[3]
		}
		out[strings.ToLower(string(m[1]))] = string(v)
	}
	return out
}
func classContents(body []byte, tag, class string, allowID bool) [][]byte {
	re := regexp.MustCompile(`(?is)<\s*(/?)\s*` + tag + `\b[^>]*>`)
	type frame struct {
		start    int
		selected bool
	}
	var stack []frame
	var out [][]byte
	for _, m := range re.FindAllSubmatchIndex(body, -1) {
		if m[2] >= 0 && m[3] > m[2] {
			if len(stack) == 0 {
				continue
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.selected {
				out = append(out, body[f.start:m[0]])
			}
			continue
		}
		a := attributes(body[m[0]:m[1]])
		selected := allowID && a["id"] == class
		for _, token := range strings.Fields(a["class"]) {
			if token == class {
				selected = true
			}
		}
		stack = append(stack, frame{m[1], selected})
	}
	return out
}
func emptyMarker(b []byte) bool {
	s := strings.ToLower(string(b))
	for _, m := range []string{"没有找到", "沒有找到", "no torrents found", "nothing found"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
func searchFragment(body []byte, kind string) ([]byte, string) {
	if kind != "hhanclub" {
		fragments := classContents(body, "table", "torrents", true)
		if len(fragments) != 1 {
			return nil, "protocol_changed"
		}
		if len(anchor.FindAll(fragments[0], -1)) == 0 && emptyMarker(fragments[0]) {
			return nil, "empty_result"
		}
		return fragments[0], ""
	}
	input := false
	for _, tag := range inputTag.FindAll(body, -1) {
		a := attributes(tag)
		if a["id"] == "searchinput" && a["name"] == "keyword" {
			input = true
		}
	}
	grid := classContents(body, "div", "torrents-table", false)
	if !input || len(grid) != 1 {
		return nil, "protocol_changed"
	}
	rows := classContents(grid[0], "div", "torrent-table-sub-info", false)
	if len(rows) == 0 {
		if emptyMarker(grid[0]) && len(anchor.FindAll(grid[0], -1)) == 0 {
			return nil, "empty_result"
		}
		return nil, "protocol_changed"
	}
	// classContents closes inner-first; sibling rows retain source order.
	if len(classContents(rows[0], "div", "torrent-table-for-spider-info", false)) != 1 {
		return nil, "protocol_changed"
	}
	return rows[0], ""
}

func firstDownload(fragment []byte, base, page *url.URL, kind string) (*url.URL, string) {
	var links []*url.URL
	firstID := ""
	for _, m := range anchor.FindAllSubmatch(fragment, -1) {
		href := m[1]
		if href == nil {
			href = m[2]
		}
		u, err := url.Parse(html.UnescapeString(string(href)))
		if err != nil {
			continue
		}
		u = page.ResolveReference(u)
		if u.User != nil || u.Fragment != "" {
			continue
		}
		if strings.HasSuffix(u.Path, "/details.php") && firstID == "" && u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) && numericID.MatchString(u.Query().Get("id")) && len(u.Query()["id"]) == 1 {
			firstID = u.Query().Get("id")
		}
		links = append(links, u)
	}
	if firstID == "" {
		return nil, "protocol_changed"
	}
	for _, u := range links {
		if !strings.HasSuffix(u.Path, "/download.php") || u.Query().Get("id") != firstID {
			continue
		}
		if u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) {
			return nil, "unverified"
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(q["id"]) != 1 || !numericID.MatchString(q.Get("id")) {
			return nil, "unverified"
		}
		if kind == "hhanclub" {
			// Explicit observed HHan link shape. Keep the passkey only in memory
			// and only send it to the configured HTTPS origin; never invent it.
			if u.Scheme != "https" || len(q) != 2 || len(q["passkey"]) != 1 || !boundedPasskey.MatchString(q.Get("passkey")) {
				return nil, "unverified"
			}
		} else if len(q) != 1 {
			return nil, "unverified"
		}
		return u, ""
	}
	return nil, "unverified"
}
