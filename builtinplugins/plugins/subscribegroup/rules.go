package subscribegroup

import (
	"regexp"
	"strings"
)

type Site struct {
	ID       string `json:"id"`
	SourceID int64  `json:"source_id"`
	Name     string `json:"name"`
	Active   bool   `json:"active"`
}
type Rule struct {
	Resolution   *string  `json:"resolution"`
	Quality      *string  `json:"quality"`
	Effect       *string  `json:"effect"`
	Include      *string  `json:"include"`
	Exclude      *string  `json:"exclude"`
	SavePath     *string  `json:"savepath"`
	Sites        []string `json:"sites"`
	SourceSites  []int64  `json:"source_sites"`
	FilterGroups []string `json:"filter_groups"`
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
							rule.SourceSites = append(rule.SourceSites, site.SourceID)
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
