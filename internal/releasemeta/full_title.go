package releasemeta

import (
	"errors"
	"math/big"
	"path/filepath"
	"strings"

	"light-media/internal/media/words"
)

func fullRustWords(title string, list []string) (string, []string, error) {
	mode := "rust"
	applied := []string{}
	for _, word := range list {
		if word == "" || strings.HasPrefix(word, "#") {
			continue
		}
		state := false
		fallback := false
		switch {
		case strings.Contains(word, " => ") && strings.Contains(word, " && ") && strings.Contains(word, " >> ") && strings.Contains(word, " <> "):
			if match := fullRXFirst(`^\s*(.*?)\s*=>\s*(.*?)\s*&&\s*(.*?)\s*<>\s*(.*?)\s*>>\s*(.*?)\s*$`, word, false, mode); match != nil {
				p := []string{}
				for _, g := range match.Groups()[1:] {
					p = append(p, strings.TrimSpace(g.String()))
				}
				title, state = replaceRustWord(title, p[0], p[1])
				if state {
					title, state, fallback = offsetRustWordWithState(title, p[2], p[3], p[4])
				}
			}
		case strings.Contains(word, " => "):
			p := strings.Split(word, " => ")
			title, state = replaceRustWord(title, p[0], p[1])
		case strings.Contains(word, " >> ") && strings.Contains(word, " <> "):
			p := strings.Split(word, " <> ")
			off := strings.Split(p[1], " >> ")
			if len(off) > 1 {
				title, state, fallback = offsetRustWordWithState(title, p[0], off[0], off[1])
			}
		default:
			if strings.TrimSpace(word) != "" {
				title, state = replaceRustWord(title, word, "")
			}
		}
		if fallback {
			return "", nil, errRustFallback
		}
		if state {
			applied = append(applied, word)
		}
	}
	return title, applied, nil
}
func fullPrepare(title string, options Options) (string, []string, error) {
	if options.SourceMode == "rust" {
		return fullRustWords(title, options.CustomWords)
	}
	matcher, e := words.New(options.CustomWords)
	if e != nil {
		return "", nil, e
	}
	r := matcher.Prepare(title)
	if r.Limited {
		return "", nil, errors.New("release_meta_words_limit")
	}
	applied := []string{}
	for _, index := range r.Applied {
		applied = append(applied, options.CustomWords[index])
	}
	return r.Title, applied, nil
}

func findFullExplicitTitle(title, mode string) (string, map[string]any, error) {
	info := map[string]any{}
	for _, match := range fullRXMatches(`(?<={\[)[\W\w]+(?=]})`, title, false, mode) {
		value := match.String()
		recognized := false
		for _, key := range []string{"tmdbid", "doubanid", "bangumiid", "anilistid"} {
			if m := fullRXFirst(`(?<=`+key+`=)\d+`, value, false, mode); m != nil {
				recognized = true
				if mode == "rust" || fullDigits(m.String(), "legacy") {
					info[key] = m.String()
				}
			}
		}
		if m := fullRXFirst(`(?<=type=)\w+`, value, false, mode); m != nil {
			recognized = true
			switch m.String() {
			case "movie", "movies":
				info["type"] = "电影"
			case "tv":
				info["type"] = "电视剧"
			}
		}
		if m := fullRXFirst(`(?:^|;)g=([0-9a-fA-F]+)(?=;|$)`, value, false, mode); m != nil {
			recognized = true
			info["episode_group"] = m.GroupByNumber(1).String()
		}
		for _, field := range []struct{ key, pattern string }{{"begin_season", `(?<=s=)\d+`}, {"end_season", `(?<=s=\d+-)\d+`}, {"begin_episode", `(?<=e=)\d+`}, {"end_episode", `(?<=e=\d+-)\d+`}} {
			if m := fullRXFirst(field.pattern, value, false, mode); m != nil {
				recognized = true
				n, e := fullInt(m.String(), mode)
				if e != nil {
					if mode == "legacy" {
						return "", nil, e
					}
				} else {
					info[field.key] = n
				}
			}
		}
		if recognized {
			title = strings.ReplaceAll(title, "{["+value+"]}", "")
		}
	}
	extended := func() {
		for _, source := range []string{"bangumi", "anilist"} {
			key := source + "id"
			if truth(info[key]) {
				continue
			}
			for _, pattern := range []string{`\[` + source + `id[=\-](\d+)\]`, `\[` + source + `[=\-](\d+)\]`, `\{` + source + `id[=\-](\d+)\}`, `\{` + source + `[=\-](\d+)\}`} {
				if m := fullRXFirst(pattern, title, false, mode); m != nil {
					info[key] = m.GroupByNumber(1).String()
					title = strings.TrimSpace(fullRXReplace(pattern, title, "", false, mode))
					break
				}
			}
		}
	}
	if mode == "rust" {
		extended()
	}
	patterns := []string{`\[tmdbid[=\-](\d+)\]`, `\[tmdb[=\-](\d+)\]`, `\{tmdbid[=\-](\d+)\}`, `\{tmdb[=\-](\d+)\}`}
	for i, pattern := range patterns {
		if i > 0 && truth(info["tmdbid"]) {
			break
		}
		if m := fullRXFirst(pattern, title, false, mode); m != nil {
			info["tmdbid"] = m.GroupByNumber(1).String()
			title = strings.TrimSpace(fullRXReplace(pattern, title, "", false, mode))
			break
		}
	}
	if mode == "legacy" {
		extended()
	}
	for _, field := range []struct{ key, source string }{{"tmdbid", "themoviedb"}, {"doubanid", "douban"}, {"bangumiid", "bangumi"}, {"anilistid", "anilist"}} {
		if truth(info[field.key]) {
			info["media_source"] = field.source
			info["media_id"] = info[field.key]
			break
		}
	}
	for _, kind := range []string{"season", "episode"} {
		begin, end := number(info["begin_"+kind]), number(info["end_"+kind])
		if begin != nil && end != nil && (mode == "rust" || begin.Sign() != 0 && end.Sign() != 0) {
			if begin.Cmp(end) > 0 {
				begin, end = end, begin
				info["begin_"+kind], info["end_"+kind] = begin, end
			}
			count := new(big.Int).Sub(end, begin)
			count.Add(count, big.NewInt(1))
			info["total_"+kind] = count
		} else if begin != nil && (mode == "rust" || begin.Sign() != 0) {
			info["total_"+kind] = 1
		}
	}
	if mode == "rust" {
		title = strings.TrimSpace(title)
	}
	return title, info, nil
}

