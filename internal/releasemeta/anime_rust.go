package releasemeta

// MoviePilot-Rust 0.2.6 parser.rs::parse_anime and its consumed name/range helpers.
// The full anitomy-pure pipeline lives in rustanime; this wrapper never calls
// Anitogo. Retry candidates replace only name, never the initial ordered result.

import (
	"errors"
	"strconv"
	"strings"
	"unicode"

	"light-media/internal/releasemeta/rustanime"
)

func rustAnimeFirst(result rustanime.Result, category rustanime.Category) *string {
	for _, element := range result.Elements {
		if element.Category == category {
			value := element.Value
			return &value
		}
	}
	return nil
}
func rustAnimeValues(result rustanime.Result, category rustanime.Category) []string {
	var values []string
	for _, element := range result.Elements {
		if element.Category == category {
			values = append(values, element.Value)
		}
	}
	return values
}
func rustAnimeValue(value *string) string {
	if value != nil {
		return *value
	}
	return ""
}
func rustAnimeRange(values []string) (*int, *int) {
	var numbers []int
	for _, value := range values {
		value = rxReplace(`v\d+$`, value, "", false)
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			numbers = append(numbers, int(n))
		}
	}
	if len(numbers) == 0 {
		return nil, nil
	}
	begin, end := numbers[0], numbers[len(numbers)-1]
	if begin == end {
		return &begin, nil
	}
	return &begin, &end
}
func rustAnimeParse(title string) (rustanime.Result, bool, error) {
	parsed, err := rustanime.Parse(title, rustanime.Options{})
	if errors.Is(err, rustanime.ErrSourcePanic) {
		return rustanime.Result{}, false, errRustFallback
	}
	if err != nil {
		return rustanime.Result{}, false, nil
	} // Source .ok() drops empty parse errors.
	return parsed, true, nil
}
func parseRustAnime(title string, subtitle *string, options Options) (Metadata, error) {
	meta := Metadata{Kind: "anime", Type: "未知"}
	if title == "" {
		return meta, nil
	}
	prepared := prepareAnimeTitle(title, false)
	parsed, ok, err := rustAnimeParse(prepared)
	if err != nil {
		return Metadata{}, err
	}
	original, _, err := rustAnimeParse(title)
	if err != nil {
		return Metadata{}, err
	}
	originName := rustAnimeFirst(original, rustanime.AnimeTitle)
	originGroup := rustAnimeFirst(original, rustanime.ReleaseGroup)
	matchedGroup, err := matchTeamOption(title, options)
	if err != nil {
		return Metadata{}, err
	}
	if ok {
		name := rustAnimeFirst(parsed, rustanime.AnimeTitle)
		if rustAnimeShouldReplace(name, originGroup, matchedGroup) && originName != nil && !rustAnimeShouldRetry(originName) && (originGroup == nil || !asciiEqualFold(*originName, *originGroup)) && (matchedGroup == nil || !asciiEqualFold(*originName, *matchedGroup)) {
			name = originName
		}
		if rustAnimeShouldReplace(name, originGroup, matchedGroup) {
			if candidate := rustAnimePreparedGroupTitle(prepared, matchedGroup); candidate != nil {
				name = candidate
			}
		}
		if rustAnimeShouldReplace(name, originGroup, matchedGroup) {
			if candidate := rustAnimePreferredBracket(title, originGroup, matchedGroup); candidate != nil {
				name = candidate
			}
		}
		if rustAnimeShouldReplace(name, originGroup, matchedGroup) {
			retry, _, err := rustAnimeParse("[ANIME]" + prepared)
			if err != nil {
				return Metadata{}, err
			}
			name = rustAnimeFirst(retry, rustanime.AnimeTitle)
		}
		if rustAnimeShouldReplace(name, originGroup, matchedGroup) {
			name = rustAnimeFirstBracket(prepared)
		}
		if name != nil {
			meta.Name = rustAnimeName(*name, title, prepared)
		}
		meta.BeginSeason, meta.EndSeason = rustAnimeRange(rustAnimeValues(parsed, rustanime.AnimeSeason))
		meta.BeginEpisode, meta.EndEpisode = rustAnimeRange(rustAnimeValues(parsed, rustanime.EpisodeNumber))
		if meta.BeginSeason != nil || meta.BeginEpisode != nil {
			meta.Type = "电视剧"
		}
		if match := rxFirst(`TV\s+(\d{1,4})`, title, true); match != nil {
			if n, err := strconv.ParseInt(match.GroupByNumber(1).String(), 10, 64); err == nil {
				meta.BeginEpisode, meta.EndEpisode, meta.Type = intPointer(int(n)), nil, "电视剧"
			}
		}
		if match := rxFirst(`TV\s+(\d{1,4})\s*-\s*(\d{1,4})`, title, true); match != nil {
			begin, e1 := strconv.ParseInt(match.GroupByNumber(1).String(), 10, 64)
			end, e2 := strconv.ParseInt(match.GroupByNumber(2).String(), 10, 64)
			if e1 == nil && e2 == nil && end >= begin {
				meta.BeginEpisode, meta.EndEpisode, meta.Type = intPointer(int(begin)), intPointer(int(end)), "电视剧"
			}
		}
		if meta.Type == "未知" {
			if typ := rustAnimeFirst(parsed, rustanime.AnimeType); typ != nil && asciiEqualFold(*typ, "TV") {
				meta.Type = "电视剧"
			} else if source := rustAnimeFirst(parsed, rustanime.Source); source == nil || !asciiEqualFold(*source, "WEBRip") {
				meta.Type = "电影"
			}
		}
		if resolution := rustAnimeFirst(parsed, rustanime.VideoResolution); resolution != nil {
			meta.Resolution = normalizeAnimeResolution(*resolution)
		}
		meta.Team = rustAnimeValue(matchedGroup)
		if matchedGroup == nil {
			meta.Team = rustAnimeValue(originGroup)
		}
		meta.Customization, err = matchCustomization(title, options)
		if err != nil {
			return Metadata{}, err
		}
		if !applySubtitle(&meta, strings.TrimSpace(title)) && subtitle != nil {
			applySubtitle(&meta, *subtitle)
		}
	}
	if meta.Type == "未知" && meta.Name == "" {
		meta.Type = "电视剧"
	}
	return meta, nil
}

