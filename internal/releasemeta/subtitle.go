package releasemeta

import (
	"strconv"
	"strings"

	"github.com/dlclark/regexp2/v2"
)

func firstCapture(groups []regexp2.Group) string {
	for _, group := range groups[1:] {
		if group.String() != "" {
			return group.String()
		}
	}
	return ""
}

func applySubtitle(meta *Metadata, text string) bool {
	if text == "" {
		return false
	}
	text = " " + text + " "
	if m := rxFirst(`Episode\s+(\d{1,4})`, text, true); m != nil {
		n, _ := strconv.Atoi(m.GroupByNumber(1).String())
		if n >= 10000 {
			return false
		}
		if meta.BeginEpisode == nil {
			meta.BeginEpisode = intPointer(n)
		}
		meta.Type = "电视剧"
		return true
	}
	matched := false
	if rxHas(`[全第季集话話期幕]`, text, true) {
		if m := rxFirst(`[全共]\s*([0-9一二三四五六七八九十]+)\s*季`, text, true); m != nil {
			if total, ok := chineseNumber(m.GroupByNumber(1).String()); ok && meta.BeginSeason == nil && meta.BeginEpisode == nil {
				meta.BeginSeason, meta.EndSeason, meta.Type = intPointer(1), intPointer(total), "电视剧"
				return true
			}
			return false
		}
		if m := rxFirst(`(?<![全共]\s*)[第\s]+([0-9一二三四五六七八九十S\-]+)\s*季(?!\s*[全共])`, text, true); m != nil {
			parts := strings.Split(strings.ReplaceAll(strings.ToUpper(m.GroupByNumber(1).String()), "S", ""), "-")
			begin, ok := chineseNumber(parts[0])
			var end *int
			if len(parts) > 1 {
				if n, valid := chineseNumber(parts[1]); valid {
					end = intPointer(n)
				} else {
					ok = false
				}
			}
			if !ok || begin > 100 || end != nil && *end > 100 {
				return false
			}
			if meta.BeginSeason == nil {
				meta.BeginSeason = intPointer(begin)
			}
			if meta.BeginSeason != nil && meta.EndSeason == nil && end != nil && *end != *meta.BeginSeason {
				meta.EndSeason = end
			}
			meta.Type, matched = "电视剧", true
		}
		if m := rxFirst(`[第]*\s*([0-9一二三四五六七八九十百零]+)\s*[集话話期幕]?\s*-\s*第*\s*([0-9一二三四五六七八九十百零]+)\s*[集话話期幕]`, text, true); m != nil {
			begin, a := chineseNumber(m.GroupByNumber(1).String())
			end, b := chineseNumber(m.GroupByNumber(2).String())
			if !a || !b || begin >= 10000 || end >= 10000 {
				return matched
			}
			if meta.BeginEpisode == nil {
				meta.BeginEpisode = intPointer(begin)
			}
			if meta.EndEpisode == nil && *meta.BeginEpisode != end {
				meta.EndEpisode = intPointer(end)
			}
			meta.Type = "电视剧"
			return true
		}
		if m := rxFirst(`(?<![全共]\s*)[第\s]+([0-9一二三四五六七八九十百零EP]+)\s*[集话話期幕](?!\s*[全共])`, text, true); m != nil {
			text := strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(m.GroupByNumber(1).String()), "E", ""), "P", "")
			begin, ok := chineseNumber(text)
			if !ok || begin >= 10000 {
				return matched
			}
			if meta.BeginEpisode == nil {
				meta.BeginEpisode = intPointer(begin)
			}
			meta.Type = "电视剧"
			return true
		}
		if m := rxFirst(`([0-9一二三四五六七八九十百零]+)\s*集\s*全|[全共]\s*([0-9一二三四五六七八九十百零]+)\s*[集话話期幕]`, text, true); m != nil {
			if _, ok := chineseNumber(firstCapture(m.Groups())); ok && meta.BeginEpisode == nil {
				meta.Type = "电视剧"
				return true
			}
			return matched
		}
	}
	if m := rxFirst(`(?<!\d)\[?\s*(\d{1,4})\s*-\s*(\d{1,4})\s*(?:(?:Fin|End)(?![a-z0-9])|完结(?![\u4e00-\u9fff]))(?:\s*\](?!\d)|(?!\s*(?:\]\d|\d))\s*)`, text, true); m != nil {
		begin, _ := strconv.Atoi(m.GroupByNumber(1).String())
		end, _ := strconv.Atoi(m.GroupByNumber(2).String())
		if begin >= 1 && begin <= end && end < 10000 && !(begin >= 1900 && end <= 2155) && meta.BeginEpisode == nil {
			meta.BeginEpisode, meta.EndEpisode, meta.Type = intPointer(begin), intPointer(end), "电视剧"
			return true
		}
	}
	return matched
}

func chineseNumber(text string) (int, bool) {
	text = strings.TrimSpace(text)
	if n, err := strconv.Atoi(text); err == nil {
		return n, true
	}
	digits := map[rune]int{'零': 0, '一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	value, pending := 0, 0
	for _, r := range text {
		if n, ok := digits[r]; ok {
			pending = pending*10 + n
		} else if r >= '0' && r <= '9' {
			pending = pending*10 + int(r-'0')
		} else if r == '十' || r == '百' {
			unit := 10
			if r == '百' {
				unit = 100
			}
			if pending == 0 {
				pending = 1
			}
			value, pending = value+pending*unit, 0
		} else {
			return 0, false
		}
	}
	return value + pending, text != ""
}
