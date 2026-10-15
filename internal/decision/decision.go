// Package decision evaluates a secret-free resource against an explicit media
// identity and authoritative episode sets. Calendar rules never create episodes.
package decision

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"light-media/internal/catalog"
	"light-media/internal/media"
	"light-media/internal/sites"
)

type Policy struct {
	Source               string   `json:"source"`
	HDR                  string   `json:"hdr"`
	Languages            []string `json:"languages"`
	Subtitles            []string `json:"subtitles"`
	PreferredGroups      []string `json:"preferred_groups"`
	PreferredResolutions []string `json:"preferred_resolutions"`
	MinSeeders           int      `json:"min_seeders"`
	FreeOnly             bool     `json:"free_only"`
	Resolution           string   `json:"resolution"`
	Codec                string   `json:"codec"`
	MinBytes             int64    `json:"min_bytes"`
	MaxBytes             int64    `json:"max_bytes"`
	Include              string   `json:"include"`
	Exclude              string   `json:"exclude"`
}
type Target struct {
	Media         catalog.Media       `json:"media"`
	Season        int                 `json:"season"`
	SeasonYear    int                 `json:"season_year"`
	Episodes      []int               `json:"episodes"`
	KnownEpisodes []int               `json:"known_episodes"`
	AllowEarly    []int               `json:"allow_early"`
	AiredEpisodes []int               `json:"aired_episodes"`
	Mappings      []media.MappingRule `json:"mappings"`
	Auto          bool                `json:"auto"`
}
type Decision struct {
	Accepted          bool     `json:"accepted"`
	Reasons           []string `json:"reasons"`
	Season            int      `json:"season"`
	Episodes          []int    `json:"episodes"`
	NeedsConfirmation bool     `json:"needs_confirmation"`
}

