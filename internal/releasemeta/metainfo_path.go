package releasemeta

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

var auxiliaryChineseStem = regexp.MustCompile(`^(双语|字幕|特效|内封|外挂|官译|简体|繁体|繁中|简中|中英|简英|多语|国英|台粤|音轨|评论|国配|台配|粤语|韩语|日语|杜比|全景声|无损|中字|国语|原声)+$`)
var parentLatinTitle = regexp.MustCompile(`[A-Za-z]{2,}`)
var chineseSeasonEpisodeStem = regexp.MustCompile(`[第共]\s*[0-9一二三四五六七八九十百零]+\s*[季集话話]`)

// ParsePath ports the pinned MetaInfoPath entry and Rust build_meta_path. The
// path is lexical: this parser does not inspect, authorize or resolve files.
func (p *MetaInfoParser) ParsePath(value string, localWords *[]string) (*MetaObject, error) {
	if p == nil {
		return nil, errors.New("metainfo_parser_nil")
	}
	if !utf8.ValidString(value) {
		return nil, errors.New("metainfo_invalid_utf8")
	}
	parts := []string{}
	for _, part := range strings.Split(value, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	rust := p.environment.Defaults.SourceMode == "rust"
	component := func(back int) string {
		at := len(parts) - 1 - back
		if at < 0 || rust && parts[at] == ".." {
			return ""
		}
		return parts[at]
	}
	name, parent, grandparent := component(0), component(1), component(2)
	file, err := p.ParseTitle(name, nil, localWords)
	if err != nil {
		return nil, err
	}
	stem := name
	if at := strings.LastIndexByte(stem, '.'); at > 0 && (rust || at != len(stem)-1) {
		stem = stem[:at]
	}
	if originalUseParentTitle(stem, parent, file, rust) {
		for _, key := range []string{"cn_name", "en_name", "original_name"} {
			file.Set(key, nil)
		}
	}
	for _, name := range []string{parent, grandparent} {
		dir, err := p.ParseTitle(name, nil, localWords)
		if err != nil {
			return nil, err
		}
		if file.Text("type") == "电视剧" || dir.Text("type") != "电视剧" {
			if rust {
				file.MergeRust(dir)
			} else {
				file.MergeLegacy(dir)
			}
		}
	}
	return file, nil
}

func originalUseParentTitle(stem, parent string, file *MetaObject, rust bool) bool {
	if !truth(file.Get("isfile")) || stem == "" || parent == "" {
		return false
	}
	keys := []string{"tmdbid", "doubanid", "media_id"}
	if !rust {
		keys = append(keys, "bangumiid", "anilistid")
	}
	for _, key := range keys {
		if rust && file.Get(key) != nil || !rust && truth(file.Get(key)) {
			return false
		}
	}
	return parentLatinTitle.MatchString(parent) && allChinese(stem) && utf8.RuneCountInString(stem) <= 16 && auxiliaryChineseStem.MatchString(stem) && !chineseSeasonEpisodeStem.MatchString(stem)
}
