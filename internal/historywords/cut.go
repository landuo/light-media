// Package historywords implements the fixed jieba-next 1.0.0rc1 default
// dictionary's accurate, HMM=False cut used by MoviePilot history search.
// It deliberately does not implement HMM, user dictionaries, or search mode.
package historywords

import (
	_ "embed"
	"encoding/binary"
	"math"
	"sort"
	"strings"
)

// Source, license and checksums are recorded in docs/history-jieba-source-20261115.md.
//go:generate go run gen_dictionary.go

//go:embed dict.idx
var packedDictionary string

type dictionary struct {
	// The file contains a 16-byte header, (offset, frequency) pairs, a
	// sentinel offset and the sorted UTF-8 words, all immutably file-backed.
	table, words    string
	count, maxRunes int
	logTotal        float64
}

func word32(data string) uint32 { return binary.LittleEndian.Uint32([]byte(data[:4])) }

func loadDictionary() *dictionary {
	count := int(word32(packedDictionary))
	start := 16 + count*8 + 4
	return &dictionary{
		table: packedDictionary[16:start], words: packedDictionary[start:],
		count: count, maxRunes: int(word32(packedDictionary[4:])),
		logTotal: math.Log(float64(binary.LittleEndian.Uint64([]byte(packedDictionary[8:16])))),
	}
}

var defaultDictionary = loadDictionary()

func (d *dictionary) lookup(word string) (frequency int, hasLonger bool) {
	wordAt := func(i int) string {
		entry := d.table[8*i:]
		return d.words[word32(entry):word32(entry[8:])]
	}
	i := sort.Search(d.count, func(i int) bool { return wordAt(i) >= word })
	if i < d.count && wordAt(i) == word {
		frequency = int(word32(d.table[8*i+4:]))
		i++
	}
	return frequency, i < d.count && strings.HasPrefix(wordAt(i), word)
}

func asciiAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// Matches upstream re_han_default, not Unicode's broader Han/letter classes.
func inWordBlock(r rune) bool {
	return r >= '\u4e00' && r <= '\u9fd5' || asciiAlnum(r) || strings.ContainsRune("+#&._%", r)
}

// Cut returns all tokens, including punctuation and each whitespace character.
// A CRLF is one token, matching upstream re_skip_default's first alternative.
func Cut(text string) []string {
	out := []string{}
	runes := []rune(text)
	for i := 0; i < len(runes); {
		if inWordBlock(runes[i]) {
			end := i + 1
			for end < len(runes) && inWordBlock(runes[end]) {
				end++
			}
			out = cutBlock(runes[i:end], out, defaultDictionary.maxRunes, defaultDictionary.logTotal, defaultDictionary.lookup)
			i = end
		} else if runes[i] == '\r' && i+1 < len(runes) && runes[i+1] == '\n' {
			out = append(out, "\r\n")
			i += 2
		} else {
			out = append(out, string(runes[i]))
			i++
		}
	}
	return out
}

func cutBlock(text []rune, out []string, maxRunes int, logTotal float64, lookup func(string) (int, bool)) []string {
	encoded := string(text)
	offsets := make([]int, len(text)+1)
	i := 0
	for pos := range encoded {
		offsets[i] = pos
		i++
	}
	offsets[len(text)] = len(encoded)
	scores := make([]float64, len(text)+1)
	next := make([]int, len(text))
	for start := len(text) - 1; start >= 0; start-- {
		best, matches := -3.14e100, 0
		hasLonger := true
		for end := start + 1; hasLonger && end <= len(text) && end-start <= maxRunes; end++ {
			var frequency int
			frequency, hasLonger = lookup(encoded[offsets[start]:offsets[end]])
			if frequency <= 0 {
				continue
			}
			candidate := math.Log(float64(frequency)) - logTotal + scores[end]
			// Rust PrefixDict visits ends in ascending order and uses >=.
			if candidate >= best {
				best, next[start] = candidate, end
			}
			matches++
			if matches == 12 {
				break
			}
		}
		if matches == 0 {
			best, next[start] = -logTotal+scores[start+1], start+1
		}
		scores[start] = best
	}
	bufferStart := -1
	for start := 0; start < len(text); {
		end := next[start]
		if end == start+1 && asciiAlnum(text[start]) {
			if bufferStart < 0 {
				bufferStart = start
			}
		} else {
			if bufferStart >= 0 {
				out = append(out, string(text[bufferStart:start]))
				bufferStart = -1
			}
			out = append(out, string(text[start:end]))
		}
		start = end
	}
	if bufferStart >= 0 {
		out = append(out, string(text[bufferStart:]))
	}
	return out
}
