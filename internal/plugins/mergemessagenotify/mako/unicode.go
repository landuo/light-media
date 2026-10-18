package mako

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// These tables are generated offline by the exact CPython 3.12 Unicode database
// used by the source dependency; Go/OS Unicode upgrades cannot change templates.
//
//go:embed unicode-cpython312.json.gz
var unicodeCompressed []byte
var unicodeMappings map[rune][4]string
var unicodeCased, unicodeIgnorable map[rune]bool
var printableRanges [][2]rune

func init() {
	z, err := gzip.NewReader(bytes.NewReader(unicodeCompressed))
	if err != nil {
		panic(err)
	}
	defer z.Close()
	var data struct {
		Version          string
		Mapping          map[string][4]string
		Cased, Ignorable []int
		Printable        [][2]rune
	}
	if err = json.NewDecoder(z).Decode(&data); err != nil {
		panic(err)
	}
	if data.Version != "15.0.0" {
		panic("unexpected CPython Unicode version")
	}
	printableRanges = data.Printable
	unicodeMappings = map[rune][4]string{}
	for k, v := range data.Mapping {
		n, err := strconv.Atoi(k)
		if err != nil {
			panic(err)
		}
		unicodeMappings[rune(n)] = v
	}
	unicodeCased = map[rune]bool{}
	unicodeIgnorable = map[rune]bool{}
	for _, n := range data.Cased {
		unicodeCased[rune(n)] = true
	}
	for _, n := range data.Ignorable {
		unicodeIgnorable[rune(n)] = true
	}
}
func pythonPrintable(r rune) bool {
	at := sort.Search(len(printableRanges), func(i int) bool { return printableRanges[i][1] >= r })
	return at < len(printableRanges) && printableRanges[at][0] <= r
}
func pythonCase(s, method string) string {
	runes := []rune(s)
	var b strings.Builder
	previousCased := false
	for i, r := range runes {
		mode := -1
		switch method {
		case "upper":
			mode = 0
		case "lower":
			mode = 1
		case "casefold":
			mode = 3
		case "capitalize":
			if i == 0 {
				mode = 2
			} else {
				mode = 1
			}
		case "title":
			if previousCased {
				mode = 1
			} else {
				mode = 2
			}
		case "swapcase":
			m, ok := unicodeMappings[r]
			if ok {
				if m[1] != string(r) {
					mode = 1
				} else if m[0] != string(r) {
					mode = 0
				}
			}
		}
		mapped := string(r)
		if row, ok := unicodeMappings[r]; ok && mode >= 0 {
			mapped = row[mode]
		}
		if r == 'Σ' && mode == 1 {
			before := false
			for j := i - 1; j >= 0; j-- {
				if unicodeIgnorable[runes[j]] {
					continue
				}
				before = unicodeCased[runes[j]]
				break
			}
			after := false
			for j := i + 1; j < len(runes); j++ {
				if unicodeIgnorable[runes[j]] {
					continue
				}
				after = unicodeCased[runes[j]]
				break
			}
			if before && !after {
				mapped = "ς"
			}
		}
		b.WriteString(mapped)
		previousCased = unicodeCased[r]
	}
	return b.String()
}
