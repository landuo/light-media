package brushflow

import (
	"context"
	"fmt"
	pyre "light-media/internal/plugins/iyuuautoseed/compat/pythonre"
	"math"
	"net/url"
	"strings"
	"time"
)

const promotionProbe = 900.0
const promotionMargin = 600.0

func match(name, s string) *pyre.CapturedMatch {
	m, e := sourcePatterns[name].SearchGroups(s, 0)
	if e != nil {
		return nil
	}
	return m
}
func group(m *pyre.CapturedMatch, i int) string {
	if m == nil || i >= len(m.Groups) || m.Groups[i] == nil {
		return ""
	}
	return *m.Groups[i]
}
func decimal(s string) int {
	n := 0
	for _, c := range s {
		v, ok := pyre.DecimalValue(c)
		if !ok {
			return 0
		}
		n = n*10 + v
	}
	return n
}
func patternMatches(name, s string) bool { ok, _ := sourcePatterns[name].Search(s); return ok }
func eachMatch(name, s string, fn func(*pyre.CapturedMatch) bool) {
	p := sourcePatterns[name]
	for from := 0; ; {
		m, e := p.SearchGroups(s, from)
		if e != nil || m == nil {
			return
		}
		if !fn(m) {
			return
		}
		if m.End <= from {
			return
		}
		from = m.End
	}
}
func parseZone(s string) *int {
	if s == "" {
		return nil
	}
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if s == "Z" || s == "UTC" || s == "GMT" {
		n := 0
		return &n
	}
	sign := 1
	if strings.HasPrefix(s, "-") {
		sign = -1
	}
	s = strings.ReplaceAll(strings.TrimLeft(s, "+-"), ":", "")
	rs := []rune(s)
	if len(rs) != 4 {
		return nil
	}
	for _, c := range rs {
		if _, ok := pyre.DecimalValue(c); !ok {
			return nil
		}
	}
	h, m := decimal(string(rs[:2])), decimal(string(rs[2:]))
	if h > 14 || m > 59 {
		return nil
	}
	n := sign * (h*3600 + m*60)
	return &n
}
func parseDeadline(v any) (time.Time, *int, bool) {
	if v == nil {
		return time.Time{}, nil, false
	}
	s := strings.TrimSpace(text(v))
	if s == "" || len([]rune(s)) > 256 {
		return time.Time{}, nil, false
	}
	m := match("PROMOTION_DATE_PATTERN", s)
	if m == nil {
		return time.Time{}, nil, false
	}
	y, mo, d := decimal(group(m, 1)), decimal(group(m, 2)), decimal(group(m, 3))
	h, mi, se := decimal(group(m, 4)), decimal(group(m, 5)), decimal(group(m, 6))
	if y < 1 || y > 9999 || mo < 1 || mo > 12 || d < 1 || h > 23 || mi > 59 || se > 59 {
		return time.Time{}, nil, false
	}
	t := time.Date(y, time.Month(mo), d, h, mi, se, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != mo || t.Day() != d {
		return time.Time{}, nil, false
	}
	if m.Groups[4] == nil {
		t = t.Add(24*time.Hour - time.Second)
	}
	return t, parseZone(group(m, 7)), true
}
func promotionDuration(v any) *int {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(text(v))
	if s == "" || len([]rune(s)) > 128 {
		return nil
	}
	total, matched := 0, false
	eachMatch("PROMOTION_DURATION_PATTERN", s, func(m *pyre.CapturedMatch) bool {
		unit := strings.ToLower(group(m, 2))
		factor := 60
		switch unit {
		case "天", "日", "day", "days":
			factor = 86400
		case "小时", "时", "hour", "hours", "hr", "hrs":
			factor = 3600
		}
		total += decimal(group(m, 1)) * factor
		matched = true
		return true
	})
	m := match("PROMOTION_LABELFREE_PATTERN", s)
	if m != nil {
		h, mi, se := decimal(group(m, 1)), decimal(group(m, 2)), decimal(group(m, 3))
		if mi < 60 && se < 60 {
			total += h*3600 + mi*60 + se
			matched = true
		}
	}
	if matched {
		return &total
	}
	return nil
}
func (p *Core) expiryAt(v any, offset float64) *time.Time {
	wall, zone, ok := parseDeadline(v)
	loc := p.h.Now().Location()
	if !ok {
		if seconds := promotionDuration(v); seconds != nil {
			t := p.h.Now().Add(time.Duration(*seconds) * time.Second)
			return &t
		}
		if truth(v) {
			p.h.Log(context.Background(), "warning", fmt.Sprintf("解析促销截止时间失败，将无法按截止时间删种：%q", v))
		}
		return nil
	}
	if zone != nil {
		t := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), 0, time.FixedZone("source", *zone)).In(loc)
		return &t
	}
	wall = wall.Add(time.Duration(offset * float64(time.Hour)))
	t := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), 0, loc)
	return &t
}
func (p *Core) expiryFromDiff(v, added any) *time.Time {
	seconds := promotionDuration(v)
	n := number(parseNumber(fallback(added, 0)))
	if seconds == nil || n <= 0 {
		return nil
	}
	t := time.Unix(0, int64(n*1e9)).In(p.h.Now().Location()).Add(time.Duration(*seconds) * time.Second)
	return &t
}
func (p *Core) inferClock(pub any, added any) float64 {
	wall, _, ok := parseDeadline(pub)
	if !ok {
		return 0
	}
	n := number(parseNumber(fallback(added, 0)))
	if n <= 0 {
		return 0
	}
	now := p.h.Now()
	stamp := float64(now.UnixNano()) / 1e9
	if n > stamp+60 {
		return 0
	}
	_, offset := now.Zone()
	elapsed := math.Max(stamp-n, 0)
	minutes := math.RoundToEven((float64(offset) - (float64(wall.Unix()) - (stamp - elapsed))) / 60)
	if math.Abs(minutes) > 840 {
		return 0
	}
	return math.RoundToEven(minutes/60) * 60
}
func (p *Core) resolveExpiry(t Task, r Row) (*time.Time, string) {
	if _, _, ok := parseDeadline(r["freedate"]); ok {
		if x := p.expiryAt(r["freedate"], t.N("timezone_offset")); x != nil {
			return x, ""
		}
	}
	if x := p.expiryFromDiff(r["freedate"], r["add_on"]); x != nil {
		return x, "剩余时间"
	}
	if x := p.expiryFromDiff(r["freedate_diff"], r["add_on"]); x != nil {
		return x, "剩余时间"
	}
	return nil, ""
}
func addedFree(r Row) bool {
	if v, ok := r["downloadvolumefactor"]; ok && v != nil {
		n := parseNumber(v)
		if n != nil {
			return number(n) == 0
		}
	}
	s := text(fallback(r["volume_factor"], ""))
	return s == "免费" || s == "2X免费" || s == "4X免费"
}
func iso(t time.Time) string {
	return t.Truncate(time.Microsecond).Format("2006-01-02T15:04:05.999999-07:00")
}
func (p *Core) promotionExpiry(ctx context.Context, t Task, info, r Row, verify bool) (*time.Time, string, error) {
	if truth(r["promotion_ended_at"]) {
		return nil, "probe_ended", nil
	}
	offset := 0.0
	if t.N("timezone_offset") == 0 && !t.B("rss_support") {
		offset = p.inferClock(fallback(info["pubdate"], r["pubdate"]), fallback(info["add_on"], r["add_on"]))
	}
	var expiry *time.Time
	source := ""
	if _, _, ok := parseDeadline(r["freedate"]); offset != 0 && ok {
		expiry = p.expiryAt(r["freedate"], offset/60)
		source = fmt.Sprintf("按发布时间自动校正时区 %+.1f 小时", offset/60)
	} else {
		expiry, source = p.resolveExpiry(t, r)
	}
	now := float64(p.h.Now().UnixNano()) / 1e9
	due := !truth(r["promotion_probe_at"]) || parseNumber(r["promotion_probe_at"]) == nil || now-number(r["promotion_probe_at"]) >= promotionProbe
	if !verify || expiry != nil && !due {
		return expiry, source, nil
	}
	siteOffset := t.N("timezone_offset")
	if offset != 0 {
		siteOffset = offset / 60
	}
	ok, e := p.refreshPromotion(ctx, t, r, siteOffset)
	if e != nil {
		return nil, "", e
	}
	if ok {
		if truth(r["promotion_ended_at"]) {
			return nil, "probe_ended", nil
		}
		expiry = nil
		if _, _, ok := parseDeadline(r["freedate"]); offset != 0 && ok {
			expiry = p.expiryAt(r["freedate"], offset/60)
		}
		if expiry == nil {
			expiry, source = p.resolveExpiry(t, r)
		}
		return expiry, source, nil
	}
	if expiry != nil {
		return expiry, source, nil
	}
	return nil, "", nil
}
func (p *Core) pageDeadline(s string, offset float64) *time.Time {
	for _, name := range []string{"PROMOTION_PAGE_REMAINING_PATTERN", "PROMOTION_PAGE_REMAINING_FALLBACK_PATTERN"} {
		var found *time.Time
		eachMatch(name, s, func(m *pyre.CapturedMatch) bool {
			v := group(m, 1)
			if _, _, ok := parseDeadline(v); ok {
				found = p.expiryAt(v, offset)
				return false
			}
			return true
		})
		if found != nil {
			return found
		}
	}
	return nil
}
func (p *Core) refreshPromotion(ctx context.Context, t Task, r Row, offset float64) (bool, error) {
	now := float64(p.h.Now().UnixNano()) / 1e9
	if truth(r["promotion_ended_at"]) {
		return true, nil
	}
	if !addedFree(r) {
		return false, nil
	}
	if now-number(parseNumber(fallback(r["promotion_probe_at"], 0))) < promotionProbe {
		return false, nil
	}
	r["promotion_probe_at"] = now
	s, e := p.site(ctx, t)
	if e != nil {
		return false, e
	}
	if s == nil || !truth(r["page_url"]) {
		return false, nil
	}
	base := fallback(s.URL, s.Domain)
	u, e := url.Parse(text(base))
	if e != nil {
		return false, nil
	}
	page, e := url.Parse(text(r["page_url"]))
	if e != nil {
		return false, nil
	}
	page = u.ResolveReference(page)
	host := page.Hostname()
	mteam := mustPattern(`^/detail/(\d+)$`)
	mm, _ := mteam.SearchGroups(page.Path, 0)
	if mm != nil {
		hpat := mustPattern(`^(?:kp|api2?)\.m-team\.(?:cc|io)$`)
		if ok, _ := hpat.Search(host); ok {
			return p.refreshMTeam(ctx, t, r, s, group(mm, 1), host, offset, now)
		}
	}
	response, e := p.h.SiteRequest(ctx, s.ID, "GET", page.String(), nil)
	if e != nil {
		p.h.Log(ctx, "error", "读取种子促销信息失败："+e.Error())
		return false, nil
	}
	body := text(response["text"])
	if !truth(response["ok"]) || body == "" {
		return false, nil
	}
	if m := match("PROMOTION_PAGE_OTHER_VERSIONS_PATTERN", body); m != nil {
		body = m.Before
	}
	if !patternMatches("PROMOTION_PAGE_TORRENT_PATTERN", body) {
		p.h.Log(ctx, "warning", fmt.Sprintf("刷流任务读取到的不是种子页面，无法确认促销状态：%s，请检查站点 Cookie 是否有效", text(r["title"])))
		return false, nil
	}
	deadline := p.pageDeadline(body, offset)
	if deadline != nil {
		if deadline.After(p.h.Now()) {
			if !patternMatches("PROMOTION_PAGE_MARKER_PATTERN", body) {
				p.h.Log(ctx, "warning", "刷流任务详情页虽有促销倒计时，但未确认当前种子为免费："+text(r["title"]))
				return false, nil
			}
			r["freedate"] = iso(*deadline)
			r["promotion_verified_at"] = now
			delete(r, "promotion_ended_at")
			p.h.Log(ctx, "info", "刷流任务读取到促销截止时间："+text(r["title"])+" "+text(r["freedate"]))
		} else {
			r["promotion_ended_at"] = now
			p.h.Log(ctx, "info", "刷流任务探测到促销已结束："+text(r["title"]))
		}
		return true, nil
	}
	if patternMatches("PROMOTION_PAGE_NONE_PATTERN", body) && !patternMatches("PROMOTION_PAGE_MARKER_PATTERN", body) {
		r["promotion_ended_at"] = now
		p.h.Log(ctx, "info", "刷流任务探测到站点已无促销："+text(r["title"]))
		return true, nil
	}
	if patternMatches("PROMOTION_PAGE_MARKER_PATTERN", body) || patternMatches("PROMOTION_PAGE_REMAINING_HINT_PATTERN", body) {
		return false, nil
	}
	p.h.Log(ctx, "warning", "刷流任务未能确认促销状态："+text(r["title"])+"，页面既没有促销剩余时间，也没有站点明确的“无优惠”说明")
	return false, nil
}
func (p *Core) refreshMTeam(ctx context.Context, t Task, r Row, s *Site, id, pageHost string, offset, now float64) (bool, error) {
	if !truth(s.Raw["apikey"]) {
		p.h.Log(ctx, "warning", "M-Team 站点未配置 API Key，无法核实种子促销")
		return false, nil
	}
	configured, _ := url.Parse(s.URL)
	host := ""
	if configured != nil {
		host = configured.Hostname()
	}
	ok, _ := mustPattern(`^api2?\.m-team\.(?:cc|io)$`).Search(host)
	if !ok {
		parts := strings.Split(pageHost, ".")
		host = "api.m-team." + parts[len(parts)-1]
	}
	result, e := p.h.SiteRequest(ctx, s.ID, "POST", "https://"+host+"/api/torrent/detail", Row{"id": id})
	if e != nil {
		p.h.Log(ctx, "error", "读取 M-Team 种子促销信息失败："+e.Error())
		return false, nil
	}
	payload := row(result["json"])
	if text(payload["code"]) != "0" {
		return false, nil
	}
	data := row(payload["data"])
	if text(data["id"]) != id {
		return false, nil
	}
	status, ok := data["status"].(map[string]any)
	if !ok {
		if v, ok := data["status"].(Row); ok {
			status = v
		} else {
			return false, nil
		}
	}
	discount, raw := status["discount"], status["discountEndTime"]
	if v, ok := status["promotionRule"].(map[string]any); ok {
		discount, raw = v["discount"], v["endTime"]
	} else if v, ok := status["promotionRule"].(Row); ok {
		discount, raw = v["discount"], v["endTime"]
	}
	mall := row(status["mallSingleFree"])
	if mall["status"] == "ONGOING" {
		discount, raw = "FREE", mall["endDate"]
	}
	if discount == nil {
		return false, nil
	}
	d := strings.ReplaceAll(strings.ToUpper(text(discount)), "_", "")
	if d != "FREE" && d != "2XFREE" && d != "FREE2X" {
		r["promotion_ended_at"] = now
		return true, nil
	}
	if _, _, ok := parseDeadline(raw); !ok {
		return false, nil
	}
	deadline := p.expiryAt(raw, offset)
	if deadline == nil {
		return false, nil
	}
	if !deadline.After(p.h.Now()) {
		r["promotion_ended_at"] = now
		return true, nil
	}
	r["freedate"] = iso(*deadline)
	r["promotion_verified_at"] = now
	delete(r, "promotion_ended_at")
	return true, nil
}
func (p *Core) verifyCandidate(ctx context.Context, t Task, r Row) (bool, error) {
	offset := t.N("timezone_offset")
	if offset == 0 && !t.B("rss_support") {
		offset = p.inferClock(r["pubdate"], r["add_on"]) / 60
	}
	ok, e := p.refreshPromotion(ctx, t, r, offset)
	if e != nil || !ok {
		return false, e
	}
	if truth(r["promotion_ended_at"]) || !truth(r["promotion_verified_at"]) {
		return false, nil
	}
	expiry, _ := p.resolveExpiry(t, r)
	return expiry != nil && p.h.Now().Add(600*time.Second).Before(*expiry), nil
}
func complete(r Row) bool {
	if v, ok := r["progress"]; ok && v != nil {
		n := parseNumber(v)
		return n != nil && number(n) >= 1
	}
	size := number(fallback(r["total_size"], fallback(r["size"], 0)))
	downloaded := parseNumber(fallback(r["downloaded"], 0))
	return size > 0 && downloaded != nil && number(downloaded) >= size
}
func (p *Core) promotionExpired(ctx context.Context, t Task, info, r Row) (bool, string, error) {
	if !t.B("del_no_free") || complete(info) {
		return false, "", nil
	}
	expiry, source, e := p.promotionExpiry(ctx, t, info, r, true)
	if e != nil {
		return false, "", e
	}
	if source == "probe_ended" {
		return true, "站点已无促销且下载未完成", nil
	}
	if expiry != nil {
		if !p.h.Now().Add(600 * time.Second).Before(*expiry) {
			reason := "促销即将结束或已过期"
			if source != "" {
				reason += "（" + source + "）"
			}
			return true, reason, nil
		}
		return false, "", nil
	}
	if t.N("promo_max_hours") != 0 {
		n := number(parseNumber(fallback(info["add_on"], r["add_on"])))
		if n > 0 && float64(p.h.Now().UnixNano())/1e9-n >= t.N("promo_max_hours")*3600 {
			return true, fmt.Sprintf("促销截止时间未知，超过兜底时长 %s 小时仍未完成下载", text(t.Row["promo_max_hours"])), nil
		}
	}
	p.warnPromotion(ctx, info, r)
	return false, "", nil
}
func (p *Core) warnPromotion(ctx context.Context, info, r Row) {
	key := text(fallback(info["hash"], fallback(r["hash"], r["title"])))
	now := float64(p.h.Now().UnixNano()) / 1e9
	p.mu.Lock()
	if now-p.warned[key] < 21600 {
		p.mu.Unlock()
		return
	}
	p.warned[key] = now
	p.mu.Unlock()
	p.h.Log(ctx, "warning", fmt.Sprintf("刷流任务删除促销过期种子：%s 的促销截止时间缺失或无法解析（freedate=%q，freedate_diff=%q），未完成下载将先暂停，请检查站点时区、Cookie 或站点促销选择器配置", text(r["title"]), r["freedate"], r["freedate_diff"]))
}
func (p *Core) NextPromotion(ctx context.Context, t Task) (*time.Time, error) {
	if !t.B("del_no_free") {
		return nil, nil
	}
	rs, e := p.records(ctx, t.ID(), "torrents")
	if e != nil {
		return nil, e
	}
	now := p.h.Now()
	var next *time.Time
	add := func(t time.Time) {
		if next == nil || t.Before(*next) {
			v := t
			next = &v
		}
	}
	for _, k := range rs.Order {
		r := rs.Values[k]
		if truth(r["deleted"]) || complete(r) {
			continue
		}
		if !truth(r["promotion_verified_at"]) && !truth(r["promotion_paused_at"]) {
			add(now)
			continue
		}
		expiry, source, e := p.promotionExpiry(ctx, t, r, r, false)
		if e != nil {
			return nil, e
		}
		if source == "probe_ended" {
			add(now)
		} else if expiry != nil {
			add(expiry.Add(-600 * time.Second))
		}
		if truth(r["promotion_verified_at"]) && source != "probe_ended" {
			last := number(parseNumber(r["promotion_probe_at"]))
			if last > 0 {
				add(time.Unix(0, int64((last+promotionProbe)*1e9)).In(now.Location()))
			} else {
				add(now)
			}
		}
	}
	return next, nil
}
func (p *Core) CheckPromotion(ctx context.Context, id string) error {
	t, ok := p.task(id)
	if !ok || !p.Enabled() || !t.B("enabled") || !t.B("del_no_free") {
		return nil
	}
	next, e := p.NextPromotion(ctx, t)
	if e != nil {
		return e
	}
	if next != nil && !next.After(p.h.Now()) {
		return p.Check(ctx, id, true)
	}
	return nil
}
