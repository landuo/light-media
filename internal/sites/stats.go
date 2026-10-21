package sites

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"light-media/internal/logging"
)

// UserStats contains only observed values. A nil number means that the site did
// not supply a value or its page could not be parsed; it never means zero.
type UserStats struct {
	UserID        string            `json:"user_id,omitempty"`
	Username      string            `json:"username,omitempty"`
	Level         string            `json:"level,omitempty"`
	JoinedAt      string            `json:"joined_at,omitempty"`
	Uploaded      *int64            `json:"uploaded"`
	Downloaded    *int64            `json:"downloaded"`
	Ratio         *float64          `json:"ratio"`
	RatioInfinite bool              `json:"ratio_infinite,omitempty"`
	Seeding       *int64            `json:"seeding"`
	SeedingBytes  *int64            `json:"seeding_bytes"`
	Leeching      *int64            `json:"leeching"`
	LeechingBytes *int64            `json:"leeching_bytes"`
	SeedingInfo   [][2]int64        `json:"seeding_info"`
	MessageUnread *int64            `json:"message_unread"`
	Messages      [][3]*string      `json:"message_unread_contents"`
	Error         *string           `json:"err_msg"`
	Bonus         *float64          `json:"bonus"`
	CapturedAt    string            `json:"captured_at"`
	Sources       map[string]string `json:"sources"`
	Warnings      []string          `json:"warnings"`
}

type UserStatsOptions struct {
	ReadMessages bool
	Location     *time.Location
}

func (c *Client) UserStats(ctx context.Context) (UserStats, error) {
	return c.UserStatsWithOptions(ctx, UserStatsOptions{})
}

func (c *Client) UserStatsWithOptions(ctx context.Context, options UserStatsOptions) (stats UserStats, err error) {
	defer func() {
		if err != nil {
			message := err.Error()
			stats.Error = &message
		}
	}()
	if c.config.Kind == "rousi" {
		value, _, e := c.SourceUserStats(ctx, options)
		return value, e
	}
	ctx = logging.SiteContext(ctx, c.config.ID)
	ctx, _ = logging.Target(ctx, "site")
	s := UserStats{CapturedAt: time.Now().UTC().Format(time.RFC3339), Sources: map[string]string{}, Warnings: []string{}}
	if c.config.Kind == "mteam" {
		s, err := c.mteamUserStats(ctx, s)
		if err == nil && options.ReadMessages {
			if e := c.mteamMessages(ctx, &s); e != nil {
				s.Warnings = append(s.Warnings, e.Error())
			}
		}
		return s, err
	}
	b, _, err := c.request(ctx, http.MethodGet, c.endpoint("index.php"), nil, c.auth(), htmlLimit)
	if err != nil {
		return s, err
	}
	doc, err := pageDOM(b)
	if err != nil {
		return s, err
	}
	parseUserHTML(&s, doc, "index.php")
	parseNexusUnread(&s, doc)
	if c.config.Kind == "hhanclub" {
		parseHhanPanel(&s, doc, "index.php")
	}
	profileDoc := doc
	for _, a := range find(doc, func(n *html.Node) bool { return n.Data == "a" }) {
		u, e := url.Parse(attribute(a, "href"))
		if e != nil || path.Base(u.Path) != "userdetails.php" || !numericID.MatchString(u.Query().Get("id")) {
			continue
		}
		u = c.base.ResolveReference(u)
		if !sameOrigin(c.base, u) {
			continue
		}
		s.UserID = u.Query().Get("id")
		s.Username = statsText(text(a), 128)
		s.Sources["user_id"], s.Sources["username"] = "index.php:userdetails link", "index.php:userdetails link"
		break
	}
	if s.UserID == "" {
		return s, errors.New("site_user_identity_missing")
	}
	u := c.endpoint("userdetails.php")
	u.RawQuery = url.Values{"id": {s.UserID}}.Encode()
	b, _, err = c.request(ctx, http.MethodGet, u, nil, c.auth(), htmlLimit)
	if err != nil {
		s.Warnings = append(s.Warnings, "userdetails:"+err.Error())
	} else if detail, e := pageDOM(b); e != nil {
		s.Warnings = append(s.Warnings, "userdetails:"+e.Error())
	} else {
		parseUserHTML(&s, detail, "userdetails.php")
		if c.config.Kind == "hhanclub" {
			parseHhanPanel(&s, detail, "userdetails.php")
		}
		profileDoc = detail
	}
	needSeedingInfo := s.SeedingInfo == nil && (s.Seeding == nil || *s.Seeding > 0)
	if s.SeedingBytes == nil || needSeedingInfo {
		if err := c.nexusSeedingStats(ctx, &s, profileDoc); err != nil {
			s.Warnings = append(s.Warnings, err.Error())
		}
	}
	if s.Uploaded == nil && s.Downloaded == nil && s.Bonus == nil && s.Seeding == nil {
		return s, errors.New("site_user_stats_unrecognized")
	}
	if options.ReadMessages {
		if e := c.nexusMessages(ctx, &s); e != nil {
			s.Warnings = append(s.Warnings, e.Error())
		}
	}
	return s, nil
}

