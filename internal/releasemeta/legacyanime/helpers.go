package legacyanime

import (
	"sort"
	"strings"
	"unicode/utf8"
)

const dashes = "-‐‑‒–—―"

func pyDigit(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !inRange(r, pyDigitRanges) {
			return false
		}
	}
	return true
}
func pyAlnum(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !inRange(r, pyAlnumRanges) {
			return false
		}
	}
	return true
}
func normalize(value string) string {
	var out strings.Builder
	for _, r := range value {
		if mapped, ok := pyNormalized[r]; ok {
			out.WriteString(mapped)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
func mostlyLatin(value string) bool {
	if value == "" {
		return false
	}
	count, total := 0, 0
	for _, r := range value {
		if !inRange(r, pyNamedRanges) {
			sourcePanic("ValueError")
		}
		if inRange(r, pyLatinRanges) {
			count++
		}
		total++
	}
	return count*2 >= total
}
func numberIndex(value string) int {
	for i, r := range []rune(value) {
		if inRange(r, pyDigitRanges) {
			return i
		}
	}
	return -1
}
func nonNumberIndex(value string) int {
	for i, r := range []rune(value) {
		if !inRange(r, pyDigitRanges) {
			return i
		}
	}
	return -1
}
func runeLen(value string) int { return utf8.RuneCountInString(value) }
func runeSlice(value string, first, last int) string {
	runes := []rune(value)
	n := len(runes)
	if first < 0 {
		first += n
	}
	if last < 0 {
		last += n
	}
	if first < 0 {
		first = 0
	}
	if last < 0 {
		last = 0
	}
	if first > n {
		first = n
	}
	if last > n {
		last = n
	}
	if first > last {
		return ""
	}
	return string(runes[first:last])
}
func tokenCat(t *token) tokenCategory {
	if t == nil {
		sourcePanic("AttributeError")
	}
	return t.category
}
func tokenContent(t *token) string {
	if t == nil {
		sourcePanic("AttributeError")
	}
	return t.content
}
func isCRC32(value string) bool {
	if runeLen(value) != 8 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("1234567890abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
func ordinal(value string) string {
	return map[string]string{"1st": "1", "First": "1", "2nd": "2", "Second": "2", "3rd": "3", "Third": "3", "4th": "4", "Fourth": "4", "5th": "5", "Fifth": "5", "6th": "6", "Sixth": "6", "7th": "7", "Seventh": "7", "8th": "8", "Eighth": "8", "9th": "9", "Ninth": "9"}[value]
}
func (s *state) buildElement(c Category, begin, end *token, keep bool) {
	var out strings.Builder
	for _, t := range s.list(0, begin, end) {
		switch t.category {
		case tokenUnknown:
			out.WriteString(t.content)
			t.category = tokenIdentifier
		case tokenBracket:
			out.WriteString(t.content)
		case tokenDelimiter:
			if keep {
				out.WriteString(t.content)
			} else if t != begin && t != end {
				if t.content == "," || t.content == "&" {
					out.WriteString(t.content)
				} else {
					out.WriteByte(' ')
				}
			}
		}
	}
	value := out.String()
	if !keep {
		value = strings.Trim(value, " "+dashes)
	}
	if value != "" {
		s.insert(c, value)
	}
}
func (s *state) isolated(t *token) bool {
	previous := s.previous(t, flagNotDelimiter)
	if tokenCat(previous) != tokenBracket {
		return false
	}
	next := s.next(t, flagNotDelimiter)
	return tokenCat(next) == tokenBracket
}
func (s *state) checkAnimeSeason(t *token) bool {
	set := func(a, b *token, value string) {
		s.insert(AnimeSeason, value)
		a.category = tokenIdentifier
		b.category = tokenIdentifier
	}
	previous := s.previous(t, flagNotDelimiter)
	if previous != nil {
		if number := ordinal(previous.content); number != "" {
			set(previous, t, number)
			return true
		}
	}
	next := s.next(t, flagNotDelimiter)
	if next != nil && pyDigit(next.content) {
		set(t, next, next.content)
		return true
	}
	return false
}

type keyword struct {
	category                        Category
	identifiable, searchable, valid bool
}

func findKeyword(value string, category Category) *keyword {
	container := keywords
	if category == FileExtension {
		container = extensionKeywords
	}
	if k, ok := container[value]; ok && (category == Unknown || k.category == category) {
		return &k
	}
	return nil
}

type interval struct{ first, last int }

func (s *state) peek(value string) []interval {
	entries := []struct {
		category Category
		words    []string
	}{{AudioTerm, []string{"Dual Audio", "Multi Audio"}}, {VideoTerm, []string{"H264", "H.264", "h264", "h.264"}}, {VideoResolution, []string{"480p", "720p", "1080p"}}, {Subtitles, []string{"Multiple Subtitle", "Multi Subs"}}, {Source, []string{"Blu-Ray"}}}
	var result []interval
	for _, entry := range entries {
		for _, word := range entry.words {
			if at := strings.Index(value, word); at >= 0 {
				s.insert(entry.category, word)
				first := runeLen(value[:at])
				result = append(result, interval{first, first + runeLen(word)})
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].first != result[j].first {
			return result[i].first < result[j].first
		}
		return result[i].last < result[j].last
	})
	return result
}
