// Package pythonidna implements the installed Python builtin IDNA 2003 codec.
// Request URL adapters using UTS46 have a different contract and do not use it.
package pythonidna

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

type mapping struct {
	key   rune
	value string
}
type interval struct{ first, last rune }

func contains(table []interval, r rune) bool {
	i := sort.Search(len(table), func(i int) bool { return table[i].last >= r })
	return i < len(table) && table[i].first <= r
}
func mapped(table []mapping, r rune) string {
	i := sort.Search(len(table), func(i int) bool { return table[i].key >= r })
	if i < len(table) && table[i].key == r {
		return table[i].value
	}
	return string(r)
}

type combiningEntry struct {
	key   rune
	value byte
}
type composeEntry struct {
	key   uint64
	value rune
}

func combiningClass(r rune) byte {
	i := sort.Search(len(combining), func(i int) bool { return combining[i].key >= r })
	if i < len(combining) && combining[i].key == r {
		return combining[i].value
	}
	return 0
}
func composed(a, b rune) (rune, bool) {
	if a >= 0x1100 && a < 0x1100+19 && b >= 0x1161 && b < 0x1161+21 {
		return 0xac00 + (a-0x1100)*21*28 + (b-0x1161)*28, true
	}
	if a >= 0xac00 && a < 0xac00+11172 && (a-0xac00)%28 == 0 && b > 0x11a7 && b < 0x11a7+28 {
		return a + b - 0x11a7, true
	}
	k := uint64(a)<<21 | uint64(b)
	i := sort.Search(len(composition), func(i int) bool { return composition[i].key >= k })
	if i < len(composition) && composition[i].key == k {
		return composition[i].value, true
	}
	return 0, false
}
func normalizeUnicode32(value string) string {
	// CPython's 3.2 compatibility normalizer uses old decompositions, but its
	// running Unicode-15 combining and composition tables. New characters must
	// keep those combining classes; treating them as inert starters is wrong.
	decomposed := []rune{}
	for _, r := range value {
		decomposition := mapped(normalize32[:], r)
		if r >= 0xac00 && r < 0xac00+11172 {
			n := r - 0xac00
			parts := []rune{0x1100 + n/(21*28), 0x1161 + (n%(21*28))/28}
			if t := n % 28; t != 0 {
				parts = append(parts, 0x11a7+t)
			}
			decomposition = string(parts)
		}
		for _, v := range decomposition {
			decomposed = append(decomposed, v)
			class := combiningClass(v)
			if class != 0 {
				for j := len(decomposed) - 1; j > 0 && combiningClass(decomposed[j-1]) > class; j-- {
					decomposed[j], decomposed[j-1] = decomposed[j-1], decomposed[j]
				}
			}
		}
	}
	if len(decomposed) == 0 {
		return ""
	}
	out := []rune{decomposed[0]}
	starter := 0
	last := combiningClass(decomposed[0])
	for _, r := range decomposed[1:] {
		class := combiningClass(r)
		if c, ok := composed(out[starter], r); ok && (last < class || last == 0) {
			out[starter] = c
			continue
		}
		out = append(out, r)
		if class == 0 {
			starter = len(out) - 1
		}
		last = class
	}
	return string(out)
}
func nameprep(value string) (string, error) {
	var b strings.Builder
	for _, r := range value {
		if !contains(b1[:], r) {
			b.WriteString(mapped(mapB2[:], r))
		}
	}
	value = normalizeUnicode32(b.String())
	hasRandAL, hasL := false, false
	for _, r := range value {
		if contains(prohibited[:], r) {
			return "", errors.New("prohibited_python_idna_character")
		}
		hasRandAL = hasRandAL || contains(randal[:], r)
		hasL = hasL || contains(lcat[:], r)
	}
	if hasRandAL {
		first, _ := utf8.DecodeRuneInString(value)
		last, _ := utf8.DecodeLastRuneInString(value)
		if hasL || !contains(randal[:], first) || !contains(randal[:], last) {
			return "", errors.New("invalid_python_idna_bidi")
		}
	}
	return value, nil
}
func ascii(value string) bool {
	for _, r := range value {
		if r >= 128 {
			return false
		}
	}
	return true
}
func labelASCII(label string) (string, error) {
	if !ascii(label) {
		var e error
		label, e = nameprep(label)
		if e != nil {
			return "", e
		}
		if !ascii(label) {
			if strings.HasPrefix(label, "xn--") {
				return "", errors.New("python_idna_ace_prefix")
			}
			label, e = encode("xn--", label)
			if e != nil {
				return "", e
			}
		}
	}
	if len(label) < 1 || len(label) > 63 {
		return "", errors.New("invalid_python_idna_label_length")
	}
	return label, nil
}

// ToASCII follows the codec, including ASCII label-length checks, Unicode dot
// separators, trailing roots and source errors. It imposes no new DNS-length cap.
func ToASCII(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("invalid_python_idna_text")
	}
	if value == "" {
		return "", nil
	}
	value = strings.NewReplacer("\u3002", ".", "\uff0e", ".", "\uff61", ".").Replace(value)
	labels := strings.Split(value, ".")
	end := len(labels)
	if labels[end-1] == "" {
		end--
	}
	for i := 0; i < end; i++ {
		var e error
		labels[i], e = labelASCII(labels[i])
		if e != nil {
			return "", e
		}
	}
	return strings.Join(labels, "."), nil
}
