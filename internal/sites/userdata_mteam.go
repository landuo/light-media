package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type sourceMTeamBaseData struct {
	data, counts         map[string]any
	role                 *string
	joinAt               any
	uploaded, downloaded int64
}

// These assignments precede message and seeding requests in MTorrent.parse.
// String/float schema validation occurs later, when Indexer builds the DTO.
func sourceMTeamBase(root map[string]any) (sourceMTeamBaseData, error) {
	b := sourceMTeamBaseData{}
	var ok bool
	b.data, ok = root["data"].(map[string]any)
	if !ok {
		if value, exists := root["data"]; exists {
			if value == nil {
				return b, &sourceUserParserFailure{kind: "mt-null-data"}
			}
			return b, sourceModuleError(nil)
		}
		b.data = map[string]any{}
	}
	role := b.data["role"]
	if !sourceTruth(role) {
		role = "1"
	}
	if !sourceHashable(role) {
		return b, sourceModuleError(nil)
	}
	if r, ok := role.(string); ok {
		if value, exists := mteamUserRoles[r]; exists {
			b.role = &value
		}
	}
	status := map[string]any{}
	if v, exists := b.data["memberStatus"]; exists {
		status, ok = v.(map[string]any)
		if !ok {
			return b, sourceModuleError(nil)
		}
	}
	b.joinAt = status["createdDate"]
	b.counts = map[string]any{}
	if v, exists := b.data["memberCount"]; exists {
		b.counts, ok = v.(map[string]any)
		if !ok {
			return b, sourceModuleError(nil)
		}
	}
	var err error
	if b.uploaded, err = sourceUserInteger(b.counts["uploaded"]); err != nil {
		if _, format := err.(*sourceUserIntegerFormatError); format {
			return b, &sourceUserParserFailure{kind: "mt-invalid-integer"}
		}
		return b, err
	}
	if b.downloaded, err = sourceUserInteger(b.counts["downloaded"]); err != nil {
		if _, format := err.(*sourceUserIntegerFormatError); format {
			return b, &sourceUserParserFailure{kind: "mt-invalid-integer"}
		}
		return b, err
	}
	return b, nil
}

type sourceMTeamReader struct {
	c            *Client
	ctx          context.Context
	s            *UserStats
	primaryError error
	requests     int
}

// The installed base reader selects POST only for nonempty params, and uses
// JSON only for the seeding page's explicit Content-Type. Failed HTTP pages
// become empty text. Admission, cancellation and our byte limit stay fatal.
func (r *sourceMTeamReader) page(endpoint string, params map[string]any, jsonBody bool) ([]byte, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	h := map[string]string{"x-api-key": r.c.config.APIKey, "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8"}
	method := http.MethodGet
	var body []byte
	if jsonBody {
		h["Content-Type"] = "application/json"
		h["Accept"] = "application/json, text/plain, */*"
	}
	if len(params) > 0 {
		method = http.MethodPost
		if jsonBody {
			var e error
			body, e = json.Marshal(params)
			if e != nil {
				return nil, sourceModuleError(nil)
			}
		} else {
			form := url.Values{}
			for k, v := range params {
				if v != nil {
					form.Set(k, sourceString(v))
				}
			}
			body = []byte(form.Encode())
		}
	}
	r.requests++
	b, _, err := r.c.request(r.ctx, method, r.c.endpoint(endpoint), body, h, htmlLimit)
	if err != nil {
		if r.ctx.Err() != nil {
			return nil, r.ctx.Err()
		}
		if t := sourceStatsTrace(r.ctx); t != nil && t.guardError != nil {
			return nil, t.guardError
		}
		if err.Error() == "site_body_limit" {
			return nil, err
		}
		if r.requests == 1 {
			r.primaryError = err
		}
		r.s.Warnings = append(r.s.Warnings, endpoint+":"+err.Error())
		return nil, nil
	}
	if jsonBody && len(b) > 0 {
		var value any
		if !decodeJSON(b, &value) {
			r.s.Warnings = append(r.s.Warnings, endpoint+":site_protocol_changed")
			return nil, nil
		}
	} else if sourceAccountChallenge(b) {
		if r.requests == 1 {
			r.primaryError = errors.New("site_login_challenge")
		}
		r.s.Warnings = append(r.s.Warnings, endpoint+":site_login_challenge")
		return nil, nil
	}
	return b, nil
}

