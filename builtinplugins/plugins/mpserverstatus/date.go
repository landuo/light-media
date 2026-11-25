package mpserverstatus

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ResponseDate follows Python3.12 email._parseaddr._parsedate_tz and
// email.utils.parsedate_to_datetime. Unknown zones and -0000 are naive.
// Source malformed dates are returned unchanged by the plugin's catch.
func ResponseDate(value string) string {
	if value == "" {
		return "-"
	}
	fields := strings.FieldsFunc(value, pythonSpace)
	if len(fields) == 0 {
		return value
	}
	days := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	dayname := false
	for _, d := range days {
		if strings.ToLower(fields[0]) == d {
			dayname = true
		}
	}
	if strings.HasSuffix(fields[0], ",") || dayname {
		fields = fields[1:]
	} else if i := strings.LastIndex(fields[0], ","); i >= 0 {
		fields[0] = fields[0][i+1:]
	}
	if len(fields) == 3 {
		if parts := strings.Split(fields[0], "-"); len(parts) == 3 {
			fields = append(parts, fields[1:]...)
		}
	}
	if len(fields) == 4 {
		s := fields[3]
		i := strings.Index(s, "+")
		if i < 0 {
			i = strings.Index(s, "-")
		}
		if i > 0 {
			fields = append(fields[:3], s[:i], s[i:])
		} else {
			fields = append(fields, "")
		}
	}
	if len(fields) < 5 {
		return value
	}
	fields = fields[:5]
	dd, mm, yy, tm, tz := fields[0], strings.ToLower(fields[1]), fields[2], fields[3], fields[4]
	if dd == "" || mm == "" || yy == "" {
		return value
	}
	months := []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec", "january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
	monthIndex := func(s string) int {
		for i, m := range months {
			if s == m {
				return i%12 + 1
			}
		}
		return 0
	}
	month := monthIndex(mm)
	if month == 0 {
		dd, mm = mm, strings.ToLower(dd)
		month = monthIndex(mm)
		if month == 0 {
			return value
		}
	}
	dd = strings.TrimSuffix(dd, ",")
	if i := strings.Index(yy, ":"); i > 0 {
		yy, tm = tm, yy
	}
	yy = strings.TrimSuffix(yy, ",")
	if yy == "" {
		return value
	}
	first, _ := utf8.DecodeRuneInString(yy)
	if !pythonDateDigit(first) {
		yy, tz = tz, yy
	}
	tm = strings.TrimSuffix(tm, ",")
	parts := strings.Split(tm, ":")
	if len(parts) == 1 && strings.Contains(tm, ".") {
		parts = strings.Split(tm, ".")
	}
	if len(parts) == 2 {
		parts = append(parts, "0")
	}
	if len(parts) != 3 {
		return value
	}
	parse := func(text string) (int, error) {
		var b strings.Builder
		previousDigit := false
		digits := 0
		for i, r := range strings.TrimFunc(text, pythonSpace) {
			if i == 0 && (r == '+' || r == '-') {
				b.WriteRune(r)
				continue
			}
			if r == '_' {
				if !previousDigit {
					return 0, errors.New("invalid integer")
				}
				previousDigit = false
				continue
			}
			found := false
			for _, zero := range pythonDecimalZeroes {
				if r >= zero && r <= zero+9 {
					b.WriteByte(byte('0' + r - zero))
					found = true
					break
				}
			}
			if !found {
				return 0, errors.New("invalid integer")
			}
			previousDigit = true
			digits++
		}
		if !previousDigit || digits > 4300 {
			return 0, errors.New("invalid integer")
		}
		n, ok := new(big.Int).SetString(b.String(), 10)
		if !ok {
			return 0, errors.New("invalid integer")
		}
		if !n.IsInt64() {
			return 0, strconv.ErrRange
		}
		return int(n.Int64()), nil
	}
	year, e := parse(yy)
	if e != nil {
		return value
	}
	day, e := parse(dd)
	if e != nil {
		return value
	}
	hour, e := parse(parts[0])
	if e != nil {
		return value
	}
	minute, e := parse(parts[1])
	if e != nil {
		return value
	}
	second, e := parse(parts[2])
	if e != nil {
		return value
	}
	if year < 100 {
		if year > 68 {
			year += 1900
		} else {
			year += 2000
		}
	}
	if year < 1 || year > 9999 || day < 1 || day > 31 || hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 {
		return value
	}
	d := time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC)
	if d.Month() != time.Month(month) || d.Day() != day {
		return value
	}
	zones := map[string]int{"UT": 0, "UTC": 0, "GMT": 0, "Z": 0, "AST": -400, "ADT": -300, "EST": -500, "EDT": -400, "CST": -600, "CDT": -500, "MST": -700, "MDT": -600, "PST": -800, "PDT": -700}
	upper := strings.ToUpper(tz)
	offset, known := zones[upper]
	if !known {
		offset, e = parse(upper)
		if errors.Is(e, strconv.ErrRange) {
			return value
		}
		known = e == nil
		if known && offset == 0 && strings.HasPrefix(upper, "-") {
			known = false
		}
	}
	zone := ""
	if known {
		if offset <= -2400 || offset >= 2400 {
			return value
		}
		zone = "UTC"
		if offset != 0 {
			sign := "+"
			if offset < 0 {
				sign = "-"
				offset = -offset
			}
			seconds := (offset/100)*3600 + (offset%100)*60
			if seconds >= 86400 {
				return value
			}
			zone = fmt.Sprintf("UTC%s%02d:%02d", sign, seconds/3600, seconds%3600/60)
		}
	}
	return d.Format("2006-01-02 15:04:05 ") + zone
}