func statsText(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > limit {
		return string([]rune(s)[:limit])
	}
	return s
}

var statsSize = regexp.MustCompile(`(?i)^([0-9][0-9,.]*)\s*([KMGTPE]?)(I?)B$`)

func statsBytes(s string) (*int64, bool) {
	m := statsSize.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return nil, false
	}
	n, e := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if e != nil {
		return nil, false
	}
	// NexusPHP labels GB/TB while counting binary bytes, matching its own UI.
	power := strings.Index(" KMGTPE", strings.ToUpper(m[2]))
	if m[2] == "" {
		power = 0
	}
	n *= math.Pow(1024, float64(power))
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n >= math.MaxInt64 {
		return nil, false
	}
	v := int64(math.Round(n))
	return &v, true
}

func statsFloat(s string) (*float64, bool) {
	n, e := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return nil, false
	}
	return &n, true
}

func statsInt(s string) (*int64, bool) {
	n, e := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 10, 64)
	if e != nil || n < 0 {
		return nil, false
	}
	return &n, true
}

func parseUserHTML(s *UserStats, doc *html.Node, source string) {
	t := statsPageText(doc)
	for _, x := range []struct {
		key, label string
		value      **int64
	}{
		{"uploaded", `(?:上传量?|上傳量?|Uploaded)`, &s.Uploaded},
		{"downloaded", `(?:下载量?|下載量?|Downloaded)`, &s.Downloaded},
		{"seeding_bytes", `(?:总做种体积|總做種體積|做种体积|做種體積|Seeding size)`, &s.SeedingBytes},
	} {
		r := regexp.MustCompile(`(?i)(?:^|[^总總\pL])` + x.label + `\s*[:：]?\s*([\d,.]+\s*[KMGTPE]?I?B)\b`)
		if m := r.FindStringSubmatch(t); m != nil {
			if n, ok := statsBytes(m[1]); ok {
				*x.value = n
				s.Sources[x.key] = source + ":label"
			}
		}
	}
	for _, x := range []struct {
		key, label string
		value      **int64
	}{
		{"seeding", `(?:总做种数|總做種數|做种中|做種中|Torrents seeding)`, &s.Seeding},
		{"leeching", `(?:下载中|下載中|Torrents leeching)`, &s.Leeching},
	} {
		if m := regexp.MustCompile(`(?i)` + x.label + `\s*[:：]?\s*([\d,]+)\b`).FindStringSubmatch(t); m != nil {
			if n, ok := statsInt(m[1]); ok {
				*x.value = n
				s.Sources[x.key] = source + ":label"
			}
		}
	}
	if m := regexp.MustCompile(`(?i)(?:分享率|Ratio)\s*[:：]?\s*([\d,.]+|∞|Inf(?:inity)?)`).FindStringSubmatch(t); m != nil {
		if n, ok := statsFloat(m[1]); ok {
			s.Ratio, s.RatioInfinite = n, false
		} else {
			s.Ratio, s.RatioInfinite = nil, true
		}
		s.Sources["ratio"] = source + ":label"
	}
	for _, a := range find(doc, func(n *html.Node) bool { return n.Data == "a" }) {
		u, e := url.Parse(attribute(a, "href"))
		if e == nil && path.Base(u.Path) == "mybonus.php" {
			if m := regexp.MustCompile(`[\d][\d,.]*`).FindString(text(a)); m != "" {
				if n, ok := statsFloat(m); ok {
					s.Bonus = n
					s.Sources["bonus"] = source + ":mybonus link"
				}
			}
		}
	}
	if s.Bonus == nil {
		if m := regexp.MustCompile(`(?:魔力值?|积分|積分|Bonus)\s*[:：]?\s*([\d,.]+)`).FindStringSubmatch(t); m != nil {
			if n, ok := statsFloat(m[1]); ok {
				s.Bonus = n
				s.Sources["bonus"] = source + ":label"
			}
		}
	}
	var coins float64
	coinObserved := false
	for _, span := range find(doc, func(n *html.Node) bool { return n.Data == "span" && hasClass(n, "ucoin-symbol") }) {
		multiplier := float64(0)
		switch {
		case hasClass(span, "ucoin-gold"):
			multiplier = 10000
		case hasClass(span, "ucoin-silver"):
			multiplier = 100
		case hasClass(span, "ucoin-copper"):
			multiplier = 1
		}
		if n, ok := statsFloat(text(span)); ok && multiplier > 0 {
			coins += *n * multiplier
			coinObserved = true
		}
	}
	if coinObserved {
		s.Bonus = &coins
		s.Sources["bonus"] = source + ":ucoin converted to copper"
	}
	for _, row := range find(doc, func(n *html.Node) bool { return n.Data == "tr" }) {
		cells := directCells(row)
		if len(cells) < 2 {
			continue
		}
		switch strings.Trim(text(cells[0]), " :：") {
		case "等级", "等級", "Class", "User class":
			v := text(cells[1])
			for _, img := range find(cells[1], func(n *html.Node) bool { return n.Data == "img" }) {
				if title := attribute(img, "title"); title != "" {
					v = title
					break
				}
			}
			if v != "" {
				s.Level = statsText(v, 80)
				s.Sources["level"] = source + ":level row"
			}
		case "加入日期", "注册日期", "註冊日期", "Join date":
			s.JoinedAt = statsText(text(cells[1]), 80)
			s.Sources["joined_at"] = source + ":join row"
		}
	}
	if s.Level == "" {
		for _, b := range find(doc, func(n *html.Node) bool { return n.Data == "b" && strings.Contains(attribute(n, "class"), "_Name") }) {
			if v := text(b); v != "" {
				s.Level = statsText(v, 80)
				s.Sources["level"] = source + ":class name"
				break
			}
		}
	}
	if s.Ratio == nil && !s.RatioInfinite && s.Uploaded != nil && s.Downloaded != nil && *s.Downloaded > 0 {
		r := float64(*s.Uploaded) / float64(*s.Downloaded)
		s.Ratio = &r
		s.Sources["ratio"] = "derived:uploaded/downloaded"
	}
}

