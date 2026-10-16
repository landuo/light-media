package legacyanime

import (
	"math/big"
	"strconv"
	"strings"
)

// IsDigit preserves Python Unicode 15's broader-than-decimal str.isdigit.
func IsDigit(value string) bool { return pyDigit(value) }

// Integer performs the installed Python int conversion and digit-count bound.
func Integer(value string) (result *big.Int, err error) {
	defer func() {
		if value := recover(); value != nil {
			if source, ok := value.(SourceError); ok {
				err = source
			} else {
				panic(value)
			}
		}
	}()
	return pyInt(value), nil
}
func sigma(runes []rune, index int) rune {
	before, after := false, false
	for i := index - 1; i >= 0; i-- {
		if inRange(runes[i], pyCaseIgnorableRanges) {
			continue
		}
		before = inRange(runes[i], pyCasedRanges)
		break
	}
	for i := index + 1; i < len(runes); i++ {
		if inRange(runes[i], pyCaseIgnorableRanges) {
			continue
		}
		after = inRange(runes[i], pyCasedRanges)
		break
	}
	if before && !after {
		return 'ς'
	}
	return 'σ'
}

// TitleCase is CPython str.title: cased-character boundaries, full title/lower
// mappings and contextual final Sigma. Numeric word starts differ from Rust's
// MoviePilot name casing, so the two implementations remain independent.
func TitleCase(value string) string {
	var out strings.Builder
	previous := false
	runes := []rune(value)
	for i, r := range runes {
		mapping := pyTitleMappings
		if previous {
			mapping = pyLowerMappings
			if r == 'Σ' {
				out.WriteRune(sigma(runes, i))
				previous = inRange(r, pyCasedRanges)
				continue
			}
		}
		if v, ok := mapping[r]; ok {
			out.WriteString(v)
		} else {
			out.WriteRune(r)
		}
		previous = inRange(r, pyCasedRanges)
	}
	return out.String()
}

// LowerCase is the installed Python full mapping with contextual final Sigma.
func LowerCase(value string) string {
	var out strings.Builder
	runes := []rune(value)
	for i, r := range runes {
		if r == 'Σ' {
			out.WriteRune(sigma(runes, i))
		} else if v, ok := pyLowerMappings[r]; ok {
			out.WriteString(v)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Strip and Fields use CPython whitespace, including its four C0 separators.
func Strip(value string) string    { return strings.TrimFunc(value, pySpace) }
func Fields(value string) []string { return strings.FieldsFunc(value, pySpace) }

// DecimalClass is the exact installed Python15 decimal class for wrappers.
func DecimalClass() string {
	var out strings.Builder
	out.WriteByte('[')
	for r := rune(0); r <= 0x10ffff; r++ {
		if _, ok := pyDecimal[r]; ok {
			out.WriteString("\\x{" + strconv.FormatInt(int64(r), 16) + "}")
		}
	}
	out.WriteByte(']')
	return out.String()
}
