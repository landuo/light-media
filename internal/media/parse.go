// Package media contains conservative, offline naming and numbering experiments.
// A syntactic filename match is not a metadata identity match.
package media

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Status string

const (
	Parsed      Status = "parsed"
	NeedsReview Status = "needs_review"
)

type Numbering struct {
	Season   *int   `json:"season,omitempty"`
	Episodes []int  `json:"episodes"`
	Form     string `json:"form"`
}

type ParseResult struct {
	Status   Status     `json:"status"`
	Reason   string     `json:"reason,omitempty"`
	Type     string     `json:"type,omitempty"`
	Title    string     `json:"title,omitempty"`
	Year     int        `json:"year,omitempty"`
	Original *Numbering `json:"original,omitempty"`
}

var (
	seasonEpisode  = regexp.MustCompile(`(?i)(?:^|[ ._\-])S(\d{1,2})E(\d{1,3})((?:E\d{1,3})+|-E?\d{1,3})?(?:$|[ ._\-])`)
	markerCount    = regexp.MustCompile(`(?i)S\d{1,2}E\d{1,3}`)
	explicitRange  = regexp.MustCompile(`(?i)^-E?(\d{1,3})$`)
	extraEpisode   = regexp.MustCompile(`(?i)E(\d{1,3})`)
	incompleteEP   = regexp.MustCompile(`(?i)^(?:E(?:$|\d)|S\d{1,2}E)`)
	displayNumber  = regexp.MustCompile(`(?i)(?:第\s*(\d{1,4})\s*[集话]|(?:^|[ ._\-])EP\s*(\d{1,4})(?:$|[ ._\-]))`)
	displayMarker  = regexp.MustCompile(`(?i)(?:第\s*[0-9一二三四五六七八九十百零〇两]+|(?:^|[ ._\-])EP\s*\d+)`)
	yearPattern    = regexp.MustCompile(`(?:^|[ ._(\-])((?:19|20)\d{2})(?:$|[ ._)\-])`)
	absoluteEP     = regexp.MustCompile(`(?i)(?:^|[ ._\-])(?:EP|第)(\d{1,4})(?:集|话)?`)
	releaseSuffix  = regexp.MustCompile(`(?i)^(?:[ ._()\-+]*(?:\d{3,4}[pi]|4k|8k|web[- .]?dl|webrip|blu[- .]?ray|bdrip|hdtv|remux|x26[45]|h[ .]?26[45]|hevc|avc|av1|aac[0-9.]*|dts|hd|ma[0-9.]*|truehd[0-9.]*|ddp?[0-9.]*|e?ac3|flac[0-9.]*|atmos|hdr(?:10)?|dovi|dv|10bit|8bit|proper|repack|multi|imax|extended|uncut|amzn|nf|netflix|dsnp|dsny|hmax|max|atvp|hulu|hbo|pcok|pmtp|itunes|it|[ ._\-]))*$`)
	releaseQuality = regexp.MustCompile(`(?i)(?:\d{3,4}[pi]|4k|8k|web[- .]?dl|webrip|blu[- .]?ray|bdrip|hdtv|remux)`)
)

