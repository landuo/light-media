package releasemeta

// Complete-object constructor work: literal source assignments are retained in
// MetaObject.Instance. The existing consumption projection is not used as output.
import (
	"errors"
	"math/big"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/releasemeta/rustanime"
)

type metaInfoEnvironment struct {
	Defaults           Options
	StreamingPlatforms map[string]string
	LegacyPinyin       func(string) (bool, error)
	RustPinyin         func(string) (bool, error)
}

func pyStrip(v string) string { return legacyanime.Strip(v) }
func fullStrip(v, mode string) string {
	if mode == "legacy" {
		return pyStrip(v)
	}
	return strings.TrimSpace(v)
}
func fullBase(title string, subtitle *string, isfile bool, mode string) *MetaObject {
	m := EmptyMetaObject()
	if title != "" || mode == "rust" {
		if title != "" {
			m.Set("org_string", fullStrip(title, mode))
		}
		var sub any
		if subtitle != nil && *subtitle != "" {
			sub = fullStrip(*subtitle, mode)
		}
		m.Set("subtitle", sub)
		m.Set("isfile", isfile)
	}
	return m
}
func fullBool(m *MetaObject, key string) bool {
	if v, ok := m.Instance[key]; ok {
		return truth(v)
	}
	return key == "_continue_flag"
}
func fullInt(v string, mode string) (*big.Int, error) {
	if mode == "legacy" {
		return legacyanime.Integer(v)
	}
	if !digits(v) {
		return nil, errors.New("ValueError")
	}
	n, ok := new(big.Int).SetString(v, 10)
	if !ok || !n.IsInt64() {
		return nil, errors.New("ValueError")
	}
	return n, nil
}
func fullDigits(v, mode string) bool {
	if mode == "legacy" {
		return legacyanime.IsDigit(v)
	}
	return digits(v)
}
func fullLen(v, mode string) int {
	if mode == "legacy" {
		return utf8.RuneCountInString(v)
	}
	return len(v)
}
func fullLower(v, mode string) string {
	if mode == "legacy" {
		return legacyanime.LowerCase(v)
	}
	return strings.ToLower(v)
}
func fullCase(v, mode string) string {
	if mode == "legacy" {
		return legacyanime.TitleCase(v)
	}
	return rustanime.TitleCase(v)
}
func fullHas(m *MetaObject, key, mode string) bool {
	if mode == "legacy" {
		return truth(m.Get(key))
	}
	return m.Get(key) != nil
}
func fullMatch(pattern, value, mode string, ignore bool) bool {
	if mode == "legacy" {
		return legacyAnimeHas(pattern, value, ignore)
	}
	return fullRXHas(pattern, value, ignore, mode)
}
func fullReplace(pattern, value, replacement, mode string, ignore bool) string {
	if mode == "legacy" {
		return legacyAnimeReplace(pattern, value, replacement, ignore)
	}
	return fullRXReplace(pattern, value, replacement, ignore, mode)
}
func fullFlag(m *MetaObject, typ string) {
	m.Set("_last_token_type", typ)
	m.Set("_continue_flag", false)
	m.Set("_stop_name_flag", true)
}
func fullAddName(m *MetaObject, key, value string) {
	if existing := m.Text(key); existing != "" {
		m.Set(key, existing+" "+value)
	} else {
		m.Set(key, value)
	}
}

