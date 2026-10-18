package seedrescue

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Configuration struct {
	Enabled, Notify, ConfirmDownloadRisk, RunOnce  bool
	Cron, Token, Downloader, SelectionMode         string
	TargetSiteID                                   int
	RSSURLs                                        []string
	SourceSiteIDs                                  []int
	MinimumSeeders, MaxDownloadCount, InitialLimit *int
	AllowedPromotions                              []string
	SavePath                                       *string
}

func Lines(v any) []string {
	rows := array(v)
	if rows == nil {
		rows = []any{}
		for _, line := range strings.FieldsFunc(strOr(v, ""), func(r rune) bool {
			return r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == 0x1c || r == 0x1d || r == 0x1e || r == 0x85 || r == 0x2028 || r == 0x2029
		}) {
			rows = append(rows, line)
		}
	}
	out := []string{}
	for _, r := range rows {
		line := strings.TrimSpace(pyString(r))
		if line != "" {
			out = append(out, line)
		}
	}
	return uniqueStrings(out)
}
func PositiveInt(v any, fallback int) int {
	n := asInt(v, fallback)
	if n <= 0 {
		return fallback
	}
	return n
}
func OptionalLimit(v any, fallback int) *int {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
		return nil
	}
	if _, ok := v.(bool); ok {
		return ptr(fallback)
	}
	if str, ok := v.(string); ok {
		n, valid := byteInteger([]byte(str))
		if !valid || !n.IsInt64() {
			return ptr(fallback)
		}
		if n.Sign() < 0 || int64(int(n.Int64())) != n.Int64() {
			return ptr(fallback)
		}
		if n.Sign() == 0 {
			return nil
		}
		return ptr(int(n.Int64()))
	}
	f, ok := floatValue(v)
	if !ok || math.IsInf(f, 0) || math.IsNaN(f) || f < 0 || f != math.Trunc(f) || f > float64(math.MaxInt) {
		return ptr(fallback)
	}
	if f == 0 {
		return nil
	}
	return ptr(int(f))
}
func ParseConfig(v Object) Configuration {
	c := Configuration{Enabled: truth(v["enabled"]), Notify: true, ConfirmDownloadRisk: true, RunOnce: truth(v["onlyonce"]), Cron: strings.TrimSpace(strOr(v["cron"], "*/30 * * * *")), Token: strings.TrimSpace(strOr(v["token"], "")), TargetSiteID: PositiveInt(v["target_site_id"], 0), RSSURLs: Lines(v["rss_urls"]), Downloader: strings.TrimSpace(strOr(v["downloader"], "")), SelectionMode: strings.ToLower(strings.TrimSpace(strOr(v["selection_mode"], "manual")))}
	if n, exists := v["notify"]; exists {
		c.Notify = truth(n)
	}
	if b, ok := v["confirm_download_risk"].(bool); ok && !b {
		c.ConfirmDownloadRisk = false
	}
	c.SourceSiteIDs = []int{}
	seen := map[int]bool{}
	for _, v := range array(v["source_site_ids"]) {
		id := PositiveInt(v, 0)
		if id != 0 && id != c.TargetSiteID && !seen[id] {
			c.SourceSiteIDs = append(c.SourceSiteIDs, id)
			seen[id] = true
		}
	}
	c.MinimumSeeders = OptionalLimit(v["minimum_seeders"], 1)
	c.MaxDownloadCount = OptionalLimit(v["max_download_count"], 2)
	c.InitialLimit = OptionalLimit(v["initial_limit"], 20)
	p := v["allowed_promotions"]
	if p == nil {
		p = []string{Free, NonFree}
	}
	c.AllowedPromotions = NormalizePromotions(p)
	if s := strings.TrimSpace(strOr(v["save_path"], "")); s != "" {
		c.SavePath = &s
	}
	if !contains([]string{"manual", "auto", "hybrid"}, c.SelectionMode) {
		c.SelectionMode = "manual"
	}
	return c
}
func (c Configuration) Current() Object {
	return Object{"enabled": c.Enabled, "notify": c.Notify, "confirm_download_risk": c.ConfirmDownloadRisk, "onlyonce": false, "cron": c.Cron, "token": c.Token, "target_site_id": c.TargetSiteID, "rss_urls": strings.Join(c.RSSURLs, "\n"), "source_site_ids": c.SourceSiteIDs, "minimum_seeders": deref(c.MinimumSeeders), "allowed_promotions": c.AllowedPromotions, "downloader": c.Downloader, "save_path": strOr(deref(c.SavePath), ""), "max_download_count": deref(c.MaxDownloadCount), "initial_limit": deref(c.InitialLimit), "selection_mode": c.SelectionMode}
}
func ParseSelection(raw []byte) (Selection, error) {
	var fields Object
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return Selection{}, fault("ValidationError", "selection", "无效的候选参数")
	}
	id, ok := fields["candidate_id"].(string)
	if !ok || utf8.RuneCountInString(id) < 1 || utf8.RuneCountInString(id) > 160 {
		return Selection{}, fault("ValidationError", "selection", "候选标识长度必须为1到160")
	}
	out := Selection{CandidateID: id}
	if f := fields["confirmed_download_factor"]; f != nil {
		n, ok := floatValue(f)
		if !ok || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return Selection{}, fault("ValidationError", "selection", "下载系数必须为非负有限数值")
		}
		out.ConfirmedDownloadFactor = &n
	}
	for key, target := range map[string]*bool{"confirm_unknown": &out.ConfirmUnknown, "skip_future_risk_confirmation": &out.SkipFutureRiskConfirmation} {
		if v, exists := fields[key]; exists {
			b, ok := modelBool(v)
			if !ok {
				return Selection{}, fault("ValidationError", "selection", "确认字段必须为布尔值")
			}
			*target = b
		}
	}
	return out, nil
}
func modelBool(v any) (bool, bool) {
	if b, ok := v.(bool); ok {
		return b, true
	}
	if s, ok := v.(string); ok {
		switch strings.ToLower(s) {
		case "0", "off", "f", "false", "n", "no":
			return false, true
		case "1", "on", "t", "true", "y", "yes":
			return true, true
		}
		return false, false
	}
	if f, ok := floatValue(v); ok && (f == 0 || f == 1) {
		return f == 1, true
	}
	return false, false
}
func candidateID(c Candidate) string {
	return strconv.Itoa(c.SiteID) + ":" + c.Match.TorrentID + ":" + strings.ToLower(c.Match.InfoHash)
}
func targetCandidateID(job Object) string {
	return "target:" + pyString(job["target_site_id"]) + ":" + pyString(job["target_hash"])
}