func parseHhanPanel(s *UserStats, doc *html.Node, source string) {
	panels := find(doc, func(n *html.Node) bool { return attribute(n, "id") == "user-info-panel" })
	if len(panels) != 1 {
		return
	}
	nthDiv := func(n *html.Node, index int) *html.Node {
		if n == nil {
			return nil
		}
		i := 0
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type == html.ElementNode && ch.Data == "div" {
				i++
				if i == index {
					return ch
				}
			}
		}
		return nil
	}
	// Native HHan dashboard layout also represents these labels using icons.
	// This exact path is the same path used by its original native parser.
	traffic := nthDiv(nthDiv(panels[0], 2), 2)
	for _, field := range []struct {
		index int
		key   string
		dst   **int64
	}{{4, "uploaded", &s.Uploaded}, {5, "downloaded", &s.Downloaded}} {
		if *field.dst != nil {
			continue
		}
		cell := nthDiv(traffic, field.index)
		if cell == nil {
			continue
		}
		value := regexp.MustCompile(`(?i)[\d,.]+\s*[KMGTPE]?I?B`).FindString(text(cell))
		if n, ok := statsBytes(value); ok {
			*field.dst = n
			s.Sources[field.key] = source + ":user-info-panel native field"
		}
	}
	if s.Ratio == nil && !s.RatioInfinite && s.Uploaded != nil && s.Downloaded != nil && *s.Downloaded > 0 {
		r := float64(*s.Uploaded) / float64(*s.Downloaded)
		s.Ratio = &r
		s.Sources["ratio"] = "derived:uploaded/downloaded"
	}
}

