package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// SourceUserData is the MoviePilot DTO, distinct from observed native values.
// Its constructor defaults are protocol values, never evidence of measured zero.
type SourceUserData struct {
	Domain        string       `json:"domain"`
	Username      *string      `json:"username"`
	UserID        any          `json:"userid"`
	UserLevel     *string      `json:"user_level"`
	JoinAt        *string      `json:"join_at"`
	Bonus         float64      `json:"bonus"`
	Upload        int64        `json:"upload"`
	Download      int64        `json:"download"`
	Ratio         float64      `json:"ratio"`
	Seeding       int64        `json:"seeding"`
	Leeching      int64        `json:"leeching"`
	SeedingSize   int64        `json:"seeding_size"`
	LeechingSize  int64        `json:"leeching_size"`
	SeedingInfo   [][2]int64   `json:"seeding_info"`
	MessageUnread int64        `json:"message_unread"`
	Messages      [][3]*string `json:"message_unread_contents"`
	Error         *string      `json:"err_msg"`
	Day           string       `json:"updated_day"`
	Time          *string      `json:"updated_time"`
}

type sourceUserStatsKey struct{}
type sourceUserStatsTrace struct {
	guardError   error
	attempted    bool
	indexParsed  bool
	none         bool
	profileSeen  bool
	profileEmpty bool
	profile      map[string]any
}

func sourceStatsTrace(ctx context.Context) *sourceUserStatsTrace {
	v, _ := ctx.Value(sourceUserStatsKey{}).(*sourceUserStatsTrace)
	return v
}

// SourceUserStats preserves the module's three outcomes: a DTO (possibly with
// err_msg), no DTO after an unsupported/throwing parser, or a revoked operation.
// The error alongside a DTO describes native connection health, not DTO truth.
func (c *Client) SourceUserStats(ctx context.Context, options UserStatsOptions) (UserStats, *SourceUserData, error) {
	trace := &sourceUserStatsTrace{}
	stats, err := c.UserStatsWithOptions(context.WithValue(ctx, sourceUserStatsKey{}, trace), options)
	if ctx.Err() != nil {
		return stats, nil, ctx.Err()
	}
	if trace.guardError != nil {
		return stats, nil, trace.guardError
	}
	if !trace.attempted || trace.none {
		return stats, nil, sourceModuleError(err)
	}
	dto := sourceDataFromObserved(stats)
	if c.config.Kind == "mteam" {
		if trace.profileSeen {
			if trace.profileEmpty {
				return stats, emptySourceUserData(), err
			}
			if trace.profile == nil {
				return stats, nil, sourceModuleError(err)
			}
			code, ok := trace.profile["code"].(string)
			if !ok || code != "0" {
				return stats, emptySourceUserData(), err
			}
			if e := sourceMTeamProfile(dto, trace.profile); e != nil {
				return stats, nil, e
			}
		}
		// Empty/failed HTTP pages return normally in the original parser. They
		// retain constructor defaults without claiming a healthy connection.
		if !trace.profileSeen {
			dto = emptySourceUserData()
		}
	} else if !trace.indexParsed {
		message := "未检测到已登陆，请检查cookies是否过期"
		dto.Error = &message
	} else if stats.Sources["ratio"] == "derived:uploaded/downloaded" || stats.RatioInfinite {
		dto.Ratio = 0
		if dto.Download > 0 {
			dto.Ratio = math.Round(float64(dto.Upload)/float64(dto.Download)*1000) / 1000
		}
	}
	return stats, dto, err
}

func sourceModuleError(err error) error {
	if err != nil {
		return err
	}
	return errors.New("site_user_source_module_failed")
}

func emptySourceUserData() *SourceUserData {
	return &SourceUserData{SeedingInfo: [][2]int64{}, Messages: [][3]*string{}}
}

func sourceDataFromObserved(s UserStats) *SourceUserData {
	d := emptySourceUserData()
	text := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	integer := func(v *int64) int64 {
		if v == nil {
			return 0
		}
		return *v
	}
	d.Username, d.UserLevel, d.JoinAt = text(s.Username), text(s.Level), text(s.JoinedAt)
	if s.UserID != "" {
		d.UserID = s.UserID
	}
	d.Upload, d.Download = integer(s.Uploaded), integer(s.Downloaded)
	if s.Bonus != nil {
		d.Bonus = *s.Bonus
	}
	if s.Ratio != nil {
		d.Ratio = *s.Ratio
	}
	d.Seeding, d.Leeching = integer(s.Seeding), integer(s.Leeching)
	d.SeedingSize, d.LeechingSize = integer(s.SeedingBytes), integer(s.LeechingBytes)
	d.MessageUnread = integer(s.MessageUnread)
	if s.SeedingInfo != nil {
		d.SeedingInfo = s.SeedingInfo
	}
	if s.Messages != nil {
		d.Messages = s.Messages
	}
	return d
}

