package integrations

import (
	"context"
	"encoding/json"
	"light-media/internal/logging"
	"net/url"
	"regexp"
	"strings"
)

// These are explicit caller-selected identities, not domain guesses. HTML parsers
// are conservative M0 probes, not complete release/discount/download adapters.
var siteKinds = map[string]bool{"mteam": true, "hhanclub": true, "ubits": true, "jiayuan": true, "1ptba": true}
var logoutLink = regexp.MustCompile(`(?i)href\s*=\s*["'][^"']*(?:logout\.php|logout)[^"']*["']`)
var userLink = regexp.MustCompile(`(?i)href\s*=\s*["'][^"']*userdetails\.php\?id=\d+`)
var detailLink = regexp.MustCompile(`(?i)href\s*=\s*["'](?:[^"']*/)?details\.php\?id=(\d+)`)
var torrentTable = regexp.MustCompile(`(?i)<table\b[^>]*(?:class|id)\s*=\s*["'][^"']*\btorrents\b`)
var passwordInput = regexp.MustCompile(`(?i)<input\b[^>]*type\s*=\s*["']password["']`)
var tableTag = regexp.MustCompile(`(?i)</?table\b[^>]*>`)
var scalarID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func validMTeamID(b json.RawMessage) bool {
	var s string
	if json.Unmarshal(b, &s) == nil {
		return scalarID.MatchString(s)
	}
	var n json.Number
	return json.Unmarshal(b, &n) == nil && scalarID.MatchString(n.String())
}
func nexusResults(body []byte) []byte {
	start := torrentTable.FindIndex(body)
	if start == nil {
		return nil
	}
	tail := body[start[0]:]
	depth := 0
	for _, span := range tableTag.FindAllIndex(tail, -1) {
		if strings.HasPrefix(strings.ToLower(string(tail[span[0]:span[1]])), "</table") {
			depth--
		} else {
			depth++
		}
		if depth == 0 {
			return tail[:span[1]]
		}
	}
	return nil
}