func sourceMTeamRoot(raw []byte) (map[string]any, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decodeErr := decoder.Decode(&value)
	if decodeErr == nil {
		decodeErr = decoder.Decode(new(any))
		if decodeErr == io.EOF {
			decodeErr = nil
		} else if decodeErr == nil {
			return nil, false, &sourceUserParserFailure{kind: "mt-invalid-json"}
		}
	}
	if decodeErr != nil {
		// encoding/json's nesting cap is a Go safety limit, not evidence that
		// the installed module raised its own JSON parsing exception.
		if syntax, ok := decodeErr.(*json.SyntaxError); ok && strings.HasSuffix(syntax.Error(), "exceeded max depth") {
			return nil, false, sourceModuleError(nil)
		}
		return nil, false, &sourceUserParserFailure{kind: "mt-invalid-json"}
	}
	if !sourceTruth(value) {
		return nil, false, nil
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, false, sourceModuleError(nil)
	}
	code, ok := m["code"].(string)
	return m, ok && code == "0", nil
}

func sourceMTeamMap(parent map[string]any, key string, orEmpty bool) (map[string]any, error) {
	v, exists := parent[key]
	if !exists || (orEmpty && !sourceTruth(v)) {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, sourceModuleError(nil)
	}
	return m, nil
}

func (c *Client) sourceMTeamUserStats(ctx context.Context, options UserStatsOptions) (s UserStats, dto *SourceUserData, err error) {
	s = UserStats{CapturedAt: time.Now().UTC().Format(time.RFC3339), Sources: map[string]string{}, Warnings: []string{}}
	defer func() {
		if err != nil {
			message := err.Error()
			s.Error = &message
		}
	}()
	// SiteParserBase pins account requests to 60 seconds, independent of the
	// native search/connection timeout. Keep the caller's cancellation context.
	client := *c
	client.config.UserAgent = "" // The original API-key reader does not forward the configured cookie UA.
	httpClient := *c.http
	httpClient.Timeout = 60 * time.Second
	client.http = &httpClient
	r := sourceMTeamReader{c: &client, ctx: ctx, s: &s}
	dto = emptySourceUserData()
	raw, e := r.page("api/member/profile", map[string]any{"uid": nil}, false)
	if e != nil {
		return s, nil, e
	}
	profile, parsed, e := sourceMTeamRoot(raw)
	if e != nil {
		return s, nil, recordSourceUserParserFailure(ctx, e)
	}
	var params map[string]any
	var base sourceMTeamBaseData
	if parsed {
		base, e = sourceMTeamBase(profile)
		if e != nil {
			return s, nil, recordSourceUserParserFailure(ctx, e)
		}
		observeSourceMTeamProfile(&s, base)
		params = map[string]any{"pageNumber": 1, "pageSize": 200, "type": "SEEDING", "userid": base.data["id"]}
		if options.ReadMessages {
			if e = r.messages(dto); e != nil {
				return s, nil, e
			}
		}
	}
	if e = r.seeding(dto, params); e != nil {
		return s, nil, e
	}
	if parsed {
		if e = sourceMTeamProfile(dto, profile); e != nil {
			return s, nil, e
		}
	}
	if r.primaryError != nil {
		return s, dto, r.primaryError
	}
	if !numericID.MatchString(s.UserID) || s.Username == "" {
		return s, dto, errors.New("site_user_identity_missing")
	}
	return s, dto, nil
}

