package releasemeta

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	animeMark   = regexp.MustCompile(`新番|月?番|[日美国][漫剧]`)
	animePrefix = regexp.MustCompile(`.*番.|.*[日美国][漫剧].`)
	// Generated from installed zhconv-rs over the CJK ranges recorded in the
	// reference fixture. Inverse-map SHA256: 8867f0f21b2d2b33eb2c30a0675a364b001ac04a4770bf7ce63f535fd5aca597.
	animeLegacyCategory = regexp.MustCompile(`(?i)[中亚亞働剧劇动動华原台外大录影日枱檯洲海清港漫片电画畫畵盘盤眎眡紀綜續纪续综美臺艺華藝視视語语连連錄録陆陸集電韓韩颱高]{2,}|TV|Animation|Movie|Documentar|Anime`)
	animeCategory       = regexp.MustCompile(`(?i)[动漫画纪录片电影视连续剧集日美韩中港台海外亚洲华语大陆综艺原盘高清]{2,}|TV|Animation|Movie|Documentar|Anime`)
	animeLeadingBracket = regexp.MustCompile(`^[^]]*]`)
	animeFileSize       = regexp.MustCompile(`(?i)[0-9.]+\s*[MGT]i?B`)
	animeTVBracket      = regexp.MustCompile(`(?i)\[TV\s+(\d{1,4})`)
	animeFourKBracket   = regexp.MustCompile(`(?i)\[4k]`)
	animeNumericBracket = regexp.MustCompile(`\[\d+`)
	animeMixedChinese   = regexp.MustCompile(`[\d|#:：\-()（）\x{4e00}-\x{9fff}]`)
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
	if legacy {
		return prepareLegacyAnimeTitle(title)
	}
	title = strings.TrimSpace(strings.NewReplacer("【", "[", "】", "]").Replace(title))
	if match := animeMark.FindStringIndex(title); match != nil {
		if match[1] < len(title)-1 {
			title = animePrefix.ReplaceAllString(title, "")
		} else if i := strings.LastIndex(title, "["); i >= 0 {
			title = title[:i]
		} else if legacy && len(title) > 0 {
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
					if name == "" || legacy && animeDigits(strings.TrimSpace(name)) || !legacy && digits(strings.TrimSpace(name)) {
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

func parseAnime(title string, subtitle *string, options Options) (Metadata, error) {
	if options.SourceMode == "rust" {
		return parseRustAnime(title, subtitle, options)
	}
	return parseLegacyAnime(title, subtitle, options)
}