func sourceMTeamProfile(dto *SourceUserData, root map[string]any) error {
	data, ok := root["data"].(map[string]any)
	if !ok || data == nil {
		return sourceModuleError(nil)
	}
	stringField := func(v any) (*string, bool) {
		if v == nil {
			return nil, true
		}
		s, ok := v.(string)
		return &s, ok
	}
	var valid bool
	dto.Username, valid = stringField(data["username"])
	if !valid {
		return sourceModuleError(nil)
	}
	switch id := data["id"].(type) {
	case nil, string:
		dto.UserID = id
	case json.Number:
		n, e := sourceUserInteger(id)
		if e != nil {
			return e
		}
		dto.UserID = n
	default:
		return sourceModuleError(nil)
	}
	role := data["role"]
	switch role.(type) {
	case nil, bool, string, json.Number:
	default:
		return sourceModuleError(nil)
	}
	if sourceFalse(role) {
		role = "1"
	}
	dto.UserLevel = nil
	if r, ok := role.(string); ok {
		if name := mteamUserRoles[r]; name != "" {
			dto.UserLevel = &name
		}
	}
	status := map[string]any{}
	if v, exists := data["memberStatus"]; exists {
		var ok bool
		status, ok = v.(map[string]any)
		if !ok {
			return sourceModuleError(nil)
		}
	}
	dto.JoinAt, valid = stringField(status["createdDate"])
	if !valid {
		return sourceModuleError(nil)
	}
	counts := map[string]any{}
	if v, exists := data["memberCount"]; exists {
		var ok bool
		counts, ok = v.(map[string]any)
		if !ok {
			return sourceModuleError(nil)
		}
	}
	var e error
	if dto.Upload, e = sourceUserInteger(counts["uploaded"]); e != nil {
		return e
	}
	if dto.Download, e = sourceUserInteger(counts["downloaded"]); e != nil {
		return e
	}
	if dto.Bonus, e = sourceFloat(counts["bonus"]); e != nil {
		return e
	}
	if dto.Ratio, e = sourceFloat(counts["shareRate"]); e != nil {
		return e
	}
	return nil
}

func sourceUserInteger(v any) (int64, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case bool:
		if n {
			return 1, nil
		}
		return 0, nil
	case string:
		if n == "" {
			return 0, nil
		}
		i, e := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		if e == nil {
			return i, nil
		}
	case json.Number:
		if i, e := n.Int64(); e == nil {
			return i, nil
		}
		f, e := n.Float64()
		if e == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f < math.MaxInt64 && f >= math.MinInt64 {
			return int64(f), nil
		}
	}
	return 0, sourceModuleError(nil)
}

func sourceFloat(v any) (float64, error) {
	if sourceFalse(v) {
		return 0, nil
	}
	if b, ok := v.(bool); ok && b {
		return 1, nil
	}
	f, e := strconv.ParseFloat(stringValue(v), 64)
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, sourceModuleError(nil)
	}
	return f, nil
}

func sourceFalse(v any) bool {
	switch n := v.(type) {
	case nil:
		return true
	case bool:
		return !n
	case string:
		return n == ""
	case json.Number:
		return n == "0"
	}
	return false
}

func sourceProfilePage(ctx context.Context, endpoint string, raw []byte) {
	trace := sourceStatsTrace(ctx)
	if trace == nil || endpoint != "api/member/profile" {
		return
	}
	trace.profileSeen = true
	if len(raw) == 0 {
		trace.profileEmpty = true
		return
	}
	var value any
	if !decodeJSON(raw, &value) {
		trace.profile = nil
		return
	}
	trace.profileEmpty = sourceFalse(value)
	if values, ok := value.([]any); ok && len(values) == 0 {
		trace.profileEmpty = true
	}
	trace.profile, _ = value.(map[string]any)
}

func sourceAccountDOM(ctx context.Context, raw []byte) (*html.Node, error) {
	if sourceStatsTrace(ctx) == nil {
		return pageDOM(raw)
	}
	for _, marker := range []string{"cf-chl-", "challenge-platform", "just a moment...", "g-recaptcha", "h-captcha", "turnstile"} {
		if strings.Contains(strings.ToLower(string(raw)), marker) {
			return nil, errors.New("site_login_challenge")
		}
	}
	doc, e := html.Parse(bytes.NewReader(raw))
	if e != nil {
		return nil, errors.New("site_invalid_html")
	}
	for _, n := range find(doc, func(n *html.Node) bool { return n.Data == "input" }) {
		if attribute(n, "type") == "password" {
			return nil, errors.New("site_authentication_failed")
		}
	}
	for _, n := range find(doc, func(n *html.Node) bool { return n.Type == html.ElementNode }) {
		if n.Data == "a" {
			for _, k := range []string{"href", "data-url", "onclick", "lay-on"} {
				if strings.Contains(attribute(n, k), "logout") {
					return doc, nil
				}
			}
			if strings.Contains(attribute(n, "href"), "mybonus") || strings.Contains(attribute(n, "href"), "usercp") || attribute(n, "id") == "myitem" {
				return doc, nil
			}
		}
		if n.Data == "form" && strings.Contains(attribute(n, "action"), "logout") || n.Data == "div" && attribute(n, "class") == "user-info-side" {
			return doc, nil
		}
	}
	return nil, errors.New("site_authentication_or_template_changed")
}

func sourceHhanShape(doc *html.Node) bool {
	panels := find(doc, func(n *html.Node) bool { return attribute(n, "id") == "user-info-panel" })
	if len(panels) == 0 {
		return false
	}
	nth := func(n *html.Node, index int) *html.Node {
		if n == nil {
			return nil
		}
		i := 0
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Data == "div" {
				i++
				if i == index {
					return c
				}
			}
		}
		return nil
	}
	second := nth(panels[0], 2)
	traffic := nth(second, 2)
	// The source XPath indexes text()[0], not merely the element itself.
	hasText := func(n *html.Node) bool {
		if n == nil {
			return false
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type == html.TextNode {
				return true
			}
		}
		return false
	}
	return hasText(nth(traffic, 4)) && hasText(nth(traffic, 5)) && hasText(nth(nth(nth(second, 1), 1), 1))
}

func sourceIndexStage(ctx context.Context, kind string, doc *html.Node) bool {
	trace := sourceStatsTrace(ctx)
	if trace == nil {
		return true
	}
	trace.indexParsed = true
	if kind == "hhanclub" && !sourceHhanShape(doc) {
		trace.none = true
		return false
	}
	return true
}

func sourceRequestStarted(ctx context.Context) {
	if trace := sourceStatsTrace(ctx); trace != nil {
		trace.attempted = true
	}
}
