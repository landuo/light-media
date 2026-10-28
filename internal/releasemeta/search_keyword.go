package releasemeta

// The fixed StringUtils predicates and get_keyword consumed by the original
// traditional MessageChain. Use the already source-checked Unicode15 regex,
// int and cn2an machinery instead of the host Go Unicode database.
import (
	"errors"
	"math/big"
	"regexp"
	"strings"
	"unicode/utf8"

	"light-media/internal/releasemeta/legacyanime"
)

type SearchKeyword struct {
	Present                      bool
	Type, Keyword, Year, Content string
	Season, Episode              *big.Int
}

// Ordinary source search appends cn2an's low-form season label to TV titles.
// Reuse the existing numeric renderer rather than maintaining another map.
func SearchSeasonChinese(number *big.Int) (string, error) {
	if number == nil {
		return "", errors.New("ValueError")
	}
	text, err := legacyArabicChinese(new(big.Int).Abs(number).String())
	if err != nil {
		return "", err
	}
	if number.Sign() < 0 {
		text = "负" + text
	}
	return text, nil
}

func SearchInputKeyword(content string) (SearchKeyword, error) {
	out := SearchKeyword{Present: content != ""}
	if content == "" {
		return out, nil
	}
	if legacyAnimeHas(`^(电视剧|动漫|\s+电视剧|\s+动漫)`, content, false) {
		out.Type = "电视剧"
	}
	content = legacyanime.Strip(legacyAnimeReplace(`^(电影|电视剧|动漫|\s+电影|\s+电视剧|\s+动漫)`, content, "", false))
	if m := legacyAnimeFirstMatch(`第\s*([0-9一二三四五六七八九十]+)\s*季`, content, true); m != nil {
		n, ok := fullLegacyChineseNumber(m.GroupByNumber(1).String())
		if !ok {
			return out, errors.New("search_keyword_invalid_chinese_number")
		}
		out.Type, out.Season = "电视剧", n
	}
	if m := legacyAnimeFirstMatch(`第\s*([0-9一二三四五六七八九十百零]+)\s*集`, content, true); m != nil {
		n, ok := fullLegacyChineseNumber(m.GroupByNumber(1).String())
		if !ok {
			return out, errors.New("search_keyword_invalid_chinese_number")
		}
		out.Type, out.Episode = "电视剧", n
		if n.Sign() != 0 && (out.Season == nil || out.Season.Sign() == 0) {
			out.Season = big.NewInt(1)
		}
	}
	if m := legacyAnimeFirstMatch(`[\s(]+(\d{4})[\s)]*`, content, false); m != nil {
		out.Year = m.GroupByNumber(1).String()
	}
	keyword := legacyanime.Strip(legacyAnimeReplace(`第\s*[0-9一二三四五六七八九十]+\s*季|第\s*[0-9一二三四五六七八九十百零]+\s*集|[\s(]+(\d{4})[\s)]*`, content, "", true))
	if keyword != "" {
		keyword = legacyAnimeReplace(`\s+`, keyword, " ", false)
	} else {
		keyword = out.Year
	}
	out.Keyword, out.Content = keyword, content
	return out, nil
}
func IsSourceLink(text string) bool {
	return text != "" && (legacyAnimeHas(`^(http|https|ftp|ftps|sftp|ws|wss)://`, text, false) || legacyAnimeHas(`^[a-zA-Z0-9.-]+(\.[a-zA-Z]{2,})?$`, text, false))
}
func IsSourceMediaTitle(text string) bool {
	text = legacyanime.Strip(legacyAnimeReplace(`\s+`, text, " ", false))
	if text == "" {
		return false
	}
	words := len(regexp.MustCompile(`[a-zA-Z]+`).FindAllString(text, -1))
	for _, r := range text {
		if r >= '\u4e00' && r <= '\u9fa5' {
			words++
		}
	}
	if legacyAnimeHas(`^#|^请[问帮你]|[?？]$|^继续$`, text, false) || words > 10 || strings.Contains(text, "://") || strings.HasPrefix(text, "magnet:?") {
		return false
	}
	if legacyAnimeHas(`帮我|请问|怎么|如何|为什么|可以|能否|推荐|介绍|谢谢|想看|找一下|搜一下`, text, false) || legacyAnimeHas(`[，。！？!?,；;]`, text, false) {
		return false
	}
	candidate := legacyAnimeReplace(`第\s*[0-9一二三四五六七八九十百零]+\s*[季集]|S\d{1,2}(?:E\d{1,4})?|E\d{1,4}|(?:19|20)\d{2}`, text, "", true)
	candidate = legacyAnimeReplace(`[\s\-_.:：·'"()\[\]【】]+`, candidate, "", false)
	return utf8.RuneCountInString(candidate) >= 2 && legacyAnimeHas(`[\u4e00-\u9fffA-Za-z]`, candidate, false)
}
func ResolveMediaAction(text string) (action, content string) {
	if strings.HasPrefix(text, "订阅") {
		return "Subscribe", legacyAnimeReplace(`订阅[:：\s]*`, text, "", false)
	}
	if strings.HasPrefix(text, "洗版") {
		return "ReSubscribe", legacyAnimeReplace(`洗版[:：\s]*`, text, "", false)
	}
	if strings.HasPrefix(text, "搜索") || strings.HasPrefix(text, "下载") {
		return "ReSearch", legacyAnimeReplace(`(搜索|下载)[:：\s]*`, text, "", false)
	}
	if IsSourceLink(text) || !IsSourceMediaTitle(text) {
		return "", text
	}
	return "Search", text
}
