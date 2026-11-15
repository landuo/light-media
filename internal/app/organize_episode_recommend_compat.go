package app

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2/v2"
	"light-media/internal/pyregex"
	"light-media/internal/releasemeta"
	"light-media/internal/releasemeta/legacyanime"
)

// Shared extraction and validation for configured rules and automatic fallback.
// Source: MP format.py
// _filter_by_extension_and_size, _build_detected_samples, _locate_episode,
// _extract_episode_with_native_fallback and _validate_auto_template.
var episodeLocatePatterns = []string{
	`(?<![A-Za-z0-9])[Ee][Pp]?(\d{1,4}(?:-[Ee]?[Pp]?\d{1,4})+)(?!\d)`,
	`(?<![A-Za-z0-9])[Ee][Pp]?(\d{1,4})(?!\d)`,
	`[Ss]\d{1,4}[Ee][Pp]?(\d{1,4}(?:-[Ee]?[Pp]?\d{1,4})+)(?!\d)`,
	`[Ss]\d{1,4}[Ee][Pp]?(\d{1,4})(?!\d)`,
	`(?<!\d)#(\d{1,4})(?!\d)`,
	`[\[【](\d{1,4})[\]】]`,
	`第(\d{1,4})(?:[話话集])`,
}
var episodeSpecialSample = regexp.MustCompile(`(?i)\[(?:SP\d+|NC(?:OP|ED)(?:[_\s-]*EP\d+)?(?:\s+VER\.\d+)?|OP|ED|MENU(?:\d+|OVA)?|OVA(?:\s+TRAILER)?|OAD|PV\d*|CM(?:\d+| COLLECTION)?|TRAILER|WEB PREVIEW(?:\s+\d+)?|SERIES REVIEW|TABLE GAME|TV SPOTS?|\d+\((?:OVA|OAD|SP)\d+\))\]`)
var episodeDigits = regexp.MustCompile(`^\d+$`)
var episodeNumberPart = regexp.MustCompile(`\d{1,4}`)
var episodePrefix = regexp.MustCompile(`^[Ee][Pp]?`)

func normalizeRecommendedEpisode(value string) string {
	parts := []string{}
	for _, p := range strings.Split(value, "-") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, episodePrefix.ReplaceAllString(p, ""))
		}
	}
	return strings.Join(parts, "-")
}
func recommendationEpisodeBounds(value string) (int, int, bool) {
	parts := strings.Split(value, "-")
	numbers := []int{}
	for _, p := range parts {
		token := episodeNumberPart.FindString(episodePrefix.ReplaceAllString(strings.TrimSpace(p), ""))
		if token == "" {
			return 0, 0, false
		}
		n, _ := strconv.Atoi(token)
		numbers = append(numbers, n)
	}
	return numbers[0], numbers[len(numbers)-1], true
}
func recommendationEpisodeEqual(a, b string) bool {
	x, y, ok := recommendationEpisodeBounds(a)
	u, v, other := recommendationEpisodeBounds(b)
	return ok && other && x == u && y == v
}
func episodePatternMatches(pattern, name string) []*regexp2.Match {
	re, err := pyregex.Compile(pattern, false)
	if err != nil {
		return nil
	}
	out := []*regexp2.Match{}
	match, err := re.FindStringMatch(name)
	for err == nil && match != nil {
		out = append(out, match)
		match, err = re.FindNextMatch(match)
	}
	return out
}
func locateRecommendedEpisode(name, value string) (int, int, bool) {
	for _, pattern := range episodeLocatePatterns {
		for _, match := range episodePatternMatches(pattern, name) {
			group := match.GroupByNumber(1)
			if recommendationEpisodeEqual(group.String(), value) {
				return group.RuneIndex, group.RuneIndex + group.RuneLength, true
			}
		}
	}
	first, last, ok := recommendationEpisodeBounds(value)
	if !ok {
		return 0, 0, false
	}
	for width := 1; width <= 4; width++ {
		candidates := []string{fmt.Sprintf("%0*d", width, first)}
		if first != last {
			candidates = nil
			for _, prefix := range []string{"", "E", "EP"} {
				candidates = append(candidates, fmt.Sprintf("%0*d-%s%0*d", width, first, prefix, width, last))
			}
		}
		for _, candidate := range candidates {
			pattern := `(?:(?<=^)|(?<=[\s._\-\[\]【】()「」『』《》〈〉〔〕]))` + regexp.QuoteMeta(candidate) + `(?:(?=$)|(?=[\s._\-\[\]【】()「」『』《》〈〉〔〕]))`
			matches := episodePatternMatches(pattern, name)
			if len(matches) > 0 {
				m := matches[len(matches)-1]
				return m.RuneIndex, m.RuneIndex + m.RuneLength, true
			}
		}
	}
	return 0, 0, false
}
func episodeFallbackToken(name string) string {
	patterns := []string{episodeLocatePatterns[2], episodeLocatePatterns[3], episodeLocatePatterns[4], `[\[【](\d{1,3})[\]】]`, `第(\d{1,4})[話话]`, `第(\d{1,4})集`, `。(\d{1,4})\s`}
	for i, pattern := range patterns {
		matches := episodePatternMatches(pattern, name)
		if len(matches) > 0 {
			at := 0
			if i == 2 || i == 3 {
				at = len(matches) - 1
			}
			return matches[at].GroupByNumber(1).String()
		}
	}
	return ""
}
func earlierEpisodeToken(name, earlier, later string) bool {
	_, end, first := locateRecommendedEpisode(name, earlier)
	start, _, second := locateRecommendedEpisode(name, later)
	return first && second && end <= start
}