// eq_ignore_ascii_case deliberately does not casefold non-ASCII characters.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
func rustAnimeShouldRetry(name *string) bool {
	if name == nil {
		return true
	}
	switch *name {
	case "CHS&CHT", "MP4", "GB MP4", "WEB-DL":
		return true
	}
	return len(*name) < 5 && !animeContainsChinese(*name)
}

const rustAnimeSubtitleLabel = `(字幕|双语|雙語|简体|繁体|简繁|繁日|中日|国语|国配|招募|片源|翻译|翻譯|校对|校對|内封|外挂|中字)`
const rustAnimeCategoryLabel = `(新番|月?番|日剧|美剧|动漫|动画|Animation|Animations|Anime|Movie|TV)`

func rustAnimeLabel(name string) bool {
	return name == "" || rxHas(rustAnimeSubtitleLabel, name, false) || rxHas(rustAnimeCategoryLabel, name, false)
}
func rustAnimeGroupTitle(name string) bool {
	return len(strings.Fields(name)) > 1 && strings.ContainsAny(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") && !strings.ContainsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || unicode.IsSpace(r))
	})
}
func rustAnimeShouldReplace(name, originGroup, matched *string) bool {
	return rustAnimeShouldRetry(name) || rustAnimeLabel(rustAnimeValue(name)) || originGroup != nil && asciiEqualFold(rustAnimeValue(name), *originGroup) || matched != nil && asciiEqualFold(rustAnimeValue(name), *matched)
}
func rustAnimeFirstBracket(prepared string) *string {
	if match := rxFirst(`^[\[【](.+?)[\]】]`, prepared, false); match != nil {
		value := strings.TrimSpace(match.GroupByNumber(1).String())
		return &value
	}
	return nil
}
func rustAnimePreparedGroupTitle(prepared string, matched *string) *string {
	candidate := rustAnimeFirstBracket(prepared)
	if candidate == nil || *candidate == "" || matched != nil && asciiEqualFold(*candidate, *matched) || !rustAnimeGroupTitle(*candidate) {
		return nil
	}
	return candidate
}
func rustAnimePreferredBracket(title string, originGroup, matched *string) *string {
	bestScore := -1
	var best *string
	for _, match := range rxMatches(`[\[【](.+?)[\]】]`, title, false) {
		content := strings.TrimSpace(match.GroupByNumber(1).String())
		if content == "" || rustAnimeShouldRetry(&content) {
			continue
		}
		content = strings.Trim(content, "[]")
		bad := false
		for _, part := range strings.Fields(content) {
			if rxHas(videoPatterns["_video_encode_re"], part, true) || rxHas(`(?<![A-Za-z0-9])(?P<bit>8|10|12|16)[\s._-]*bits?(?![A-Za-z0-9])`, part, true) {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		if (originGroup != nil && asciiEqualFold(content, *originGroup) || matched != nil && asciiEqualFold(content, *matched)) && !rustAnimeGroupTitle(content) {
			continue
		}
		asciiOnly := !strings.ContainsFunc(content, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._- ", r))
		})
		if digits(content) || asciiOnly && (rxHas(videoPatterns["_resources_pix_re"], content, true) || rxHas(videoPatterns["_resources_pix_re2"], content, true) || rxHas(`(?:2160|1080|720|480)[PIpi]|4K|UHD|Blu[\-.]?ray|REMUX|WEB[\-.]?DL|HDTV`, content, true) || rxHas(videoPatterns["_season_re"], content, true) || rxHas(videoPatterns["_episode_re"], content, true)) {
			continue
		}
		if rustAnimeLabel(content) {
			continue
		}
		score := 0
		asciiAlpha := strings.ContainsAny(content, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")
		if asciiAlpha {
			score += 3
		}
		if animeContainsChinese(content) {
			score += 2
		}
		if strings.Contains(content, "/") {
			score += 4
			if asciiAlpha {
				score += 4
			}
		}
		if len(strings.Fields(content)) > 1 {
			score++
		}
		if len(content) >= 4 {
			score++
		}
		if best == nil || score > bestScore || score == bestScore && len(content) > len(*best) {
			value := content
			best = &value
			bestScore = score
		}
	}
	return best
}

const rustAnimeNoString = `S\d{2}\s*-\s*S\d{2}|S\d{2}|\s+S\d{1,2}|EP?\d{2,4}\s*-\s*EP?\d{2,4}|EP?\d{2,4}|\s+EP?\d{1,4}|\s+GB`
const rustAnimeChineseSeason = `[第共]\s*[0-9一二三四五六七八九十百零]+\s*[季集话話]`

func rustAnimeCleanChinese(name string) string {
	name = strings.TrimSpace(rxReplace(`^(电影|电视剧|动漫|\s+电影|\s+电视剧|\s+动漫)`, name, "", false))
	name = strings.TrimSpace(rxReplace(`第\s*[0-9一二三四五六七八九十]+\s*季|第\s*[0-9一二三四五六七八九十百零]+\s*集|[\s(]+(\d{4})[\s)]*`, name, "", false))
	name = strings.TrimSpace(rxReplace(`\s+`, name, " ", false))
	return strings.TrimSpace(rxReplace(rustAnimeNoString, name, "", true))
}
func rustAnimeKeepChinese(cn, en, original, prepared string) bool {
	cleaned := rustAnimeCleanChinese(cn)
	if cn == "" || en == "" || cleaned == "" || !strings.ContainsAny(en, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") {
		return false
	}
	if strings.ContainsFunc(cleaned, func(r rune) bool {
		return !(unicode.IsSpace(r) || r >= '\u4e00' && r <= '\u9fff' || strings.ContainsRune("：:！!？?·・《》「」", r))
	}) {
		return false
	}
	if rxHas(rustAnimeChineseSeason, cleaned, false) || rxHas(rustAnimeSubtitleLabel, cleaned, false) || rxHas(rustAnimeCategoryLabel, cleaned, false) {
		return false
	}
	if strings.Contains(original, "[") && strings.Contains(original, "/") && !strings.Contains(prepared, "/") {
		for _, m := range rxMatches(`[\[【](.+?)[\]】]`, original, false) {
			if strings.Contains(m.GroupByNumber(1).String(), "/") {
				return false
			}
		}
	}
	return true
}
func rustAnimeASCIIKey(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, value)
}
func rustAnimeName(name, original, prepared string) string {
	cn, en := "", ""
	split := true
	if strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		if animeContainsChinese(parts[0]) {
			cn = strings.TrimSpace(parts[0])
			en = strings.TrimSpace(parts[1])
			if !rustAnimeKeepChinese(cn, en, original, prepared) {
				cn = ""
			}
			split = false
		} else if animeContainsChinese(parts[len(parts)-1]) {
			cn = strings.TrimSpace(parts[len(parts)-1])
			en = strings.TrimSpace(parts[0])
			if !rustAnimeKeepChinese(cn, en, original, prepared) {
				cn = ""
			}
			split = false
		} else {
			name = parts[len(parts)-1]
		}
	}
	if split {
		last := ""
		for _, word := range strings.Fields(name) {
			word = strings.TrimSuffix(word, "]")
			if word == "" || digits(word) {
				if last == "cn" {
					cn += " " + word
				} else if last == "en" {
					en += " " + word
				}
			} else if animeContainsChinese(word) {
				cn += " " + word
				last = "cn"
			} else {
				en += " " + word
				last = "en"
			}
		}
		cn, en = strings.TrimSpace(cn), strings.TrimSpace(en)
	}
	// Restore only a slash candidate that is already ASCII-equivalent to en.
	if parts := strings.Split(original, "/"); len(parts) > 1 && en != "" {
		rest := strings.TrimSpace(parts[1])
		end := len(rest)
		for _, delimiter := range []string{" - ", " [", "[", "]", "("} {
			if at := strings.Index(rest, delimiter); at >= 0 && at < end {
				end = at
			}
		}
		candidate := strings.TrimSpace(rest[:end])
		key := rustAnimeASCIIKey(candidate)
		if strings.ContainsAny(candidate, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") && key != "" && rustAnimeASCIIKey(en) == key {
			en = candidate
		}
	}
	cn = rustAnimeCleanChinese(cn)
	if en != "" {
		cleaned := rxReplace(rustAnimeChineseSeason, en, "", false)
		cleaned = rxReplace(`[\x{4e00}-\x{9fff}]+`, cleaned, " ", false)
		cleaned = strings.TrimSpace(rxReplace(rustAnimeNoString, cleaned, "", true))
		if cleaned != "" {
			en = rustanime.TitleCase(cleaned)
		}
	}
	if cn != "" && animeAllChinese(cn) {
		return cn
	}
	if en != "" {
		return en
	}
	return cn
}
