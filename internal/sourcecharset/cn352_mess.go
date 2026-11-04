package sourcecharset

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Exact CharInfo slots used by the original ten detector states. The fixed
// Unicode-backed constructor is separate from these state algorithms.
type cnCharInfo struct {
	character                                                                                                rune
	printable, alpha, upper, lower, space, digit, ascii, caseVariable                                        bool
	flags                                                                                                    uint16
	accentuated, latin, cjk, katakana, halfwidthKatakana, arabic, ligature, superscript, sentenceOpen, glyph bool
	punct, symbol, separator, emoticon, safe, commonCJK                                                      bool
	rangeName                                                                                                string
	rangePresent                                                                                             bool
	unaccented                                                                                               rune
}
type cnInfoLookup func(rune) (cnCharInfo, error)

func cnRound(value float64, digits int) (float64, error) {
	// Formatting a binary64 directly performs the required decimal ties-to-even
	// rounding, unlike multiply-by-10^n then RoundToEven (which double rounds).
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("nonfinite source charset score")
	}
	return strconv.ParseFloat(strconv.FormatFloat(value, 'f', digits, 64), 64)
}
func cnSuspiciousRanges(a string, aPresent bool, b string, bPresent bool) (bool, error) {
	if !aPresent || !bPresent {
		return true, nil
	}
	if err := cnLoad(); err != nil {
		return false, err
	}
	fa, oka := cnData.RangeFamilies[a]
	fb, okb := cnData.RangeFamilies[b]
	if !oka || !okb {
		return false, errors.New("missing fixed Unicode range family")
	}
	if fa == fb || slices.Contains(cnData.CompatibleAny, fa) || slices.Contains(cnData.CompatibleAny, fb) {
		return false, nil
	}
	for _, pair := range cnData.CompatibleFamilies {
		if (pair[0] == fa && pair[1] == fb) || (pair[0] == fb && pair[1] == fa) {
			return false, nil
		}
	}
	if a == "Basic Latin" {
		return !slices.Contains(cnData.BasicLatinCompatible, fb), nil
	}
	if b == "Basic Latin" {
		return !slices.Contains(cnData.BasicLatinCompatible, fa), nil
	}
	return true, nil
}

type cnSymbolState struct {
	punctuation, symbols, count int
	last                        rune
	seen                        bool
}

func (d *cnSymbolState) feed(c rune, i cnCharInfo) {
	d.count++
	if (!d.seen || c != d.last) && !i.safe {
		if i.punct {
			d.punctuation++
		} else if !i.digit && i.symbol && !i.emoticon {
			d.symbols += 2
		}
	}
	d.last = c
	d.seen = true
}
func (d *cnSymbolState) ratio() float64 {
	if d.count == 0 {
		return 0
	}
	r := float64(d.punctuation+d.symbols) / float64(d.count)
	if r >= 0.3 {
		return r
	}
	return 0
}

type cnAccentState struct{ count, accents int }

func (d *cnAccentState) feed(i cnCharInfo) {
	d.count++
	if i.accentuated {
		d.accents++
	}
}
func (d *cnAccentState) ratio() float64 {
	if d.count < 8 {
		return 0
	}
	r := float64(d.accents) / float64(d.count)
	if r >= 0.35 {
		return r
	}
	return 0
}

type cnUnprintableState struct {
	count, unprintable int
	escape             bool
}

func (d *cnUnprintableState) feed(c rune, i cnCharInfo) {
	if c == 0x1b {
		d.escape = true
	}
	if !i.printable && !i.space && c != 0x1a && c != 0xfeff {
		d.unprintable++
	}
	d.count++
}
func (d *cnUnprintableState) ratio() float64 {
	if d.count == 0 {
		return 0
	}
	if d.escape {
		return 1
	}
	return float64(d.unprintable*8) / float64(d.count)
}

type cnDuplicateAccentState struct {
	count, successive int
	last              cnCharInfo
	seen, lastAccent  bool
}

func (d *cnDuplicateAccentState) feed(i cnCharInfo) {
	d.count++
	if d.seen && i.accentuated && d.lastAccent {
		if i.upper && d.last.upper {
			d.successive++
		}
		if i.unaccented == d.last.unaccented {
			d.successive++
		}
	}
	d.last = i
	d.seen = true
	d.lastAccent = i.accentuated
}
func (d *cnDuplicateAccentState) ratio() float64 {
	if d.count == 0 {
		return 0
	}
	return float64(d.successive*2) / float64(d.count)
}