func fullVideoName(m *MetaObject, token string, options Options) {
	mode := options.SourceMode
	if unknown := m.Text("_unknown_name_str"); unknown != "" {
		if !fullHas(m, "cn_name", mode) {
			if !fullHas(m, "en_name", mode) {
				m.Set("en_name", unknown)
			} else if unknown != m.Text("year") {
				fullAddName(m, "en_name", unknown)
			}
			m.Set("_last_token_type", "enname")
		}
		m.Set("_unknown_name_str", "")
	}
	if fullBool(m, "_stop_name_flag") {
		return
	}
	if asciiEqualFold(token, "AKA") {
		m.Set("_continue_flag", false)
		m.Set("_stop_name_flag", true)
		return
	}
	if slices.Contains([]string{"共", "第", "季", "集", "话", "話", "期"}, token) {
		m.Set("_last_token_type", "name_se_words")
		return
	}
	if animeContainsChinese(token) {
		m.Set("_last_token_type", "cnname")
		if !fullHas(m, "cn_name", mode) {
			m.Set("cn_name", token)
		} else if !fullBool(m, "_stop_cnname_flag") {
			if fullMatch(`剧场版|劇場版|电影版|電影版`, token, mode, true) || !fullMatch(videoPatterns["_name_no_chinese_re"], token, mode, true) && !strings.ContainsAny(token, "共第季集话話期") {
				fullAddName(m, "cn_name", token)
			}
			m.Set("_stop_cnname_flag", true)
		}
		return
	}
	roman := fullMatch(videoPatterns["_roman_numerals"], token, mode, false)
	numeric := fullDigits(token, mode)
	if numeric || roman {
		if m.Text("_last_token_type") == "name_se_words" {
			return
		}
		if m.Name() != "" {
			if strings.HasPrefix(token, "0") {
				return
			}
			n, e := fullInt(token, mode)
			if numeric && mode == "legacy" && e != nil {
				return
			}
			if !roman && m.Text("_last_token_type") == "cnname" && e == nil && n.Cmp(big.NewInt(1900)) < 0 {
				return
			}
			if numeric && fullLen(token, mode) < 4 || roman {
				if key := m.Text("_last_token_type"); key == "cnname" {
					fullAddName(m, "cn_name", token)
				} else if key == "enname" {
					fullAddName(m, "en_name", token)
				}
				m.Set("_continue_flag", false)
			} else if numeric && fullLen(token, mode) == 4 && m.Text("_unknown_name_str") == "" {
				m.Set("_unknown_name_str", token)
			}
		} else if m.Text("_unknown_name_str") == "" {
			m.Set("_unknown_name_str", token)
		}
		return
	}
	if fullMatch(videoPatterns["_season_re"], token, mode, true) {
		if fullHas(m, "en_name", mode) && fullMatch(`SEASON$`, m.Text("en_name"), mode, true) {
			m.Set("en_name", m.Text("en_name")+" ")
		}
		m.Set("_stop_name_flag", true)
		return
	}
	if fullMatch(videoPatterns["_episode_re"], token, mode, true) || fullMatch(videoPatterns["_resources_type_re"], token, mode, true) || fullMatch(videoPatterns["_resources_pix_re"], token, mode, true) {
		m.Set("_stop_name_flag", true)
		return
	}
	if !configuredMediaExtension("."+fullLower(token, mode), options) {
		fullAddName(m, "en_name", token)
		m.Set("_last_token_type", "enname")
	}
}
func fullVideoYear(m *MetaObject, token, mode string) error {
	if m.Name() == "" || !fullDigits(token, mode) || fullLen(token, mode) != 4 {
		return nil
	}
	n, e := fullInt(token, mode)
	if e != nil {
		return e
	}
	if n.Cmp(big.NewInt(1900)) <= 0 || n.Cmp(big.NewInt(2050)) >= 0 {
		return nil
	}
	if old := m.Text("year"); fullHas(m, "year", mode) {
		if fullHas(m, "en_name", mode) {
			m.Set("en_name", pyStrip(m.Text("en_name"))+" "+old)
		} else if fullHas(m, "cn_name", mode) {
			fullAddName(m, "cn_name", old)
		}
	} else if fullHas(m, "en_name", mode) && fullMatch(`SEASON$`, m.Text("en_name"), mode, true) {
		m.Set("en_name", m.Text("en_name")+" ")
	}
	m.Set("year", token)
	fullFlag(m, "year")
	return nil
}

type fullTokens struct {
	list  []string
	index int
}

