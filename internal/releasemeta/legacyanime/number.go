package legacyanime

import (
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Python's int is arbitrary precision but its installed default limits decimal
// strings to 4300 digits. str2int catches only ValueError; direct int does not.
func pyInt(value string) *big.Int {
	value = strings.TrimFunc(value, func(r rune) bool { return pySpace(r) && !(r >= 0x1c && r <= 0x1f) })
	var normalized strings.Builder
	digits := 0
	for _, r := range value {
		if v, ok := pyDecimal[r]; ok {
			normalized.WriteByte(byte('0' + v))
			digits++
		} else {
			normalized.WriteRune(r)
		}
	}
	if digits > 4300 {
		sourcePanic("ValueError")
	}
	text := normalized.String()
	if text == "" {
		sourcePanic("ValueError")
	}
	// Python permits underscores only between decimal digits.
	for i, r := range text {
		if r == '_' && (i == 0 || i == len(text)-1 || text[i-1] < '0' || text[i-1] > '9' || text[i+1] < '0' || text[i+1] > '9') {
			sourcePanic("ValueError")
		}
	}
	text = strings.ReplaceAll(text, "_", "")
	if n, ok := new(big.Int).SetString(text, 10); ok {
		return n
	}
	sourcePanic("ValueError")
	return nil
}
func str2int(value string) (number *big.Int) {
	defer func() {
		if err := recover(); err != nil {
			if e, ok := err.(SourceError); ok && e.Class == "ValueError" {
				number = new(big.Int)
			} else {
				panic(err)
			}
		}
	}()
	return pyInt(value)
}
func validEpisode(value string) bool { return str2int(value).Cmp(big.NewInt(1899)) <= 0 }
func (s *state) setEpisode(number string, t *token, validate bool) bool {
	if validate && !validEpisode(number) {
		return false
	}
	t.category = tokenIdentifier
	category := EpisodeNumber
	if s.checkAlt {
		if len(s.elements[EpisodeNumber]) == 0 {
			sourcePanic("IndexError")
		}
		episode := s.elements[EpisodeNumber][0]
		cmp := str2int(number).Cmp(str2int(episode))
		if cmp > 0 {
			category = EpisodeNumberAlt
		} else if cmp < 0 {
			s.remove(EpisodeNumber, episode)
			s.insert(EpisodeNumberAlt, episode)
		} else {
			return false
		}
	}
	s.insert(category, number)
	return true
}
func (s *state) setAlternative(number string, t *token) bool {
	s.insert(EpisodeNumberAlt, number)
	t.category = tokenIdentifier
	return true
}
func (s *state) setVolume(number string, t *token, validate bool) bool {
	if validate && pyInt(number).Cmp(big.NewInt(20)) > 0 {
		return false
	}
	s.insert(VolumeNumber, number)
	t.category = tokenIdentifier
	return true
}
func (s *state) setSeason(number string, t *token) bool {
	if !pyDigit(number) {
		return false
	}
	s.insert(AnimeSeason, number)
	t.category = tokenIdentifier
	return true
}

var regexCache sync.Map
var decimalPattern = func() string {
	keys := make([]int, 0, len(pyDecimal))
	for r := range pyDecimal {
		keys = append(keys, int(r))
	}
	sort.Ints(keys)
	var out strings.Builder
	out.WriteByte('[')
	for _, r := range keys {
		fmt.Fprintf(&out, `\x{%x}`, r)
	}
	out.WriteByte(']')
	return out.String()
}()

func match(pattern, value string, ignoreCase bool) []string {
	key := pattern
	if ignoreCase {
		key = "i:" + key
	}
	var re *regexp.Regexp
	if v, ok := regexCache.Load(key); ok {
		re = v.(*regexp.Regexp)
	} else {
		source := strings.ReplaceAll(pattern, `\d`, decimalPattern)
		source = strings.ReplaceAll(source, `$`, "(?:\\n)?$")
		source = "^(?:" + source + ")"
		if ignoreCase {
			source = "(?i)" + source
		}
		re = regexp.MustCompile(source)
		regexCache.Store(key, re)
	}
	return re.FindStringSubmatch(value)
}
func isResolution(value string) bool {
	return match(`\d{3,4}([pP]|([xX×]\d{3,4}))$`, value, false) != nil
}
func (s *state) checkExtent(c Category, t *token) bool {
	next := s.next(t, flagNotDelimiter)
	if tokenCat(next) == tokenUnknown && numberIndex(next.content) >= 0 {
		switch c {
		case EpisodeNumber:
			if !s.matchEpisodePatterns(next.content, next) {
				s.setEpisode(next.content, next, false)
			}
		case VolumeNumber:
			if !s.matchVolumePatterns(next.content, next) {
				s.setVolume(next.content, next, false)
			}
		default:
			return false
		}
		t.category = tokenIdentifier
		return true
	}
	return false
}
func (s *state) numberAfterPrefix(c Category, t *token) bool {
	at := numberIndex(t.content)
	if at < 0 {
		at = runeLen(t.content)
	} // Python [:None] is the full string.
	prefix := runeSlice(t.content, 0, at)
	if findKeyword(normalize(prefix), c) != nil {
		number := runeSlice(t.content, at, runeLen(t.content))
		switch c {
		case EpisodePrefix:
			if s.matchEpisodePatterns(number, t) {
				return true
			}
			return s.setEpisode(number, t, false)
		case VolumePrefix:
			if s.matchVolumePatterns(number, t) {
				return true
			}
			return s.setVolume(number, t, false)
		case AnimeSeasonPrefix:
			return s.setSeason(number, t)
		}
	}
	return false
}
func (s *state) numberBeforeNumber(t *token) bool {
	separator := s.next(t, flagNotDelimiter)
	if separator != nil && (separator.content == "&" || separator.content == "of") {
		other := s.next(separator, flagNotDelimiter)
		if other != nil && pyDigit(other.content) {
			s.setEpisode(t.content, t, false)
			if separator.content == "&" {
				s.setEpisode(other.content, t, false)
			}
			separator.category = tokenIdentifier
			other.category = tokenIdentifier
			return true
		}
	}
	return false
}
func (s *state) searchEpisodePatterns(tokens []*token) bool {
	for _, t := range tokens {
		runes := []rune(t.content)
		if len(runes) == 0 {
			sourcePanic("IndexError")
		}
		if !inRange(runes[0], pyDigitRanges) {
			if s.numberAfterPrefix(EpisodePrefix, t) {
				return true
			}
			if s.numberAfterPrefix(VolumePrefix, t) {
				continue
			}
			if s.numberAfterPrefix(AnimeSeasonPrefix, t) {
				continue
			}
		} else if s.numberBeforeNumber(t) {
			return true
		}
		if s.matchEpisodePatterns(t.content, t) {
			return true
		}
	}
	return false
}
func (s *state) matchSingleEpisode(word string, t *token) bool {
	if m := match(`(\d{1,3})[vV](\d)$`, word, false); m != nil {
		s.setEpisode(m[1], t, false)
		s.insert(ReleaseVersion, m[2])
		return true
	}
	return false
}
func (s *state) matchMultiEpisode(word string, t *token) bool {
	if m := match(`(\d{1,3})(?:[vV](\d))?[-~&+](\d{1,3})(?:[vV](\d))?$`, word, false); m != nil {
		if pyInt(m[1]).Cmp(pyInt(m[3])) < 0 && s.setEpisode(m[1], t, true) {
			s.setEpisode(m[3], t, false)
			if m[2] != "" {
				s.insert(ReleaseVersion, m[2])
			}
			if m[4] != "" {
				s.insert(ReleaseVersion, m[4])
			}
			return true
		}
	}
	return false
}
func (s *state) matchSeasonEpisode(word string, t *token) bool {
	if m := match(`S?(\d{1,2})(?:-S?(\d{1,2}))?(?:x|[ ._-x]?E)(\d{1,3})(?:-E?(\d{1,3}))?(?:[vV](\d))?$`, word, true); m != nil {
		s.insert(AnimeSeason, m[1])
		if m[2] != "" {
			s.insert(AnimeSeason, m[2])
		}
		s.setEpisode(m[3], t, false)
		if m[4] != "" {
			s.setEpisode(m[4], t, false)
		}
		if m[5] != "" {
			s.insert(ReleaseVersion, m[5])
		}
		return true
	}
	return false
}
func (s *state) matchTypeEpisode(word string, t *token) bool {
	at := numberIndex(word)
	if at < 0 {
		at = runeLen(word)
	}
	prefix := runeSlice(word, 0, at)
	if keyword := findKeyword(normalize(prefix), AnimeType); keyword != nil {
		s.insert(AnimeType, prefix)
		number := runeSlice(word, at, runeLen(word))
		if s.matchEpisodePatterns(number, t) || s.setEpisode(number, t, true) {
			index := s.index(t)
			t.content = number
			category := tokenUnknown
			if keyword.identifiable {
				category = tokenIdentifier
			}
			s.insertToken(index, &token{category, prefix, t.enclosed})
			return true
		}
	}
	return false
}
func (s *state) matchFractional(word string, t *token) bool {
	return match(`\d+\.5$`, word, false) != nil && s.setEpisode(word, t, true)
}
func (s *state) matchPartial(word string, t *token) bool {
	at := nonNumberIndex(word)
	if at < 0 {
		at = 0
	}
	suffix := runeSlice(word, at, runeLen(word))
	return runeLen(suffix) == 1 && strings.Contains("ABCabc", suffix) && s.setEpisode(word, t, true)
}
func (s *state) matchNumberSign(word string, t *token) bool {
	if !strings.HasPrefix(word, "#") {
		return false
	}
	if m := match(`#(\d{1,3})(?:[-~&+](\d{1,3}))?(?:[vV](\d))?$`, word, false); m != nil && s.setEpisode(m[1], t, true) {
		if m[2] != "" {
			s.setEpisode(m[2], t, true)
		}
		if m[3] != "" {
			s.insert(ReleaseVersion, m[3])
		}
		return true
	}
	return false
}
func (s *state) matchJapanese(word string, t *token) bool {
	if !strings.HasSuffix(word, "話") {
		return false
	}
	if m := match(`(\d{1,3})話$`, word, false); m != nil {
		return s.setEpisode(m[1], t, false)
	}
	return false
}
func (s *state) matchEpisodePatterns(word string, t *token) bool {
	if pyDigit(word) {
		return false
	}
	word = strings.Trim(word, " -")
	runes := []rune(word)
	if len(runes) == 0 {
		sourcePanic("IndexError")
	}
	front, back := inRange(runes[0], pyDigitRanges), inRange(runes[len(runes)-1], pyDigitRanges)
	if front && back && s.matchSingleEpisode(word, t) {
		return true
	}
	if front && back && s.matchMultiEpisode(word, t) {
		return true
	}
	if back && s.matchSeasonEpisode(word, t) {
		return true
	}
	if !front && s.matchTypeEpisode(word, t) {
		return true
	}
	if front && back && s.matchFractional(word, t) {
		return true
	}
	if front && !back && s.matchPartial(word, t) {
		return true
	}
	if back && s.matchNumberSign(word, t) {
		return true
	}
	if front && s.matchJapanese(word, t) {
		return true
	}
	return false
}
func (s *state) matchVolumePatterns(word string, t *token) bool {
	if pyDigit(word) {
		return false
	}
	word = strings.Trim(word, " -")
	runes := []rune(word)
	if len(runes) == 0 {
		sourcePanic("IndexError")
	}
	front, back := inRange(runes[0], pyDigitRanges), inRange(runes[len(runes)-1], pyDigitRanges)
	if front && back {
		if m := match(`(\d{1,2})[vV](\d)$`, word, false); m != nil {
			s.setVolume(m[1], t, false)
			s.insert(ReleaseVersion, m[2])
			return true
		}
	}
	if front && back {
		if m := match(`(\d{1,2})[-~&+](\d{1,2})(?:[vV](\d))?$`, word, false); m != nil && pyInt(m[1]).Cmp(pyInt(m[2])) < 0 && s.setVolume(m[1], t, true) {
			s.setVolume(m[2], t, false)
			if m[3] != "" {
				s.insert(ReleaseVersion, m[3])
			}
			return true
		}
	}
	return false
}
func (s *state) searchEquivalent(tokens []*token) bool {
	for _, t := range tokens {
		if s.isolated(t) || !validEpisode(t.content) {
			continue
		}
		next := s.next(t, flagNotDelimiter)
		if next == nil || next.category != tokenBracket {
			continue
		}
		next = s.next(next, flagEnclosed|flagNotDelimiter)
		if next == nil || next.category != tokenUnknown {
			continue
		}
		if !s.isolated(next) || !pyDigit(next.content) || !validEpisode(next.content) {
			continue
		}
		episode, alt := t, next
		cmp := pyInt(t.content).Cmp(pyInt(next.content))
		if cmp > 0 {
			episode, alt = next, t
		} else if cmp == 0 {
			alt = t
		}
		s.setEpisode(episode.content, episode, false)
		s.setAlternative(alt.content, alt)
		return true
	}
	return false
}
func (s *state) searchSeparated(tokens []*token) bool {
	for _, t := range tokens {
		previous := s.previous(t, flagNotDelimiter)
		if tokenCat(previous) == tokenUnknown && runeLen(previous.content) == 1 && strings.Contains(dashes, previous.content) {
			if s.setEpisode(t.content, t, true) {
				previous.category = tokenIdentifier
				return true
			}
		}
	}
	return false
}
func (s *state) searchIsolatedEpisodes(tokens []*token) bool {
	for _, t := range tokens {
		if !t.enclosed || !s.isolated(t) {
			continue
		}
		if s.setEpisode(t.content, t, true) {
			return true
		}
	}
	return false
}
func asciiLowerEqual(value, target string) bool {
	if len(value) != len(target) {
		return false
	}
	for i := range value {
		c := value[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != target[i] {
			return false
		}
	}
	return true
}
func (s *state) searchLastNumber(tokens []*token) bool {
	for _, t := range tokens {
		index := s.index(t)
		if index == 0 || t.enclosed {
			continue
		}
		all := true
		for _, previous := range s.tokens[:index] {
			if !(previous.enclosed || previous.category == tokenDelimiter) {
				all = false
				break
			}
		}
		if all {
			continue
		}
		previous := s.previous(t, flagNotDelimiter)
		if tokenCat(previous) == tokenUnknown && (asciiLowerEqual(previous.content, "movie") || asciiLowerEqual(previous.content, "part")) {
			continue
		}
		if s.setEpisode(t.content, t, true) {
			return true
		}
	}
	return false
}
