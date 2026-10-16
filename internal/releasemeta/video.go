package releasemeta

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type videoState struct {
	cn, en, unknown, year, lastType, last string
	stopName, stopCN                      bool
	video, audio                          string
	sources, effects                      []string
}

func (s *videoState) named() bool { return s.cn != "" || s.en != "" }

func parseVideo(title string, subtitle *string, isfile bool, options Options) (Metadata, error) {
	m := Metadata{Type: "未知", Kind: "video"}
	if title == "" {
		return m, nil
	}
	if isfile && digits(title) && len(title) < 5 {
		n, _ := strconv.Atoi(title)
		m.BeginEpisode, m.Type = intPointer(n), "电视剧"
		return m, nil
	}
	if match := rxFirst(`^(?:Season\s+|S)(\d{1,3})$`, title, true); match != nil {
		if n, err := metadataInteger(match.GroupByNumber(1).String(), options.SourceMode); err == nil {
			m.BeginSeason = intPointer(n)
		}
		m.Type = "电视剧"
		return m, nil
	}
	working := title
	if match := rxFirst(`^[\[【](.+?)[\]】]`, working, false); match != nil {
		prefix := match.GroupByNumber(1).String()
		working = strings.TrimPrefix(working, match.String())
		if rxHas(`[A-Za-z]+\..+(?:19|20)\d{2}`, prefix, false) && rxHas(`(?:2160|1080|720|480)[PIpi]|4K|UHD|Blu[\-.]?ray|REMUX|WEB[\-.]?DL|HDTV`, prefix, true) {
			working = prefix + working
		}
	}
	working = rxReplace(`([\s.]+)(\d{4})-(\d{4})`, working, "${1}${2}", false)
	working = rxReplace(`[0-9.]+\s*[MGT]i?B(?![A-Z]+)`, working, "", true)
	working = rxReplace(`\d{4}[\s._-]\d{1,2}[\s._-]\d{1,2}`, working, "", false)
	tokens := strings.FieldsFunc(working, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".()[]-【】/～;&|#_「」~", r)
	})
	s := videoState{}
	iteration := 0
	for index := 0; index < len(tokens); index++ {
		iteration++
		token := tokens[index]
		upper := strings.ToUpper(token)
		if s.named() && (s.year != "" || m.BeginSeason != nil || m.BeginEpisode != nil || m.Resolution != "") && rxHas(videoPatterns["_part_re"], token, true) {
			if index+1 < len(tokens) {
				next := tokens[index+1]
				if digits(next) && (len(next) == 1 || len(next) == 2 && strings.HasPrefix(next, "0")) || slices.Contains([]string{"A", "B", "C", "I", "II", "III"}, strings.ToUpper(next)) {
					index++
				}
			}
			s.lastType = "part"
			continue
		}
		if !initVideoName(&s, token, options) {
			continue
		}
		if s.named() && digits(token) && len(token) == 4 {
			if n, _ := strconv.Atoi(token); n > 1900 && n < 2050 {
				s.year, s.lastType, s.stopName = token, "year", true
				continue
			}
		}
		if s.named() {
			if match := rxFirst(videoPatterns["_resources_pix_re"], token, true); match != nil {
				s.lastType, s.stopName = "pix", true
				if m.Resolution == "" {
					m.Resolution = strings.ToLower(firstCapture(match.Groups()))
					if digits(m.Resolution) || options.SourceMode == "legacy" && unicodeDecimalDigits(m.Resolution) {
						m.Resolution += "p"
					}
				}
				continue
			}
			if match := rxFirst(videoPatterns["_resources_pix_re2"], token, true); match != nil {
				s.lastType, s.stopName = "pix", true
				if m.Resolution == "" {
					m.Resolution = strings.ToLower(match.GroupByNumber(1).String())
				}
				continue
			}
		}
		seasons := rxMatches(videoPatterns["_season_re"], token, true)
		if len(seasons) > 0 {
			s.lastType, s.stopName, m.Type = "season", true, "电视剧"
			for _, match := range seasons {
				n, err := metadataInteger(firstCapture(match.Groups()), options.SourceMode)
				if err != nil {
					break
				}
				if m.BeginSeason == nil {
					m.BeginSeason = intPointer(n)
				} else if n > *m.BeginSeason {
					m.EndSeason = intPointer(n)
					if isfile {
						m.EndSeason = nil
					}
				}
			}
		} else if digits(token) {
			if s.lastType == "SEASON" && m.BeginSeason == nil && len(token) < 3 {
				n, _ := strconv.Atoi(token)
				m.BeginSeason, m.Type, s.lastType, s.stopName = intPointer(n), "电视剧", "season", true
				continue
			}
		} else if upper == "SEASON" && m.BeginSeason == nil {
			s.lastType = "SEASON"
		} else if m.Type == "电视剧" && m.BeginSeason == nil {
			m.BeginSeason = intPointer(1)
		}
		episodes := rxMatches(videoPatterns["_episode_re"], token, true)
		if len(episodes) > 0 {
			s.lastType, s.stopName, m.Type = "episode", true, "电视剧"
			for _, match := range episodes {
				n, err := metadataInteger(firstCapture(match.Groups()), options.SourceMode)
				if err != nil {
					break
				}
				if m.BeginEpisode == nil {
					m.BeginEpisode = intPointer(n)
				} else if n > *m.BeginEpisode {
					m.EndEpisode = intPointer(n)
					if isfile && n-*m.BeginEpisode+1 > 2 {
						m.EndEpisode = nil
					}
				}
			}
			continue
		} else if digits(token) {
			n, _ := strconv.Atoi(token)
			if m.BeginEpisode != nil && m.EndEpisode == nil && len(token) < 5 && n > *m.BeginEpisode && s.lastType == "episode" {
				m.EndEpisode, m.Type = intPointer(n), "电视剧"
				if isfile && n-*m.BeginEpisode+1 > 2 {
					m.EndEpisode = nil
				}
				continue
			} else if m.BeginEpisode == nil && len(token) > 1 && len(token) < 4 && s.lastType != "year" && s.lastType != "videoencode" && token != s.unknown || s.lastType == "EPISODE" && m.BeginEpisode == nil && len(token) < 5 {
				m.BeginEpisode, m.Type, s.lastType, s.stopName = intPointer(n), "电视剧", "episode", true
				continue
			}
		} else if upper == "EPISODE" {
			s.lastType = "EPISODE"
		}
		if s.named() && initResourceType(&s, token, options.SourceMode) {
			continue
		}
		if s.named() && initStreamingPlatform(tokens, &index, iteration, token) {
			continue
		}
		if s.named() && (s.year != "" || m.Resolution != "" || m.BeginSeason != nil || m.BeginEpisode != nil) {
			if rxHas(videoPatterns["_video_encode_re"], token, true) || upper == "H" || upper == "X" {
				s.lastType, s.stopName, s.video = "videoencode", true, token
				s.last = upper
				continue
			}
			if rxHas(`(?<![A-Za-z0-9])(?:8|10|12|16)[\s._-]*bits?(?![A-Za-z0-9])`, token, true) {
				s.lastType, s.stopName = "videobit", true
				continue
			}
			if rxHas(videoPatterns["_audio_encode_re"], token, true) {
				s.lastType, s.stopName, s.audio = "audioencode", true, token
				s.last = upper
				continue
			}
		}
		if s.named() && rxHas(`\d{2,3}(?=FPS)`, token, true) {
			s.lastType, s.stopName = "fps", true
		}
	}
	m.Quality = strings.TrimSpace(strings.Join(s.sources, " "))
	slices.Reverse(s.effects)
	m.Effect = strings.Join(s.effects, " ")
	if strings.Contains(m.Quality, "BluRay") && (subtitle != nil && rxHas(`DIY`, *subtitle, true) || rxHas(`-DIY@`, title, true)) {
		m.Quality += " DIY"
	}
	if !applySubtitle(&m, title) && subtitle != nil {
		applySubtitle(&m, *subtitle)
	}
	for _, name := range []string{s.cn, s.en} {
		cleaned := strings.TrimSpace(rxReplace(videoPatterns["_name_nostring_re"], name, "", true))
		if digits(cleaned) && s.year == "" && m.BeginSeason == nil && m.Resolution == "" && m.Quality == "" && s.audio == "" && s.video == "" {
			n, _ := strconv.Atoi(cleaned)
			if n < 1800 && m.BeginEpisode == nil {
				m.BeginEpisode = intPointer(n)
			}
		}
		if m.Name == "" && cleaned != "" {
			m.Name = cleaned
		}
	}
	var err error
	m.Team, err = matchTeam(title, options)
	if err == nil {
		m.Customization, err = matchCustomization(title, options)
	}
	return m, err
}

