// Package pinyin ports the complete dependency closure used by MetaVideo's
// legacy Pinyin2Hanzi predicate and MoviePilot-Rust's golia_pinyin predicate.
// The latter is the fixed inputx-pinyin 1.0.2 syllable inventory/segmenter.
package pinyin

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed legacy_syllables.txt
var legacyText string

//go:embed rust_syllables.txt
var rustText string

//go:embed legacy-installed-source.json
var legacyReference []byte

func inventory(text string) map[string]bool {
	out := map[string]bool{}
	for _, s := range strings.Fields(text) {
		out[s] = true
	}
	return out
}

var legacySet = inventory(legacyText)
var rustSet = inventory(rustText)
var pythonSpaces, pythonASCIIExceptions = pythonTables()

func pythonTables() (map[rune]bool, map[rune]string) {
	var ref struct {
		Whitespace []int             `json:"whitespace"`
		Lower      map[string]string `json:"ascii_lower_exceptions"`
	}
	if err := json.Unmarshal(legacyReference, &ref); err != nil {
		panic(err)
	}
	spaces := map[rune]bool{}
	for _, r := range ref.Whitespace {
		spaces[rune(r)] = true
	}
	exceptions := map[rune]string{}
	for key, value := range ref.Lower {
		n, err := strconv.Atoi(key)
		if err != nil {
			panic(err)
		}
		exceptions[rune(n)] = value
	}
	return spaces, exceptions
}

var ErrInvalidUTF8 = errors.New("pinyin name is not valid UTF-8")
var ErrRustUTF8Boundary = errors.New("original Rust segmenter panics at a non-character byte boundary")

// LegacySyllable exactly matches util.is_pinyin: membership only, with no
// lowercasing, tone removal, u/v normalization or continuous segmentation.
func LegacySyllable(value string) bool { return legacySet[value] }

// LegacyName includes the actual MetaVideo guard: empty is false, while
// nonempty all-whitespace is true (the lower().split() loop has no entries).
// Python lower can only turn a non-ASCII scalar into inventory ASCII through
// the runtime-derived exception table. Contextual non-ASCII lower mappings
// remain non-ASCII and therefore cannot match this ASCII-only inventory.
func LegacyName(name string) (bool, error) {
	if !utf8.ValidString(name) {
		return false, ErrInvalidUTF8
	}
	if name == "" {
		return false, nil
	}
	for _, token := range strings.FieldsFunc(name, func(r rune) bool { return pythonSpaces[r] }) {
		var lower strings.Builder
		for _, r := range token {
			switch {
			case r >= 'A' && r <= 'Z':
				lower.WriteRune(r + 32)
			case r < 128:
				lower.WriteRune(r)
			default:
				value, ok := pythonASCIIExceptions[r]
				if !ok {
					return false, nil
				}
				lower.WriteString(value)
			}
		}
		if !LegacySyllable(lower.String()) {
			return false, nil
		}
	}
	return true, nil
}

func asciiLower(input string) string {
	out := []byte(input)
	for i, b := range out {
		if b >= 'A' && b <= 'Z' {
			out[i] = b + 32
		}
	}
	return string(out)
}
func asciiLetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }

// RustSyllable preserves is_valid_syllable's ASCII-insensitive set lookup;
// non-ASCII letters are not Unicode-normalized into Mandarin syllables.
func RustSyllable(value string) bool { return rustSet[asciiLower(value)] }

type edge struct {
	previous int
	syllable string
}
type Segmentation struct {
	Syllables []string `json:"syllables"`
}

func (s Segmentation) Len() int    { return len(s.Syllables) }
func (s Segmentation) Empty() bool { return len(s.Syllables) == 0 }

// graph is the exact six-byte candidate DP from segmenter.rs. Boundary checks
// turn a raw Rust UTF-8 slicing panic into an explicit error; the actual name
// wrapper only calls this for ASCII and thus never encounters this boundary.
func graph(input string) (string, [][]edge, []bool, error) {
	if !utf8.ValidString(input) {
		return "", nil, nil, ErrInvalidUTF8
	}
	s := asciiLower(input)
	n := len(s)
	dp := make([][]edge, n+1)
	reachable := make([]bool, n+1)
	reachable[0] = true
	for i := 0; i < n; i++ {
		if !reachable[i] {
			continue
		}
		end := i + 6
		if end > n {
			end = n
		}
		for j := i + 1; j <= end; j++ {
			if j < n && !utf8.RuneStart(s[j]) {
				return "", nil, nil, fmt.Errorf("%w: byte %d", ErrRustUTF8Boundary, j)
			}
			candidate := s[i:j]
			if RustSyllable(candidate) {
				dp[j] = append(dp[j], edge{i, candidate})
				reachable[j] = true
			}
		}
	}
	return s, dp, reachable, nil
}

// Segment retains all valid paths, original predecessor insertion/backtrack
// order, and stable sorting by ascending syllable count. Empty yields one
// empty segmentation; impossible nonempty input yields an empty slice.
func Segment(input string) ([]Segmentation, error) {
	s, dp, reachable, err := graph(input)
	if err != nil {
		return nil, err
	}
	if s == "" {
		return []Segmentation{{Syllables: []string{}}}, nil
	}
	if !reachable[len(s)] {
		return []Segmentation{}, nil
	}
	out := []Segmentation{}
	path := []string{}
	var walk func(int)
	walk = func(pos int) {
		if pos == 0 {
			parts := make([]string, len(path))
			for i := range path {
				parts[len(path)-i-1] = path[i]
			}
			out = append(out, Segmentation{parts})
			return
		}
		for _, e := range dp[pos] {
			path = append(path, e.syllable)
			walk(e.previous)
			path = path[:len(path)-1]
		}
	}
	walk(len(s))
	sort.SliceStable(out, func(i, j int) bool { return out[i].Len() < out[j].Len() })
	return out, nil
}

func rustWord(word string) (bool, error) {
	for _, b := range []byte(word) {
		if !asciiLetter(rune(b)) {
			return false, nil
		}
	}
	if RustSyllable(word) {
		return true, nil
	}
	s, _, reachable, err := graph(word)
	if err != nil {
		return false, err
	}
	return s != "" && reachable[len(s)], nil
}

// RustName follows the parser's split_whitespace -> trim non-ASCII alphabetic
// ends -> ASCII lowercase -> remove empty words -> all words check. For the
// boolean consumer, DP reachability is exactly equivalent to the source's
// first nonempty segmentation, without materializing exponentially many paths.
// Segment above remains available with the full source enumeration semantics.
func RustName(name string) (bool, error) {
	if !utf8.ValidString(name) {
		return false, ErrInvalidUTF8
	}
	words := []string{}
	for _, token := range strings.FieldsFunc(name, unicode.IsSpace) {
		cleaned := asciiLower(strings.TrimFunc(token, func(r rune) bool { return !asciiLetter(r) }))
		if cleaned != "" {
			words = append(words, cleaned)
		}
	}
	if len(words) == 0 {
		return false, nil
	}
	for _, word := range words {
		ok, err := rustWord(word)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func LegacyInventory() []string {
	out := make([]string, 0, len(legacySet))
	for s := range legacySet {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
func RustInventory() []string {
	out := make([]string, 0, len(rustSet))
	for s := range rustSet {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