type cnRangeState struct {
	count, suspicious int
	last              string
	lastPresent, seen bool
}

func (d *cnRangeState) feed(i cnCharInfo) error {
	d.count++
	if i.space || i.punct || i.safe {
		d.seen = false
		d.last = ""
		d.lastPresent = false
		return nil
	}
	if !d.seen {
		d.seen = true
		d.last = i.rangeName
		d.lastPresent = i.rangePresent
		return nil
	}
	if d.last != i.rangeName || !d.lastPresent || !i.rangePresent {
		bad, err := cnSuspiciousRanges(d.last, d.lastPresent, i.rangeName, i.rangePresent)
		if err != nil {
			return err
		}
		if bad {
			d.suspicious++
		}
	}
	d.last = i.rangeName
	d.lastPresent = i.rangePresent
	return nil
}
func (d *cnRangeState) ratio() float64 {
	if d.count <= 13 {
		return 0
	}
	return float64(d.suspicious*2) / float64(d.count)
}

type cnWeirdWordState struct {
	words, badWords, foreignLong, characters, badCharacters, invalidWords       int
	bad, invalid, foreignWatch                                                  bool
	length, accents, glyphs, uppers                                             int
	last                                                                        rune
	lastAccent, lastUpper, firstLower, nonASCII, lastLigature, internalLigature bool
}

func (d *cnWeirdWordState) feed(c rune, i cnCharInfo) {
	if i.alpha {
		if d.lastLigature {
			d.internalLigature = true
		}
		d.lastLigature = i.ligature
		if d.length == 0 {
			d.firstLower = i.lower
		}
		d.length++
		d.last = c
		d.lastUpper = i.upper
		if i.upper {
			d.uppers++
		}
		if !i.ascii {
			d.nonASCII = true
		}
		d.lastAccent = i.accentuated
		if i.accentuated {
			d.accents++
		}
		if i.glyph {
			d.glyphs++
		} else if !d.foreignWatch && (!i.latin || i.accentuated) {
			d.foreignWatch = true
		}
		return
	}
	if d.length == 0 {
		return
	}
	if i.sentenceOpen || (i.superscript && d.internalLigature) {
		d.bad = true
		d.invalid = true
	}
	if i.space || i.punct || i.separator {
		d.words++
		n := d.length
		d.characters += n
		if n >= 4 {
			if float64(d.accents)/float64(n) >= 0.5 {
				d.bad = true
			} else if d.lastAccent && d.lastUpper && d.uppers != n {
				d.foreignLong++
				d.bad = true
			} else if d.glyphs == 1 {
				d.bad = true
				d.foreignLong++
			} else if d.nonASCII && d.firstLower && d.uppers == n-1 {
				d.foreignLong++
				d.bad = true
			}
		}
		if n >= 24 && d.foreignWatch {
			camel := d.uppers > 0 && float64(d.uppers)/float64(n) <= 0.3
			if !camel {
				d.foreignLong++
				d.bad = true
			}
		}
		if d.bad {
			d.badWords++
			d.badCharacters += n
			d.bad = false
		}
		if d.invalid {
			d.invalidWords++
			d.invalid = false
		}
		d.foreignWatch = false
		d.length = 0
		d.last = 0
		d.lastAccent = false
		d.lastUpper = false
		d.accents = 0
		d.glyphs = 0
		d.uppers = 0
		d.firstLower = false
		d.nonASCII = false
		d.lastLigature = false
		d.internalLigature = false
	} else if !strings.ContainsRune("<>-=~|_", c) && !i.digit && i.symbol {
		d.bad = true
		d.length++
		d.last = c
		d.lastAccent = false
		d.lastUpper = i.upper
	}
}
func (d *cnWeirdWordState) ratio() float64 {
	if d.invalidWords > 0 {
		return 1
	}
	if d.words <= 10 && d.foreignLong == 0 {
		return 0
	}
	return float64(d.badCharacters) / float64(d.characters)
}

type cnUncommonCJKState struct{ count, uncommon int }

func (d *cnUncommonCJKState) feed(i cnCharInfo) {
	d.count++
	if !i.commonCJK {
		d.uncommon++
	}
}
func (d *cnUncommonCJKState) ratio() float64 {
	if d.count < 4 {
		return 0
	}
	return math.Max(0, float64(2*d.uncommon-d.count)/float64(5*max(d.count, 16)))
}

type cnKatakanaState struct{ katakana, halfwidth, cjk, uncommonCJK int }