func statsPageText(doc *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		if n.Type == html.ElementNode && n.Data == "img" {
			for _, key := range []string{"alt", "title"} {
				v := strings.TrimSpace(attribute(n, key))
				switch v {
				case "上传", "上传量", "上傳量", "下载", "下载量", "下載量", "分享率", "Uploaded", "Downloaded", "Ratio", "做种中", "下载中":
					b.WriteString(v)
					b.WriteString(": ")
				}
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			visit(ch)
		}
	}
	visit(doc)
	return strings.Join(strings.Fields(b.String()), " ")
}

func directCells(row *html.Node) []*html.Node {
	var cells []*html.Node
	for n := row.FirstChild; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode && (n.Data == "td" || n.Data == "th") {
			cells = append(cells, n)
		}
	}
	return cells
}

func (c *Client) statsJSON(ctx context.Context, endpoint string, in any) (map[string]any, error) {
	payload, _ := json.Marshal(in)
	b, _, err := c.request(ctx, http.MethodPost, c.endpoint(endpoint), payload, c.auth(), htmlLimit)
	if err != nil {
		return nil, err
	}
	data, err := envelope(b)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if !decodeJSON(data, &m) || m == nil {
		return nil, errors.New("site_protocol_changed")
	}
	return m, nil
}

var mteamUserRoles = map[string]string{"1": "User", "2": "Power User", "3": "Elite User", "4": "Crazy User", "5": "Insane User", "6": "Veteran User", "7": "Extreme User", "8": "Ultimate User", "9": "Nexus Master", "10": "VIP", "11": "Retiree", "12": "Uploader", "13": "Moderator", "14": "Administrator", "15": "Sysop", "16": "Staff", "17": "Offer memberStaff", "18": "Bet memberStaff"}