func observeSourceMTeamProfile(s *UserStats, b sourceMTeamBaseData) {
	s.UserID = stringValue(b.data["id"])
	s.Username = statsText(stringValue(b.data["username"]), 128)
	if s.UserID != "" {
		s.Sources["user_id"] = "api/member/profile"
	}
	if s.Username != "" {
		s.Sources["username"] = "api/member/profile"
	}
	if b.role != nil && sourceTruth(b.data["role"]) {
		s.Level = *b.role
		s.Sources["level"] = "api/member/profile:role"
	}
	if text, ok := b.joinAt.(string); ok && text != "" {
		s.JoinedAt = statsText(text, 80)
		s.Sources["joined_at"] = "api/member/profile:memberStatus.createdDate"
	}
	for key, dst := range map[string]**int64{"uploaded": &s.Uploaded, "downloaded": &s.Downloaded} {
		v := b.counts[key]
		switch v.(type) {
		case string, json.Number:
		default:
			continue
		}
		if stringValue(v) == "" {
			continue
		}
		if n, e := sourceUserInteger(v); e == nil && n >= 0 {
			*dst = &n
			s.Sources[key] = "api/member/profile:memberCount." + key
		}
	}
	for key, dst := range map[string]**float64{"shareRate": &s.Ratio, "bonus": &s.Bonus} {
		v := b.counts[key]
		switch v.(type) {
		case string, json.Number:
		default:
			continue
		}
		if stringValue(v) == "" {
			continue
		}
		if n, e := sourceFloat(v); e == nil && n >= 0 {
			*dst = &n
			s.Sources[map[string]string{"shareRate": "ratio", "bonus": "bonus"}[key]] = "api/member/profile:memberCount." + key
		}
	}
}

func (r *sourceMTeamReader) messages(dto *SourceUserData) error {
	raw, e := r.page("api/msg/search", map[string]any{"keyword": "", "box": "-2", "type": "pageNumber", "pageSize": 100}, false)
	if e != nil {
		return e
	}
	m, ok, e := sourceMTeamRoot(raw)
	if e != nil {
		return recordSourceUserParserFailure(r.ctx, e)
	}
	if !ok {
		return nil
	}
	data, e := sourceMTeamMap(m, "data", false)
	if e != nil {
		return e
	}
	values, exists := data["data"]
	if !exists {
		values = []any{}
	}
	rows, ok := values.([]any)
	nativeList := ok
	if !ok {
		// Iterating an empty dict/string does not raise in the original method.
		switch v := values.(type) {
		case map[string]any:
			ok = len(v) == 0
		case string:
			ok = v == ""
		}
		if !ok {
			return sourceModuleError(nil)
		}
		rows = []any{}
	}
	if len(rows) > 10000 {
		return errors.New("site_message_contents_incomplete")
	}
	if nativeList {
		r.s.Messages = [][3]*string{}
	}
	allNative := nativeList
	for _, v := range rows {
		message, ok := v.(map[string]any)
		if !ok {
			return sourceModuleError(nil)
		}
		if !sourceTruth(message["unread"]) || !sourceTruth(message["title"]) || !sourceTruth(message["createdDate"]) || !sourceTruth(message["context"]) {
			continue
		}
		row := []any{message["title"], message["createdDate"], message["context"]}
		dto.Messages = append(dto.Messages, row)
		var observed [3]*string
		valid := true
		for i, v := range row {
			s, ok := v.(string)
			if !ok {
				valid = false
				break
			}
			observed[i] = &s
		}
		if valid && allNative {
			r.s.Messages = append(r.s.Messages, observed)
		} else if !valid {
			allNative = false
			r.s.Messages = nil
			r.s.Warnings = append(r.s.Warnings, "site_message_native_type_unknown")
		}
		// MTorrent intentionally ignores the markRead response body/status.
		if _, e = r.page("api/msg/markRead", map[string]any{"msgId": message["id"]}, false); e != nil {
			return e
		}
	}
	r.s.Sources["message_unread_contents"] = "api/msg/search:source unread title,createdDate,context"
	return nil
}

