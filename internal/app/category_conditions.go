package app

import (
	"math/big"
	"sort"
	"strings"
	"unicode"

	"light-media/internal/catalog"
)

type categoryNumberRange struct{ first, last *big.Int }

func categoryDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// Original semantics: comma separates values without trimming; a hyphen splits
// at the first occurrence. Numeric endpoints expand inclusively; non-numeric
// ranges contribute only their two endpoints, including exclusions.
func parseMediaCategoryCondition(field, expression string) (mediaCategoryCondition, error) {
	return parseMediaCategoryConditionMode(field, expression, false)
}

func parseMediaCategoryConditionMode(field, expression string, runtimeSemantics bool) (mediaCategoryCondition, error) {
	condition := mediaCategoryCondition{field: field, include: map[string]bool{}, exclude: map[string]bool{}, runtimeSemantics: runtimeSemantics}
	add := func(value string) {
		target := condition.include
		if strings.HasPrefix(value, "!") {
			value, target = strings.TrimPrefix(value, "!"), condition.exclude
		}
		target[catalog.NormalizeCategoryValue(value)] = true
	}
	for _, token := range strings.Split(expression, ",") {
		if token == "" {
			continue
		}
		first, last, isRange := strings.Cut(token, "-")
		if !isRange {
			add(token)
			continue
		}
		prefix := ""
		if strings.HasPrefix(first, "!") {
			prefix, first = "!", strings.TrimPrefix(first, "!")
		}
		digits := categoryDigits
		if runtimeSemantics {
			digits = categoryPythonDigits
		}
		if digits(first) && digits(last) {
			begin, beginOK := categoryDecimalInteger(first)
			end, endOK := categoryDecimalInteger(last)
			if !beginOK || !endOK {
				// Python isdigit also includes e.g. superscript ², but int
				// accepts only decimal digits. init never runs that int().
				condition.deferredRangeError = true
				continue
			}
			if begin.Cmp(end) > 0 {
				continue // Python range(a,b+1) is empty when a>b.
			}
			rangeValue := categoryNumberRange{first: begin, last: end}
			if prefix == "!" {
				condition.excludeRanges = append(condition.excludeRanges, rangeValue)
			} else {
				condition.includeRanges = append(condition.includeRanges, rangeValue)
			}
		} else {
			add(prefix + first)
			add(prefix + last)
		}
	}
	return condition, nil
}

func categoryPythonDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if unicode.IsDigit(char) {
			continue
		}
		found := false
		// Unicode 15.0 Numeric_Type=Digit minus Numeric_Type=Decimal,
		// matching the archived oracle's CPython 3.12 isdigit() table.
		for _, interval := range [][2]rune{
			{0x00b2, 0x00b3}, {0x00b9, 0x00b9}, {0x1369, 0x1371}, {0x19da, 0x19da},
			{0x2070, 0x2070}, {0x2074, 0x2079}, {0x2080, 0x2089}, {0x2460, 0x2468},
			{0x2474, 0x247c}, {0x2488, 0x2490}, {0x24ea, 0x24ea}, {0x24f5, 0x24fd},
			{0x24ff, 0x24ff}, {0x2776, 0x277e}, {0x2780, 0x2788}, {0x278a, 0x2792},
			{0x10a40, 0x10a43}, {0x10e60, 0x10e68}, {0x11052, 0x1105a}, {0x1f100, 0x1f10a},
		} {
			if char >= interval[0] && char <= interval[1] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func categoryDecimalInteger(value string) (*big.Int, bool) {
	var ascii strings.Builder
	for _, char := range value {
		digit := -1
		if char >= '0' && char <= '9' {
			digit = int(char - '0')
		} else if char <= 0xffff {
			for _, interval := range unicode.Digit.R16 {
				if uint16(char) >= interval.Lo && uint16(char) <= interval.Hi && (uint16(char)-interval.Lo)%interval.Stride == 0 {
					digit = int((uint16(char) - interval.Lo) / interval.Stride % 10)
					break
				}
			}
		} else {
			for _, interval := range unicode.Digit.R32 {
				if uint32(char) >= interval.Lo && uint32(char) <= interval.Hi && (uint32(char)-interval.Lo)%interval.Stride == 0 {
					digit = int((uint32(char) - interval.Lo) / interval.Stride % 10)
					break
				}
			}
		}
		if digit < 0 {
			return nil, false
		}
		ascii.WriteByte(byte('0' + digit))
	}
	return new(big.Int).SetString(ascii.String(), 10)
}

func categoryRangeContains(ranges []categoryNumberRange, value string) bool {
	if !categoryDigits(value) {
		return false
	}
	number, ok := new(big.Int).SetString(value, 10)
	if !ok || number.String() != value {
		return false
	}
	for _, interval := range ranges {
		if number.Cmp(interval.first) >= 0 && number.Cmp(interval.last) <= 0 {
			return true
		}
	}
	return false
}

func (c mediaCategoryCondition) matches(values []string, extra catalog.CategoryField) bool {
	return c.matchesPresent(values, extra, false)
}

func (c mediaCategoryCondition) matchesPresent(values []string, extra catalog.CategoryField, infoPresent bool) bool {
	present := extra.Present || infoPresent
	included := len(c.include) == 0 && len(c.includeRanges) == 0
	excluded := false
	for _, value := range values {
		if value == "" && !infoPresent {
			continue
		}
		present = true
		value = catalog.NormalizeCategoryValue(value)
		included = included || c.include[value] || categoryRangeContains(c.includeRanges, value)
		excluded = excluded || c.exclude[value] || categoryRangeContains(c.excludeRanges, value)
	}
	if extra.Present {
		hashes := make(map[string]bool, len(extra.Hashes))
		for _, hash := range extra.Hashes {
			hashes[hash] = true
		}
		for value := range c.include {
			included = included || hashes[catalog.CategoryValueHash(value)]
		}
		for value := range c.exclude {
			excluded = excluded || hashes[catalog.CategoryValueHash(value)]
		}
		for _, integer := range extra.Integers {
			included = included || categoryRangeContains(c.includeRanges, integer)
			excluded = excluded || categoryRangeContains(c.excludeRanges, integer)
		}
	}
	return present && included && !excluded
}

func (c mediaCategoryCondition) matchesWithError(values []string, extra catalog.CategoryField, infoPresent bool) (bool, error) {
	if c.deferredSplitError {
		// Original skips value.split when info_value is falsey, but not when
		// a different condition has already set match_flag to False.
		if !infoPresent && !extra.Present {
			return false, nil
		}
		return false, categoryPolicyError("条件「" + c.field + "」不是字符串，无法执行split")
	}
	if c.deferredRangeError {
		if !infoPresent && !extra.Present {
			return false, nil
		}
		return false, categoryPolicyError("条件「" + c.field + "」的数字范围不能转换为整数")
	}
	return c.matchesPresent(values, extra, c.runtimeSemantics && infoPresent), nil
}

func (p mediaCategoryPolicy) extraFields(kind string) []string {
	set := map[string]bool{}
	for _, rule := range p[kind] {
		for _, condition := range rule.conditions {
			switch condition.field {
			case "genre_ids", "original_language", "origin_country", "production_countries", "release_year":
			default:
				set[condition.field] = true
			}
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