func (t *fullTokens) cur() string {
	if t.index >= len(t.list) {
		return ""
	}
	return t.list[t.index]
}
func (t *fullTokens) next() string {
	v := t.cur()
	if v != "" {
		t.index++
	}
	return v
}
func (t *fullTokens) peek() string {
	if t.index+1 >= len(t.list) {
		return ""
	}
	return t.list[t.index+1]
}
func fullVideoTokens(text, mode string) *fullTokens {
	var list []string
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || mode == "legacy" && r >= 0x1c && r <= 0x1f || strings.ContainsRune("()[]-【】/～;&|#_「」~", r)
	})
	for _, part := range parts {
		if mode == "rust" && fullMatch(`(`+videoPatterns["_audio_encode_re"]+`)`, part, mode, true) {
			list = append(list, part)
			continue
		}
		for _, token := range strings.Split(part, ".") {
			if token != "" {
				list = append(list, token)
			}
		}
	}
	return &fullTokens{list: list}
}
func fullVideoPart(m *MetaObject, token, mode string, tokens *fullTokens) {
	if m.Name() == "" || !fullHas(m, "year", mode) && m.Number("begin_season") == nil && !fullHas(m, "begin_episode", mode) && !fullHas(m, "resource_pix", mode) && !fullHas(m, "resource_type", mode) {
		return
	}
	if !fullMatch(videoPatterns["_part_re"], token, mode, true) {
		return
	}
	if !fullHas(m, "part", mode) {
		m.Set("part", token)
	}
	next := tokens.cur()
	if next != "" && (fullDigits(next, mode) && (fullLen(next, mode) == 1 || fullLen(next, mode) == 2 && strings.HasPrefix(next, "0")) || slices.Contains([]string{"A", "B", "C", "I", "II", "III"}, strings.ToUpper(next))) {
		m.Set("part", m.Text("part")+next)
		tokens.next()
	}
	m.Set("_last_token_type", "part")
	m.Set("_continue_flag", false)
}

