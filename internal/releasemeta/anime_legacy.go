package releasemeta

// This wrapper follows the actual installed MetaAnime assignment order. Unlike
// Rust, Python retries replace the entire Anitopy dictionary, UNKNOWN is truthy,
// and a caught exception preserves every assignment that preceded it.
import (
	"errors"
	"fmt"
	"github.com/dlclark/regexp2/v2"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"light-media/internal/releasemeta/legacyanime"
)

const legacyAnimeNamePattern = `S\d{2}\s*-\s*S\d{2}|S\d{2}|\s+S\d{1,2}|EP?\d{2,4}\s*-\s*EP?\d{2,4}|EP?\d{2,4}|\s+EP?\d{1,4}|\s+GB`

var legacyAnimeDecimalClass = legacyanime.DecimalClass()

const legacyAnimeWhitespaceClass = `[\x{9}-\x{d}\x{1c}-\x{20}\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]`

func legacyAnimePattern(pattern string) string {
	var out strings.Builder
	class := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			next := pattern[i+1]
			replacement := ""
			if next == 'd' {
				replacement = legacyAnimeDecimalClass
			} else if next == 's' {
				replacement = legacyAnimeWhitespaceClass
			}
			if replacement != "" {
				if class {
					replacement = replacement[1 : len(replacement)-1]
				}
				out.WriteString(replacement)
				i++
				continue
			}
			out.WriteByte(c)
			out.WriteByte(next)
			i++
			continue
		}
		if c == '[' {
			class = true
		} else if c == ']' {
			class = false
		}
		out.WriteByte(c)
	}
	return out.String()
}
func legacyAnimeFirstMatch(pattern, value string, ignoreCase bool) *regexp2.Match {
	return rxFirst(legacyAnimePattern(pattern), value, ignoreCase)
}
func legacyAnimeReplace(pattern, value, replacement string, ignoreCase bool) string {
	return rxReplace(legacyAnimePattern(pattern), value, replacement, ignoreCase)
}
func legacyAnimeHas(pattern, value string, ignoreCase bool) bool {
	return rxHas(legacyAnimePattern(pattern), value, ignoreCase)
}
func prepareLegacyAnimeTitle(title string) string {
	if title == "" {
		return title
	}
	title = legacyanime.Strip(strings.NewReplacer("【", "[", "】", "]").Replace(title))
	if m := legacyAnimeFirstMatch(`新番|月?番|[日美国][漫剧]`, title, false); m != nil {
		if m.RuneIndex+m.RuneLength < utf8.RuneCountInString(title)-1 {
			title = legacyAnimeReplace(`.*番.|.*[日美国][漫剧].`, title, "", false)
		} else if index := strings.LastIndex(title, "["); index >= 0 {
			title = title[:index]
		} else {
			runes := []rune(title)
			if len(runes) > 0 {
				title = string(runes[:len(runes)-1])
			}
		}
	}
	first := strings.SplitN(title, "]", 2)[0]
	if first != "" && animeLegacyCategory.MatchString(first) {
		title = legacyanime.Strip(legacyAnimeReplace(`^[^]]*]`, title, "", false))
	}
	title = legacyAnimeReplace(`[0-9.]+\s*[MGT]i?B(?![A-Z]+)`, title, "", true)
	title = legacyAnimeReplace(`\[TV\s+(\d{1,4})`, title, `[${1}`, true)
	title = legacyAnimeReplace(`\[4k]`, title, "2160p", true)
	names := strings.Split(title, "]")
	if len(names) > 1 && !strings.Contains(title, "- ") {
		var result []string
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
				parts := strings.Split(name, "/")
				picked := legacyanime.Strip(parts[len(parts)-1])
				if picked == "" {
					picked = legacyanime.Strip(parts[0])
				}
				result = append(result, left+picked)
			} else if name != "" {
				if animeContainsChinese(name) && !animeAllChinese(name) {
					if !legacyAnimeHas(`\[\d+`, name, true) {
						name = legacyanime.Strip(legacyAnimeReplace(`[\d|#:：\-()（）\x{4e00}-\x{9fff}]`, name, "", false))
					}
					if name == "" || legacyanime.IsDigit(legacyanime.Strip(name)) {
						continue
					}
				}
				if name == "[" {
					result = append(result, "")
				} else {
					result = append(result, left+legacyanime.Strip(name))
				}
			}
		}
		return strings.Join(result, "]")
	}
	return title
}