func initVideoName(s *videoState, token string, options Options) bool {
	if s.unknown != "" {
		if s.cn == "" {
			if s.en == "" {
				s.en = s.unknown
			} else if s.unknown != s.year {
				s.en += " " + s.unknown
			}
			s.lastType = "enname"
		}
		s.unknown = ""
	}
	if s.stopName {
		return true
	}
	if strings.EqualFold(token, "AKA") {
		s.stopName = true
		return false
	}
	if slices.Contains([]string{"共", "第", "季", "集", "话", "話", "期"}, token) {
		s.lastType = "name_se_words"
		return true
	}
	if strings.ContainsFunc(token, func(r rune) bool { return r >= '\u4e00' && r <= '\u9fff' }) {
		s.lastType = "cnname"
		if s.cn == "" {
			s.cn = token
		} else if !s.stopCN {
			if rxHas(`剧场版|劇場版|电影版|電影版`, token, true) || !rxHas(videoPatterns["_name_no_chinese_re"], token, true) && !strings.ContainsAny(token, "共第季集话話期") {
				s.cn += " " + token
			}
			s.stopCN = true
		}
		return true
	}
	roman := rxHas(videoPatterns["_roman_numerals"], token, false)
	if digits(token) || roman {
		if s.lastType == "name_se_words" {
			return true
		}
		if s.named() {
			if strings.HasPrefix(token, "0") {
				return true
			}
			if n, _ := strconv.Atoi(token); !roman && s.lastType == "cnname" && n < 1900 {
				return true
			}
			if digits(token) && len(token) < 4 || roman {
				if s.lastType == "cnname" {
					s.cn += " " + token
				} else if s.lastType == "enname" {
					s.en += " " + token
				}
				return false
			}
		}
		if s.unknown == "" {
			s.unknown = token
		}
	} else if rxHas(videoPatterns["_season_re"], token, true) || rxHas(videoPatterns["_episode_re"], token, true) || rxHas(videoPatterns["_resources_type_re"], token, true) || rxHas(videoPatterns["_resources_pix_re"], token, true) {
		s.stopName = true
	} else if !configuredMediaExtension("."+strings.ToLower(token), options) {
		if s.en != "" {
			s.en += " "
		}
		s.en += token
		s.lastType = "enname"
	}
	return true
}