var (
	yearMarker       = regexp.MustCompile(`(?:19|20)[0-9]{2}`)
	epMarker         = regexp.MustCompile(`(?i)(?:S[0-9]{1,2}E[0-9]{1,3}|EP\s*[0-9]{1,4}|第\s*[0-9一二三四五六七八九十百零〇两]+\s*[集话])`)
	seasonPack       = regexp.MustCompile(`(?i)(?:^|[ ._\-])S([0-9]{1,2})(?:$|[ ._\-])`)
	qualityMarker    = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(?:4320p|2160p|1080p|1080i|720p|576p|480p|4k|8k|web[ ._-]?dl|webrip|blu[ ._-]?ray|bdrip|hdtv|remux|x26[45]|h[ ._-]?26[45]|hevc|avc|av1)(?:$|[^A-Za-z0-9])`)
	resolutionMarker = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(4320p|2160p|1080p|1080i|720p|576p|480p|4k|8k)(?:$|[^A-Za-z0-9])`)
	codecMarker      = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(x265|h[ ._-]?265|hevc|x264|h[ ._-]?264|avc|av1|vc[ ._-]?1)(?:$|[^A-Za-z0-9])`)
	leadingGroup     = regexp.MustCompile(`^\s*(?:\[[^\]\r\n]{1,128}\]|【[^】\r\n]{1,128}】)\s*`)
	groupSuffix      = regexp.MustCompile(`-([A-Za-z][A-Za-z0-9_.]{1,63})$`)
)

// Evaluate distinguishes hard rejection from an explicit manual confirmation.
// A confirmation item can be previewed manually, but is never auto accepted.
func Evaluate(resource sites.Resource, target Target, policy Policy) Decision {
	out := Decision{Accepted: true, Reasons: []string{}, Season: target.Season, Episodes: []int{}}
	hard := false
	reason := func(value string) {
		for _, old := range out.Reasons {
			if old == value {
				return
			}
		}
		out.Reasons = append(out.Reasons, value)
	}
	reject := func(value string) { hard = true; reason(value) }
	review := func(value string) { out.NeedsConfirmation = true; reason(value) }
	finish := func() Decision { out.Accepted = !hard && (!target.Auto || !out.NeedsConfirmation); return out }
	if target.Media.ID <= 0 || (target.Media.MediaType != "movie" && target.Media.MediaType != "tv") || (normalizeTitle(target.Media.Title) == "" && normalizeTitle(target.Media.OriginalTitle) == "") {
		reject("invalid_media_identity")
		return finish()
	}
	if !checkResourcePolicy(resource, policy, reject) {
		return finish()
	}
	matched, year := titleIdentity(resource.Title, target.Media)
	if !matched {
		reject("title_mismatch")
		return finish()
	}
	if target.Media.MediaType == "movie" {
		out.Season = 0
		if epMarker.MatchString(resource.Title) || seasonPack.MatchString(resource.Title) {
			reject("series_numbering_on_movie")
		}
		if year == 0 || target.Media.Year == 0 {
			review("movie_year_unconfirmed")
		} else if year != target.Media.Year {
			review("movie_year_conflict")
		}
		return finish()
	}
	if target.Season <= 0 || target.Season > 99 || len(target.Mappings) > 128 {
		reject("invalid_target_season")
		return finish()
	}
	known, ok := episodeSet(target.KnownEpisodes)
	if !ok {
		reject("invalid_known_episodes")
		return finish()
	}
	wanted, ok := episodeSet(target.Episodes)
	if !ok {
		reject("invalid_target_episodes")
		return finish()
	}
	aired, ok := episodeSet(target.AiredEpisodes)
	if !ok {
		reject("invalid_aired_episodes")
		return finish()
	}
	early, ok := episodeSet(target.AllowEarly)
	if !ok {
		reject("invalid_early_episodes")
		return finish()
	}
	for _, set := range []map[int]bool{wanted, aired, early} {
		for ep := range set {
			if !known[ep] {
				reject("episode_not_in_authoritative_metadata")
			}
		}
	}
	if hard {
		return finish()
	}
	if len(known) == 0 {
		review("episode_metadata_unavailable")
		return finish()
	}
	if target.Auto && len(wanted) == 0 {
		reject("automatic_target_episodes_required")
		return finish()
	}
	if year != 0 && target.Media.Year != 0 && year != target.Media.Year && year != target.SeasonYear {
		review("series_year_conflict")
	}
	filename := resourceFilename(resource.Title)
	parsed := media.Parse(filename)
	if parsed.Status != media.Parsed || parsed.Type != "episode" || parsed.Original == nil {
		if m := seasonPack.FindStringSubmatch(resource.Title); len(m) > 1 {
			season, _ := strconv.Atoi(m[1])
			out.Season = season
			if season != target.Season {
				review("resource_season_outside_target")
			}
			review("season_pack_requires_confirmation")
		} else {
			review("episode_numbering_unresolved")
		}
		return finish()
	}
	authoritative := make([]media.Episode, 0, len(known))
	for ep := range known {
		authoritative = append(authoritative, media.Episode{Season: target.Season, Episode: ep})
	}
	source := media.Source{Site: resource.SiteID}
	if m := groupSuffix.FindStringSubmatch(strings.TrimSuffix(filename, ".mkv")); len(m) > 1 {
		source.Group = m[1]
	}
	mapping := media.Resolve(fmt.Sprintf("tv:%d", target.Media.ID), *parsed.Original, source, target.Mappings, authoritative, nil)
	if mapping.Status != media.Parsed {
		review("numbering_" + mapping.Reason)
		return finish()
	}
	for _, ep := range mapping.Standard {
		if ep.Season != target.Season {
			review("resource_season_outside_target")
		}
		out.Season = ep.Season
		out.Episodes = append(out.Episodes, ep.Episode)
		if len(wanted) > 0 && !wanted[ep.Episode] {
			review("resource_episode_outside_target")
		}
		if !aired[ep.Episode] && !early[ep.Episode] {
			review("episode_not_confirmed_aired")
		}
	}
	sort.Ints(out.Episodes)
	return finish()
}

// EvaluateResource applies release filters without inventing a media identity.
// Acceptance here only describes resource policy; it never authorizes automatic
// media matching or file organization.
func EvaluateResource(resource sites.Resource, policy Policy) Decision {
	out := Decision{Accepted: true, Reasons: []string{}, Episodes: []int{}}
	reject := func(reason string) {
		out.Accepted = false
		for _, old := range out.Reasons {
			if old == reason {
				return
			}
		}
		out.Reasons = append(out.Reasons, reason)
	}
	checkResourcePolicy(resource, policy, reject)
	return out
}

// False preserves the two early returns used by identity-aware Evaluate.
func checkResourcePolicy(resource sites.Resource, policy Policy, reject func(string)) bool {
	if len(resource.Title) == 0 || len(resource.Title) > 4096 || len(resource.Subtitle) > 4096 || strings.ContainsAny(resource.Title+resource.Subtitle, "\x00\r\n") {
		reject("invalid_resource_title")
		return false
	}
	if policy.MinSeeders < 0 || policy.MinBytes < 0 || policy.MaxBytes < 0 || (policy.MaxBytes > 0 && policy.MinBytes > policy.MaxBytes) {
		reject("invalid_policy")
	}
	if len(policy.Resolution) > 32 || len(policy.Codec) > 32 {
		reject("invalid_policy_quality")
		return false
	}
	// Zero is an explicit useful minimum. Unknown counts use the adapter's -1.
	if resource.Seeders < 0 {
		reject("seeders_unknown")
	} else if resource.Seeders < policy.MinSeeders {
		reject("insufficient_seeders")
	}
	if resource.Size <= 0 {
		reject("resource_size_unknown")
	} else {
		if policy.MinBytes > 0 && resource.Size < policy.MinBytes {
			reject("below_minimum_size")
		}
		if policy.MaxBytes > 0 && resource.Size > policy.MaxBytes {
			reject("above_maximum_size")
		}
	}
	if policy.FreeOnly {
		if !resource.Free || resource.DownloadFactor != 0 {
			reject("free_promotion_not_confirmed")
		}
		if resource.PromotionEnd != "" {
			expires, err := time.Parse(time.RFC3339, resource.PromotionEnd)
			if err != nil {
				reject("promotion_expiry_unknown")
			} else if !expires.After(time.Now()) {
				reject("free_promotion_expired")
			}
		}
	}
	text := resource.Title + " " + resource.Subtitle
	for _, filter := range []struct {
		name, expression string
		include          bool
	}{{"include", policy.Include, true}, {"exclude", policy.Exclude, false}} {
		if filter.expression == "" {
			continue
		}
		if len(filter.expression) > 512 {
			reject("invalid_" + filter.name + "_regex")
			continue
		}
		r, err := regexp.Compile(filter.expression)
		if err != nil {
			reject("invalid_" + filter.name + "_regex")
			continue
		}
		if filter.include && !r.MatchString(text) {
			reject("include_not_matched")
		}
		if !filter.include && r.MatchString(text) {
			reject("excluded_by_pattern")
		}
	}
	checkQuality(resource, policy, text, reject)
	checkTags(resource, policy, reject)
	return true
}

func episodeSet(values []int) (map[int]bool, bool) {
	if len(values) > 10000 {
		return nil, false
	}
	out := map[int]bool{}
	for _, ep := range values {
		if ep <= 0 || ep > 9999 || out[ep] {
			return nil, false
		}
		out[ep] = true
	}
	return out, true
}
func normalizeTitle(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}
func matchesTitle(value string, m catalog.Media) bool {
	aliases := []string{normalizeTitle(m.Title), normalizeTitle(m.OriginalTitle)}
	for i, alias := range m.Aliases {
		if i >= 32 {
			break
		}
		if len(alias) <= 512 {
			aliases = append(aliases, normalizeTitle(alias))
		}
	}
	for n := 0; n < 3; n++ {
		candidate := normalizeTitle(value)
		for _, a := range aliases {
			if a != "" && candidate == a {
				return true
			}
		}
		if aliases[0] != "" && aliases[1] != "" && (candidate == aliases[0]+aliases[1] || candidate == aliases[1]+aliases[0]) {
			return true
		}
		prefix := leadingGroup.FindStringIndex(value)
		if prefix == nil {
			return false
		}
		value = value[prefix[1]:]
	}
	return false
}
func markerCut(value string) int {
	cut := len(value)
	for _, r := range []*regexp.Regexp{epMarker, seasonPack, qualityMarker} {
		if loc := r.FindStringIndex(value); loc != nil && loc[0] < cut {
			cut = loc[0]
		}
	}
	return cut
}
func titleIdentity(title string, m catalog.Media) (bool, int) {
	stem := stripExtension(title)
	cut := markerCut(stem)
	prefix := strings.Trim(stem[:cut], " ._()-")
	if matchesTitle(prefix, m) {
		return true, 0
	}
	// A year is recognized as identity evidence only if its preceding title
	// matches. This preserves works whose actual title contains a year (2012).
	years := yearMarker.FindAllStringIndex(prefix, -1)
	for i := len(years) - 1; i >= 0; i-- {
		loc := years[i]
		if loc[1] != len(prefix) && strings.Trim(prefix[loc[1]:], " ._()-") != "" {
			continue
		}
		candidate := strings.Trim(prefix[:loc[0]], " ._()-")
		if matchesTitle(candidate, m) {
			year, _ := strconv.Atoi(prefix[loc[0]:loc[1]])
			return true, year
		}
	}
	return false, 0
}
func stripExtension(title string) string {
	ext := strings.ToLower(filepath.Ext(title))
	switch ext {
	case ".torrent", ".mkv", ".mp4", ".avi", ".m4v", ".ts", ".mov":
		return strings.TrimSuffix(title, filepath.Ext(title))
	default:
		return title
	}
}
func resourceFilename(title string) string {
	return stripExtension(title) + ".mkv"
}
func resolution(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "4k":
		return "2160p"
	case "8k":
		return "4320p"
	case "4320p", "2160p", "1080p", "1080i", "720p", "576p", "480p":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}
func codec(value string) string {
	v := strings.ToLower(strings.NewReplacer(".", "", "_", "", "-", "", " ", "").Replace(value))
	switch v {
	case "x265", "h265", "hevc":
		return "hevc"
	case "x264", "h264", "avc":
		return "avc"
	case "av1":
		return "av1"
	case "vc1":
		return "vc1"
	default:
		return ""
	}
}
func checkQuality(resource sites.Resource, policy Policy, text string, reject func(string)) {
	for _, q := range []struct {
		name, wanted, supplied string
		marker                 *regexp.Regexp
		normalize              func(string) string
	}{{"resolution", policy.Resolution, resource.Resolution, resolutionMarker, resolution}, {"codec", policy.Codec, resource.Codec, codecMarker, codec}} {
		if q.wanted == "" {
			continue
		}
		wanted := q.normalize(q.wanted)
		if wanted == "" {
			reject("invalid_policy_" + q.name)
			continue
		}
		observed := q.normalize(q.supplied)
		if q.supplied != "" && observed == "" {
			reject(q.name + "_unknown")
			continue
		}
		seen := map[string]bool{}
		for _, match := range qualityValues(q.marker, text) {
			if v := q.normalize(match); v != "" {
				seen[v] = true
			}
		}
		if len(seen) > 1 {
			reject(q.name + "_conflict")
			continue
		}
		for v := range seen {
			if observed != "" && observed != v {
				reject(q.name + "_conflict")
			}
			observed = v
		}
		if observed == "" {
			reject(q.name + "_unknown")
		} else if observed != wanted {
			reject(q.name + "_mismatch")
		}
	}
}

// Keep the trailing delimiter available for the next token: RE2 lacks lookahead,
// and consuming the sole space in "1080p 2160p" would otherwise hide a conflict.
func qualityValues(pattern *regexp.Regexp, text string) []string {
	out := []string{}
	for offset := 0; offset < len(text); {
		m := pattern.FindStringSubmatchIndex(text[offset:])
		if len(m) < 4 {
			break
		}
		out = append(out, text[offset+m[2]:offset+m[3]])
		advance := m[1] - 1
		if advance < 1 {
			advance = 1
		}
		offset += advance
	}
	return out
}
