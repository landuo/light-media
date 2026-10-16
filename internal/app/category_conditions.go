package app

import (
	"math/big"
	"sort"
	"strings"

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
	condition := mediaCategoryCondition{field: field, include: map[string]bool{}, exclude: map[string]bool{}}
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
		if categoryDigits(first) && categoryDigits(last) {
			begin, _ := new(big.Int).SetString(first, 10)
			end, _ := new(big.Int).SetString(last, 10)
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
	present := extra.Present
	included := len(c.include) == 0 && len(c.includeRanges) == 0
	excluded := false
	for _, value := range values {
		if value == "" {
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
