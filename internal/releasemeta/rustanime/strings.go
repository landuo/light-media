// String parsing stages translated from anitomy-pure 0.1.0/src/parsing/string.rs.
// Source crate checksum: 33f525032668d2aff5dff115958157db7aecf1dc2fd5f5df93cf1be1452dfd4a.
// Copyright and license: upstream MPL-2.0; see LICENSE.md.
package rustanime

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rust's Alphabetic property also includes Other_Alphabetic marks. This differs
// from a letters-only check and matters for title and release-group boundaries.
func stringAlphabetic(c rune) bool {
	return sourceAlphabetic(c)
}

func stringAlphanumeric(c rune) bool { return sourceAlphabetic(c) || sourceNumeric(c) }

func stringUppercase(c rune) bool {
	return sourceUppercase(c)
}

func stringAny(s string, predicate func(rune) bool) bool {
	for _, c := range s {
		if predicate(c) {
			return true
		}
	}
	return false
}

func stringAll(s string, predicate func(rune) bool) bool {
	for _, c := range s {
		if !predicate(c) {
			return false
		}
	}
	return true
}

func stringASCIIDigits(s string) bool {
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseAnimeTitle extracts the first outside unknown span, with the upstream
// corner-bracket pre-pass and two-bracket fallback kept in their original order.
func parseAnimeTitle(tokens []*token, found *elements, d string) {
	for i, tok := range tokens {
		if tok.corner && tok.inside && tok.containsUnknown() {
			title := "「" + tok.raw + "」"
			for _, st := range tok.subtokens {
				st.found = true
			}
			for _, after := range tokens[i+1:] {
				if !after.inside {
					for _, st := range after.subtokens {
						st.found = true
					}
				}
			}
			found.push(AnimeTitle, title)
			return
		}
	}

	for i, tok := range tokens {
		if !tok.containsUnknown() || tok.inside {
			continue
		}
		if !stringAny(tok.raw, stringAlphanumeric) {
			continue
		}
		var digitUnknowns []*subtoken
		for _, st := range tok.subtokens {
			if !st.found && st.value != "" {
				digitUnknowns = append(digitUnknowns, st)
			}
		}
		if len(digitUnknowns) == 1 && stringASCIIDigits(digitUnknowns[0].value) {
			for _, st := range tok.subtokens {
				if !st.found && st.value != "" {
					st.found = true
				}
			}
			continue
		}
		if !found.empty(EpisodeNumber) {
			firstUnknown := -1
			for j, st := range tok.subtokens {
				if !st.found {
					firstUnknown = j
					break
				}
			}
			if firstUnknown > 0 {
				continue
			}
		}
		found.push(AnimeTitle, findAnimeTitle(tokens, i, d, found))
		return
	}

	first, second := -1, -1
	for i, tok := range tokens {
		if tok.containsUnknown() && tok.inside {
			if first < 0 {
				first = i
			} else {
				second = i
				break
			}
		}
	}
	if first >= 0 && second >= 0 {
		firstCount, secondCount := 0, 0
		for _, st := range tokens[first].subtokens {
			if !st.found {
				firstCount++
			}
		}
		for _, st := range tokens[second].subtokens {
			if !st.found {
				secondCount++
			}
		}
		chosen := second
		if firstCount > secondCount {
			chosen = first
		}
		found.push(AnimeTitle, findAnimeTitle(tokens, chosen, d, found))
	}
}

func findAnimeTitle(tokens []*token, startingIndex int, d string, found *elements) string {
	tokenIndex := startingIndex
	animeTitle := ""
	tok := tokens[tokenIndex]
	unknownStart := 0
	for tok.subtokens[unknownStart].found {
		unknownStart++
	}
	for i := unknownStart; i < len(tok.subtokens); {
		st := tok.subtokens[i]
		if st.found {
			return strings.Trim(animeTitle, d)
		}
		current := st.value
		nextFound := i+1 < len(tok.subtokens) && tok.subtokens[i+1].found
		if nextFound && stringAll(current, func(c rune) bool { return !stringAlphanumeric(c) }) {
			st.found = true
			i++
			continue
		}

		// These lengths are bytes in Rust (&str::len), not rune counts.
		if strings.ContainsRune(d, '.') && animeTitle != "" && len(current) <= 2 {
			lastSpaceWord := strings.TrimLeftFunc(animeTitle, unicode.IsSpace)
			lastSpaceWord = lastSpaceWord[strings.LastIndexByte(lastSpaceWord, ' ')+1:]
			previousSegment := lastSpaceWord[strings.LastIndexByte(lastSpaceWord, '.')+1:]
			if len(previousSegment) <= 2 && previousSegment != "" && strings.Contains(tok.raw, lastSpaceWord+"."+current) {
				animeTitle += "." + current
				st.found = true
				i++
				continue
			}
		}
		if strings.ContainsRune(d, '-') && animeTitle != "" {
			lastWord := strings.TrimLeftFunc(animeTitle, unicode.IsSpace)
			lastWord = lastWord[strings.LastIndexByte(lastWord, ' ')+1:]
			if lastWord != "" {
				bothSingleDigits := len(lastWord) == 1 && len(current) == 1 && stringASCIIDigits(lastWord) && stringASCIIDigits(current)
				firstLast, _ := utf8.DecodeRuneInString(lastWord)
				firstCurrent, _ := utf8.DecodeRuneInString(current)
				bothAlphaWords := len(lastWord) >= 2 && len(current) >= 2 && stringAll(lastWord, stringAlphabetic) && stringAll(current, stringAlphabetic) && stringUppercase(firstLast) && stringUppercase(firstCurrent) && nextFound
				if (bothSingleDigits || bothAlphaWords) && strings.Contains(tok.raw, lastWord+"-"+current) {
					animeTitle += "-" + current
					st.found = true
					i++
					continue
				}
			}
		}

		if current != "" {
			alphaLen := 0
			for alphaLen < len(current) && ((current[alphaLen] >= 'A' && current[alphaLen] <= 'Z') || (current[alphaLen] >= 'a' && current[alphaLen] <= 'z')) {
				alphaLen++
			}
			if alphaLen > 0 && alphaLen < len(current) {
				prefix, suffix := current[:alphaLen], current[alphaLen:]
				if kw := keywords.find(prefix); kw != nil {
					if (kw.category == EpisodePrefix && !kw.valid) || (kw.category == AnimeType && kw.valid && kw.searchable) {
						hasDigit, cleanSuffix := false, suffix != ""
						for j := 0; j < len(suffix); j++ {
							if suffix[j] >= '0' && suffix[j] <= '9' {
								hasDigit = true
							} else if suffix[j] != '.' {
								cleanSuffix = false
							}
						}
						if cleanSuffix && hasDigit && !found.empty(EpisodeNumber) {
							ep := found.find(EpisodeNumber)
							first := ep.Value
							if dot := strings.IndexByte(first, '.'); dot >= 0 {
								first = first[:dot]
							}
							if ep.Value == suffix || first == suffix {
								animeTitle += " " + prefix
								st.found = true
								i++
								continue
							}
						}
					} else {
						st.found = true
						return strings.Trim(animeTitle, d)
					}
				}
			}
		}
		animeTitle += " " + current
		st.found = true
		i++
	}

	for tokenIndex++; tokenIndex < len(tokens); tokenIndex++ {
		tok = tokens[tokenIndex]
		embeddedBracket := tok.inside && !tok.weak && tokenIndex+1 < len(tokens) && !tokens[tokenIndex+1].inside && tokens[tokenIndex+1].containsUnknown()
		if !tok.containsUnknown() || (tok.inside && !tok.weak && !embeddedBracket) {
			return strings.Trim(animeTitle, d)
		}
		last := ' '
		if previousRaw := tokens[tokenIndex-1].raw; previousRaw != "" {
			last, _ = utf8.DecodeLastRuneInString(previousRaw)
		}
		startsWithDash := strings.ContainsRune(d, '-') && strings.HasPrefix(strings.TrimLeft(tok.raw, " "), "- ")
		if tok.subtokens[0].found {
			return strings.Trim(animeTitle, d)
		}
		if tok.weak {
			if strings.ContainsRune(d, last) {
				animeTitle += " ("
			} else {
				animeTitle += "("
			}
			animeTitle += tok.subtokens[0].value
		} else if embeddedBracket {
			if strings.ContainsRune(d, last) {
				animeTitle += " ["
			} else {
				animeTitle += "["
			}
			animeTitle += tok.subtokens[0].value
		} else if startsWithDash {
			animeTitle += " - " + tok.subtokens[0].value
		} else {
			animeTitle += " " + tok.subtokens[0].value
		}
		tok.subtokens[0].found = true
		for _, st := range tok.subtokens[1:] {
			if st.found {
				return strings.Trim(animeTitle, d)
			}
			animeTitle += " " + st.value
			st.found = true
		}
		if tok.weak {
			animeTitle += ")"
		} else if embeddedBracket {
			animeTitle += "]"
		}
	}
	return strings.Trim(animeTitle, d)
}

func parseReleaseGroup(tokens []*token, found *elements, d string) {
	if !found.empty(ReleaseGroup) {
		return
	}
	groupIndex := -1
	groupHasBefore, groupHasRawAlphaBefore := false, false
	for i, tok := range tokens {
		if !tok.inside || tok.weak || tok.corner || (!tok.fullUnknown() && !tok.containsUnknown()) {
			continue
		}
		hasBefore, hasRawAlphabeticBefore := false, false
		for _, before := range tokens[:i] {
			if !before.inside && stringAny(before.raw, stringAlphabetic) {
				hasRawAlphabeticBefore = true
				if before.containsUnknown() {
					hasBefore = true
				}
			}
		}
		hasAfter := false
		for _, after := range tokens[i+1:] {
			if !after.inside && after.containsUnknown() {
				hasAfter = true
				break
			}
		}
		if hasBefore && hasAfter {
			continue
		}
		groupIndex, groupHasBefore, groupHasRawAlphaBefore = i, hasBefore, hasRawAlphabeticBefore
		break
	}
	if groupIndex >= 0 {
		tok := tokens[groupIndex]
		if tok.fullUnknown() {
			byPosition := -1
			for i, st := range tok.subtokens {
				if upper(st.value) == "BY" {
					byPosition = i
					break
				}
			}
			if byPosition > 0 && byPosition+1 < len(tok.subtokens) {
				values := make([]string, len(tok.subtokens)-byPosition-1)
				for i, st := range tok.subtokens[byPosition+1:] {
					values[i] = st.value
				}
				for _, st := range tok.subtokens {
					st.found = true
				}
				found.push(ReleaseGroup, strings.Join(values, " "))
				return
			}
			for _, st := range tok.subtokens {
				st.found = true
			}
			found.push(ReleaseGroup, tok.raw)
		} else if (!groupHasRawAlphaBefore || tok.paren) && (tok.paren || !groupHasBefore) {
			parseParticularStringSubtoken(tok, found, ReleaseGroup, d)
		}
	}

	if found.empty(ReleaseGroup) {
		for _, tok := range tokens {
			if tok.inside || tok.weak || tok.corner {
				continue
			}
			candidate := -1
			for i := len(tok.subtokens) - 1; i >= 0; i-- {
				st := tok.subtokens[i]
				if !st.found && len(st.value) >= 2 && stringAll(st.value, stringAlphabetic) {
					candidate = i
					break
				}
			}
			if candidate <= 0 || !tok.subtokens[candidate-1].found {
				continue
			}
			allAfterFound := true
			for _, st := range tok.subtokens[candidate+1:] {
				if !st.found && st.value != "" {
					allAfterFound = false
					break
				}
			}
			if !allAfterFound {
				continue
			}
			group := tok.subtokens[candidate].value
			if !strings.Contains(tok.raw, "-"+group) {
				continue
			}
			tok.subtokens[candidate].found = true
			found.push(ReleaseGroup, group)
			break
		}
	}
}

func parseEpisodeTitle(tokens []*token, found *elements, d string) {
	for _, tok := range tokens {
		if tok.containsUnknown() && !tok.inside {
			if !stringAny(tok.raw, stringAlphanumeric) {
				continue
			}
			parseParticularStringSubtoken(tok, found, EpisodeTitle, d)
			return
		}
	}
}

func parseParticularStringSubtoken(tok *token, found *elements, cat Category, d string) {
	i := 0
	for tok.subtokens[i].found {
		i++
	}
	value := ""
	if cat == EpisodeTitle {
		for i < len(tok.subtokens) && !tok.subtokens[i].found && tok.subtokens[i].value != "" && !stringAny(tok.subtokens[i].value, stringAlphanumeric) {
			tok.subtokens[i].found = true
			i++
		}
	}
	for i < len(tok.subtokens) {
		st := tok.subtokens[i]
		if st.found {
			if trimmed := strings.Trim(value, d); trimmed != "" {
				found.push(cat, trimmed)
			}
			value = ""
			break
		}
		value += " " + st.value
		st.found = true
		i++
	}
	if value != "" {
		found.push(cat, strings.Trim(value, d))
	}
}

func parseMultipleKeyword(found *elements, manager keywordManager, left, right string) bool {
	for _, sep := range []string{".", " ", "-", "_"} {
		trimmed := strings.TrimSpace(left + sep + right)
		if kw := manager.find(trimmed); kw != nil && (found.empty(kw.category) || !kw.category.singular()) && kw.category.searchable() {
			found.push(kw.category, trimmed)
			return true
		}
	}
	return false
}
