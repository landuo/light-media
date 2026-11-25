package sitestatistic

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Captured from the exact isolated source Python3.12.13/Unicode15 image.
// Go's newer Unicode tables do not define Python's isdigit or decimal values.
//
//go:embed reference/python312-unicode-tables.json
var characterJSON []byte
var characterProfile struct {
	Digits      [][2]int `json:"digit_ranges"`
	Decimals    [][2]int `json:"decimal_ranges"`
	Spaces      []int    `json:"space_codepoints"`
	FloatSpaces []int    `json:"float_space_codepoints"`
	IntSpaces   []int    `json:"int_space_codepoints"`
	Columns     []string `json:"model_columns"`
}

func init() {
	if e := json.Unmarshal(characterJSON, &characterProfile); e != nil {
		panic(e)
	}
}
func inRanges(r rune, ranges [][2]int) bool {
	i := sort.Search(len(ranges), func(i int) bool { return int(r) <= ranges[i][1] })
	return i < len(ranges) && int(r) >= ranges[i][0]
}
func decimal(r rune) (int, bool) {
	i := sort.Search(len(characterProfile.Decimals), func(i int) bool { return int(r) <= characterProfile.Decimals[i][1] })
	if i < len(characterProfile.Decimals) && int(r) >= characterProfile.Decimals[i][0] {
		return int(r) - characterProfile.Decimals[i][0], true
	}
	return 0, false
}
func codepointIn(r rune, values []int) bool {
	i := sort.SearchInts(values, int(r))
	return i < len(values) && values[i] == int(r)
}
func sourceIsDigit(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if !inRanges(r, characterProfile.Digits) {
			return false
		}
	}
	return true
}
func sourceFloatSpace(r rune) bool { return codepointIn(r, characterProfile.FloatSpaces) }
func sourceIntSpace(r rune) bool   { return codepointIn(r, characterProfile.IntSpaces) }
func normalizeDecimal(text string) string {
	return strings.Map(func(r rune) rune {
		if v, ok := decimal(r); ok {
			return rune('0' + v)
		}
		return r
	}, text)
}

const decimalDigits = `[0-9](?:_?[0-9])*`

var pythonFloatSyntax = regexp.MustCompile(`^[+-]?(?:(?:` + decimalDigits + `(?:\.(?:` + decimalDigits + `)?)?|\.` + decimalDigits + `)(?:[eE][+-]?` + decimalDigits + `)?|(?i:inf(?:inity)?|nan))$`)
var pythonIntSyntax = regexp.MustCompile(`^[+-]?` + decimalDigits + `$`)

type SourceError struct{ Kind, Message string }

func (e *SourceError) Error() string { return e.Kind + ": " + e.Message }
func sourceFloat(text string) (float64, error) {
	s := normalizeDecimal(strings.TrimFunc(text, sourceFloatSpace))
	if !pythonFloatSyntax.MatchString(s) {
		return 0, &SourceError{"ValueError", "could not convert string to float: " + pyRepr(text)}
	}
	s = strings.ReplaceAll(s, "_", "")
	lower := strings.ToLower(s)
	if lower == "nan" || lower == "+nan" || lower == "-nan" {
		return math.NaN(), nil
	}
	if lower == "inf" || lower == "+inf" || lower == "infinity" || lower == "+infinity" {
		return math.Inf(1), nil
	}
	if lower == "-inf" || lower == "-infinity" {
		return math.Inf(-1), nil
	}
	f, e := strconv.ParseFloat(s, 64)
	if _, ok := e.(*strconv.NumError); ok && math.IsInf(f, 0) {
		return f, nil
	}
	return f, e
}
func sourceIntString(text string) (*big.Int, error) {
	s := normalizeDecimal(strings.TrimFunc(text, sourceIntSpace))
	if !pythonIntSyntax.MatchString(s) {
		return nil, &SourceError{"ValueError", "invalid literal for int() with base 10: " + pyRepr(text)}
	}
	s = strings.ReplaceAll(s, "_", "")
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, &SourceError{"ValueError", "invalid integer"}
	}
	return n, nil
}
func subDigit(v any) bool {
	if v == nil {
		return false
	}
	if s, ok := v.(string); ok {
		return sourceIsDigit(s)
	}
	return isNumeric(v)
}
func subInteger(v any) *big.Int {
	if s, ok := v.(string); ok {
		f, e := sourceFloat(s)
		if e != nil {
			panic(e)
		}
		return integer(f)
	}
	return integer(v)
}

// Source numeric strptime permits an unpadded month/day and a space-padded
// ASCII day. Its year uses Unicode decimal \d; its month/day ranges differ.
func parseSourceDate(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, &SourceError{"TypeError", "strptime() argument 1 must be str"}
	}
	bad := func() (time.Time, error) {
		return time.Time{}, &SourceError{"ValueError", fmt.Sprintf("time data %s does not match format '%%Y-%%m-%%d'", pyRepr(s))}
	}
	parts := strings.Split(s, "-")
	if len(parts) != 3 {
		return bad()
	}
	years := []rune(parts[0])
	if len(years) != 4 {
		return bad()
	}
	year := 0
	for _, r := range years {
		d, valid := decimal(r)
		if !valid {
			return bad()
		}
		year = year*10 + d
	}
	if year < 1 {
		return time.Time{}, &SourceError{"ValueError", "year 0 is out of range"}
	}
	monthText := parts[1]
	month, e := strconv.Atoi(monthText)
	if e != nil || month < 1 || month > 12 || len(monthText) > 2 || len(monthText) == 2 && monthText[0] != '0' && monthText[0] != '1' {
		return bad()
	}
	for _, r := range monthText {
		if r < '0' || r > '9' {
			return bad()
		}
	}
	days := []rune(parts[2])
	day := 0
	if len(days) == 1 && days[0] >= '1' && days[0] <= '9' {
		day = int(days[0] - '0')
	} else if len(days) == 2 {
		switch {
		case (days[0] == ' ' || days[0] == '0') && days[1] >= '1' && days[1] <= '9':
			day = int(days[1] - '0')
		case days[0] == '1' || days[0] == '2':
			d, valid := decimal(days[1])
			if !valid {
				return bad()
			}
			day = int(days[0]-'0')*10 + d
		case days[0] == '3' && (days[1] == '0' || days[1] == '1'):
			day = 30 + int(days[1]-'0')
		default:
			return bad()
		}
	} else {
		return bad()
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if date.Year() != year || int(date.Month()) != month || date.Day() != day {
		return time.Time{}, &SourceError{"ValueError", "day is out of range for month"}
	}
	return date, nil
}
func sourceDay(date time.Time) string {
	return fmt.Sprintf("%d-%02d-%02d", date.Year(), date.Month(), date.Day())
}