func (c *Client) mteamUserStats(ctx context.Context, s UserStats) (UserStats, error) {
	m, err := c.statsJSON(ctx, "api/member/profile", map[string]any{})
	if err != nil {
		return s, err
	}
	s.UserID, s.Username = stringValue(m["id"]), statsText(stringValue(m["username"]), 128)
	if !numericID.MatchString(s.UserID) || s.Username == "" {
		return s, errors.New("site_user_identity_missing")
	}
	s.Sources["user_id"], s.Sources["username"] = "api/member/profile", "api/member/profile"
	if v := mteamUserRoles[stringValue(m["role"])]; v != "" {
		s.Level = v
		s.Sources["level"] = "api/member/profile:role"
	}
	if status, ok := m["memberStatus"].(map[string]any); ok {
		s.JoinedAt = statsText(stringValue(status["createdDate"]), 80)
		s.Sources["joined_at"] = "api/member/profile:memberStatus.createdDate"
	}
	counts, _ := m["memberCount"].(map[string]any)
	for key, dst := range map[string]**int64{"uploaded": &s.Uploaded, "downloaded": &s.Downloaded} {
		if n, ok := integer(counts[key]); ok {
			*dst = &n
			s.Sources[key] = "api/member/profile:memberCount." + key
		}
	}
	if n, ok := statsFloat(stringValue(counts["bonus"])); ok {
		s.Bonus = n
		s.Sources["bonus"] = "api/member/profile:memberCount.bonus"
	}
	ratio := stringValue(counts["shareRate"])
	if n, ok := statsFloat(ratio); ok {
		s.Ratio = n
		s.Sources["ratio"] = "api/member/profile:memberCount.shareRate"
	} else if ratio == "∞" || strings.EqualFold(ratio, "inf") || strings.EqualFold(ratio, "infinity") {
		s.RatioInfinite = true
		s.Sources["ratio"] = "api/member/profile:memberCount.shareRate"
	}
	peer, e := c.statsJSON(ctx, "api/tracker/myPeerStatus", map[string]any{"uid": s.UserID})
	if e != nil {
		s.Warnings = append(s.Warnings, "peer_status:"+e.Error())
	} else {
		for key, dst := range map[string]**int64{"seeder": &s.Seeding, "leecher": &s.Leeching} {
			if n, ok := integer(peer[key]); ok {
				*dst = &n
				field := map[string]string{"seeder": "seeding", "leecher": "leeching"}[key]
				s.Sources[field] = "api/tracker/myPeerStatus:" + key
			}
		}
	}
	var size, count int64
	info := [][2]int64{}
	infoComplete := true
	seen := map[string]bool{}
	complete := false
	for page := 1; page <= 50; page++ {
		list, e := c.statsJSON(ctx, "api/member/getUserTorrentList", map[string]any{"userid": s.UserID, "type": "SEEDING", "pageNumber": page, "pageSize": 200})
		if e != nil {
			s.Warnings = append(s.Warnings, "seeding_list:"+e.Error())
			break
		}
		rows, ok := list["data"].([]any)
		if !ok || len(rows) > 200 {
			s.Warnings = append(s.Warnings, "site_seeding_protocol_changed")
			break
		}
		valid := true
		for _, v := range rows {
			row, ok := v.(map[string]any)
			if !ok {
				valid = false
				break
			}
			torrent, ok := row["torrent"].(map[string]any)
			if !ok {
				valid = false
				break
			}
			id := stringValue(torrent["id"])
			n, ok := integer(torrent["size"])
			if !numericID.MatchString(id) || !ok || seen[id] || n > math.MaxInt64-size {
				valid = false
				break
			}
			seen[id] = true
			size += n
			count++
			if peers, ok := integer(torrent["source"]); ok {
				info = append(info, [2]int64{peers, n})
			} else {
				infoComplete = false
			}
		}
		if !valid {
			s.Warnings = append(s.Warnings, "site_seeding_protocol_changed")
			break
		}
		if total, ok := integer(list["total"]); ok {
			if count == total {
				complete = true
				break
			}
			if count > total || len(rows) == 0 {
				s.Warnings = append(s.Warnings, "site_seeding_count_mismatch")
				break
			}
		} else if len(rows) < 200 {
			complete = true
			break
		}
	}
	if complete {
		s.SeedingBytes = &size
		s.Sources["seeding_bytes"] = "api/member/getUserTorrentList:all pages"
		if infoComplete {
			s.SeedingInfo = info
			s.Sources["seeding_info"] = "api/member/getUserTorrentList:torrent.source,size/all pages"
		}
		if s.Seeding == nil || sourceStatsTrace(ctx) != nil {
			s.Seeding = &count
			s.Sources["seeding"] = "api/member/getUserTorrentList:all pages"
		}
	} else {
		s.Warnings = append(s.Warnings, "site_seeding_size_incomplete")
	}
	return s, nil
}