func fullVideoResolution(m *MetaObject, token, mode string) {
	if m.Name() == "" {
		return
	}
	pattern := videoPatterns["_resources_pix_re"]
	if mode == "legacy" {
		pattern = legacyAnimePattern(pattern)
	}
	if match := fullRXFirst(pattern, token, true, mode); match != nil {
		fullFlag(m, "pix")
		if !fullHas(m, "resource_pix", mode) {
			pix := fullLower(firstCapture(match.Groups()), mode)
			if fullDigits(pix, mode) && !strings.ContainsRune("kpi", rune(pix[len(pix)-1])) {
				pix += "p"
			}
			m.Set("resource_pix", pix)
		}
		return
	}
	if match := fullRXFirst(videoPatterns["_resources_pix_re2"], token, true, mode); match != nil {
		fullFlag(m, "pix")
		if !fullHas(m, "resource_pix", mode) {
			m.Set("resource_pix", fullLower(match.GroupByNumber(1).String(), mode))
		}
	}
}
func fullSetRange(m *MetaObject, kind string, n *big.Int, isfile bool) {
	begin, end, total := "begin_"+kind, "end_"+kind, "total_"+kind
	first := m.Number(begin)
	if first == nil {
		m.Set(begin, n)
		m.Set(total, 1)
	} else if n.Cmp(first) > 0 {
		m.Set(end, n)
		count := new(big.Int).Sub(n, first)
		count.Add(count, big.NewInt(1))
		m.Set(total, count)
		limit := int64(1)
		if kind == "episode" {
			limit = 2
		}
		if isfile && count.Cmp(big.NewInt(limit)) > 0 {
			m.Set(end, nil)
			m.Set(total, 1)
		}
	}
}
func fullVideoSeasonEpisode(m *MetaObject, token, mode, kind string, isfile bool) error {
	pattern := videoPatterns["_season_re"]
	if kind == "episode" {
		pattern = videoPatterns["_episode_re"]
	}
	if mode == "legacy" {
		pattern = legacyAnimePattern(pattern)
	}
	matches := fullRXMatches(pattern, token, true, mode)
	if len(matches) > 0 {
		m.Set("_last_token_type", kind)
		m.Set("type", "电视剧")
		m.Set("_stop_name_flag", true)
		m.Set("_continue_flag", kind == "season")
		for _, match := range matches {
			value := firstCapture(match.Groups())
			if mode == "legacy" && !fullDigits(value, mode) {
				break
			}
			n, e := fullInt(value, mode)
			if e != nil {
				if mode == "legacy" {
					return e
				}
				continue
			}
			fullSetRange(m, kind, n, isfile)
		}
		return nil
	}
	if fullDigits(token, mode) {
		n, e := fullInt(token, mode)
		if e != nil {
			return nil
		}
		length := fullLen(token, mode)
		if kind == "season" {
			if m.Text("_last_token_type") == "SEASON" && m.Number("begin_season") == nil && length < 3 {
				m.Set("begin_season", n)
				m.Set("total_season", 1)
				fullFlag(m, "season")
				m.Set("type", "电视剧")
			}
			return nil
		}
		first := m.Number("begin_episode")
		if first != nil && m.Number("end_episode") == nil && length < 5 && n.Cmp(first) > 0 && m.Text("_last_token_type") == "episode" {
			fullSetRange(m, "episode", n, isfile)
			m.Set("_continue_flag", false)
			m.Set("type", "电视剧")
		} else if first == nil && length > 1 && length < 4 && m.Text("_last_token_type") != "year" && m.Text("_last_token_type") != "videoencode" && token != m.Text("_unknown_name_str") || m.Text("_last_token_type") == "EPISODE" && first == nil && length < 5 {
			m.Set("begin_episode", n)
			m.Set("total_episode", 1)
			fullFlag(m, "episode")
			m.Set("type", "电视剧")
		}
		return nil
	}
	if kind == "season" {
		if asciiEqualFold(token, "SEASON") && m.Number("begin_season") == nil {
			m.Set("_last_token_type", "SEASON")
		} else if m.Text("type") == "电视剧" && m.Number("begin_season") == nil {
			m.Set("begin_season", 1)
		}
	} else if asciiEqualFold(token, "EPISODE") {
		m.Set("_last_token_type", "EPISODE")
	}
	return nil
}
func fullVideoResource(m *MetaObject, token, mode string) {
	if m.Name() == "" {
		return
	}
	sources := []string{}
	if source := m.Text("_source"); source != "" {
		if mode == "legacy" {
			sources = []string{source}
		} else {
			sources, _ = m.Instance["_sources"].([]string)
		}
	}
	effects, _ := m.Instance["_effect"].([]string)
	state := videoState{sources: sources, effects: effects, lastType: m.Text("_last_token_type"), last: m.Text("_last_token")}
	beforeType, beforeLast := state.lastType, state.last
	if !initResourceType(&state, token, mode) {
		return
	}
	m.Set("_source", strings.Join(state.sources, " "))
	m.Set("_effect", state.effects)
	if mode == "rust" {
		m.Set("_sources", state.sources)
	}
	if state.lastType != beforeType {
		m.Set("_last_token_type", state.lastType)
	}
	if state.last != beforeLast {
		m.Set("_last_token", state.last)
	}
	m.Set("_continue_flag", false)
	if fullMatch(videoPatterns["_source_re"], token, mode, true) || fullMatch(videoPatterns["_effect_re"], token, mode, true) {
		m.Set("_stop_name_flag", true)
		m.Set("_last_token_type", state.lastType)
		if fullMatch(videoPatterns["_effect_re"], token, mode, true) {
			m.Set("_last_token", state.last)
		}
	}
}
func fullVideoPlatform(m *MetaObject, token, mode string, tokens *fullTokens, lookup map[string]string, index int) {
	if m.Name() == "" {
		return
	}
	name, found := lookup[strings.ToUpper(token)]
	query := 1
	previous, next := "", tokens.peek()
	if i := index - 2; i >= 0 && i < len(tokens.list) {
		previous = tokens.list[i]
	}
	if !found {
		for _, adj := range []struct {
			value string
			next  bool
		}{{previous, false}, {next, true}} {
			if adj.value == "" || mode == "legacy" && name != "" {
				continue
			}
			for _, separator := range []string{" ", "-"} {
				value := adj.value + separator + token
				if adj.next {
					value = token + separator + adj.value
				}
				if v, ok := lookup[strings.ToUpper(value)]; ok {
					name, found, query = v, true, 2
					if adj.next {
						tokens.next()
					}
					break
				}
			}
		}
	}
	if !found || mode == "legacy" && name == "" {
		return
	}
	start, end := max(0, index-query-query), min(len(tokens.list), index+query)
	for _, v := range tokens.list[start:end] {
		if slices.Contains([]string{"WEB", "DL", "WEBDL", "WEBRIP"}, strings.ToUpper(v)) {
			m.Set("web_source", name)
			m.Set("_continue_flag", false)
			return
		}
	}
}
func fullHasVideoEvidence(m *MetaObject, mode string) bool {
	return m.Name() != "" && (fullHas(m, "year", mode) || fullHas(m, "resource_pix", mode) || fullHas(m, "resource_type", mode) || m.Number("begin_season") != nil || fullHas(m, "begin_episode", mode))
}
func fullVideoCodec(m *MetaObject, token, mode, kind string) {
	if !fullHasVideoEvidence(m, mode) {
		return
	}
	upper := strings.ToUpper(token)
	if kind == "bit" {
		bitMode := mode
		if mode == "legacy" {
			bitMode = "legacy_metabase"
		}
		if match := fullRXFirst(`(?<![A-Za-z0-9])(?P<bit>8|10|12|16)[\s._-]*bits?(?![A-Za-z0-9])`, token, true, bitMode); match != nil {
			fullFlag(m, "videobit")
			if !fullHas(m, "video_bit", mode) {
				m.Set("video_bit", match.GroupByName("bit").String()+"bit")
			}
		}
		return
	}
	if kind == "video" {
		if match := fullRXFirst(`(`+videoPatterns["_video_encode_re"]+`)`, token, true, mode); match != nil {
			fullFlag(m, "videoencode")
			if !fullHas(m, "video_encode", mode) {
				v := upper
				if match.GroupByNumber(2).String() != "" {
					v = strings.ToUpper(match.GroupByNumber(2).String())
				} else if match.GroupByNumber(3).String() != "" {
					v = strings.ToLower(match.GroupByNumber(3).String())
				}
				m.Set("video_encode", v)
				m.Set("_last_token", v)
			} else if m.Text("video_encode") == "10bit" {
				m.Set("video_encode", upper+" 10bit")
				m.Set("_last_token", upper)
			}
			return
		}
		if upper == "H" || upper == "X" {
			fullFlag(m, "videoencode")
			if upper == "X" {
				upper = "x"
			}
			m.Set("_last_token", upper)
		} else if slices.Contains([]string{"264", "265"}, token) && m.Text("_last_token_type") == "videoencode" && slices.Contains([]string{"H", "X"}, m.Text("_last_token")) || fullDigits(token, mode) && m.Text("_last_token_type") == "videoencode" && slices.Contains([]string{"VC", "MPEG"}, m.Text("_last_token")) {
			m.Set("video_encode", m.Text("_last_token")+token)
		} else if upper == "10BIT" {
			m.Set("_last_token_type", "videoencode")
			if !fullHas(m, "video_encode", mode) {
				m.Set("video_encode", "10bit")
			} else {
				m.Set("video_encode", m.Text("video_encode")+" 10bit")
			}
		}
		return
	}
	if match := fullRXFirst(`(`+videoPatterns["_audio_encode_re"]+`)`, token, true, mode); match != nil {
		value := match.GroupByNumber(1).String()
		fullFlag(m, "audioencode")
		m.Set("_last_token", strings.ToUpper(value))
		if !fullHas(m, "audio_encode", mode) {
			m.Set("audio_encode", value)
		} else if strings.ToUpper(m.Text("audio_encode")) == "DTS" {
			m.Set("audio_encode", m.Text("audio_encode")+"-"+value)
		} else {
			fullAddName(m, "audio_encode", value)
		}
	} else if fullDigits(token, mode) && m.Text("_last_token_type") == "audioencode" {
		audio := m.Text("audio_encode")
		if fullHas(m, "audio_encode", mode) {
			if fullDigits(m.Text("_last_token"), mode) {
				audio += "." + token
			} else if runes := []rune(audio); len(runes) > 0 && fullDigits(string(runes[len(runes)-1]), mode) {
				audio = string(runes[:len(runes)-1]) + " " + string(runes[len(runes)-1]) + "." + token
			} else {
				audio += " " + token
			}
			m.Set("audio_encode", audio)
		}
		m.Set("_last_token", token)
	} else if mode == "rust" && token == "7³" && m.Text("_last_token_type") == "audioencode" {
		if m.Get("audio_encode") != nil {
			fullAddName(m, "audio_encode", token)
		}
		m.Set("_last_token", token)
	}
}

