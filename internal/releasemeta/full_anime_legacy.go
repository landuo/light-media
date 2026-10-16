package releasemeta

import (
	"math/big"
	"strings"

	"light-media/internal/releasemeta/legacyanime"
)

func fullAnimeLegacyName(m *MetaObject, name string) error {
	split := true
	if strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		if animeContainsChinese(parts[0]) {
			m.Set("cn_name", parts[0])
			if len(parts) > 1 {
				m.Set("en_name", parts[1])
			}
			split = false
		} else if animeContainsChinese(parts[len(parts)-1]) {
			m.Set("cn_name", parts[len(parts)-1])
			if len(parts) > 1 {
				m.Set("en_name", parts[0])
			}
			split = false
		} else {
			name = parts[len(parts)-1]
		}
	}
	if split {
		last := ""
		for _, word := range legacyanime.Fields(name) {
			if strings.HasSuffix(word, "]") {
				word = strings.TrimSuffix(word, "]")
			}
			key := ""
			if legacyanime.IsDigit(word) {
				if last == "cn" {
					key = "cn_name"
				} else if last == "en" {
					key = "en_name"
				}
			} else if animeContainsChinese(word) {
				key, last = "cn_name", "cn"
			} else {
				key, last = "en_name", "en"
			}
			if key != "" {
				m.Set(key, m.Text(key)+" "+word)
			}
		}
	}
	if m.Text("cn_name") != "" {
		clean, e := legacyAnimeChineseKeyword(m.Text("cn_name"))
		if e != nil {
			return e
		}
		if clean == "" {
			m.Set("cn_name", nil)
		} else {
			m.Set("cn_name", clean)
		}
		if clean != "" {
			m.Set("cn_name", legacyanime.Strip(legacyAnimeReplace(legacyAnimeNamePattern, clean, "", true)))
		}
	}
	if m.Text("en_name") != "" {
		m.Set("en_name", legacyanime.TitleCase(legacyanime.Strip(legacyAnimeReplace(legacyAnimeNamePattern, m.Text("en_name"), "", true))))
		m.Set("_name", m.Text("en_name"))
	}
	return nil
}
func fullAnimeSetRange(m *MetaObject, kind string, values []string, mode string) error {
	var nums []*big.Int
	for _, value := range values {
		value = pyStrip(value)
		if mode == "legacy" && !legacyanime.IsDigit(value) {
			continue
		}
		if mode == "rust" {
			value = rxReplace(`v\d+$`, value, "", false)
		}
		n, e := fullInt(value, mode)
		if e != nil {
			if mode == "legacy" {
				return e
			}
			continue
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return nil
	}
	first, last := nums[0], nums[len(nums)-1]
	m.Set("begin_"+kind, first)
	if first.Cmp(last) != 0 {
		m.Set("end_"+kind, last)
		count := new(big.Int).Sub(last, first)
		count.Add(count, big.NewInt(1))
		m.Set("total_"+kind, count)
	} else {
		m.Set("total_"+kind, 1)
	}
	m.Set("type", "电视剧")
	return nil
}
func fullVideoBit(value string) any {
	if match := rxFirst(`(?<![A-Za-z0-9])(?P<bit>8|10|12|16)[\s._-]*bits?(?![A-Za-z0-9])`, value, true); match != nil {
		return match.GroupByName("bit").String() + "bit"
	}
	return nil
}
func fullAnimeFPS(m *MetaObject, title, mode string) error {
	pattern := `((\d{2,3})(?=FPS))`
	if mode == "legacy" {
		pattern = legacyAnimePattern(pattern)
	}
	if match := rxFirst(pattern, title, true); match != nil {
		value := match.GroupByNumber(1).String()
		if fullDigits(value, mode) {
			n, e := fullInt(value, mode)
			if e != nil {
				return e
			}
			m.Set("fps", n)
		}
	}
	return nil
}

// The source catches ordinary errors around the full legacy constructor; every
// assignment already made survives. Source nontermination is an explicit error.
func parseFullLegacyAnime(title string, subtitle *string, isfile bool, options Options) (*MetaObject, error) {
	m := fullBase(title, subtitle, isfile, "legacy")
	m.Kind = "anime"
	if title == "" {
		return m, nil
	}
	original, e := legacyAnimeParse(title)
	if e != nil {
		if e.Error() == "source_non_terminating" {
			return nil, e
		}
		return m, nil
	}
	prepared := prepareLegacyAnimeTitle(title)
	parsed, e := legacyAnimeParse(prepared)
	if e != nil {
		if e.Error() == "source_non_terminating" {
			return nil, e
		}
		return m, nil
	}
	if parsed == nil {
		return m, nil
	}
	name := legacyAnimeFirst(parsed, "anime_title")
	if legacyAnimeShouldRetry(name) {
		parsed, e = legacyAnimeParse("[ANIME]" + prepared)
		if e != nil {
			if e.Error() == "source_non_terminating" {
				return nil, e
			}
			return m, nil
		}
		if parsed != nil {
			name = legacyAnimeFirst(parsed, "anime_title")
		}
	}
	if legacyAnimeShouldRetry(name) {
		if match := rxFirst(`\[(.+?)]`, prepared, false); match != nil && match.GroupByNumber(1).String() != "" {
			name = pyStrip(match.GroupByNumber(1).String())
		}
	}
	if e := fullAnimeLegacyName(m, name); e != nil {
		return m, nil
	}
	if parsed == nil {
		return m, nil
	}
	if year := legacyAnimeFirst(parsed, "anime_year"); legacyanime.IsDigit(year) {
		m.Set("year", year)
	}
	if e := fullAnimeSetRange(m, "season", legacyAnimeValues(parsed, "anime_season"), "legacy"); e != nil {
		return m, nil
	}
	episodes := legacyAnimeValues(parsed, "episode_number")
	if len(episodes) > 0 && episodes[0] != "" {
		first, e := legacyanime.Integer(episodes[0])
		if e == nil {
			m.Set("begin_episode", first)
			var last *big.Int
			if len(episodes) > 1 && episodes[len(episodes)-1] != "" {
				last, e = legacyanime.Integer(episodes[len(episodes)-1])
			}
			if e == nil {
				if last != nil && last.Cmp(first) != 0 {
					m.Set("end_episode", last)
					total := new(big.Int).Sub(last, first)
					total.Add(total, big.NewInt(1))
					m.Set("total_episode", total)
				} else {
					m.Set("total_episode", 1)
				}
			}
		}
		if e != nil {
			m.Set("begin_episode", nil)
			m.Set("end_episode", nil)
		}
		m.Set("type", "电视剧")
	}
	var resolution any
	if values := legacyAnimeValues(parsed, "video_resolution"); len(values) > 0 {
		pix := values[0]
		if pix != "" {
			if at := strings.LastIndexAny(pix, "Xx"); at >= 0 {
				pix = pix[at+1:] + "p"
			} else {
				pix = legacyanime.LowerCase(pix)
			}
			if legacyanime.IsDigit(pix) {
				pix += "p"
			}
		}
		resolution = pix
	}
	m.Set("resource_pix", resolution)
	team, e := matchTeam(title, options)
	if e != nil {
		return m, nil
	}
	if team == "" {
		if original == nil {
			return m, nil
		}
		team = legacyAnimeFirst(original, "release_group")
	}
	if team != "" {
		m.Set("resource_team", team)
	} else {
		m.Set("resource_team", nil)
	}
	custom, e := matchCustomization(title, options)
	if e != nil {
		return m, nil
	}
	if custom != "" {
		m.Set("customization", custom)
	} else {
		m.Set("customization", nil)
	}
	var video, audio any
	if values := legacyAnimeValues(parsed, "video_term"); len(values) > 0 {
		video = values[0]
	}
	m.Set("video_encode", video)
	bit := fullVideoBit(title)
	if bit == nil {
		bit = fullVideoBit(m.Text("video_encode"))
	}
	m.Set("video_bit", bit)
	if values := legacyAnimeValues(parsed, "audio_term"); len(values) > 0 {
		audio = values[0]
	}
	m.Set("audio_encode", audio)
	if e := fullAnimeFPS(m, title, "legacy"); e != nil {
		return m, nil
	}
	if e := fullSubtitle(m, m.Text("org_string"), "legacy"); e != nil {
		return m, nil
	}
	if !fullBool(m, "_subtitle_flag") && m.Text("subtitle") != "" {
		if e := fullSubtitle(m, m.Text("subtitle"), "legacy"); e != nil {
			return m, nil
		}
	}
	return m, nil
}
