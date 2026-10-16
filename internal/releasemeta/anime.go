package releasemeta

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"light-media/internal/releasemeta/anitogo"
)

var (
	animeMark   = regexp.MustCompile(`新番|月?番|[日美国][漫剧]`)
	animePrefix = regexp.MustCompile(`.*番.|.*[日美国][漫剧].`)
	// Generated from installed zhconv-rs over the CJK ranges recorded in the
	// reference fixture. Inverse-map SHA256: 8867f0f21b2d2b33eb2c30a0675a364b001ac04a4770bf7ce63f535fd5aca597.
	animeLegacyCategory      = regexp.MustCompile(`(?i)[中亚亞働剧劇动動华原台外大录影日枱檯洲海清港漫片电画畫畵盘盤眎眡紀綜續纪续综美臺艺華藝視视語语连連錄録陆陸集電韓韩颱高]{2,}|TV|Animation|Movie|Documentar|Anime`)
	animeCategory            = regexp.MustCompile(`(?i)[动漫画纪录片电影视连续剧集日美韩中港台海外亚洲华语大陆综艺原盘高清]{2,}|TV|Animation|Movie|Documentar|Anime`)
	animeLeadingBracket      = regexp.MustCompile(`^[^]]*]`)
	animeFileSize            = regexp.MustCompile(`(?i)[0-9.]+\s*[MGT]i?B`)
	animeTVBracket           = regexp.MustCompile(`(?i)\[TV\s+(\d{1,4})`)
	animeFourKBracket        = regexp.MustCompile(`(?i)\[4k]`)
	animeNumericBracket      = regexp.MustCompile(`\[\d+`)
	animeMixedChinese        = regexp.MustCompile(`[\d|#:：\-()（）\x{4e00}-\x{9fff}]`)
	animeLegacySeasonRange   = regexp.MustCompile(`(?i)\bS\d{1,2}-S?\d{1,2}\b`)
	animeResolutionTokens    = regexp.MustCompile(`(?i)\b(?:\d{3,4}[pi]|\d{3,4}x\d{3,4}|[248]k)\b`)
	animeRustSeasonTokens    = regexp.MustCompile(`(?i)\bS(\d+)(?:E\d+)?\b`)
	animeRustExplicitEpisode = regexp.MustCompile(`(?i)\b(?:S?\d{1,2}(?:-S?\d{1,2})?[xE]\d{1,4}(?:-E?\d{1,4})?|EP?\d{1,4}|\d{1,4}v\d|\d{1,4}[-~&+]\d{1,4}|\d+[ABC]|\d+話)\b|#\d{1,4}`)
	animeRustDecimal         = regexp.MustCompile(`\b(\d+)\.(\d+)\b`)
)

func animeContainsChinese(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}
func animeAllChinese(value string) bool {
	for _, r := range value {
		if r != ' ' && (r < '\u4e00' || r > '\u9fff') {
			return false
		}
	}
	return true
}
func animeDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
func prepareAnimeTitle(title string, legacy bool) string {
	title = strings.TrimSpace(strings.NewReplacer("【", "[", "】", "]").Replace(title))
	if match := animeMark.FindStringIndex(title); match != nil {
		if match[1] < len(title)-1 {
			title = animePrefix.ReplaceAllString(title, "")
		} else if i := strings.LastIndex(title, "["); i >= 0 {
			title = title[:i]
		} else if len(title) > 0 {
			_, n := utf8.DecodeLastRuneInString(title)
			title = title[:len(title)-n]
		}
	}
	first := strings.SplitN(title, "]", 2)[0]
	category := animeCategory
	if legacy {
		category = animeLegacyCategory
	}
	if first != "" && category.MatchString(first) {
		title = strings.TrimSpace(animeLeadingBracket.ReplaceAllString(title, ""))
	}
	// The source negative lookahead after the unit only rejects an additional
	// ASCII letter. Preserve those suffixes rather than stripping a partial unit.
	title = animeReplaceSizes(title)
	title = animeTVBracket.ReplaceAllString(title, "[${1}")
	title = animeFourKBracket.ReplaceAllString(title, "2160p")
	names := strings.Split(title, "]")
	if len(names) > 1 && !strings.Contains(title, "- ") {
		result := make([]string, 0, len(names))
		for _, name := range names {
			if name == "" {
				continue
			}
			left := ""
			if strings.HasPrefix(name, "[") {
				left = "["
				name = name[1:]
			}
			if name != "" && strings.Contains(name, "/") {
				split := strings.Split(name, "/")
				picked := strings.TrimSpace(split[len(split)-1])
				if picked == "" {
					picked = strings.TrimSpace(split[0])
				}
				result = append(result, left+picked)
			} else if name != "" {
				if animeContainsChinese(name) && !animeAllChinese(name) {
					if !animeNumericBracket.MatchString(name) {
						name = strings.TrimSpace(animeMixedChinese.ReplaceAllString(name, ""))
					}
					if name == "" || animeDigits(strings.TrimSpace(name)) {
						continue
					}
				}
				if name == "[" {
					result = append(result, "")
				} else {
					result = append(result, left+strings.TrimSpace(name))
				}
			}
		}
		return strings.Join(result, "]")
	}
	return title
}
func animeReplaceSizes(title string) string {
	matches := animeFileSize.FindAllStringIndex(title, -1)
	if len(matches) == 0 {
		return title
	}
	var result strings.Builder
	last := 0
	for _, at := range matches {
		if at[1] < len(title) && ((title[at[1]] >= 'a' && title[at[1]] <= 'z') || (title[at[1]] >= 'A' && title[at[1]] <= 'Z')) {
			continue
		}
		result.WriteString(title[last:at[0]])
		last = at[1]
	}
	result.WriteString(title[last:])
	return result.String()
}
func animeParseInt(value string) *int {
	if value == "" {
		return nil
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return nil
		}
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return &n
}
func animeRange(values []string) (*int, *int) {
	var parsed []*int
	for _, value := range values {
		if n := animeParseInt(strings.TrimSpace(value)); n != nil {
			parsed = append(parsed, n)
		}
	}
	if len(parsed) == 0 {
		return nil, nil
	}
	if len(parsed) > 1 && *parsed[0] != *parsed[len(parsed)-1] {
		return parsed[0], parsed[len(parsed)-1]
	}
	return parsed[0], nil
}
func normalizeAnimeResolution(value string) string {
	if at := strings.LastIndexAny(value, "xX"); at >= 0 {
		value = value[at+1:] + "p"
	} else {
		value = strings.ToLower(value)
	}
	if animeDigits(value) {
		value += "p"
	}
	return value
}