func fullVideoFixName(m *MetaObject, key, mode string) error {
	original := m.Get(key)
	name, _ := original.(string)
	if name == "" {
		if mode == "rust" && original != nil {
			m.Set(key, nil)
		} else {
			m.Set(key, original)
		}
		return nil
	}
	name = fullReplace(videoPatterns["_name_nostring_re"], name, "", mode, true)
	name = fullReplace(`\s+`, pyStrip(name), " ", mode, false)
	if mode == "rust" && name == "" {
		m.Set(key, nil)
		return nil
	}
	decimal := name != ""
	for _, r := range name {
		if _, e := legacyanime.Integer(string(r)); e != nil {
			decimal = false
			break
		}
	}
	if mode == "rust" {
		decimal = digits(name)
	}
	if decimal && !fullHas(m, "year", mode) && m.Number("begin_season") == nil && !fullHas(m, "resource_pix", mode) && !fullHas(m, "resource_type", mode) && !fullHas(m, "audio_encode", mode) && !fullHas(m, "video_encode", mode) {
		n, e := fullInt(name, mode)
		if e != nil {
			return e
		}
		if n.Cmp(big.NewInt(1800)) < 0 {
			if m.Number("begin_episode") == nil {
				m.Set("begin_episode", n)
				m.Set(key, nil)
				return nil
			}
			first, last := m.Number("begin_episode"), m.Number("end_episode")
			inside := n.Cmp(first) == 0
			if last != nil {
				inside = n.Cmp(first) >= 0 && n.Cmp(last) <= 0
			}
			if inside {
				m.Set(key, nil)
				return nil
			}
		}
	}
	if key == "en_name" {
		name = fullCase(name, mode)
	}
	m.Set(key, name)
	return nil
}