func initResourceType(s *videoState, token, mode string) bool {
	upper := strings.ToUpper(token)
	if upper == "DL" && s.lastType == "source" && s.last == "WEB" || upper == "WEBDL" {
		if mode == "legacy" {
			s.sources = []string{"WEB-DL"}
		} else {
			if upper == "DL" && len(s.sources) > 0 && strings.EqualFold(s.sources[len(s.sources)-1], "WEB") {
				s.sources = s.sources[:len(s.sources)-1]
			}
			appendSource(s, "WEB-DL")
		}
		return true
	}
	if upper == "RAY" && s.lastType == "source" && s.last == "BLU" {
		if mode == "legacy" {
			if strings.Join(s.sources, " ") == "UHD" {
				s.sources = []string{"UHD BluRay"}
			} else {
				s.sources = []string{"BluRay"}
			}
		} else {
			if len(s.sources) > 0 && strings.EqualFold(s.sources[len(s.sources)-1], "BLU") {
				s.sources = s.sources[:len(s.sources)-1]
			}
			appendSource(s, "BluRay")
		}
		return true
	}
	if mode == "legacy" && upper == "REMUX" && strings.Join(s.sources, " ") == "BluRay" {
		s.sources = []string{"BluRay REMUX"}
		return true
	}
	if mode == "legacy" && upper == "BLURAY" && strings.Join(s.sources, " ") == "UHD" {
		s.sources = []string{"UHD BluRay"}
		return true
	}
	if rxHas(videoPatterns["_source_re"], token, true) {
		s.lastType, s.stopName = "source", true
		if mode == "rust" {
			appendSource(s, token)
			s.last = upper
		} else if len(s.sources) == 0 {
			s.sources = []string{token}
			s.last = upper
		}
		return true
	}
	if rxHas(videoPatterns["_effect_re"], token, true) {
		s.lastType, s.stopName, s.last = "effect", true, upper
		if !slices.Contains(s.effects, token) {
			s.effects = append(s.effects, token)
		}
		return true
	}
	return false
}

