package media

import (
	"strings"
	"unicode"
)

// NormalizeTitle treats release-name punctuation and common Latin accent
// spelling as equivalent, while retaining letters and numbers in other scripts.
// Identity still requires the provider's type/year and a unique matching title.
func NormalizeTitle(value string) string {
	return strings.Map(func(r rune) rune {
		r = unicode.ToLower(r)
		for _, group := range []struct {
			variants string
			plain    rune
		}{
			{"àáâãäåāăą", 'a'}, {"çćč", 'c'}, {"ďđ", 'd'},
			{"èéêëēėęě", 'e'}, {"ìíîïīįı", 'i'}, {"ñńň", 'n'},
			{"òóôõöøōő", 'o'}, {"ŕř", 'r'}, {"śšş", 's'},
			{"ťţ", 't'}, {"ùúûüūůűų", 'u'}, {"ýÿ", 'y'}, {"źżž", 'z'},
		} {
			if strings.ContainsRune(group.variants, r) {
				return group.plain
			}
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return -1
	}, value)
}