// parseFullVideo follows the installed constructor assignment sequence. Pinyin
// callbacks are mandatory only when the corresponding real source branch runs;
// absence is an explicit work-state error, never a guessed false result.
func parseFullVideo(title string, subtitle *string, isfile bool, options Options, env metaInfoEnvironment) (*MetaObject, error) {
	mode := options.SourceMode
	m := fullBase(title, subtitle, isfile, mode)
	if title == "" {
		return m, nil
	}
	m.Set("_source", "")
	m.Set("_effect", []string{})
	m.Set("_index", 0)
	if isfile && fullDigits(title, mode) && fullLen(title, mode) < 5 {
		n, e := fullInt(title, mode)
		if e != nil {
			return nil, e
		}
		m.Set("begin_episode", n)
		if mode == "rust" {
			m.Set("total_episode", 1)
		}
		m.Set("type", "电视剧")
		return m, nil
	}
	pattern := `^(?:Season\s+|S)(\d{1,3})$`
	if mode == "legacy" {
		pattern = legacyAnimePattern(pattern)
	}
	if match := fullRXFirst(pattern, title, true, mode); match != nil {
		m.Set("type", "电视剧")
		n, e := fullInt(match.GroupByNumber(1).String(), mode)
		if e != nil && mode == "legacy" {
			return nil, e
		}
		if e == nil {
			m.Set("begin_season", n)
			m.Set("total_season", 1)
		}
		return m, nil
	}
	working := title
	if match := fullRXFirst(`^[\[【](.+?)[\]】]`, working, false, mode); match != nil {
		prefix := match.GroupByNumber(1).String()
		_, length := match.ByteRange()
		working = working[length:]
		if fullMatch(`[A-Za-z]+\..+(?:19|20)\d{2}`, prefix, mode, false) && fullMatch(`(?:2160|1080|720|480)[PIpi]|4K|UHD|Blu[\-.]?ray|REMUX|WEB[\-.]?DL|HDTV`, prefix, mode, true) {
			working = prefix + working
		}
	}
	working = fullReplace(`([\s.]+)(\d{4})-(\d{4})`, working, `${1}${2}`, mode, false)
	working = fullReplace(`[0-9.]+\s*[MGT]i?B(?![A-Z]+)`, working, "", mode, true)
	working = fullReplace(`\d{4}[\s._-]\d{1,2}[\s._-]\d{1,2}`, working, "", mode, false)
	tokens := fullVideoTokens(working, mode)
	index := 0
	for token := tokens.next(); token != ""; token = tokens.next() {
		index++
		m.Set("_index", index)
		fullVideoPart(m, token, mode, tokens)
		if fullBool(m, "_continue_flag") {
			fullVideoName(m, token, options)
		}
		if fullBool(m, "_continue_flag") {
			if e := fullVideoYear(m, token, mode); e != nil {
				return nil, e
			}
		}
		if fullBool(m, "_continue_flag") {
			fullVideoResolution(m, token, mode)
		}
		if fullBool(m, "_continue_flag") {
			if e := fullVideoSeasonEpisode(m, token, mode, "season", isfile); e != nil {
				return nil, e
			}
		}
		if fullBool(m, "_continue_flag") {
			if e := fullVideoSeasonEpisode(m, token, mode, "episode", isfile); e != nil {
				return nil, e
			}
		}
		if fullBool(m, "_continue_flag") {
			fullVideoResource(m, token, mode)
		}
		if fullBool(m, "_continue_flag") {
			fullVideoPlatform(m, token, mode, tokens, env.StreamingPlatforms, index)
		}
		for _, kind := range []string{"video", "bit", "audio"} {
			if fullBool(m, "_continue_flag") {
				fullVideoCodec(m, token, mode, kind)
			}
		}
		if fullBool(m, "_continue_flag") && m.Name() != "" {
			pattern := `((\d{2,3})(?=FPS))`
			if mode == "legacy" {
				pattern = legacyAnimePattern(pattern)
			}
			if match := fullRXFirst(pattern, token, true, mode); match != nil {
				fullFlag(m, "fps")
				value := match.GroupByNumber(1).String()
				if fullDigits(value, mode) {
					if n, e := fullInt(value, mode); e == nil {
						m.Set("fps", n)
						m.Set("_last_token", n.String()+"FPS")
					} else if mode == "legacy" {
						return nil, e
					}
				}
			}
		}
		m.Set("_continue_flag", true)
	}
	effects, _ := m.Instance["_effect"].([]string)
	if len(effects) > 0 {
		slices.Reverse(effects)
		m.Set("_effect", effects)
		m.Set("resource_effect", strings.Join(effects, " "))
	}
	if source := m.Text("_source"); source != "" {
		m.Set("resource_type", strings.TrimSpace(source))
	}
	if strings.Contains(m.Text("resource_type"), "BluRay") && (m.Text("subtitle") != "" && fullMatch(`DIY`, m.Text("subtitle"), mode, true) || fullMatch(`-DIY@`, title, mode, true)) {
		m.Set("resource_type", m.Text("resource_type")+" DIY")
	}
	if e := fullSubtitle(m, m.Text("org_string"), mode); e != nil {
		return nil, e
	}
	if !fullBool(m, "_subtitle_flag") && m.Text("subtitle") != "" {
		if e := fullSubtitle(m, m.Text("subtitle"), mode); e != nil {
			return nil, e
		}
	}
	if e := fullVideoFixName(m, "cn_name", mode); e != nil {
		return nil, e
	}
	if e := fullVideoFixName(m, "en_name", mode); e != nil {
		return nil, e
	}
	if strings.ToUpper(m.Text("part")) == "PART" {
		m.Set("part", nil)
	}
	if !fullHas(m, "cn_name", mode) && fullHas(m, "en_name", mode) && m.Text("subtitle") != "" {
		check := env.LegacyPinyin
		if mode == "rust" {
			check = env.RustPinyin
		}
		if check == nil {
			return nil, errors.New("prototype_pinyin_dependency_required")
		}
		valid, e := check(m.Text("en_name"))
		if e != nil {
			return nil, e
		}
		if valid {
			description := m.Text("subtitle")
			if mode == "rust" && subtitle != nil {
				description = *subtitle
			}
			splitPattern := fullPattern(`[\s/|]+`, mode)
			parts, _ := expression(splitPattern, false).Split(description, -1)
			first := ""
			if len(parts) > 0 {
				first = parts[0]
			}
			if animeContainsChinese(first) && utf8.RuneCountInString(first) == len(legacyanime.Fields(m.Text("en_name"))) {
				m.Set("cn_name", first)
			}
		}
	}
	team, e := matchTeamOption(title, options)
	if e != nil {
		return nil, e
	}
	if team != nil && (mode == "rust" || *team != "") {
		m.Set("resource_team", *team)
	} else {
		m.Set("resource_team", nil)
	}
	custom, e := matchCustomization(title, options)
	if e != nil {
		return nil, e
	}
	if custom != "" {
		m.Set("customization", custom)
	} else {
		m.Set("customization", nil)
	}
	if !fullHas(m, "video_bit", mode) {
		bitMode := mode
		if mode == "legacy" {
			bitMode = "legacy_metabase"
		}
		if match := fullRXFirst(`(?<![A-Za-z0-9])(?P<bit>8|10|12|16)[\s._-]*bits?(?![A-Za-z0-9])`, m.Text("video_encode"), true, bitMode); match != nil {
			m.Set("video_bit", match.GroupByName("bit").String()+"bit")
		} else {
			m.Set("video_bit", nil)
		}
	}
	return m, nil
}