// Python3.12.13 Unicode15.0 str.isdigit, exact actual-image table (83 ranges).
// Source model SHA256: 2d6903ad90fcffcce85c222138dd247d88f6ae7d32d04ed71dff14f9bf92264a.
var pythonDateDigitRanges = [][2]rune{
	{0x30, 0x39},
	{0xb2, 0xb3},
	{0xb9, 0xb9},
	{0x660, 0x669},
	{0x6f0, 0x6f9},
	{0x7c0, 0x7c9},
	{0x966, 0x96f},
	{0x9e6, 0x9ef},
	{0xa66, 0xa6f},
	{0xae6, 0xaef},
	{0xb66, 0xb6f},
	{0xbe6, 0xbef},
	{0xc66, 0xc6f},
	{0xce6, 0xcef},
	{0xd66, 0xd6f},
	{0xde6, 0xdef},
	{0xe50, 0xe59},
	{0xed0, 0xed9},
	{0xf20, 0xf29},
	{0x1040, 0x1049},
	{0x1090, 0x1099},
	{0x1369, 0x1371},
	{0x17e0, 0x17e9},
	{0x1810, 0x1819},
	{0x1946, 0x194f},
	{0x19d0, 0x19da},
	{0x1a80, 0x1a89},
	{0x1a90, 0x1a99},
	{0x1b50, 0x1b59},
	{0x1bb0, 0x1bb9},
	{0x1c40, 0x1c49},
	{0x1c50, 0x1c59},
	{0x2070, 0x2070},
	{0x2074, 0x2079},
	{0x2080, 0x2089},
	{0x2460, 0x2468},
	{0x2474, 0x247c},
	{0x2488, 0x2490},
	{0x24ea, 0x24ea},
	{0x24f5, 0x24fd},
	{0x24ff, 0x24ff},
	{0x2776, 0x277e},
	{0x2780, 0x2788},
	{0x278a, 0x2792},
	{0xa620, 0xa629},
	{0xa8d0, 0xa8d9},
	{0xa900, 0xa909},
	{0xa9d0, 0xa9d9},
	{0xa9f0, 0xa9f9},
	{0xaa50, 0xaa59},
	{0xabf0, 0xabf9},
	{0xff10, 0xff19},
	{0x104a0, 0x104a9},
	{0x10a40, 0x10a43},
	{0x10d30, 0x10d39},
	{0x10e60, 0x10e68},
	{0x11052, 0x1105a},
	{0x11066, 0x1106f},
	{0x110f0, 0x110f9},
	{0x11136, 0x1113f},
	{0x111d0, 0x111d9},
	{0x112f0, 0x112f9},
	{0x11450, 0x11459},
	{0x114d0, 0x114d9},
	{0x11650, 0x11659},
	{0x116c0, 0x116c9},
	{0x11730, 0x11739},
	{0x118e0, 0x118e9},
	{0x11950, 0x11959},
	{0x11c50, 0x11c59},
	{0x11d50, 0x11d59},
	{0x11da0, 0x11da9},
	{0x11f50, 0x11f59},
	{0x16a60, 0x16a69},
	{0x16ac0, 0x16ac9},
	{0x16b50, 0x16b59},
	{0x1d7ce, 0x1d7ff},
	{0x1e140, 0x1e149},
	{0x1e2f0, 0x1e2f9},
	{0x1e4f0, 0x1e4f9},
	{0x1e950, 0x1e959},
	{0x1f100, 0x1f10a},
	{0x1fbf0, 0x1fbf9},
}

func pythonDateDigit(r rune) bool {
	for _, pair := range pythonDateDigitRanges {
		if r >= pair[0] && r <= pair[1] {
			return true
		}
	}
	return false
}