func (r *sourceMTeamReader) seeding(dto *SourceUserData, params map[string]any) error {
	for page := 0; page < 50; page++ {
		raw, e := r.page("api/member/getUserTorrentList", params, true)
		if e != nil {
			return e
		}
		m, ok, e := sourceMTeamRoot(raw)
		if e != nil {
			return recordSourceUserParserFailure(r.ctx, e)
		}
		if !ok {
			return nil
		}
		data, e := sourceMTeamMap(m, "data", true)
		if e != nil {
			return nil
		}
		values := data["data"]
		if !sourceTruth(values) {
			values = []any{}
		}
		rows, ok := values.([]any)
		if !ok {
			return nil
		}
		var size, count int64
		info := [][2]int64{}
		for _, v := range rows {
			row, ok := v.(map[string]any)
			if !ok {
				continue
			}
			torrent, ok := row["torrent"].(map[string]any)
			if !ok {
				continue
			}
			n, e := sourceUserInteger(torrent["size"])
			if e != nil {
				return e
			}
			peers, e := sourceUserInteger(torrent["source"])
			if e != nil {
				return e
			}
			if n > 0 && size > math.MaxInt64-n || n < 0 && size < math.MinInt64-n {
				return errors.New("site_user_integer_limit")
			}
			size += n
			count++
			info = append(info, [2]int64{peers, n})
		}
		if size > 0 && dto.SeedingSize > math.MaxInt64-size || size < 0 && dto.SeedingSize < math.MinInt64-size {
			return errors.New("site_user_integer_limit")
		}
		dto.Seeding += count
		dto.SeedingSize += size
		dto.SeedingInfo = append(dto.SeedingInfo, info...)
		peerRaw, e := r.page("api/tracker/myPeerStatus", map[string]any{"uid": sourceMTeamUserID(params)}, false)
		if e != nil {
			return e
		}
		peer, known := sourceMTeamPeerCount(peerRaw)
		if peer.Sign() == 0 || peer.Cmp(big.NewInt(dto.Seeding)) <= 0 {
			nonnegative := dto.SeedingSize >= 0
			for _, pair := range dto.SeedingInfo {
				if pair[0] < 0 || pair[1] < 0 {
					nonnegative = false
				}
			}
			if known && nonnegative {
				n, size := dto.Seeding, dto.SeedingSize
				r.s.Seeding = &n
				r.s.SeedingBytes = &size
				r.s.SeedingInfo = append([][2]int64{}, dto.SeedingInfo...)
				r.s.Sources["seeding"], r.s.Sources["seeding_bytes"], r.s.Sources["seeding_info"] = "api/member/getUserTorrentList:source pages", "api/member/getUserTorrentList:source pages", "api/member/getUserTorrentList:torrent.source,size/source pages"
			}
			return nil
		}
		if params == nil {
			return sourceModuleError(nil)
		} // The original next-page assignment also fails here.
		params["pageNumber"] = page + 2
	}
	return errors.New("site_account_pagination_limit")
}

func sourceMTeamUserID(params map[string]any) any {
	if params == nil {
		return nil
	}
	return params["userid"]
}

func sourceMTeamPeerCount(raw []byte) (*big.Int, bool) {
	zero := big.NewInt(0)
	if len(raw) == 0 {
		return zero, false
	}
	var m map[string]any
	if !decodeJSON(raw, &m) || m == nil {
		return zero, false
	}
	data, e := sourceMTeamMap(m, "data", false)
	if e != nil {
		return zero, false
	}
	v := data["seeder"]
	if !sourceTruth(v) {
		_, numeric := v.(json.Number)
		return zero, numeric
	}
	n, e := sourceInteger(v)
	if e != nil {
		return zero, false
	}
	return n, true
}

func sourceAccountChallenge(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	doc, e := html.Parse(strings.NewReader(string(raw)))
	if e != nil {
		return false
	}
	for _, n := range find(doc, func(n *html.Node) bool { return n.Type == html.ElementNode }) {
		if n.Data == "title" {
			v := strings.Join(strings.Fields(messageNodeString(n)), " ")
			for _, title := range []string{"Just a moment...", "请稍候…", "DDOS-GUARD"} {
				if strings.EqualFold(v, title) {
					return true
				}
			}
		}
		switch attribute(n, "id") {
		case "cf-challenge-running", "cf-please-wait", "challenge-spinner", "trk_jschal_js":
			return true
		}
		if hasClass(n, "ray_id") || hasClass(n, "attack-box") {
			return true
		}
		if attribute(n, "id") == "js_info" {
			for p := n.Parent; p != nil; p = p.Parent {
				if p.Data == "td" && hasClass(p, "info") {
					return true
				}
			}
		}
		if n.Data == "h2" {
			for p := n.Parent; p != nil; p = p.Parent {
				if p.Data == "div" && hasClass(p, "text-box") {
					for a := p.Parent; a != nil; a = a.Parent {
						if a.Data == "div" && hasClass(a, "vc") {
							return true
						}
					}
				}
			}
		}
	}
	return false
}