// Parse accepts explicit SxxExx and EPxxx forms, and conservative Title (year)
// movies. Numeric suffixes without a marker never imply an episode number.
func Parse(filename string) ParseResult {
	if filename == "" || strings.ContainsAny(filename, `/\`) {
		return review("filename_must_be_basename")
	}
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mkv", ".mp4", ".avi", ".m4v", ".ts", ".mov":
	default:
		return review("unsupported_media_extension")
	}
	stem := strings.TrimSuffix(filename, filepath.Ext(filename))
	matches := seasonEpisode.FindAllStringSubmatchIndex(stem, -1)
	if len(matches) > 1 || len(markerCount.FindAllString(stem, -1)) > 1 {
		return review("multiple_season_episode_markers")
	}
	if len(matches) == 1 {
		m := matches[0]
		title := cleanTitle(stem[:m[0]])
		if title == "" {
			return review("missing_series_title")
		}
		season, _ := strconv.Atoi(stem[m[2]:m[3]])
		first, _ := strconv.Atoi(stem[m[4]:m[5]])
		if season == 0 || first == 0 {
			return review("special_or_zero_numbering")
		}
		episodes := []int{first}
		suffix := ""
		if m[6] >= 0 {
			suffix = stem[m[6]:m[7]]
		}
		end := m[5]
		if m[7] >= 0 {
			end = m[7]
		}
		if incompleteEP.MatchString(strings.TrimLeft(stem[end:], " ._-")) {
			return review("incomplete_or_conflicting_episode_suffix")
		}
		if r := explicitRange.FindStringSubmatch(suffix); r != nil {
			last, _ := strconv.Atoi(r[1])
			if last <= first || last-first > 50 {
				return review("invalid_or_excessive_episode_range")
			}
			for n := first + 1; n <= last; n++ {
				episodes = append(episodes, n)
			}
		} else {
			seen := map[int]bool{first: true}
			for _, ep := range extraEpisode.FindAllStringSubmatch(suffix, -1) {
				n, _ := strconv.Atoi(ep[1])
				if n == 0 || seen[n] {
					return review("invalid_or_duplicate_episode")
				}
				seen[n] = true
				episodes = append(episodes, n)
			}
		}
		// A human-readable episode label is redundant evidence, not permission
		// to override explicit SxxExx. Conflicting or unsupported labels stop.
		tail := stem[end:]
		labels := displayNumber.FindAllStringSubmatch(tail, -1)
		if len(displayMarker.FindAllString(tail, -1)) > len(labels) {
			return review("unsupported_display_episode_numbering")
		}
		if len(labels) > 0 {
			if len(labels) != 1 || len(episodes) != 1 {
				return review("multiple_display_episode_markers")
			}
			number := labels[0][1]
			if number == "" {
				number = labels[0][2]
			}
			n, _ := strconv.Atoi(number)
			if n != first {
				return review("conflicting_display_episode_number")
			}
		}
		return ParseResult{Status: Parsed, Type: "episode", Title: title, Original: &Numbering{Season: &season, Episodes: episodes, Form: "season_episode"}}
	}
	if a := absoluteEP.FindAllStringSubmatchIndex(stem, -1); len(a) > 0 {
		if len(a) != 1 {
			return review("multiple_absolute_episode_markers")
		}
		m := a[0]
		if m[1] < len(stem) && !strings.ContainsRune(" ._-", rune(stem[m[1]])) {
			return review("invalid_absolute_episode_marker_boundary")
		}
		n, _ := strconv.Atoi(stem[m[2]:m[3]])
		title := cleanTitle(stem[:m[0]])
		if n == 0 || title == "" {
			return review("invalid_absolute_episode")
		}
		return ParseResult{Status: Parsed, Type: "episode", Title: title, Original: &Numbering{Episodes: []int{n}, Form: "absolute_episode"}}
	}
	years := yearPattern.FindAllStringSubmatchIndex(stem, -1)
	if len(years) != 1 {
		return review("missing_or_ambiguous_year_or_episode")
	}
	m := years[0]
	year, _ := strconv.Atoi(stem[m[2]:m[3]])
	title := cleanTitle(stem[:m[0]])
	suffix := strings.Trim(stem[m[3]:], " ()-._")
	validSuffix := releaseSuffix.MatchString(suffix)
	if !validSuffix && releaseQuality.MatchString(suffix) {
		// Release groups are arbitrary names, not film titles. Only remove a
		// final group after an otherwise fully recognized technical suffix.
		if split := strings.LastIndexByte(suffix, '-'); split > 0 {
			group := suffix[split+1:]
			if len(group) <= 64 && regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._]*$`).MatchString(group) {
				validSuffix = releaseSuffix.MatchString(suffix[:split])
			}
		}
	}
	if title == "" || !validSuffix {
		return review("ambiguous_movie_suffix_or_title")
	}
	return ParseResult{Status: Parsed, Type: "movie", Title: title, Year: year}
}

func cleanTitle(s string) string {
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, "_", " ")
	return strings.Join(strings.Fields(strings.Trim(s, " ()-")), " ")
}

func review(reason string) ParseResult { return ParseResult{Status: NeedsReview, Reason: reason} }