var hhanInputTag = regexp.MustCompile(`(?i)<input\b[^>]*>`)
var hhanDivTag = regexp.MustCompile(`(?is)<\s*(/?)\s*div\b[^>]*>`)
var hhanAttribute = regexp.MustCompile(`(?i)(?:^|[[:space:]])(id|name|class)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

func pageState(b []byte) string {
	s := strings.ToLower(string(b))
	for _, marker := range []string{"cf-chl-", "challenge-platform", "just a moment...", "g-recaptcha", "h-captcha", "turnstile"} {
		if strings.Contains(s, marker) {
			return "login_challenge"
		}
	}
	if passwordInput.Match(b) {
		return "authentication_failed"
	}
	return ""
}
func ProbeSite(ctx context.Context, s Site) (r Result) {
	r = Result{ID: safe(s.ID), Kind: safe(s.Kind), Status: "unverified", Stage: "configuration", Evidence: "live_request"}
	ctx, r.TraceTargetID = logging.Target(ctx, "site")
	if !safeToken.MatchString(s.ID) || len(s.Query) > 256 {
		r.Status = "invalid_input"
		return
	}
	if !siteKinds[s.Kind] {
		r.Status = "unsupported_kind"
		return
	}
	t, status := newTransport(s.BaseURL)
	if status != "" {
		r.Status = status
		return
	}
	defer func() { r.Requests = t.count }()
	r.Capabilities = []Capability{{Name: "resolve_metainfo", ProtocolKnown: false}, {Name: "recent", ProtocolKnown: false}}
	if s.Kind == "mteam" {
		mteam(ctx, t, s, &r)
		return
	}
	if s.Cookie == "" {
		r.Status = "unverified"
		return
	}
	r.Protocol = "nexusphp_candidate"
	if s.Kind == "hhanclub" {
		r.Protocol = "hhanclub_html_candidate"
	}
	r.Stage = "authentication_check"
	body, _, status := t.request(ctx, "GET", t.endpoint("index.php"), nil, map[string]string{"Cookie": s.Cookie}, "", "")
	if p := pageState(body); p != "" {
		r.Status = p
		return
	}
	if status != "" {
		r.Status = status
		return
	}
	if !logoutLink.Match(body) || !userLink.Match(body) {
		r.Status = "protocol_changed"
		return
	}
	r.Capabilities = append(r.Capabilities, capRead("check"))
	if s.Query == "" {
		r.Status = "ok"
		r.Stage = "check_complete_search_unverified"
		return
	}
	r.Stage = "search_page"
	q := url.Values{"search": {s.Query}, "page": {"0"}, "search_area": {"0"}, "search_mode": {"0"}}
	if s.Kind == "hhanclub" {
		// This site's observed search form uses keyword, not Nexus' search.
		q = url.Values{"keyword": {s.Query}, "page": {"0"}}
	}
	body, _, status = t.request(ctx, "GET", t.endpoint("torrents.php")+"?"+q.Encode(), nil, map[string]string{"Cookie": s.Cookie}, "", "")
	if p := pageState(body); p != "" {
		r.Status = p
		return
	}
	if status != "" {
		r.Status = status
		return
	}
	if !logoutLink.Match(body) {
		r.Status = "protocol_changed"
		return
	}
	if s.Kind == "hhanclub" {
		count, status := hhanSearchShape(body)
		r.Status = status
		if status == "protocol_changed" {
			return
		}
		r.SampleCount = count
		r.Capabilities = append(r.Capabilities, capRead("search_page_shape"))
		r.Stage = "complete"
		return
	}
	if !torrentTable.Match(body) {
		r.Status = "protocol_changed"
		return
	}
	resultsBody := nexusResults(body)
	if resultsBody == nil {
		r.Status = "protocol_changed"
		return
	}
	matches := detailLink.FindAllSubmatch(resultsBody, -1)
	ids := map[string]bool{}
	for _, m := range matches {
		ids[string(m[1])] = true
	}
	r.SampleCount = len(ids)
	r.Capabilities = append(r.Capabilities, capRead("search_page_shape"))
	r.Status = "ok"
	r.Stage = "complete"
	if len(ids) == 0 { // A recognized table alone cannot establish semantic empty results.
		lower := strings.ToLower(string(resultsBody))
		if strings.Contains(lower, "没有找到") || strings.Contains(lower, "沒有找到") || strings.Contains(lower, "no torrents found") || strings.Contains(lower, "nothing found") {
			r.Status = "empty_result"
		} else {
			r.Status = "protocol_changed"
		}
	}
	return
}

// hhanSearchShape is a narrow M0 selector probe based on an authorized page
// shape, not a general HTML parser or complete torrent resource adapter.
func hhanSearchShape(body []byte) (int, string) {
	searchForm := false
	for _, tag := range hhanInputTag.FindAll(body, -1) {
		attrs := hhanAttributes(tag)
		if attrs["id"] == "searchinput" && attrs["name"] == "keyword" {
			searchForm = true
			break
		}
	}
	grids := hhanDivContents(body, "torrents-table")
	if !searchForm || len(grids) != 1 {
		return 0, "protocol_changed"
	}
	grid := grids[0]
	rows := hhanDivContents(grid, "torrent-table-sub-info")
	ids := map[string]bool{}
	if len(rows) == 0 {
		// A grid without rows is empty only when its own content says so.
		if detailLink.Match(grid) || len(hhanDivContents(grid, "torrent-table-for-spider-info")) != 0 || !hhanEmptyMarker(grid) {
			return 0, "protocol_changed"
		}
		return 0, "empty_result"
	}
	for _, row := range rows {
		info := hhanDivContents(row, "torrent-table-for-spider-info")
		if len(info) != 1 {
			return 0, "protocol_changed"
		}
		matches := detailLink.FindAllSubmatch(info[0], -1)
		if len(matches) == 0 {
			return 0, "protocol_changed"
		}
		for _, m := range matches {
			ids[string(m[1])] = true
		}
	}
	return len(ids), "ok"
}

func hhanEmptyMarker(body []byte) bool {
	lower := strings.ToLower(string(body))
	for _, marker := range []string{"没有找到", "沒有找到", "no torrents found", "nothing found"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func hhanAttributes(tag []byte) map[string]string {
	attrs := map[string]string{}
	for _, m := range hhanAttribute.FindAllSubmatch(tag, -1) {
		value := m[2]
		if value == nil {
			value = m[3]
		}
		attrs[strings.ToLower(string(m[1]))] = string(value)
	}
	return attrs
}

// Match exact class tokens and balanced div fragments so an unrelated details
// link elsewhere in the page cannot masquerade as a recognized result row.
func hhanDivContents(body []byte, class string) [][]byte {
	type frame struct {
		start   int
		matches bool
	}
	var stack []frame
	var contents [][]byte
	for _, indexes := range hhanDivTag.FindAllSubmatchIndex(body, -1) {
		closing := indexes[2] >= 0 && indexes[3] > indexes[2]
		if closing {
			if len(stack) == 0 {
				continue
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.matches {
				contents = append(contents, body[f.start:indexes[0]])
			}
			continue
		}
		match := false
		for _, token := range strings.Fields(hhanAttributes(body[indexes[0]:indexes[1]])["class"]) {
			if token == class {
				match = true
				break
			}
		}
		stack = append(stack, frame{indexes[1], match})
	}
	return contents
}

func mteam(ctx context.Context, t *transport, s Site, r *Result) {
	if s.APIKey == "" {
		r.Status = "unverified"
		return
	}
	r.Protocol = "mteam_api_candidate"
	r.Stage = "authentication_check"
	headers := map[string]string{"x-api-key": s.APIKey, "Content-Type": "application/json"}
	body, _, status := t.request(ctx, "POST", t.endpoint("api/member/profile"), []byte(`{}`), headers, "", "")
	if p := pageState(body); p != "" {
		r.Status = p
		return
	}
	if status != "" {
		r.Status = status
		return
	}
	data, status := mteamEnvelope(body)
	if status != "" {
		r.Status = status
		return
	}
	var profile map[string]json.RawMessage
	if json.Unmarshal(data, &profile) != nil || profile == nil {
		r.Status = "protocol_changed"
		return
	}
	if !validMTeamID(profile["id"]) {
		r.Status = "protocol_changed"
		return
	}
	r.Capabilities = append(r.Capabilities, capRead("check"))
	if s.Query == "" {
		r.Status = "ok"
		r.Stage = "check_complete_search_unverified"
		return
	}
	r.Stage = "search_page"
	payload, _ := json.Marshal(map[string]any{"keyword": s.Query, "pageNumber": 1, "pageSize": 10, "categories": []string{}, "visible": 1})
	body, _, status = t.request(ctx, "POST", t.endpoint("api/torrent/search"), payload, headers, "", "")
	if p := pageState(body); p != "" {
		r.Status = p
		return
	}
	if status != "" {
		r.Status = status
		return
	}
	data, status = mteamEnvelope(body)
	if status != "" {
		r.Status = status
		return
	}
	var result struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &result) != nil || result.Data == nil || len(result.Data) > 10 {
		r.Status = "protocol_changed"
		return
	}
	for _, item := range result.Data {
		var row map[string]json.RawMessage
		if json.Unmarshal(item, &row) != nil || row == nil {
			r.Status = "protocol_changed"
			return
		}
		if !validMTeamID(row["id"]) {
			r.Status = "protocol_changed"
			return
		}
	}
	r.SampleCount = len(result.Data)
	r.Capabilities = append(r.Capabilities, capRead("search_page_shape"))
	r.Status = "ok"
	r.Stage = "complete"
	if r.SampleCount == 0 {
		r.Status = "empty_result"
	}
}
func mteamEnvelope(body []byte) (json.RawMessage, string) {
	var env map[string]json.RawMessage
	if !decode(body, &env) || env == nil {
		return nil, "protocol_changed"
	}
	var code string
	if b, ok := env["code"]; !ok {
		return nil, "protocol_changed"
	} else if json.Unmarshal(b, &code) != nil {
		var n json.Number
		if json.Unmarshal(b, &n) != nil {
			return nil, "protocol_changed"
		}
		code = n.String()
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
	data, ok := env["data"]
	if !ok || string(data) == "null" {
		return nil, "protocol_changed"
	}
	return data, ""
}
