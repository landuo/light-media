// Package historywords implements the fixed jieba-next 1.0.0rc1 default
// dictionary's accurate, HMM=False cut used by MoviePilot history search.
// It deliberately does not implement HMM, user dictionaries, or search mode.
package historywords

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Source, license, and checksums: docs/history-jieba-source-20261115.md.
//
//go:embed dict.txt.gz
var compressedDictionary []byte

var defaultDictionary = sync.OnceValue(loadDictionary)

type dictionary struct {
	frequencies map[string]int
	maxRunes    int
	logTotal    float64
}

func loadDictionary() *dictionary {
	r, err := gzip.NewReader(bytes.NewReader(compressedDictionary))
	if err != nil {
		panic(err)
	}
	defer r.Close()
	d := &dictionary{frequencies: make(map[string]int, 350000)}
	total := 0
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Split(strings.TrimSpace(scanner.Text()), " ")
		if len(fields) < 2 {
			panic("invalid embedded jieba dictionary entry")
		}
		frequency, err := strconv.Atoi(fields[1])
		if err != nil || frequency < 0 {
			panic("invalid embedded jieba dictionary frequency")
		}
		d.frequencies[fields[0]] = frequency
		// Upstream sums every line, including any repeated dictionary word.
		total += frequency
		d.maxRunes = max(d.maxRunes, utf8.RuneCountInString(fields[0]))
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
	d.logTotal = math.Log(float64(total))
	return d
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
			out = defaultDictionary().cutBlock(runes[i:end], out)
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

func (d *dictionary) cutBlock(text []rune, out []string) []string {
	scores := make([]float64, len(text)+1)
	next := make([]int, len(text))
	for start := len(text) - 1; start >= 0; start-- {
		best, matches := -3.14e100, 0
		for end := start + 1; end <= len(text) && end-start <= d.maxRunes; end++ {
			frequency := d.frequencies[string(text[start:end])]
			if frequency <= 0 {
				continue
			}
			candidate := math.Log(float64(frequency)) - d.logTotal + scores[end]
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
			best, next[start] = -d.logTotal+scores[start+1], start+1
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
