// Package subscribegroup implements the installed SubscribeGroup 2.8.7 rules.
// It plans field updates only. Database authority and durable event receipts
// belong to the host; this package cannot issue requests or change subscriptions.
package subscribegroup

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type Configuration struct {
	Enabled       bool     `json:"enabled"`
	Category      bool     `json:"category"`
	Clear         bool     `json:"clear"`
	ClearHandle   bool     `json:"clear_handle"`
	UpdateDetails []string `json:"update_details"`
	UpdateConfs   *string  `json:"update_confs"`
}
type Site struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}
type Rule struct {
	Resolution   *string  `json:"resolution"`
	Quality      *string  `json:"quality"`
	Effect       *string  `json:"effect"`
	Include      *string  `json:"include"`
	Exclude      *string  `json:"exclude"`
	SavePath     *string  `json:"savepath"`
	Sites        []string `json:"sites"`
	FilterGroups []string `json:"filter_groups"`
}
type Subscription struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Year         any      `json:"year"`
	Type         string   `json:"type"`
	TMDBID       string   `json:"tmdbid"`
	Season       int      `json:"season"`
	Resolution   string   `json:"resolution"`
	Quality      string   `json:"quality"`
	Effect       string   `json:"effect"`
	Include      string   `json:"include"`
	Exclude      string   `json:"exclude"`
	SavePath     string   `json:"save_path"`
	Sites        []string `json:"sites"`
	FilterGroups []string `json:"filter_groups"`
}
type Metadata struct {
	Resolution    string `json:"resource_pix"`
	Quality       string `json:"resource_type"`
	Effect        string `json:"resource_effect"`
	Team          string `json:"resource_team"`
	Customization string `json:"customization"`
}
type Download struct {
	Hash       string    `json:"hash"`
	Type       string    `json:"type"`
	TMDBID     string    `json:"tmdbid"`
	Seasons    string    `json:"seasons"`
	SiteID     string    `json:"site_id"`
	Metadata   *Metadata `json:"metadata"`
	HasContext bool      `json:"has_context"`
	HasHistory bool      `json:"has_history"`
}
type History struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Time    string `json:"time"`
}
type Update struct {
	ID     string         `json:"id"`
	Fields map[string]any `json:"fields"`
}
type field struct {
	key   string
	value any
}

// Machine retains the source's category cache across nonempty reconfiguration.
// A host carries it across isolated worker calls within one application
// session. A cold application start rebuilds from the then-current config,
// just as the original long-lived plugin object starts with an empty cache.
type Machine struct {
	Config       Configuration   `json:"config"`
	Rules        map[string]Rule `json:"rules"`
	History      []History       `json:"history"`
	Handled      []string        `json:"handled"`
	Updates      []Update        `json:"updates"`
	ConfigWrites int             `json:"config_writes"`
}