// Anitopy preidentifies lowercase resolution keywords while tokenizing
// each bracket chunk, before its general keyword and numeric passes. Its
// configuration order (480p, 720p, 1080p) therefore matters inside one chunk.
func animeResolution(title, parsed string, legacy bool) string {
	if legacy {
		for _, chunk := range animeBracketChunks(title) {
			for _, keyword := range []string{"480p", "720p", "1080p"} {
				if strings.Contains(chunk, keyword) {
					return keyword
				}
			}
		}
		// Python's decimal class includes fullwidth and Arabic digits. The
		// resource field keeps those characters; only season integers convert.
		for _, match := range rxMatches(`(?<!\w)\d{3,4}(?:[pP]|[xX×]\d{3,4})(?!\w)`, title, false) {
			return match.String()
		}
	}
	for _, at := range animeResolutionTokens.FindAllStringIndex(title, -1) {
		candidate := title[at[0]:at[1]]
		if legacy && (strings.HasSuffix(strings.ToLower(candidate), "i") || strings.HasSuffix(strings.ToLower(candidate), "k")) {
			continue
		}
		return candidate
	}
	return parsed
}

func animeLegacyDecimalTitle(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			if value, err := metadataInteger(string(r), "legacy"); err == nil {
				return rune('0' + value)
			}
		}
		return r
	}, title)
}
func animeBracketChunks(title string) []string {
	opens := map[rune]rune{'(': ')', '[': ']', '{': '}', '「': '」', '『': '』', '【': '】', '（': '）'}
	var result []string
	var closing rune
	start := 0
	for at, r := range title {
		if closing == 0 {
			if close, ok := opens[r]; ok {
				if at > start {
					result = append(result, title[start:at])
				}
				closing = close
				start = at + utf8.RuneLen(r)
			}
		} else if r == closing {
			if at > start {
				result = append(result, title[start:at])
			}
			closing = 0
			start = at + utf8.RuneLen(r)
		}
	}
	if start < len(title) {
		result = append(result, title[start:])
	}
	return result
}

// Anitomy-pure 0.1.0 splits hyphens before its prefix pass. This records
// seasons from both S01-S03 and S01E01-S03E12, including zero and reversed
// intervals. Its wrapper retains insertion order rather than sorting seasons.
func animeRustSeasons(title string, begin, end *int) (*int, *int) {
	var values []string
	for _, m := range animeRustSeasonTokens.FindAllStringSubmatch(title, -1) {
		values = append(values, m[1])
	}
	if len(values) > 1 {
		return animeRange(values)
	}
	return begin, end
}

