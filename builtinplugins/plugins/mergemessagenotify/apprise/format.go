package apprise

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

//go:embed testdata/emoji-patterns.json
var emojiData []byte
var emojiOnce sync.Once
var emojiMap map[string]string
var emojiPattern *regexp.Regexp

func loadEmojiEngine() {
	emojiOnce.Do(func() {
		var pairs [][2]string
		if err := json.Unmarshal(emojiData, &pairs); err != nil {
			panic(err)
		}
		table := map[string]string{}
		patterns := make([]string, 0, len(pairs))
		for _, p := range pairs {
			table[p[0]] = p[1]
			patterns = append(patterns, p[0])
		}
		emojiMap = table
		emojiPattern = regexp.MustCompile("(?i)(" + strings.Join(patterns, "|") + ")")
	})
}

type SourceError struct{ Kind string }

func (e *SourceError) Error() string { return fmt.Sprintf("original Apprise %s", e.Kind) }
func applyEmojis(text string) (string, error) {
	loadEmojiEngine()
	var sourceError error
	result := emojiPattern.ReplaceAllStringFunc(text, func(match string) string {
		if value, ok := emojiMap[match]; ok {
			return value
		}
		// Source regex aliases/uppercase match but the literal dictionary lookup
		// raises KeyError. Preserve that exception instead of silently correcting it.
		sourceError = &SourceError{Kind: "KeyError"}
		return match
	})
	return result, sourceError
}
func smartSplit(text string, limit int, format string) []string {
	runes := []rune(text)
	if len(runes) == 0 || limit <= 0 {
		return []string{""}
	}
	chunks := []string{}
	start := 0
	for start < len(runes) {
		if len(runes)-start <= limit {
			chunks = append(chunks, string(runes[start:]))
			break
		}
		end := start + limit
		newline, space, punct := -1, -1, -1
		for i := start; i < end; i++ {
			switch runes[i] {
			case '\r', '\n':
				newline = i + 1
			case ' ', '\t':
				space = i + 1
			}
			if strings.ContainsRune(".!?:;", runes[i]) && i+1 < end && splitWhitespace(runes[i+1]) {
				j := i + 2
				for j < end && splitWhitespace(runes[j]) {
					j++
				}
				punct = j
			}
		}
		split := end
		if newline != -1 {
			split = newline
		} else if space != -1 {
			split = space
		} else if punct != -1 {
			split = punct
		}
		original := split
		if format == "html" || format == "markdown" {
			split = htmlAdjust(runes, start, split)
		}
		if format == "markdown" {
			split = markdownAdjust(runes, start, split)
		}
		if split <= start {
			split = original
		}
		chunks = append(chunks, string(runes[start:split]))
		start = split
	}
	return chunks
}
func splitWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\v' || r == '\f'
}
func htmlAdjust(text []rune, start, split int) int {
	if split <= start || split > len(text) {
		return split
	}
	amp := -1
	for i := max(start, split-16); i < split; i++ {
		if text[i] == '&' {
			amp = i
		}
	}
	if amp == -1 {
		return split
	}
	semi := -1
	for i := amp; i < min(len(text), split+16); i++ {
		if text[i] == ';' {
			semi = i
			break
		}
	}
	if semi != -1 && amp > start && amp < split && split <= semi {
		return amp
	}
	return split
}
func markdownAdjust(text []rune, start, split int) int {
	if split <= start || split > len(text) {
		return split
	}
	left := -1
	lo := max(start, split-32)
	for i := lo; i < split; i++ {
		if text[i] == '[' {
			left = i
		}
	}
	if left == -1 {
		for i := lo; i < split; i++ {
			if text[i] == '!' {
				left = i
			}
		}
	}
	if left == -1 {
		return split
	}
	right := -1
	for i := left; i < min(len(text), split+32); i++ {
		if text[i] == ')' {
			right = i
			break
		}
	}
	if right != -1 && left < split && split < right {
		return left
	}
	return split
}
