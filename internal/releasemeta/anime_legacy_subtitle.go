// Installed legacy MetaBase subtitle assignment sequence. Python cn2an and
// Python15 character classes remain separate from the Rust wrapper.
package releasemeta

import "strings"

func applyLegacySubtitle(meta *Metadata, text string) bool {
	if text == "" {
		return false
	}
	text = " " + text + " "
	if m := legacyAnimeFirstMatch(`Episode\s+(\d{1,4})`, text, true); m != nil {
		n, _ := metadataInteger(m.GroupByNumber(1).String(), "legacy")
		if n >= 10000 {
			return false
		}
		if meta.BeginEpisode == nil && meta.legacyBeginEpisode == "" {
			meta.BeginEpisode = intPointer(n)
		}
		meta.Type = "电视剧"
		return true
	}
	matched := false
	if legacyAnimeHas(`[全第季集话話期幕]`, text, true) {
		if m := legacyAnimeFirstMatch(`[全共]\s*([0-9一二三四五六七八九十]+)\s*季`, text, true); m != nil {
			if total, ok := legacyChineseNumber(m.GroupByNumber(1).String()); ok && meta.BeginSeason == nil && meta.legacyBeginSeason == "" && meta.BeginEpisode == nil && meta.legacyBeginEpisode == "" {
				meta.BeginSeason, meta.EndSeason, meta.Type = intPointer(1), intPointer(total), "电视剧"
				return true
			}
			return false
		}
		if m := legacyAnimeFirstMatch(`(?<![全共]\s*)[第\s]+([0-9一二三四五六七八九十S\-]+)\s*季(?!\s*[全共])`, text, true); m != nil {
			parts := strings.Split(strings.ReplaceAll(strings.ToUpper(m.GroupByNumber(1).String()), "S", ""), "-")
			begin, ok := legacyChineseNumber(parts[0])
			var end *int
			if len(parts) > 1 {
				if n, valid := legacyChineseNumber(parts[1]); valid {
					end = intPointer(n)
				} else {
					ok = false
				}
			}
			if !ok || begin > 100 || end != nil && *end > 100 {
				return false
			}
			if meta.BeginSeason == nil && meta.legacyBeginSeason == "" {
				meta.BeginSeason = intPointer(begin)
			}
			if (meta.BeginSeason != nil || meta.legacyBeginSeason != "") && meta.EndSeason == nil && meta.legacyEndSeason == "" && end != nil && (meta.BeginSeason == nil || *end != *meta.BeginSeason) {
				meta.EndSeason = end
			}
			meta.Type, matched = "电视剧", true
		}
		if m := legacyAnimeFirstMatch(`[第]*\s*([0-9一二三四五六七八九十百零]+)\s*[集话話期幕]?\s*-\s*第*\s*([0-9一二三四五六七八九十百零]+)\s*[集话話期幕]`, text, true); m != nil {
			begin, a := legacyChineseNumber(m.GroupByNumber(1).String())
			end, b := legacyChineseNumber(m.GroupByNumber(2).String())
			if !a || !b || begin >= 10000 || end >= 10000 {
				return matched
			}
			if meta.BeginEpisode == nil && meta.legacyBeginEpisode == "" {
				meta.BeginEpisode = intPointer(begin)
			}
			if meta.EndEpisode == nil && meta.legacyEndEpisode == "" && (meta.BeginEpisode == nil || *meta.BeginEpisode != end) {
				meta.EndEpisode = intPointer(end)
			}
			meta.Type = "电视剧"
			return true
		}
		if m := legacyAnimeFirstMatch(`(?<![全共]\s*)[第\s]+([0-9一二三四五六七八九十百零EP]+)\s*[集话話期幕](?!\s*[全共])`, text, true); m != nil {
			text := strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(m.GroupByNumber(1).String()), "E", ""), "P", "")
			begin, ok := legacyChineseNumber(text)
			if !ok || begin >= 10000 {
				return matched
			}
			if meta.BeginEpisode == nil && meta.legacyBeginEpisode == "" {
				meta.BeginEpisode = intPointer(begin)
			}
			meta.Type = "电视剧"
			return true
		}
		if m := legacyAnimeFirstMatch(`([0-9一二三四五六七八九十百零]+)\s*集\s*全|[全共]\s*([0-9一二三四五六七八九十百零]+)\s*[集话話期幕]`, text, true); m != nil {
			if _, ok := legacyChineseNumber(firstCapture(m.Groups())); ok && meta.BeginEpisode == nil && meta.legacyBeginEpisode == "" {
				meta.Type = "电视剧"
				return true
			}
			return matched
		}
	}
	if m := legacyAnimeFirstMatch(`(?<!\d)\[?\s*(\d{1,4})\s*-\s*(\d{1,4})\s*(?:(?:Fin|End)(?![a-z0-9])|完结(?![\u4e00-\u9fff]))(?:\s*\](?!\d)|(?!\s*(?:\]\d|\d))\s*)`, text, true); m != nil {
		begin, _ := metadataInteger(m.GroupByNumber(1).String(), "legacy")
		end, _ := metadataInteger(m.GroupByNumber(2).String(), "legacy")
		if begin >= 1 && begin <= end && end < 10000 && !(begin >= 1900 && end <= 2155) && meta.BeginEpisode == nil && meta.legacyBeginEpisode == "" {
			meta.BeginEpisode, meta.EndEpisode, meta.Type = intPointer(begin), intPointer(end), "电视剧"
			return true
		}
	}
	return matched
}