func legacyAnimeParse(title string) (map[string]any, error) {
	r, err := legacyanime.Parse(title, legacyanime.Options{})
	return r.Dictionary, err
}
func legacyAnimeValues(parsed map[string]any, key string) []string {
	switch v := parsed[key].(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
func legacyAnimeFirst(parsed map[string]any, key string) string {
	v := legacyAnimeValues(parsed, key)
	if len(v) > 0 {
		return v[0]
	}
	return ""
}
func legacyAnimeShouldRetry(name string) bool {
	switch name {
	case "", "CHS&CHT", "MP4", "GB MP4", "WEB-DL":
		return true
	}
	return utf8.RuneCountInString(name) < 5 && !animeContainsChinese(name)
}
func legacyAnimeName(cn, en string) string {
	if cn != "" && animeAllChinese(cn) {
		return cn
	}
	if en != "" {
		return en
	}
	return cn
}
func legacyAnimeInteger(value string) (*int, string, error) {
	n, err := legacyanime.Integer(value)
	if err != nil {
		return nil, "", err
	}
	if n.IsInt64() {
		v := n.Int64()
		if int64(int(v)) == v {
			return intPointer(int(v)), "", nil
		}
	}
	return nil, n.String(), nil
}
func legacyAnimeSeasonRange(values []string) (begin, end *int, beginRaw, endRaw string, err error) {
	var numbers []*big.Int
	for _, value := range values {
		value = legacyanime.Strip(value)
		if !legacyanime.IsDigit(value) {
			continue
		}
		n, e := legacyanime.Integer(value)
		if e != nil {
			return nil, nil, "", "", e
		}
		numbers = append(numbers, n)
	}
	if len(numbers) == 0 {
		return
	}
	begin, beginRaw, err = legacyAnimeInteger(numbers[0].String())
	if err != nil {
		return
	}
	if len(numbers) > 1 && numbers[0].Cmp(numbers[len(numbers)-1]) != 0 {
		end, endRaw, err = legacyAnimeInteger(numbers[len(numbers)-1].String())
	}
	return
}
func parseLegacyAnime(title string, subtitle *string, options Options) (meta Metadata, parseErr error) {
	meta = Metadata{Kind: "anime", Type: "未知"}
	var cn, en string
	defer func() { meta.Name = legacyAnimeName(cn, en) }()
	if title == "" {
		return
	}
	original, err := legacyAnimeParse(title)
	if err != nil {
		if err.Error() == "source_non_terminating" {
			return meta, err
		}
		return meta, nil
	}
	prepared := prepareAnimeTitle(title, true)
	parsed, err := legacyAnimeParse(prepared)
	if err != nil {
		if err.Error() == "source_non_terminating" {
			return meta, err
		}
		return meta, nil
	}
	if parsed == nil {
		return
	}
	name := legacyAnimeFirst(parsed, "anime_title")
	if legacyAnimeShouldRetry(name) {
		parsed, err = legacyAnimeParse("[ANIME]" + prepared)
		if err != nil {
			if err.Error() == "source_non_terminating" {
				return meta, err
			}
			return meta, nil
		}
		if parsed != nil {
			name = legacyAnimeFirst(parsed, "anime_title")
		}
	}
	if legacyAnimeShouldRetry(name) {
		if m := legacyAnimeFirstMatch(`\[(.+?)]`, prepared, false); m != nil && m.GroupByNumber(1).String() != "" {
			name = legacyanime.Strip(m.GroupByNumber(1).String())
		}
	}
	split := true
	if strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		if animeContainsChinese(parts[0]) {
			cn = parts[0]
			if len(parts) > 1 {
				en = parts[1]
			}
			split = false
		} else if animeContainsChinese(parts[len(parts)-1]) {
			cn = parts[len(parts)-1]
			if len(parts) > 1 {
				en = parts[0]
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
			if legacyanime.IsDigit(word) {
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
	}
	if cn != "" {
		clean, e := legacyAnimeChineseKeyword(cn)
		if e != nil {
			return meta, nil
		}
		cn = clean
		if cn != "" {
			cn = legacyanime.Strip(legacyAnimeReplace(legacyAnimeNamePattern, cn, "", true))
		}
	}
	if en != "" {
		en = legacyanime.TitleCase(legacyanime.Strip(legacyAnimeReplace(legacyAnimeNamePattern, en, "", true)))
	}
	if parsed == nil {
		return
	} // Source dict.get raises after the name assignments.
	meta.BeginSeason, meta.EndSeason, meta.legacyBeginSeason, meta.legacyEndSeason, err = legacyAnimeSeasonRange(legacyAnimeValues(parsed, "anime_season"))
	if err != nil {
		return meta, nil
	}
	if meta.BeginSeason != nil || meta.legacyBeginSeason != "" {
		meta.Type = "电视剧"
	}
	episodes := legacyAnimeValues(parsed, "episode_number")
	if len(episodes) > 0 && episodes[0] != "" {
		meta.BeginEpisode, meta.legacyBeginEpisode, err = legacyAnimeInteger(episodes[0])
		if err == nil && len(episodes) > 1 {
			var end *int
			var raw string
			end, raw, err = legacyAnimeInteger(episodes[len(episodes)-1])
			if err == nil {
				first, _ := legacyanime.Integer(episodes[0])
				last, _ := legacyanime.Integer(episodes[len(episodes)-1])
				if first.Cmp(last) != 0 {
					meta.EndEpisode, meta.legacyEndEpisode = end, raw
				}
			}
		}
		if err != nil {
			meta.BeginEpisode, meta.EndEpisode = nil, nil
			meta.legacyBeginEpisode, meta.legacyEndEpisode = "", ""
		}
		meta.Type = "电视剧"
	}
	meta.Resolution = legacyAnimeFirst(parsed, "video_resolution")
	if meta.Resolution != "" {
		if at := strings.LastIndexAny(meta.Resolution, "xX"); at >= 0 {
			meta.Resolution = meta.Resolution[at+1:] + "p"
		} else {
			meta.Resolution = legacyanime.LowerCase(meta.Resolution)
		}
		if legacyanime.IsDigit(meta.Resolution) {
			meta.Resolution += "p"
		}
	}
	meta.Team, err = matchTeam(title, options)
	if err != nil {
		return meta, nil
	}
	if meta.Team == "" {
		if original == nil {
			return
		}
		meta.Team = legacyAnimeFirst(original, "release_group")
	}
	meta.Customization, err = matchCustomization(title, options)
	if err != nil {
		return meta, nil
	}
	// extract_video_bit only returns a label. FPS integer conversion is a later
	// source failure point; preserve its control dependency before subtitles.
	if fps := legacyAnimeFirstMatch(`((\d{2,3})(?=FPS))`, title, true); fps != nil {
		v := fps.GroupByNumber(1).String()
		if legacyanime.IsDigit(v) {
			if _, e := legacyanime.Integer(v); e != nil {
				return meta, nil
			}
		}
	}
	if !applyLegacySubtitle(&meta, legacyanime.Strip(title)) && subtitle != nil {
		applyLegacySubtitle(&meta, legacyanime.Strip(*subtitle))
	}
	return
}

// get_keyword's numeric validation can fail before resource assignments, even
// though this caller discards the returned season/episode numbers.
func legacyAnimeChineseKeyword(value string) (string, error) {
	value = legacyanime.Strip(legacyAnimeReplace(`^(电影|电视剧|动漫|\s+电影|\s+电视剧|\s+动漫)`, value, "", false))
	for _, pattern := range []string{`第\s*([0-9一二三四五六七八九十]+)\s*季`, `第\s*([0-9一二三四五六七八九十百零]+)\s*集`} {
		if match := legacyAnimeFirstMatch(pattern, value, true); match != nil {
			if err := legacyValidateChineseNumber(match.GroupByNumber(1).String()); err != nil {
				return "", err
			}
		}
	}
	year := ""
	if match := legacyAnimeFirstMatch(`[\s(]+(\d{4})[\s)]*`, value, false); match != nil {
		year = match.GroupByNumber(1).String()
	}
	value = legacyanime.Strip(legacyAnimeReplace(`第\s*[0-9一二三四五六七八九十]+\s*季|第\s*[0-9一二三四五六七八九十百零]+\s*集|[\s(]+\d{4}[\s)]*`, value, "", true))
	if value == "" {
		return year, nil
	}
	return legacyAnimeReplace(`\s+`, value, " ", false), nil
}
func legacyValidateChineseNumber(value string) error {
	value = strings.NewReplacer("零十", "零一十", "零百", "零一百").Replace(value)
	// Source smart mode first accepts an Arabic number and optional unit.
	if m := legacyAnimeFirstMatch(`^([0-9]+)([十百千万亿]?)$`, value, false); m != nil {
		if m.GroupByNumber(2).String() == "" {
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return err
			}
		}
		return nil
	}
	var convertErr error
	value = regexp.MustCompile(`[0-9]+`).ReplaceAllStringFunc(value, func(v string) string {
		result, e := legacyArabicChinese(v)
		if e != nil {
			convertErr = e
		}
		return result
	})
	if convertErr != nil {
		return convertErr
	}
	// Exact normal-mode normalization sequence, expressed without lookaheads.
	value = legacyAnimeReplace(`(万)零(?=[零〇一壹幺二贰两三叁四肆五伍六陆七柒八捌九玖][千仟])`, value, `${1}`, false)
	value = legacyAnimeReplace(`([万亿])([零〇一壹幺二贰两三叁四肆五伍六陆七柒八捌九玖][十拾百佰])`, value, `${1}零${2}`, false)
	value = legacyAnimeReplace(`([千仟])([零〇一壹幺二贰两三叁四肆五伍六陆七柒八捌九玖]?[十拾])`, value, `${1}零${2}`, false)
	value = legacyAnimeReplace(`([百佰])([十拾])`, value, `${1}一${2}`, false)
	value = strings.NewReplacer("零十", "零一十", "零拾", "零一拾").Replace(value)
	if value != "" && (legacyCNNormal.FindString(value) == value || legacyCNAll.FindString(value) == value) {
		return nil
	}
	if utf8.RuneCountInString(value) >= 3 && legacyCNSpeaking.FindString(value) == value {
		r := []rune(value)
		var last rune
		for _, c := range r {
			if strings.ContainsRune("十拾百佰千仟万亿", c) {
				last = c
			}
		}
		if last == '十' || last == '拾' || last == '亿' {
			return errors.New("KeyError")
		}
		return nil
	}
	return errors.New("ValueError")
}
func legacyArabicChinese(value string) (string, error) {
	const digits = "零一二三四五六七八九"
	d := []rune(digits)
	if len(value) > 1 && value[0] == '0' {
		var out strings.Builder
		for _, c := range value {
			out.WriteRune(d[c-'0'])
		}
		return out.String(), nil
	}
	n, err := legacyanime.Integer(value)
	if err != nil {
		return "", err
	}
	value = n.String()
	if len(value) > 16 {
		return "", errors.New("ValueError")
	}
	units := []string{"", "十", "百", "千", "万", "十", "百", "千", "亿", "十", "百", "千", "万", "十", "百", "千"}
	var out string
	for i, c := range value {
		pos := len(value) - i - 1
		if c != '0' {
			out += string(d[c-'0']) + units[pos]
		} else {
			if pos%4 == 0 {
				out += "零" + units[pos]
			}
			if i > 0 && !strings.HasSuffix(out, "零") {
				out += "零"
			}
		}
	}
	out = strings.NewReplacer("零零", "零", "零万", "万", "零亿", "亿", "亿万", "亿").Replace(out)
	out = strings.Trim(out, "零")
	out = legacyAnimeReplace(`([万亿])零([一二三四五六七八九壹贰叁肆伍陆柒捌玖][千仟])`, out, `${1}${2}`, false)
	if strings.HasPrefix(out, "一十") {
		out = strings.TrimPrefix(out, "一")
	}
	if out == "" {
		out = "零"
	}
	return out, nil
}

// Kept separate from native int diagnostic fields so Python-supported arbitrary
// precision season labels survive the downstream season-string projection.
func legacySeasonLabel(begin, end string) string {
	pad := func(v string) string {
		if len(v) < 2 {
			return "0" + v
		}
		return v
	}
	if end != "" {
		return fmt.Sprintf("S%s-S%s", pad(begin), pad(end))
	}
	return "S" + pad(begin)
}

// CN2An smart conversion for the numeric alphabet consumed by MetaBase. The
// validation must precede conversion: e.g. 十十 is rejected rather than 20.
func legacyChineseNumber(value string) (int, bool) {
	value = legacyanime.Strip(value)
	if err := legacyValidateChineseNumber(value); err != nil {
		return 0, false
	}
	if m := legacyAnimeFirstMatch(`^([0-9]+)([十百千万亿]?)$`, value, false); m != nil {
		digits := m.GroupByNumber(1).String()
		unit := m.GroupByNumber(2).String()
		var n *big.Int
		if unit == "" {
			f, e := strconv.ParseFloat(digits, 64)
			if e != nil {
				return 0, false
			}
			n, _ = new(big.Float).SetFloat64(f).Int(nil)
		} else {
			n, _ = new(big.Int).SetString(digits, 10)
			mult := map[string]int64{"十": 10, "百": 100, "千": 1000, "万": 10000, "亿": 100000000}
			n.Mul(n, big.NewInt(mult[unit]))
		}
		if !n.IsInt64() || int64(int(n.Int64())) != n.Int64() {
			return 0, false
		}
		return int(n.Int64()), true
	}
	value = strings.NewReplacer("零十", "零一十", "零百", "零一百").Replace(value)
	value = regexp.MustCompile(`[0-9]+`).ReplaceAllStringFunc(value, func(v string) string { v, _ = legacyArabicChinese(v); return v })
	value = legacyAnimeReplace(`(万)零(?=[零〇一壹幺二贰两三叁四肆五伍六陆七柒八捌九玖][千仟])`, value, `${1}`, false)
	value = legacyAnimeReplace(`([万亿])([零〇一壹幺二贰两三叁四肆五伍六陆七柒八捌九玖][十拾百佰])`, value, `${1}零${2}`, false)
	value = legacyAnimeReplace(`([千仟])([零〇一壹幺二贰两三叁四肆五伍六陆七柒八捌九玖]?[十拾])`, value, `${1}零${2}`, false)
	value = legacyAnimeReplace(`([百佰])([十拾])`, value, `${1}一${2}`, false)
	value = strings.NewReplacer("零十", "零一十", "零拾", "零一拾").Replace(value)
	nums := map[rune]int64{'零': 0, '一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int64{'十': 10, '百': 100, '千': 1000, '万': 10000, '亿': 100000000}
	if legacyCNAll.FindString(value) == value {
		out := new(big.Int)
		for _, r := range value {
			out.Mul(out, big.NewInt(10))
			out.Add(out, big.NewInt(nums[r]))
		}
		if !out.IsInt64() {
			return 0, false
		}
		return int(out.Int64()), true
	}
	if legacyCNNormal.FindString(value) != value {
		var last rune
		for _, r := range value {
			if _, ok := units[r]; ok {
				last = r
			}
		}
		back := map[int64]string{10: "十", 100: "百", 1000: "千", 10000: "万", 100000000: "亿"}
		value += back[units[last]/10]
	}
	out, unit, large := new(big.Int), int64(1), int64(1)
	runes := []rune(value)
	for i := len(runes) - 1; i >= 0; i-- {
		r := runes[i]
		if n, ok := nums[r]; ok {
			out.Add(out, new(big.Int).Mul(big.NewInt(n), big.NewInt(unit)))
		} else {
			unit = units[r]
			if unit%10000 == 0 {
				if unit > large {
					large = unit
				} else {
					large *= unit
					unit = large
				}
			}
			if unit < large {
				unit *= large
			}
			if i == 0 {
				out.Add(out, big.NewInt(unit))
			}
		}
	}
	if !out.IsInt64() {
		return 0, false
	}
	return int(out.Int64()), true
}
