package decision

import (
	"regexp"
	"strings"
	"unicode"

	"light-media/internal/sites"
)

var sourceTags = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(WEB[ ._-]?DL|WEBRIP|BLU[ ._-]?RAY|BDRIP|HDTV|DVD|REMUX)(?:$|[^A-Za-z0-9])`)
var hdr10Tag = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(HDR10)(?:$|[^A-Za-z0-9+P])`)
var hdr10PlusTag = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(HDR10\+|HDR10PLUS)(?:$|[^A-Za-z0-9])`)
var visionTag = regexp.MustCompile(`(?i)杜比视界|(?:^|[^A-Za-z0-9])(DOLBY[ ._-]*VISION|DOVI|DV)(?:$|[^A-Za-z0-9])`)
var sdrTag = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(SDR)(?:$|[^A-Za-z0-9])`)
var genericHDRTag = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(HDR)(?:$|[^A-Za-z0-9])`)
var noneSubtitles = regexp.MustCompile(`(?i)无字幕|无字版|(?:^|[^A-Za-z0-9])NO[ ._-]SUB(?:TITLE)?S?(?:$|[^A-Za-z0-9])`)
var noChineseSubtitles = regexp.MustCompile(`(?i)无中文字幕|无中字|无中文|NO[ ._-]CHINESE[ ._-]SUB(?:TITLE)?S?`)
var chineseSubtitles = regexp.MustCompile(`(?i)中文字幕|中文内嵌|中文内封|中字|简[体中]|繁[体中]|中英双字|中英字幕|(?:^|[^A-Za-z0-9])(?:CHS|CHT|CHI[ ._-]SUB|ZH[ ._-]SUB)(?:$|[^A-Za-z0-9])`)
var simplifiedSubtitles = regexp.MustCompile(`(?i)简体|简中|简繁|(?:^|[^A-Za-z0-9])CHS(?:$|[^A-Za-z0-9])`)
var traditionalSubtitles = regexp.MustCompile(`(?i)繁体|繁中|简繁|(?:^|[^A-Za-z0-9])CHT(?:$|[^A-Za-z0-9])`)
var englishSubtitles = regexp.MustCompile(`(?i)英语字幕|英文字幕|英字|中英双字|中英字幕|CHS[ .&+_-]+ENG|(?:^|[^A-Za-z0-9])(?:ENGLISH|ENG|EN)[ ._-]+SUB(?:TITLE)?S?(?:$|[^A-Za-z0-9])`)

func normalizedSource(value string) string {
	v := strings.ToUpper(strings.NewReplacer(" ", "", ".", "", "_", "", "-", "").Replace(value))
	switch v {
	case "WEBDL", "WEBRIP", "BLURAY", "BDRIP", "HDTV", "DVD", "REMUX":
		return v
	default:
		return ""
	}
}
func languageCode(value string, subtitle bool) string {
	v := strings.ToLower(strings.TrimSpace(value))
	switch v {
	case "zh", "cmn", "mandarin", "zh-cn", "国语", "普通话", "中文":
		return "zh"
	case "zh-hans", "chs", "简体", "简中":
		if subtitle {
			return "zh-hans"
		}
		return "zh"
	case "zh-hant", "cht", "繁体", "繁中":
		if subtitle {
			return "zh-hant"
		}
		return "zh"
	case "en", "eng", "english", "英语", "英文":
		return "en"
	case "ja", "jpn", "japanese", "日语":
		return "ja"
	case "ko", "kor", "korean", "韩语":
		return "ko"
	case "yue", "cantonese", "粤语":
		return "yue"
	case "fr", "fre", "french", "法语":
		return "fr"
	case "de", "ger", "german", "德语":
		return "de"
	case "es", "spa", "spanish", "西班牙语":
		return "es"
	case "th", "tha", "thai", "泰语":
		return "th"
	default:
		return ""
	}
}

// Strip the identity portion before interpreting release tokens as language tags.
// The word "English" in the film title "English Teacher" is not audio evidence.
func releaseTagText(resource sites.Resource) string {
	title := stripExtension(resource.Title)
	start := len(title)
	if m := epMarker.FindStringIndex(title); m != nil {
		start = m[1]
	} else if years := yearMarker.FindAllStringIndex(title, -1); len(years) > 0 {
		start = years[len(years)-1][1]
	} else if m := qualityMarker.FindStringIndex(title); m != nil {
		start = m[0]
	}
	return title[start:] + " " + resource.Subtitle
}

type audioTagSpec struct {
	code       string
	expression *regexp.Regexp
}

var audioSpecs = []audioTagSpec{
	{"zh", regexp.MustCompile(`(?i)国语|普通话|国配|中文音轨|中文配音|国英双语|中英双语(?:版|音轨)|国粤双语|国日双语|(?:^|[^A-Za-z0-9])MANDARIN(?:$|[^A-Za-z0-9])`)},
	{"en", regexp.MustCompile(`(?i)英语(?:音轨|配音|发音|原声|中字)|英文音轨|国英双语|中英双语(?:版|音轨)|(?:^|[^A-Za-z0-9])(?:ENGLISH|ENG[ ._-]AUDIO|EN[ ._-]AUDIO)(?:$|[^A-Za-z0-9])`)},
	{"ja", regexp.MustCompile(`(?i)日语(?:音轨|配音|发音|原声|中字)|国日双语|(?:^|[^A-Za-z0-9])(?:JAPANESE|JPN[ ._-]AUDIO)(?:$|[^A-Za-z0-9])`)},
	{"ko", regexp.MustCompile(`(?i)韩语(?:音轨|配音|发音|原声|中字)|(?:^|[^A-Za-z0-9])(?:KOREAN|KOR[ ._-]AUDIO)(?:$|[^A-Za-z0-9])`)},
	{"yue", regexp.MustCompile(`(?i)粤语|粤配|国粤双语|(?:^|[^A-Za-z0-9])CANTONESE(?:$|[^A-Za-z0-9])`)},
	{"fr", regexp.MustCompile(`(?i)法语(?:音轨|配音|发音|原声|中字)|(?:^|[^A-Za-z0-9])FRENCH(?:$|[^A-Za-z0-9])`)},
	{"de", regexp.MustCompile(`(?i)德语(?:音轨|配音|发音|原声|中字)|(?:^|[^A-Za-z0-9])GERMAN(?:$|[^A-Za-z0-9])`)},
	{"es", regexp.MustCompile(`(?i)西班牙语(?:音轨|配音|发音|原声|中字)|(?:^|[^A-Za-z0-9])SPANISH(?:$|[^A-Za-z0-9])`)},
	{"th", regexp.MustCompile(`(?i)泰语(?:音轨|配音|发音|原声|中字)|(?:^|[^A-Za-z0-9])THAI(?:$|[^A-Za-z0-9])`)},
}
var audioNegatives = regexp.MustCompile(`(?i)非国语|无国语|无国配|无粤语|无粤配|NO[ ._-](?:MANDARIN|CANTONESE|ENGLISH)`)
var subLanguageContext = regexp.MustCompile(`(?i)(?:ENGLISH|MANDARIN|CANTONESE|JAPANESE|KOREAN|FRENCH|GERMAN|SPANISH|THAI)[ ._-]+SUB(?:TITLE)?S?`)
var nativeLanguageWord = regexp.MustCompile(`(?i)\b(?:ENGLISH|MANDARIN|CANTONESE|JAPANESE|KOREAN|FRENCH|GERMAN|SPANISH|THAI)\b`)
var explicitNativeAudio = regexp.MustCompile(`(?i)(?:\b(?:ENGLISH|MANDARIN|CANTONESE|JAPANESE|KOREAN|FRENCH|GERMAN|SPANISH|THAI)[ .:_-]+(?:AUDIO|DTS|TRUEHD|FLAC|AAC|DDP|AC3)\b|\bAUDIO[ .:_-]+(?:ENGLISH|MANDARIN|CANTONESE|JAPANESE|KOREAN|FRENCH|GERMAN|SPANISH|THAI)\b)`)
var subtitleSpecs = []audioTagSpec{
	{"ja", regexp.MustCompile(`(?i)日文字幕|日语字幕|JAPANESE[ ._-]+SUB(?:TITLE)?S?`)},
	{"ko", regexp.MustCompile(`(?i)韩文字幕|韩语字幕|KOREAN[ ._-]+SUB(?:TITLE)?S?`)},
	{"fr", regexp.MustCompile(`(?i)法文字幕|法语字幕|FRENCH[ ._-]+SUB(?:TITLE)?S?`)},
	{"de", regexp.MustCompile(`(?i)德文字幕|德语字幕|GERMAN[ ._-]+SUB(?:TITLE)?S?`)},
	{"es", regexp.MustCompile(`(?i)西班牙语字幕|SPANISH[ ._-]+SUB(?:TITLE)?S?`)},
	{"th", regexp.MustCompile(`(?i)泰文字幕|泰语字幕|THAI[ ._-]+SUB(?:TITLE)?S?`)},
}

func checkTags(resource sites.Resource, policy Policy, reject func(string)) {
	text := releaseTagText(resource)
	if len(policy.Source) > 64 || len(policy.HDR) > 64 {
		reject("invalid_policy_tags")
		return
	}
	if len(policy.PreferredGroups) > 32 || len(policy.PreferredResolutions) > 16 {
		reject("invalid_policy_preferences")
		return
	}
	for _, group := range policy.PreferredGroups {
		if !validGroup(group) {
			reject("invalid_policy_preferred_group")
		}
	}
	for _, res := range policy.PreferredResolutions {
		if resolution(res) == "" {
			reject("invalid_policy_preferred_resolution")
		}
	}
	if policy.Source != "" {
		wanted := normalizedSource(policy.Source)
		if wanted == "" {
			reject("invalid_policy_source")
		} else {
			seen := map[string]bool{}
			for _, tag := range qualityValues(sourceTags, text) {
				seen[normalizedSource(tag)] = true
			}
			supplied := normalizedSource(resource.Source)
			if resource.Source != "" && supplied == "" {
				reject("source_unknown")
			}
			if supplied != "" {
				seen[supplied] = true
			}
			primary := 0
			for source := range seen {
				if source != "REMUX" {
					primary++
				}
			}
			if primary > 1 {
				reject("source_conflict")
			} else if len(seen) == 0 {
				reject("source_unknown")
			} else if !seen[wanted] {
				reject("source_mismatch")
			}
		}
	}
	if policy.HDR != "" {
		wanted := strings.ToLower(strings.TrimSpace(policy.HDR))
		switch wanted {
		case "sdr", "hdr10", "hdr10plus", "dolby_vision":
		default:
			reject("invalid_policy_hdr")
			wanted = ""
		}
		seen := map[string]bool{}
		if sdrTag.MatchString(text) {
			seen["sdr"] = true
		}
		if hdr10Tag.MatchString(text) {
			seen["hdr10"] = true
		}
		if hdr10PlusTag.MatchString(text) {
			seen["hdr10plus"] = true
		}
		if visionTag.MatchString(text) {
			seen["dolby_vision"] = true
		}
		if seen["sdr"] && (seen["hdr10"] || seen["hdr10plus"] || seen["dolby_vision"] || genericHDRTag.MatchString(text)) {
			reject("hdr_conflict")
		} else if len(seen) == 0 {
			reject("hdr_unknown")
		} else if wanted != "" && !seen[wanted] {
			reject("hdr_mismatch")
		}
	}
	if len(policy.Languages) > 16 || len(policy.Subtitles) > 16 {
		reject("invalid_policy_language_count")
		return
	}
	if len(policy.Languages) > 0 {
		// Subtitle language labels and explicit negatives are not audio evidence.
		// Provider subtitle text often repeats a film's translated title. Bare
		// native words there need an audio label; release-suffix tokens remain
		// valid explicit evidence on their own.
		audioResource := resource
		audioResource.Subtitle = nativeLanguageWord.ReplaceAllString(resource.Subtitle, " ") + " " + strings.Join(explicitNativeAudio.FindAllString(resource.Subtitle, -1), " ")
		audioText := subLanguageContext.ReplaceAllString(audioNegatives.ReplaceAllString(releaseTagText(audioResource), " "), " ")
		negative := map[string]bool{}
		for _, tag := range audioNegatives.FindAllString(text, -1) {
			lower := strings.ToLower(tag)
			switch {
			case strings.Contains(lower, "国语") || strings.Contains(lower, "国配") || strings.Contains(lower, "mandarin"):
				negative["zh"] = true
			case strings.Contains(lower, "粤语") || strings.Contains(lower, "粤配") || strings.Contains(lower, "cantonese"):
				negative["yue"] = true
			case strings.Contains(lower, "english"):
				negative["en"] = true
			}
		}
		for _, required := range policy.Languages {
			if len(required) > 32 {
				reject("invalid_policy_language")
				continue
			}
			code := languageCode(required, false)
			if code == "" {
				reject("invalid_policy_language")
				continue
			}
			found := false
			for _, spec := range audioSpecs {
				if spec.code == code && spec.expression.MatchString(audioText) {
					found = true
				}
			}
			if !found {
				reject("audio_language_not_confirmed")
			} else if negative[code] {
				reject("audio_language_conflict")
			}
		}
	}
	if len(policy.Subtitles) > 0 {
		negativeChinese := noChineseSubtitles.MatchString(text)
		positiveText := noChineseSubtitles.ReplaceAllString(text, " ")
		subs := map[string]bool{}
		if chineseSubtitles.MatchString(positiveText) {
			subs["zh"] = true
		}
		if simplifiedSubtitles.MatchString(positiveText) {
			subs["zh"] = true
			subs["zh-hans"] = true
		}
		if traditionalSubtitles.MatchString(positiveText) {
			subs["zh"] = true
			subs["zh-hant"] = true
		}
		if englishSubtitles.MatchString(positiveText) {
			subs["en"] = true
		}
		for _, spec := range subtitleSpecs {
			if spec.expression.MatchString(text) {
				subs[spec.code] = true
			}
		}
		if (noneSubtitles.MatchString(text) && len(subs) > 0) || (negativeChinese && subs["zh"]) {
			reject("subtitle_conflict")
		}
		for _, required := range policy.Subtitles {
			if len(required) > 32 {
				reject("invalid_policy_subtitle_language")
				continue
			}
			code := languageCode(required, true)
			if code == "" {
				reject("invalid_policy_subtitle_language")
				continue
			}
			if !subs[code] {
				reject("subtitle_language_not_confirmed")
			}
		}
	}
}
func validGroup(value string) bool {
	if len(value) == 0 || len(value) > 64 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' && r != '.' && r != '-' {
			return false
		}
	}
	return true
}

// Rank applies explicit preferences after hard filtering. Resolution preference
// takes priority, then release group; unknown labels simply earn no preference.
func Rank(resource sites.Resource, policy Policy) int {
	if len(resource.Title) > 4096 || len(resource.Subtitle) > 4096 || len(policy.PreferredGroups) > 32 || len(policy.PreferredResolutions) > 16 {
		return 0
	}
	score := 0
	observed := resolution(resource.Resolution)
	values := qualityValues(resolutionMarker, resource.Title+" "+resource.Subtitle)
	seen := map[string]bool{}
	if observed != "" {
		seen[observed] = true
	}
	for _, v := range values {
		seen[resolution(v)] = true
	}
	if len(seen) == 1 {
		for v := range seen {
			observed = v
		}
	} else {
		observed = ""
	}
	for i, wanted := range policy.PreferredResolutions {
		if observed != "" && observed == resolution(wanted) {
			score += (len(policy.PreferredResolutions) - i) * 1000
			break
		}
	}
	title := strings.ToLower(strings.TrimSpace(stripExtension(resource.Title)))
	for i, group := range policy.PreferredGroups {
		if validGroup(group) && strings.HasSuffix(title, "-"+strings.ToLower(group)) {
			score += (len(policy.PreferredGroups) - i) * 10
			break
		}
	}
	return score
}
