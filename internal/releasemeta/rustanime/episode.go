package rustanime

// Complete episode-number passes translated from anitomy-pure 0.1.0
// src/parsing/episode.rs (MPL-2.0), preserving insertion and token mutation order.

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

func parseEpWithVersion(value string) (ep, version string, ok bool) {
	n := len(value)
	if n >= 2 && (value[n-2] == 'v' || value[n-2] == 'V') && value[n-1] >= '0' && value[n-1] <= '9' {
		digits := value[:n-2]
		if len(digits) > 0 && len(digits) <= 4 && allDigits(digits) {
			return digits, value[n-1:], true
		}
	}
	if n <= 4 && allDigits(value) {
		return value, "", true
	}
	return "", "", false
}
func isFractal(value string) bool {
	if at := strings.IndexByte(value, '.'); at >= 0 {
		return allDigits(value[:at]) && allDigits(value[at+1:])
	}
	return false
}
func parseSingleEP(value string, found *elements) bool {
	n := len(value)
	if n < 3 || n > 6 || value[n-1] < '0' || value[n-1] > '9' || (value[n-2] != 'v' && value[n-2] != 'V') {
		return false
	}
	ep := value[:n-2]
	if len(ep) == 0 || len(ep) > 4 || !allDigits(ep) {
		return false
	}
	found.push(EpisodeNumber, ep)
	found.push(ReleaseVersion, value[n-1:])
	return true
}
func matchMultipleEP(value string, found *elements) bool {
	at := strings.IndexAny(value, "-~&+")
	if at < 0 {
		return false
	}
	ep1, ver1, ok1 := parseEpWithVersion(value[:at])
	ep2, ver2, ok2 := parseEpWithVersion(value[at+1:])
	if !ok1 || !ok2 {
		return false
	}
	n1, _ := strconv.ParseUint(ep1, 10, 32)
	n2, _ := strconv.ParseUint(ep2, 10, 32)
	if n1 >= n2 {
		return false
	}
	found.push(EpisodeNumber, ep1)
	found.push(EpisodeNumber, ep2)
	if ver1 != "" {
		found.push(ReleaseVersion, ver1)
	}
	if ver2 != "" {
		found.push(ReleaseVersion, ver2)
	}
	return true
}
func matchSeasonEP(value string, found *elements) bool {
	n, pos := len(value), 0
	if pos < n && (value[pos] == 'S' || value[pos] == 's') {
		pos++
	}
	start := pos
	for pos < n && asciiDigit(value[pos]) && pos-start < 2 {
		pos++
	}
	if pos == start {
		return false
	}
	s1 := value[start:pos]
	if parseI32Default(s1, 0) == 0 {
		return false
	}
	s2 := ""
	if pos < n && value[pos] == '-' {
		save := pos
		pos++
		if pos < n && (value[pos] == 'S' || value[pos] == 's') {
			pos++
		}
		start = pos
		for pos < n && asciiDigit(value[pos]) && pos-start < 2 {
			pos++
		}
		if pos == start {
			pos = save
		} else {
			s2 = value[start:pos]
		}
	}
	if pos >= n {
		return false
	}
	if value[pos] == 'x' || value[pos] == 'X' {
		pos++
	} else {
		if strings.ContainsRune(" ._-", rune(value[pos])) {
			pos++
		}
		if pos >= n || (value[pos] != 'E' && value[pos] != 'e') {
			return false
		}
		pos++
	}
	start = pos
	for pos < n && asciiDigit(value[pos]) && pos-start < 4 {
		pos++
	}
	if pos == start {
		return false
	}
	ep1 := value[start:pos]
	ep2 := ""
	if pos < n && value[pos] == '-' {
		save := pos
		pos++
		if pos < n && (value[pos] == 'E' || value[pos] == 'e') {
			pos++
		}
		start = pos
		for pos < n && asciiDigit(value[pos]) && pos-start < 4 {
			pos++
		}
		if pos == start {
			pos = save
		} else {
			ep2 = value[start:pos]
		}
	}
	version := ""
	if pos+1 < n && (value[pos] == 'v' || value[pos] == 'V') && asciiDigit(value[pos+1]) {
		pos += 2
		version = value[pos-1 : pos]
	}
	if pos != n {
		return false
	}
	found.push(AnimeSeason, s1)
	if s2 != "" {
		found.push(AnimeSeason, s2)
	}
	found.push(EpisodeNumber, ep1)
	if ep2 != "" {
		found.push(EpisodeNumber, ep2)
	}
	if version != "" {
		found.push(ReleaseVersion, version)
	}
	return true
}
func matchFractalEpisode(value string, found *elements) bool {
	if isFractal(value) {
		found.push(EpisodeNumber, value)
		return true
	}
	return false
}
func matchJapaneseCounter(value string, found *elements) bool {
	if !strings.HasSuffix(value, "話") {
		return false
	}
	num := strings.TrimSuffix(value, "話")
	if allDigits(num) {
		found.push(EpisodeNumber, num)
		return true
	}
	return false
}
func matchNumberSign(value string, found *elements) bool {
	if !strings.HasPrefix(value, "#") {
		return false
	}
	rest := value[1:]
	pos := 0
	for pos < len(rest) && asciiDigit(rest[pos]) && pos < 4 {
		pos++
	}
	if pos == 0 {
		return false
	}
	ep1 := rest[:pos]
	ep2 := ""
	if pos < len(rest) && strings.ContainsRune("-~&+", rune(rest[pos])) {
		pos++
		start := pos
		for pos < len(rest) && asciiDigit(rest[pos]) && pos-start < 4 {
			pos++
		}
		if pos == start {
			return false
		}
		ep2 = rest[start:pos]
	}
	version := ""
	if pos+1 < len(rest) && (rest[pos] == 'v' || rest[pos] == 'V') && asciiDigit(rest[pos+1]) {
		pos += 2
		version = rest[pos-1 : pos]
	}
	if pos != len(rest) {
		return false
	}
	found.push(EpisodeNumber, ep1)
	if ep2 != "" {
		found.push(EpisodeNumber, ep2)
	}
	if version != "" {
		found.push(ReleaseVersion, version)
	}
	return true
}
func matchPartialEpisode(value string, found *elements) bool {
	if len(value) == 0 {
		return false
	}
	last := value[len(value)-1]
	if last >= 'a' && last <= 'z' {
		last -= 32
	}
	if last != 'A' && last != 'B' && last != 'C' {
		return false
	}
	prefix := value[:len(value)-1]
	if !allDigits(prefix) {
		return false
	}
	found.push(EpisodeNumber, value)
	return true
}
func parseSingleSubtoken(d, value string, found *elements) bool {
	if matchNumberSign(value, found) || matchMultipleEP(value, found) || matchSeasonEP(value, found) || matchTypeEpisode(value, found, d) || parseSingleEP(value, found) || matchFractalEpisode(value, found) || matchPartialEpisode(value, found) || matchJapaneseCounter(value, found) {
		return true
	}
	return false
}
func matchTypeEpisode(value string, found *elements, d string) bool {
	potential, data := splitTypeAndEpisode(value)
	key := strings.Trim(potential, d)
	if k := keywords.find(key); k != nil {
		cat := k.category
		suppress := cat == EpisodePrefix && (!k.valid || isFractal(data))
		if !suppress {
			found.push(cat, key)
		}
		if isDigit(data) {
			if cat == AnimeSeasonPrefix {
				found.push(AnimeSeason, data)
			}
			if cat == EpisodePrefix || cat == AnimeType {
				found.push(EpisodeNumber, data)
			}
			if cat == VolumePrefix {
				found.push(VolumeNumber, data)
			}
			return true
		}
		parseSingleSubtoken(d, data, found)
		return true
	}
	return false
}
func asciiDigit(value byte) bool { return value >= '0' && value <= '9' }
func asciiAlphabetic(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
func alphabeticRune(value rune) bool {
	return sourceAlphabetic(value)
}

func parseEpisodeNumber(d string, tokens []*token, found *elements) {
	for _, t := range tokens {
		if !t.containsUnknown() {
			continue
		}
		subs := t.subtokens
		for index := 0; index < len(subs); index++ {
			if subs[index].found {
				continue
			}
			value := subs[index].value
			if value == "" {
				subs[index].found = true
				continue
			}
			if isDigit(value) || !containsDigit(value) {
				continue
			}
			toParse, skipNext := value, false
			if index+1 < len(subs) && isDigit(subs[index+1].value) && !subs[index+1].found {
				if combined := value + "." + subs[index+1].value; strings.Contains(t.raw, combined) {
					toParse, skipNext = combined, true
				} else if combined := value + "-" + subs[index+1].value; strings.Contains(t.raw, combined) {
					toParse, skipNext = combined, true
				}
			}
			before := found.count(EpisodePrefix)
			if parseSingleSubtoken(d, toParse, found) {
				startsAlpha := len(toParse) > 0 && asciiAlphabetic(toParse[0])
				prefixAdded := found.count(EpisodePrefix) > before
				if !startsAlpha || prefixAdded {
					subs[index].found = true
				}
				if skipNext {
					subs[index+1].found = true
				}
				if !skipNext && strings.HasPrefix(toParse, "#") {
					n1 := parseI32Default(toParse[1:], -1)
					ep2 := ""
					ep2index, dashIndex := 0, -1
					if index+1 < len(subs) {
						next := subs[index+1]
						if isDigit(next.value) && !next.found {
							if strings.Contains(t.raw, toParse+"-"+next.value) {
								ep2, ep2index = next.value, index+1
							}
						} else if next.value == "-" && !next.found && index+2 < len(subs) {
							after := subs[index+2]
							if isDigit(after.value) && !after.found && strings.Contains(t.raw, toParse+"-"+after.value) {
								ep2, ep2index, dashIndex = after.value, index+2, index+1
							}
						}
					}
					if ep2 != "" && n1 >= 0 && parseI32Default(ep2, -1) > n1 {
						found.push(EpisodeNumber, ep2)
						subs[ep2index].found = true
						if dashIndex >= 0 {
							subs[dashIndex].found = true
						}
					}
				}
			}
		}
	}
	if found.empty(EpisodeNumber) {
		for _, t := range tokens {
			if !t.containsUnknown() {
				continue
			}
			subs := t.subtokens
			for index, st := range subs {
				value := st.value
				if !isDigit(value) {
					continue
				}
				if index+1 < len(subs) && isDigit(subs[index+1].value) {
					next := subs[index+1]
					right, left := parseI32Unwrap(next.value), parseI32Unwrap(value)
					fractal := value + "." + next.value
					if strings.Contains(t.raw, fractal) {
						textBefore, textAfter := false, false
						for _, item := range subs[:index] {
							textBefore = textBefore || !item.found && !isDigit(item.value)
						}
						for _, item := range subs[index+2:] {
							textAfter = textAfter || !item.found && !isDigit(item.value)
						}
						preceded := index > 0 && !subs[index-1].found && subs[index-1].value == "-"
						if !textBefore || !textAfter || preceded {
							subs[index].found = true
							subs[index+1].found = true
							found.push(EpisodeNumber, fractal)
							return
						}
						continue
					}
					if left < right {
						subs[index].found = true
						subs[index+1].found = true
						found.push(EpisodeNumber, value)
						found.push(EpisodeNumber, next.value)
						return
					}
				}
				if index+2 < len(subs) && isDigit(subs[index+2].value) {
					middle := subs[index+1].value
					right, left := parseI32Unwrap(subs[index+2].value), parseI32Unwrap(value)
					first, width := utf8.DecodeRuneInString(middle)
					if width == 0 {
						panic(ErrSourcePanic)
					}
					if middle == "of" && left < right {
						subs[index].found = true
						subs[index+1].found = true
						subs[index+2].found = true
						found.push(EpisodeNumber, value)
						return
					}
					if strings.ContainsRune(d, first) && len(middle) == 1 && left < right {
						subs[index+1].found = true
						subs[index+2].found = true
						found.push(EpisodeNumber, subs[index+2].value)
						return
					}
				}
			}
		}
	}
	if found.empty(EpisodeNumber) {
		for _, t := range tokens {
			if !t.containsUnknown() {
				continue
			}
			subs := t.subtokens
			for index := 0; index < len(subs); {
				st := subs[index]
				index++
				if st.found || !isDigit(st.value) {
					continue
				}
				if index < len(subs) && isDigit(subs[index].value) && !subs[index].found && strings.Contains(t.raw, st.value+"."+subs[index].value) {
					index++
					continue
				}
				if !t.inside {
					pos := index - 1
					preceded := pos > 0 && !subs[pos-1].found && subs[pos-1].value == "-"
					if !preceded {
						followed := false
						if index < len(subs) && !subs[index].found && subs[index].value == "-" {
							for _, item := range subs[index+1:] {
								followed = followed || !item.found && !isDigit(item.value)
							}
						}
						if !followed {
							before, after, count := false, false, 0
							for _, item := range subs[:pos] {
								before = before || !item.found && !isDigit(item.value)
							}
							for _, item := range subs[index:] {
								after = after || !item.found && !isDigit(item.value)
								if item.found {
									count++
								}
							}
							if before && after && (count < 2 || isAnimeYear(st.value)) {
								continue
							}
							if pos == 0 && after {
								continue
							}
						}
					}
				}
				if t.inside && index-1 > 0 {
					prev := subs[index-2]
					if !prev.found {
						last, _ := utf8.DecodeLastRuneInString(prev.value)
						if prev.value != "" && alphabeticRune(last) {
							continue
						}
					}
				}
				if len(st.value) == 1 && index >= 2 {
					prev := subs[index-2]
					if !isDigit(prev.value) && !prev.found && strings.Contains(t.raw, prev.value+"."+st.value) {
						continue
					}
				}
				st.found = true
				found.push(EpisodeNumber, st.value)
				return
			}
		}
	}
}

func detectEpisodeNumberAlt(tokens []*token, found *elements) {
	count := found.count(EpisodeNumber)
	if count == 2 {
		eps := found.all(EpisodeNumber)
		v1, v2 := eps[0].Value, eps[1].Value
		n1, n2 := parseI32Default(v1, -1), parseI32Default(v2, -1)
		if n1 >= 0 && n2 >= 0 && n1 == n2 {
			found.removeFirst(EpisodeNumber)
			found.removeFirst(EpisodeNumber)
			if len(v1) <= len(v2) {
				found.push(EpisodeNumber, v1)
			} else {
				found.push(EpisodeNumber, v2)
			}
			found.removeFirst(EpisodePrefix)
		} else if n1 >= 0 && n2 >= 0 && !found.empty(AnimeSeason) {
			small, large := v1, v2
			if n1 >= n2 {
				small, large = v2, v1
			}
			found.removeFirst(EpisodeNumber)
			found.removeFirst(EpisodeNumber)
			found.push(EpisodeNumber, small)
			found.push(EpisodeNumberAlt, large)
			found.removeFirst(EpisodePrefix)
		}
		return
	}
	if count != 1 {
		return
	}
	for _, t := range tokens {
		if !t.inside || !t.paren || !t.containsUnknown() || len(t.subtokens) != 1 || t.subtokens[0].found {
			continue
		}
		st := t.subtokens[0]
		if !isDigit(st.value) {
			continue
		}
		number := parseI32Default(st.value, 0)
		if number == 0 {
			continue
		}
		if ep := found.find(EpisodeNumber); ep != nil {
			n := parseI32Default(ep.Value, 0)
			if number == n {
				continue
			}
			if number < n {
				if len(st.value) != len(ep.Value) {
					continue
				}
				old := found.removeFirst(EpisodeNumber)
				found.push(EpisodeNumber, st.value)
				found.push(EpisodeNumberAlt, *old)
				st.found = true
			} else {
				found.push(EpisodeNumberAlt, st.value)
				st.found = true
			}
		}
	}
}