func (d *cnKatakanaState) feed(i cnCharInfo) {
	if i.katakana {
		d.katakana++
		if i.halfwidthKatakana {
			d.halfwidth++
		}
		return
	}
	d.cjk++
	if !i.commonCJK {
		d.uncommonCJK++
	}
}
func (d *cnKatakanaState) ratio() float64 {
	if d.halfwidth >= 4 && d.halfwidth == d.katakana && d.cjk >= 3 && d.cjk == d.uncommonCJK {
		return 1
	}
	return 0
}

type cnUpperLowerState struct {
	buffered                                     bool
	sinceSep, successive, finalSuccessive, count int
	lastUpper, lastLower, asciiOnly              bool
}

func (d *cnUpperLowerState) feed(i cnCharInfo) {
	sep := !(i.alpha && i.caseVariable)
	if sep && d.sinceSep > 0 {
		if d.sinceSep <= 64 && !i.digit && !d.asciiOnly {
			d.finalSuccessive += d.successive
		}
		d.successive = 0
		d.sinceSep = 0
		d.buffered = false
		d.count++
		d.asciiOnly = true
		return
	}
	if d.asciiOnly && !i.ascii {
		d.asciiOnly = false
	}
	if d.sinceSep > 0 {
		if (i.upper && d.lastLower) || (i.lower && d.lastUpper) {
			if d.buffered {
				d.successive += 2
				d.buffered = false
			} else {
				d.buffered = true
			}
		} else {
			d.buffered = false
		}
	}
	d.count++
	d.sinceSep++
	d.lastUpper = i.upper
	d.lastLower = i.lower
}
func (d *cnUpperLowerState) ratio() float64 {
	if d.count == 0 {
		return 0
	}
	return float64(d.finalSuccessive) / float64(d.count)
}

type cnArabicState struct{ count, isolated int }

func (d *cnArabicState) feed(i cnCharInfo) {
	d.count++
	if i.flags&(1<<8) != 0 {
		d.isolated++
	}
}
func (d *cnArabicState) ratio() float64 {
	if d.count < 8 {
		return 0
	}
	return float64(d.isolated) / float64(d.count)
}

// Source mess_ratio with its exact eligibility, summation order, block ceiling,
// trailing-newline flush and decimal rounding. No global cache policy is added.
func cnMessRatio(points []rune, lookup cnInfoLookup, threshold float64) (float64, error) {
	if lookup == nil {
		return 0, errors.New("fixed Unicode classifier is required")
	}
	n := len(points)
	step := 128
	if n < 511 {
		step = 32
	} else if n < 1024 {
		step = 64
	}
	ascii := true
	for _, c := range points {
		if c >= 128 {
			ascii = false
			break
		}
	}
	sp, ta, up, sda, sr, sw, cu, sk, au, ai := cnSymbolState{}, cnAccentState{}, cnUnprintableState{}, cnDuplicateAccentState{}, cnRangeState{}, cnWeirdWordState{}, cnUncommonCJKState{}, cnKatakanaState{}, cnUpperLowerState{asciiOnly: true}, cnArabicState{}
	sum := func() float64 {
		return sp.ratio() + ta.ratio() + up.ratio() + sda.ratio() + sr.ratio() + sw.ratio() + cu.ratio() + sk.ratio() + au.ratio() + ai.ratio()
	}
	mean := 0.0
	stopped := false
	for start := 0; start < n; start += step {
		for _, c := range points[start:min(start+step, n)] {
			info, err := lookup(c)
			if err != nil {
				return 0, err
			}
			up.feed(c, info)
			sw.feed(c, info)
			if ascii {
				if info.printable {
					sp.feed(c, info)
				}
				continue
			}
			au.feed(info)
			if info.printable {
				sp.feed(c, info)
				if err := sr.feed(info); err != nil {
					return 0, err
				}
			}
			if info.alpha {
				ta.feed(info)
				if info.latin {
					sda.feed(info)
				}
				if info.cjk {
					cu.feed(info)
					sk.feed(info)
				} else if info.katakana {
					sk.feed(info)
				}
				if info.arabic {
					ai.feed(info)
				}
			}
		}
		mean = sum()
		if mean >= threshold {
			stopped = true
			break
		}
	}
	if !stopped {
		info, err := lookup('\n')
		if err != nil {
			return 0, err
		}
		sw.feed('\n', info)
		if !ascii {
			au.feed(info)
		}
		up.feed('\n', info)
		mean = sum()
	}
	return cnRound(mean, 3)
}