func appendSource(s *videoState, value string) {
	canonical := map[string]string{"BLURAY": "BluRay", "HDTV": "HDTV", "UHDTV": "UHDTV", "HDDVD": "HDDVD", "WEBRIP": "WEBRip", "DVDRIP": "DVDRip", "BDRIP": "BDRIP", "BLU": "BLU", "WEB": "WEB", "BD": "BD", "HDRIP": "HDRip", "REMUX": "REMUX", "UHD": "UHD", "WEBDL": "WEB-DL", "WEB-DL": "WEB-DL"}[strings.ToUpper(value)]
	for _, old := range s.sources {
		if strings.EqualFold(old, canonical) {
			return
		}
	}
	s.sources = append(s.sources, canonical)
}

func digits(text string) bool {
	return text != "" && !strings.ContainsFunc(text, func(r rune) bool { return r < '0' || r > '9' })
}

func unicodeDecimalDigits(text string) bool {
	return text != "" && !strings.ContainsFunc(text, func(r rune) bool { return !unicode.IsDigit(r) })
}

func metadataInteger(text, mode string) (int, error) {
	if mode != "legacy" || utf8.RuneCountInString(text) == len(text) {
		return strconv.Atoi(text)
	}
	var normalized strings.Builder
	for _, r := range text {
		matched := false
		for _, table := range unicode.Digit.R16 {
			if r >= rune(table.Lo) && r <= rune(table.Hi) && (r-rune(table.Lo))%rune(table.Stride) == 0 {
				normalized.WriteByte('0' + byte((r-rune(table.Lo))/rune(table.Stride)%10))
				matched = true
				break
			}
		}
		if !matched {
			for _, table := range unicode.Digit.R32 {
				if r >= rune(table.Lo) && r <= rune(table.Hi) && (r-rune(table.Lo))%rune(table.Stride) == 0 {
					normalized.WriteByte('0' + byte((r-rune(table.Lo))/rune(table.Stride)%10))
					matched = true
					break
				}
			}
		}
		if !matched {
			return strconv.Atoi(text)
		}
	}
	return strconv.Atoi(normalized.String())
}

func initStreamingPlatform(tokens []string, index *int, iteration int, token string) bool {
	platform := streamingPlatforms[strings.ToUpper(token)]
	queryRange := 1
	if !platform {
		for _, item := range []struct {
			at   int
			next bool
		}{{iteration - 2, false}, {*index + 2, true}} {
			if item.at < 0 || item.at >= len(tokens) {
				continue
			}
			for _, separator := range []string{" ", "-"} {
				combined := tokens[item.at] + separator + token
				if item.next {
					combined = token + separator + tokens[item.at]
				}
				if streamingPlatforms[strings.ToUpper(combined)] {
					platform, queryRange = true, 2
					if item.next && *index+1 < len(tokens) {
						*index++
					}
					break
				}
			}
			if platform {
				break
			}
		}
	}
	if !platform {
		return false
	}
	start, end := max(0, iteration-queryRange-queryRange), min(len(tokens), iteration+queryRange)
	for _, token := range tokens[start:end] {
		if slices.Contains([]string{"WEB", "DL", "WEBDL", "WEBRIP"}, strings.ToUpper(token)) {
			return true
		}
	}
	return false
}
