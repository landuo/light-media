package rustanime

// This file translates the Parser pipeline in anitomy-pure 0.1.0 src/lib.rs
// and its ignored-string/extension helpers. Upstream license: MPL-2.0.

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrStringIsEmpty = errors.New("Parsing Error : nothing to parse")
var ErrSourcePanic = errors.New("source parser panic")

// Parse 执行 anitomy-pure 0.1.0 的单文件名流水线，保留元素插入和 token 消费顺序。
func Parse(filename string, options Options) (result Result, err error) {
	defer func() {
		if recover() != nil {
			result = Result{}
			err = ErrSourcePanic
		}
	}()
	if !utf8.ValidString(filename) {
		return Result{}, errors.New("invalid UTF-8")
	}
	found := elements{{FileName, filename}}
	if ext := getExtension(filename); ext != "" {
		found.push(FileExtension, ext)
	}
	working := removeExtension(filename)
	if working == "" {
		return Result{}, ErrStringIsEmpty
	}
	if strings.Contains(working, "[") && !strings.Contains(working, "]") {
		return Result{Elements: []Element(found)}, nil
	}
	delimiters := " _.-&+,|"
	if options.AllowedDelimiters != nil {
		delimiters = *options.AllowedDelimiters
	}
	tokens := splitRawData(removeIgnoredString(working, options.IgnoredStrings), delimiters)
	trace := func(name string) {
		if options.Trace {
			result.Stages = append(result.Stages, stageSnapshot(name, &found, tokens))
		}
	}
	trace("split")
	parseKeywords(&found, tokens)
	trace("keywords")
	if enabled(options.EpisodeNumber) {
		parseEpisodeNumber(delimiters, tokens, &found)
		trace("episode")
		detectEpisodeNumberAlt(tokens, &found)
		trace("episode_alt")
	}
	parseAnimeTitle(tokens, &found, delimiters)
	trace("anime_title")
	if enabled(options.ReleaseGroup) {
		parseReleaseGroup(tokens, &found, delimiters)
		trace("release_group")
	}
	if enabled(options.EpisodeTitle) {
		parseEpisodeTitle(tokens, &found, delimiters)
		trace("episode_title")
	}
	result.Elements = []Element(found)
	return result, nil
}
func enabled(value *bool) bool { return value == nil || *value }
func stageSnapshot(name string, e *elements, tokens []*token) Stage {
	stage := Stage{Name: name, Elements: append([]Element{}, (*e)...), Tokens: make([]TokenState, 0, len(tokens))}
	for _, t := range tokens {
		state := TokenState{Raw: t.raw, Inside: t.inside, Weak: t.weak, Paren: t.paren, Corner: t.corner, Subtokens: make([]SubtokenState, 0, len(t.subtokens))}
		for _, s := range t.subtokens {
			state.Subtokens = append(state.Subtokens, SubtokenState{s.value, s.found})
		}
		stage.Tokens = append(stage.Tokens, state)
	}
	return stage
}

var extensionSet = func() map[string]bool {
	m := map[string]bool{}
	for _, value := range strings.Split(knownExtensions, "|") {
		if value != "" {
			m[value] = true
		}
	}
	return m
}()

func getExtension(value string) string {
	// Rust's Unix Path components remove trailing slash and CurDir segments;
	// ParentDir is not resolved and has no file_name. Do not clean the full path,
	// since that would incorrectly resolve "a.mkv/..".
	base := value
	for {
		base = strings.TrimRight(base, "/")
		at := strings.LastIndexByte(base, '/')
		if base[at+1:] == "." && at >= 0 {
			base = base[:at]
			continue
		}
		base = base[at+1:]
		break
	}
	if base == ".." {
		return ""
	}
	at := strings.LastIndexByte(base, '.')
	if at <= 0 {
		return ""
	}
	ext := base[at:]
	if !extensionSet[ext] {
		return ""
	}
	return strings.TrimPrefix(ext, ".")
}
func removeExtension(value string) string {
	if ext := getExtension(value); ext != "" {
		return strings.TrimSuffix(value, "."+ext)
	}
	return value
}
func removeIgnoredString(value string, list []string) string {
	for _, item := range list {
		if (strings.HasPrefix(item, "['") || strings.HasPrefix(item, "[\"")) && strings.HasSuffix(item, "]") {
			inside := false
			var current strings.Builder
			for _, r := range item {
				if r == '\'' || r == '"' {
					if inside {
						value = strings.ReplaceAll(value, current.String(), "")
						current.Reset()
					}
					inside = !inside
				} else if inside {
					current.WriteRune(r)
				}
			}
		} else {
			value = strings.ReplaceAll(value, item, "")
		}
	}
	return value
}