func New() *Machine {
	return &Machine{Config: Configuration{UpdateDetails: []string{}}, Rules: map[string]Rule{}, Updates: []Update{}}
}
func (m *Machine) State() bool { return m.Config.Enabled || m.Config.Category }
func (m *Machine) Init(config *Configuration, sites []Site) {
	if config == nil {
		return
	}
	m.Config = *config
	m.Config.UpdateDetails = slices.Clone(config.UpdateDetails)
	if m.Config.UpdateDetails == nil {
		m.Config.UpdateDetails = []string{}
	}
	if config.UpdateConfs != nil && *config.UpdateConfs != "" {
		if m.Rules == nil {
			m.Rules = map[string]Rule{}
		}
		for category, rule := range ParseRules(*config.UpdateConfs, sites) {
			m.Rules[category] = rule
		}
	} else {
		m.Rules = map[string]Rule{}
	}
	if m.Config.ClearHandle {
		m.Handled = nil
		m.Config.ClearHandle = false
		m.ConfigWrites++
	}
	if m.Config.Clear {
		m.History = nil
		m.Config.Clear = false
		m.ConfigWrites++
	}
}
func ParseRules(text string, sites []Site) map[string]Rule {
	rules := map[string]Rule{}
	for _, line := range strings.Split(text, "\n") {
		category := ""
		rule := Rule{Sites: []string{}, FilterGroups: []string{}}
		for _, part := range strings.Split(line, "#") {
			key, value, ok := strings.Cut(part, ":")
			if !ok {
				continue
			}
			v := value
			switch key {
			case "category":
				category = value
			case "resolution":
				rule.Resolution = &v
			case "quality":
				rule.Quality = &v
			case "effect":
				rule.Effect = &v
			case "include":
				rule.Include = &v
			case "exclude":
				rule.Exclude = &v
			case "savepath":
				rule.SavePath = &v
			case "filter_groups":
				rule.FilterGroups = strings.Split(value, ",")
			case "sites":
				for _, name := range strings.Split(value, ",") {
					for _, site := range sites {
						if site.Active && site.Name == name {
							rule.Sites = append(rule.Sites, site.ID)
							break
						}
					}
				}
			}
		}
		if category != "" {
			for _, name := range strings.Split(category, ",") {
				rules[name] = rule
			}
		}
	}
	return rules
}
func has(value *string) bool { return value != nil && *value != "" }
func (m *Machine) SubscriptionAdded(sub *Subscription, category string, at string) bool {
	if !m.Config.Category || len(m.Rules) == 0 || sub == nil || category == "" {
		return false
	}
	rule, found := m.Rules[category]
	if !found {
		return false
	}
	fields := []field{}
	if has(rule.Include) {
		fields = append(fields, field{"include", *rule.Include})
	}
	if has(rule.Exclude) {
		fields = append(fields, field{"exclude", *rule.Exclude})
	}
	if len(rule.Sites) > 0 {
		fields = append(fields, field{"sites", rule.Sites})
	}
	if len(rule.FilterGroups) > 0 {
		fields = append(fields, field{"filter_groups", rule.FilterGroups})
	}
	if has(rule.Resolution) {
		fields = append(fields, field{"resolution", ParseResolution(*rule.Resolution)})
	}
	if has(rule.Quality) {
		fields = append(fields, field{"quality", ParseQuality(*rule.Quality)})
	}
	if has(rule.Effect) {
		fields = append(fields, field{"effect", ParseEffect(*rule.Effect)})
	}
	if has(rule.SavePath) {
		year := fmt.Sprint(sub.Year)
		if sub.Year == nil {
			year = "None"
		}
		fields = append(fields, field{"save_path", strings.ReplaceAll(*rule.SavePath, "{name}", sub.Name+" ("+year+")")})
	}
	m.record(*sub, fields, "二级分类自定义配置 "+category, at)
	return true // The original also writes/history-records an empty patch.
}
func (m *Machine) DownloadAdded(download Download, subscriptions []Subscription, rssSites []string, at string) error {
	if !m.Config.Enabled || len(m.Config.UpdateDetails) == 0 || download.Hash == "" || !download.HasContext || !download.HasHistory {
		return nil
	}
	key := download.Type + ":" + download.TMDBID
	if slices.Contains(m.Handled, key) || download.Type != "电视剧" {
		return nil
	}
	var season *int
	if download.Seasons != "" && !strings.Contains(download.Seasons, "-") {
		n, err := strconv.Atoi(strings.TrimSpace(strings.ReplaceAll(download.Seasons, "S", "")))
		if err != nil {
			return errors.New("ValueError")
		}
		season = &n
	}
	for _, sub := range subscriptions {
		if sub.TMDBID != download.TMDBID || season != nil && sub.Season != *season || sub.Type != "电视剧" {
			continue
		}
		fields := []field{}
		if meta := download.Metadata; meta != nil {
			if slices.Contains(m.Config.UpdateDetails, "分辨率") && sub.Resolution == "" && meta.Resolution != "" {
				fields = append(fields, field{"resolution", ParseResolution(meta.Resolution)})
			}
			if slices.Contains(m.Config.UpdateDetails, "资源质量") && sub.Quality == "" && meta.Quality != "" {
				fields = append(fields, field{"quality", ParseQuality(meta.Quality)})
			}
			if slices.Contains(m.Config.UpdateDetails, "特效") && sub.Effect == "" && meta.Effect != "" {
				fields = append(fields, field{"effect", ParseEffect(meta.Effect)})
			}
			if slices.Contains(m.Config.UpdateDetails, "制作组") && sub.Include == "" {
				team := meta.Team
				if team != "" && meta.Customization != "" {
					team = meta.Customization + ".+" + team
				} else if team == "" {
					team = meta.Customization
				}
				if team != "" {
					fields = append(fields, field{"include", team})
				}
			}
		}
		if slices.Contains(m.Config.UpdateDetails, "站点") && len(sub.Sites) == 0 && download.SiteID != "" && download.SiteID != "0" && slices.Contains(rssSites, download.SiteID) {
			fields = append(fields, field{"sites", []string{download.SiteID}})
		}
		if len(fields) == 0 {
			continue
		}
		m.record(sub, fields, "种子下载自定义配置", at)
		// Deliberately once per changed subscription, not once per event/season.
		m.Handled = append(m.Handled, key)
	}
	return nil
}
func (m *Machine) record(sub Subscription, fields []field, kind, at string) {
	patch := map[string]any{}
	for _, item := range fields {
		patch[item.key] = item.value
	}
	m.Updates = append(m.Updates, Update{ID: sub.ID, Fields: patch})
	m.History = append(m.History, History{Name: sub.Name, Type: kind, Content: dumpFields(fields), Time: at})
}

