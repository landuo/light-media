package releasemeta

import (
	"sync"

	"github.com/dlclark/regexp2/v2"
	"light-media/internal/pyregex"
)

var staticRegex sync.Map

func expression(pattern string, ignoreCase bool) *regexp2.Regexp {
	key := pattern
	if ignoreCase {
		key = "i:" + pattern
	}
	if cached, ok := staticRegex.Load(key); ok {
		return cached.(*regexp2.Regexp)
	}
	re, err := pyregex.Compile(pattern, ignoreCase)
	if err != nil {
		panic(err) // Only immutable source expressions reach this helper.
	}
	staticRegex.Store(key, re)
	return re
}

func rxFirst(pattern, value string, ignoreCase bool) *regexp2.Match {
	m, _ := expression(pattern, ignoreCase).FindStringMatch(value)
	return m
}
func rxHas(pattern, value string, ignoreCase bool) bool {
	return rxFirst(pattern, value, ignoreCase) != nil
}
func rxMatches(pattern, value string, ignoreCase bool) []*regexp2.Match {
	re := expression(pattern, ignoreCase)
	var result []*regexp2.Match
	for m, err := re.FindStringMatch(value); m != nil && err == nil; m, err = re.FindNextMatch(m) {
		result = append(result, m)
	}
	return result
}
func rxReplace(pattern, value, replacement string, ignoreCase bool) string {
	result, err := expression(pattern, ignoreCase).Replace(value, replacement, -1, -1)
	if err != nil {
		return value
	}
	return result
}