// The anitomy-pure numeric-range pass runs across all bracket chunks before
// its bare-episode pass. A decimal at the start of an unbracketed chunk can
// therefore become the first episode even when an earlier chunk has "- 01".
// The MoviePilot wrapper then discards that fractional value when parsing i64.
func animeRustFractionalEpisode(title string) bool {
	if animeRustExplicitEpisode.MatchString(title) {
		return false
	}
	closing := rune(0)
	start := 0
	opens := map[rune]rune{'(': ')', '[': ']', '{': '}', '「': '」', '『': '』', '【': '】', '（': '）'}
	chunks := []string{}
	for at, r := range title {
		if closing == 0 {
			if close, ok := opens[r]; ok {
				if at > start {
					chunks = append(chunks, title[start:at])
				}
				closing = close
			}
		}
		if closing != 0 && r == closing {
			closing = 0
			start = at + utf8.RuneLen(r)
		}
	}
	if closing == 0 && start < len(title) {
		chunks = append(chunks, title[start:])
	}
	for _, chunk := range chunks {
		for _, m := range animeRustDecimal.FindAllStringSubmatchIndex(chunk, -1) {
			decimal := chunk[m[2]:m[5]]
			// 5.1 is consumed by the preceding audio-keyword pass.
			if decimal == "5.1" {
				continue
			}
			before := strings.Trim(strings.ReplaceAll(chunk[:m[2]], "5.1", ""), " _.-&+,|")
			after := strings.Trim(strings.ReplaceAll(chunk[m[5]:], "5.1", ""), " _.-&+,|")
			if before == "" || after == "" || strings.HasSuffix(strings.TrimSpace(chunk[:m[2]]), "-") {
				return true
			}
		}
	}
	return false
}

func parseAnime(title string, subtitle *string, options Options) (Metadata, error) {
	result := Metadata{Kind: "anime", Type: "未知"}
	if title == "" {
		return result, nil
	}
	prepared := prepareAnimeTitle(title, options.SourceMode == "legacy")
	if prepared == "" {
		if options.SourceMode == "rust" {
			result.Type = "电视剧" // Rust's final unknown/no-name default after Parser::parse fails.
		}
		return result, nil
	}
	original := anitogo.Parse(title, anitogo.DefaultOptions)
	parseTitle := prepared
	// Anitopy 2.1.1 only parses a season interval when an episode follows
	// inside the same token; a bare S01-S03 remains part of the title.
	if options.SourceMode == "legacy" {
		parseTitle = animeLegacyDecimalTitle(parseTitle)
		parseTitle = animeLegacySeasonRange.ReplaceAllString(parseTitle, "")
	}
	parsed := anitogo.Parse(parseTitle, anitogo.DefaultOptions)
	rustCorrupt := options.SourceMode == "rust" && strings.Contains(parseTitle, "[") && !strings.Contains(parseTitle, "]")
	if rustCorrupt {
		parsed = &anitogo.Elements{} // anitomy-pure returns only FileName/Ext before tokenization.
	}
	if options.SourceMode == "rust" && strings.Contains(title, "[") && !strings.Contains(title, "]") {
		original = &anitogo.Elements{}
	}
	result.Name = strings.TrimSpace(parsed.AnimeTitle)
	result.BeginSeason, result.EndSeason = animeRange(parsed.AnimeSeason)
	result.BeginEpisode, result.EndEpisode = animeRange(parsed.EpisodeNumber)
	if options.SourceMode != "legacy" && !rustCorrupt {
		result.BeginSeason, result.EndSeason = animeRustSeasons(parseTitle, result.BeginSeason, result.EndSeason)
		if animeRustFractionalEpisode(parseTitle) {
			result.BeginEpisode = nil
			result.EndEpisode = nil
		}
	}
	if result.BeginSeason != nil || result.BeginEpisode != nil {
		result.Type = "电视剧"
	}
	if options.SourceMode != "legacy" && result.Type == "未知" {
		if len(parsed.AnimeType) > 0 && strings.EqualFold(parsed.AnimeType[0], "TV") {
			result.Type = "电视剧"
		} else if len(parsed.Source) == 0 || !strings.EqualFold(parsed.Source[0], "WEBRip") {
			result.Type = "电影"
		}
	}
	resolution := animeResolution(prepared, parsed.VideoResolution, options.SourceMode == "legacy")
	if options.SourceMode == "rust" {
		resolution = rustAnimeResolution(prepared)
	}
	if rustCorrupt {
		resolution = ""
	}
	result.Resolution = normalizeAnimeResolution(resolution)
	team, err := matchTeam(title, options)
	if err != nil {
		return result, nil // Source MetaAnime catches the error after earlier assignments.
	}
	if team == "" {
		team = original.ReleaseGroup
	}
	result.Team = team
	result.Customization, err = matchCustomization(title, options)
	if err != nil {
		return result, nil // Subtitle parsing is after the failed matcher in the source.
	}
	if !applySubtitle(&result, title) && subtitle != nil {
		applySubtitle(&result, *subtitle)
	}
	if options.SourceMode != "legacy" && result.Type == "未知" && result.Name == "" {
		result.Type = "电视剧"
	}
	return result, nil
}