var resolutionRules = []string{`1080[pi]|x1080`, `4K|2160p|x2160`, `720[pi]|x720`}
var qualityRules = []string{`Blu-?Ray.+VC-?1|Blu-?Ray.+AVC|UHD.+blu-?ray.+HEVC|MiniBD`, `Remux`, `Blu-?Ray`, `UHD|UltraHD`, `WEB-?DL|WEB-?RIP`, `HDTV`, `[Hx].?265|HEVC`, `[Hx].?264|AVC`}

// Raw Python match patterns and ordinary replacement literals have different escaping.
var effectMatchRules = []string{`Dolby[\\s.]+Vision|DOVI|[\\s.]+DV[\\s.]+`, `Dolby[\\s.]*\+?Atmos|Atmos`, `[\\s.]+HDR[\\s.]+|HDR10|HDR10\+`, `[\\s.]+SDR[\\s.]+`}
var effectRules = []string{`Dolby[\s.]+Vision|DOVI|[\s.]+DV[\s.]+`, `Dolby[\s.]*\+?Atmos|Atmos`, `[\s.]+HDR[\s.]+|HDR10|HDR10\+`, `[\s.]+SDR[\s.]+`}

func compileRules(expressions []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(expressions))
	for i, s := range expressions {
		out[i] = regexp.MustCompile("(?i)^(?:" + s + ")")
	}
	return out
}

var resolutionMatchers = compileRules(resolutionRules)
var qualityMatchers = compileRules(qualityRules)
var effectMatchers = compileRules(effectMatchRules)

// The pinned source patterns are ASCII and use Python re.IGNORECASE. Go's
// Unicode simple folding already includes long s and Kelvin K, but unlike
// Python it does not join dotted/dotless I to ASCII i. Fold these two runes
// only in the match input: unmatched user text must remain byte-for-byte
// unchanged. This is not a general Python regular-expression translator.
var parserCaseInput = strings.NewReplacer("\u0130", "i", "\u0131", "i")

func ParseResolution(value string) string {
	for i, re := range resolutionMatchers {
		if re.MatchString(parserCaseInput.Replace(value)) {
			return resolutionRules[i]
		}
	}
	return value
}
func normalize(value string, expressions []string, matchers []*regexp.Regexp) string {
	for i, re := range matchers {
		if re.MatchString(parserCaseInput.Replace(value)) {
			value = expressions[i]
		}
	}
	return value
}
func ParseQuality(value string) string { return normalize(value, qualityRules, qualityMatchers) }
func ParseEffect(value string) string  { return normalize(value, effectRules, effectMatchers) }

// Python's json.dumps keeps insertion order, spaces and ensure_ascii=True.
// History content is user-visible in the source, not just a transport object.
func dumpFields(fields []field) string {
	parts := make([]string, len(fields))
	for i, item := range fields {
		parts[i] = asciiString(item.key) + ": " + dumpValue(item.value)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
func dumpValue(value any) string {
	switch v := value.(type) {
	case string:
		return asciiString(v)
	case []string:
		parts := make([]string, len(v))
		for i, s := range v {
			parts[i] = asciiString(s)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		raw, _ := json.Marshal(value)
		return string(raw)
	}
}
func asciiString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 32 || r > 126 {
				if r <= 0xffff {
					fmt.Fprintf(&b, `\u%04x`, r)
				} else {
					n := r - 0x10000
					fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+n/1024, 0xdc00+n%1024)
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
