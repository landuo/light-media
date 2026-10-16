package rustanime

// Source: anitomy-pure 0.1.0 src/parsing/number.rs (MPL-2.0).

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"strconv"
	"strings"
	"unicode/utf8"
)

func upper(value string) string {
	ascii := true
	for i := 0; i < len(value); i++ {
		if value[i] >= 128 {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToUpper(value)
	}
	var result strings.Builder
	for _, r := range value {
		if mapped, ok := sourceUpperMappings[r]; ok {
			result.WriteString(mapped)
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}
func lower(value string) string { return cases.Lower(language.Und).String(value) }
func allDigits(value string) bool {
	return value != "" && !strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' })
}
func isDigit(value string) bool       { return allDigits(strings.TrimLeft(value, "+-")) }
func containsDigit(value string) bool { return strings.ContainsFunc(value, sourceNumeric) }
func isHexa(value string) bool {
	return value != "" && !strings.ContainsFunc(value, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') })
}
func isCRC32(value string) bool { return len(value) == 8 && isHexa(value) }
func isResolution(value string) bool {
	n := len(value)
	if n == 0 {
		return false
	}
	if n == 2 && strings.ContainsRune("248", rune(value[0])) && (value[1] == 'K' || value[1] == 'k') {
		return true
	}
	if strings.ContainsRune("pPiI", rune(value[n-1])) {
		return (n == 4 || n == 5) && allDigits(value[:n-1])
	}
	if at := strings.IndexAny(value, "xX×"); at >= 0 {
		_, width := utf8.DecodeRuneInString(value[at:])
		left, right := value[:at], value[at+width:]
		return (len(left) == 3 || len(left) == 4) && (len(right) == 3 || len(right) == 4) && allDigits(left) && allDigits(right)
	}
	return false
}
func isAnimeYear(value string) bool {
	n, err := strconv.ParseInt(value, 10, 32)
	return err == nil && n >= 1917 && n <= 2050
}
func parseI32Default(value string, fallback int) int {
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return fallback
	}
	return int(n)
}
func parseI32Unwrap(value string) int {
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		panic(ErrSourcePanic)
	}
	return int(n)
}
func ordinalsToNumber(value string) string {
	switch lower(value) {
	case "1st", "first":
		return "1"
	case "2nd", "second":
		return "2"
	case "3rd", "third":
		return "3"
	case "4th", "fourth":
		return "4"
	case "5th", "fifth":
		return "5"
	case "6th", "sixth":
		return "6"
	case "7th", "seventh":
		return "7"
	case "8th", "eighth":
		return "8"
	case "9th", "ninth":
		return "9"
	}
	return ""
}
