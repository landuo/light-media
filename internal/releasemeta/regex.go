package releasemeta

import (
	"sync"

	"github.com/dlclark/regexp2/v2"
	"light-media/internal/pyregex"
)

var staticRegex sync.Map

type configuredPatternKey struct {
	pattern    string
	ignoreCase bool
}

// Scoped to one frozen parser. Concurrent titles reuse its programs, and
// dropping the parser releases the user-supplied rules. Overflow still matches.
type configuredPatterns struct {
	limit    int
	bytes    int
	mu       sync.Mutex
	programs map[configuredPatternKey]*regexp2.Regexp
}

func (c *configuredPatterns) compile(pattern string, ignoreCase bool) (*regexp2.Regexp, error) {
	if c == nil || len(pattern) > 4096 {
		return pyregex.Compile(pattern, ignoreCase)
	}
	key := configuredPatternKey{pattern, ignoreCase}
	c.mu.Lock()
	if re := c.programs[key]; re != nil {
		c.mu.Unlock()
		return re, nil
	}
	c.mu.Unlock()
	re, err := pyregex.Compile(pattern, ignoreCase)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	limit := c.limit
	if limit == 0 {
		limit = 64
	}
	// Bound the extra storage of a long-lived configuration containing large patterns.
	if len(c.programs) < limit && (c.limit == 0 || c.bytes+len(pattern) <= 96<<10) {
		if c.programs == nil {
			c.programs = make(map[configuredPatternKey]*regexp2.Regexp)
		}
		if existing := c.programs[key]; existing != nil {
			return existing, nil
		}
		c.programs[key] = re
		c.bytes += len(pattern)
	}
	return re, nil
}

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