func (c *Client) nexusSeedingStats(ctx context.Context, s *UserStats, profile *html.Node) error {
	u := c.endpoint("getusertorrentlistajax.php")
	u.RawQuery = url.Values{"userid": {s.UserID}, "type": {"seeding"}}.Encode()
	var firstBody []byte
	var csrf string
	for _, meta := range find(profile, func(n *html.Node) bool { return n.Data == "meta" && attribute(n, "name") == "x-csrf" }) {
		csrf = attribute(meta, "content")
	}
	for _, a := range find(profile, func(n *html.Node) bool { return n.Data == "a" }) {
		href := attribute(a, "href")
		if csrf != "" && strings.Contains(href, "getusertorrentlistajax") && strings.Contains(href, "seeding") {
			match := regexp.MustCompile(`getusertorrentlistajax\(\s*'([0-9]+)'`).FindStringSubmatch(href)
			if match != nil && match[1] == s.UserID {
				u = c.endpoint("ajax_getusertorrentlist.php")
				firstBody = []byte(url.Values{"userid": {s.UserID}, "type": {"seeding"}, "csrf": {csrf}}.Encode())
				break
			}
		}
		link, e := url.Parse(href)
		if e != nil || link.Query().Get("type") != "seeding" {
			continue
		}
		link = c.base.ResolveReference(link)
		if !sameOrigin(c.base, link) || (path.Base(link.Path) != "getusertorrentlist.php" && path.Base(link.Path) != "getusertorrentlistajax.php") {
			continue
		}
		q := link.Query()
		if id := q.Get("userid"); id != "" && id != s.UserID {
			continue
		}
		q.Set("userid", s.UserID)
		link.RawQuery = q.Encode()
		u = link
		break
	}
	var total, count int64
	info := [][2]int64{}
	infoComplete := true
	seenURLs := map[string]bool{}
	seenIDs := map[string]bool{}
	for page := 0; page < 50; page++ {
		if seenURLs[u.String()] {
			return errors.New("site_seeding_pagination_loop")
		}
		seenURLs[u.String()] = true
		method := http.MethodGet
		headers := c.auth()
		var body []byte
		if page == 0 && firstBody != nil {
			method = http.MethodPost
			body = firstBody
			headers["Content-Type"] = "application/x-www-form-urlencoded"
		}
		b, _, err := c.request(ctx, method, u, body, headers, htmlLimit)
		if err != nil {
			return err
		}
		doc, err := html.Parse(strings.NewReader(string(b)))
		if err != nil {
			return errors.New("site_seeding_protocol_changed")
		}
		if c.config.Kind == "nexusphp" {
			if len(find(doc, func(n *html.Node) bool {
				return n.Data == "input" && strings.EqualFold(attribute(n, "type"), "password")
			})) > 0 {
				return errors.New("site_authentication_failed")
			}
			for _, marker := range []string{"cf-chl-", "challenge-platform", "just a moment...", "g-recaptcha", "h-captcha", "turnstile"} {
				if strings.Contains(strings.ToLower(string(b)), marker) {
					return errors.New("site_login_challenge")
				}
			}
		}
		// The AJAX response has no navigation/auth marker. A recognized size
		// header and valid detail rows are required before accepting its table.
		found := false
		for _, table := range find(doc, func(n *html.Node) bool { return n.Data == "table" }) {
			rows := find(table, func(n *html.Node) bool { return n.Data == "tr" })
			if len(rows) == 0 {
				continue
			}
			sizeCol, seedersCol := -1, -1
			for i, cell := range directCells(rows[0]) {
				label := strings.ToLower(text(cell))
				if label == "大小" || label == "尺寸" || label == "size" {
					sizeCol = i
				}
				if label == "在做种" || label == "做种者" || label == "做種者" || label == "seeders" {
					seedersCol = i
				}
				for _, img := range find(cell, func(n *html.Node) bool { return n.Data == "img" }) {
					if attribute(img, "class") == "size" || attribute(img, "alt") == "size" {
						sizeCol = i
					}
					if hasClass(img, "seeders") || strings.EqualFold(attribute(img, "alt"), "seeders") {
						seedersCol = i
					}
				}
			}
			if sizeCol < 0 {
				continue
			}
			found = true
			for _, row := range rows[1:] {
				cells := directCells(row)
				if len(cells) <= sizeCol {
					continue
				}
				details := find(row, func(n *html.Node) bool {
					if n.Data != "a" {
						return false
					}
					link, e := url.Parse(attribute(n, "href"))
					return e == nil && path.Base(link.Path) == "details.php" && numericID.MatchString(link.Query().Get("id"))
				})
				if len(details) == 0 {
					if c.config.Kind == "nexusphp" && text(row) != "" {
						return errors.New("site_seeding_protocol_changed")
					}
					continue
				}
				link, _ := url.Parse(attribute(details[0], "href"))
				if c.config.Kind == "nexusphp" && !sameOrigin(u.ResolveReference(link), c.base) {
					return errors.New("site_seeding_protocol_changed")
				}
				id := link.Query().Get("id")
				if seenIDs[id] {
					return errors.New("site_seeding_duplicate_row")
				}
				seenIDs[id] = true
				n, ok := statsBytes(strings.ReplaceAll(text(cells[sizeCol]), " ", ""))
				if !ok || *n > math.MaxInt64-total {
					return errors.New("site_seeding_size_unrecognized")
				}
				total += *n
				count++
				if seedersCol >= 0 && seedersCol < len(cells) {
					if peers, ok := statsInt(text(cells[seedersCol])); ok {
						info = append(info, [2]int64{*peers, *n})
					} else {
						infoComplete = false
					}
				} else {
					infoComplete = false
				}
			}
			break
		}
		if !found {
			t := text(doc)
			if page == 0 && (strings.Contains(t, "没有种子") || strings.Contains(t, "沒有種子") || strings.Contains(strings.ToLower(t), "no torrents")) {
				found = true
			} else {
				return errors.New("site_seeding_table_unrecognized")
			}
		}
		var next *url.URL
		for _, a := range find(doc, func(n *html.Node) bool { return n.Data == "a" }) {
			label := text(a)
			if !strings.Contains(label, "下一页") && !strings.Contains(label, "下一頁") && label != "Next" && label != "下一页 >>" {
				continue
			}
			link, e := url.Parse(attribute(a, "href"))
			if e != nil {
				return errors.New("site_seeding_pagination_invalid")
			}
			link = u.ResolveReference(link)
			if !sameOrigin(c.base, link) || (path.Base(link.Path) != "getusertorrentlistajax.php" && path.Base(link.Path) != "getusertorrentlist.php" && path.Base(link.Path) != "ajax_getusertorrentlist.php") {
				return errors.New("site_seeding_pagination_invalid")
			}
			q := link.Query()
			q.Set("userid", s.UserID)
			q.Set("type", "seeding")
			link.RawQuery = q.Encode()
			next = link
			break
		}
		if next == nil {
			if s.Seeding != nil && *s.Seeding != count {
				return errors.New("site_seeding_size_incomplete")
			}
			s.SeedingBytes = &total
			s.Sources["seeding_bytes"] = "getusertorrentlistajax.php:all pages"
			if infoComplete {
				s.SeedingInfo = info
				s.Sources["seeding_info"] = "getusertorrentlistajax.php:seeders,size/all pages"
			}
			if s.Seeding == nil {
				s.Seeding = &count
				s.Sources["seeding"] = "getusertorrentlistajax.php:all pages"
			}
			return nil
		}
		u = next
	}
	return errors.New("site_seeding_size_incomplete")
}
