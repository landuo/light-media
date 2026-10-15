package app

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"light-media/internal/catalog"
)

var fallbackIMDb = regexp.MustCompile(`^tt[0-9]{7,10}$`)

// fallbackSearchQueries is a finite title-identity search plan. Each actual site
// request still goes through withSite's quota, rate and subscription guards.
// An explicit query freezes a prior site's search for Preview/Add revalidation;
// it never silently expands that revalidation to different keywords.
func fallbackSearchQueries(identity catalog.Media, userQuery string) []string {
	queries := []string{}
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.Join(strings.Fields(value), " ")
		if len(queries) >= 4 || !validFallbackQuery(value) {
			return
		}
		key := strings.ToLower(value)
		if seen[key] {
			return
		}
		seen[key] = true
		queries = append(queries, value)
	}
	if userQuery != "" {
		if validFallbackQuery(userQuery) {
			add(userQuery)
		}
		return queries
	}
	add(identity.Title)
	if fallbackIMDb.MatchString(identity.IMDbID) {
		add(identity.IMDbID)
	}
	original := identity.OriginalTitle
	if identity.MediaType == "movie" && validFallbackYear(identity.Year) && original != "" && !sameFallbackTitle(original, identity.Title) {
		original = withFallbackYear(original, identity.Year)
	}
	add(original)
	for _, alias := range identity.Aliases {
		if hasFallbackHan(alias) && !sameFallbackTitle(alias, identity.Title) && !sameFallbackTitle(alias, identity.OriginalTitle) {
			add(alias)
			break
		}
	}
	if len(queries) < 4 && validFallbackYear(identity.Year) {
		add(withFallbackYear(identity.Title, identity.Year))
	}
	return queries
}

func validFallbackQuery(value string) bool {
	if strings.TrimSpace(value) == "" || len(value) > 480 || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 120 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validFallbackYear(year int) bool { return year >= 1800 && year <= 2199 }
func sameFallbackTitle(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}
func hasFallbackHan(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
func withFallbackYear(title string, year int) string {
	number := strconv.Itoa(year)
	fields := strings.Fields(title)
	for _, field := range fields {
		if strings.Trim(field, "()[]（）【】") == number {
			return title
		}
	}
	return strings.TrimSpace(title) + " " + number
}