var fullRustFields = []string{"isfile", "title", "org_string", "subtitle", "type", "cn_name", "en_name", "original_name", "year", "total_season", "begin_season", "end_season", "total_episode", "begin_episode", "end_episode", "part", "resource_type", "resource_effect", "resource_pix", "resource_team", "customization", "web_source", "video_encode", "video_bit", "audio_encode", "apply_words", "tmdbid", "doubanid", "media_source", "media_id", "episode_group", "fps"}

func fullRustObject(m *MetaObject) *MetaObject {
	out := EmptyMetaObject()
	out.Kind = m.Kind
	for _, key := range fullRustFields {
		out.Set(key, m.Get(key))
	}
	return out
}

// parseFullTitle preserves both global defaults and optional local-list presence.
// It is a work prototype; unavailable constructor dependencies return explicit
// errors rather than returning the existing five-field consumption projection.
func parseFullTitle(title string, subtitle *string, localWords *[]string, env metaInfoEnvironment) (*MetaObject, error) {
	return buildFullTitle(title, subtitle, localWords, env, true)
}
func buildFullTitle(title string, subtitle *string, localWords *[]string, env metaInfoEnvironment, withOriginal bool) (*MetaObject, error) {
	options := env.Defaults
	if options.SourceMode == "" {
		options.SourceMode = "rust"
	}
	if localWords != nil && (options.SourceMode == "rust" || len(*localWords) > 0) {
		options.CustomWords = *localWords
	}
	prepared, applied, e := fullPrepare(title, options)
	if errors.Is(e, errRustFallback) && options.SourceMode == "rust" {
		env.Defaults.SourceMode = "legacy"
		return buildFullTitle(title, subtitle, localWords, env, withOriginal)
	}
	if e != nil {
		return nil, e
	}
	working, explicit, e := findFullExplicitTitle(prepared, options.SourceMode)
	if e != nil {
		return nil, e
	}
	isfile := false
	if stem, ext, ok := metaTitleSuffix(working); ok && configuredMediaExtension(fullLower(ext, options.SourceMode), options) {
		isfile = true
		working = stem
	}
	anime := isAnime(working)
	if options.SourceMode == "legacy" {
		anime = isLegacyAnime(working)
	}
	var m *MetaObject
	if anime {
		if options.SourceMode == "legacy" {
			m, e = parseFullLegacyAnime(working, subtitle, isfile, options)
		} else {
			m, e = parseFullRustAnime(working, subtitle, isfile, options)
		}
	} else {
		m, e = parseFullVideo(working, subtitle, isfile, options, env)
	}
	if errors.Is(e, errRustFallback) && options.SourceMode == "rust" {
		env.Defaults.SourceMode = "legacy"
		return buildFullTitle(title, subtitle, localWords, env, withOriginal)
	}
	if e != nil {
		return nil, e
	}
	if !anime {
		m.Kind = "video"
	}
	m.Set("title", title)
	m.Set("apply_words", applied)
	for _, key := range []string{"tmdbid", "doubanid", "media_source", "media_id", "type", "episode_group", "begin_season", "end_season", "total_season", "begin_episode", "end_episode", "total_episode"} {
		value, ok := explicit[key]
		if !ok {
			continue
		}
		if key == "tmdbid" {
			var valid bool
			n, e := fullInt(value.(string), options.SourceMode)
			valid = e == nil
			if !valid {
				continue
			}
			value = n
		}
		m.Set(key, value)
	}
	if withOriginal {
		if len(applied) > 0 {
			var original *MetaObject
			if options.SourceMode == "legacy" {
				original, e = buildFullTitle(title, subtitle, nil, env, false)
			} else {
				empty := []string{}
				original, e = buildFullTitle(title, subtitle, &empty, env, false)
			}
			if e != nil {
				return nil, e
			}
			name := original.Name()
			if name == "" {
				name = m.Name()
			}
			m.Set("original_name", name)
		} else {
			if options.SourceMode == "rust" && m.Name() == "" {
				m.Set("original_name", nil)
			} else {
				m.Set("original_name", m.Name())
			}
		}
	}
	if options.SourceMode == "rust" {
		return fullRustObject(m), nil
	}
	return m, nil
}

// Both actual Python Path.suffix and Rust Path.extension treat a leading-only
// dot as part of a filename, and resolve the lexical terminal path component.
func metaTitleSuffix(title string) (string, string, bool) {
	base := filepath.Base(filepath.Clean(title))
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return "", "", false
	}
	at := strings.LastIndexByte(base, '.')
	if at <= 0 {
		return "", "", false
	}
	return base[:at], base[at:], true
}