type episodeCompatibilitySample struct{ name, expected, native string }

type episodeExtraction struct {
	value, native                      string
	usedNativeFallback, nativeVerified bool
}

func extractRecommendedEpisode(sample organizeEpisodeSample, nativeParser *releasemeta.MetaInfoParser) episodeExtraction {
	native := ""
	if nativeParser != nil {
		meta, err := nativeParser.ParsePath(sample.path, nil)
		if err == nil && meta.Number("begin_episode") != nil {
			native = meta.Number("begin_episode").String()
			if last := meta.Number("end_episode"); last != nil && last.String() != native {
				native += "-" + last.String()
			}
		}
	}
	value := ""
	parsed, err := legacyanime.Parse(sample.name, legacyanime.Options{})
	if err == nil {
		switch episode := parsed.Dictionary["episode_number"].(type) {
		case string:
			value = episode
		case []string:
			value = strings.Join(episode, "-")
		}
	}
	return resolveRecommendedEpisode(sample.name, value, episodeFallbackToken(sample.name), native)
}

// MP _extract_episode_with_native_fallback, including title-sequence conflict degradation.
func resolveRecommendedEpisode(name, value, fallback, native string) episodeExtraction {
	value, fallback = normalizeRecommendedEpisode(value), normalizeRecommendedEpisode(fallback)
	first, last, _ := recommendationEpisodeBounds(value)
	if value == "" || (fallback != "" && !recommendationEpisodeEqual(value, fallback) && first == last && earlierEpisodeToken(name, value, fallback)) {
		value = fallback
	}
	result := episodeExtraction{value: value, native: native}
	if value != "" && native != "" {
		if recommendationEpisodeEqual(value, native) {
			result.nativeVerified = true
		} else if earlierEpisodeToken(name, native, value) {
			result.native = ""
		}
	} else if value == "" && native != "" {
		result.value = native
		result.usedNativeFallback = true
	}
	return result
}

func episodeCompatibilitySamples(ctx context.Context, samples []organizeEpisodeSample, nativeParser *releasemeta.MetaInfoParser) ([]episodeCompatibilitySample, error) {
	detected, _, err := detectRecommendedSamples(ctx, filterAutomaticEpisodeSamples(samples, false), func(sample organizeEpisodeSample) episodeExtraction {
		return extractRecommendedEpisode(sample, nativeParser)
	})
	if err != nil {
		return nil, err
	}
	result := make([]episodeCompatibilitySample, 0, len(detected))
	for _, sample := range detected {
		result = append(result, sample.episodeCompatibilitySample)
	}
	return result, nil
}
func validateEpisodeCompatibility(format *regexp.Regexp, samples []episodeCompatibilitySample) bool {
	for _, sample := range samples {
		match := format.FindStringSubmatch(sample.name)
		if match == nil {
			return false
		}
		actual := match[format.SubexpIndex("ep")]
		// The final consumer, rather than a regex token, decides valid episode ranges.
		if _, err := organizeEpisodeRange(actual); err != nil {
			return false
		}
		if !recommendationEpisodeEqual(actual, sample.expected) || sample.native != "" && !recommendationEpisodeEqual(actual, sample.native) {
			return false
		}
	}
	return true
}
