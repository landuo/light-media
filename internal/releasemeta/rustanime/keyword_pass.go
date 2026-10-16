package rustanime

// Complete keyword-state pass translated from anitomy-pure 0.1.0
// src/parsing/mod.rs. Upstream MPL-2.0.

import "strings"

func parseKeywords(found *elements, tokens []*token) {
	for _, t := range tokens {
		parseSingleToken(found, t, keywords)
		if t.containsUnknown() && t.isolatedNumber() {
			value := t.subtokens[0].value
			if isAnimeYear(value) {
				found.push(AnimeYear, value)
				t.subtokens[0].found = true
			}
			if (value == "480" || value == "720" || value == "1080") && found.empty(VideoResolution) {
				found.push(VideoResolution, value)
				t.subtokens[0].found = true
			}
		}
	}
}
func parseSingleToken(found *elements, t *token, manager keywordManager) {
	subs := t.subtokens
	for index := 0; index < len(subs); index++ {
		value := subs[index].value
		if value == "" || subs[index].found {
			continue
		}
		if isDigit(value) && len(value) != 8 {
			if index+1 < len(subs) {
				left, right := subs[index].value, subs[index+1].value
				if left == "5" && right == "1" {
					subs[index].found = true
					subs[index+1].found = true
					found.push(AudioTerm, "5.1")
				} else if parseMultipleKeyword(found, manager, left, right) {
					subs[index].found = true
					subs[index+1].found = true
				} else if t.inside && isDigit(right) && strings.Contains(t.raw, left+"."+right) {
					precedingNumeric := index == 0 || subs[index-1].found || isDigit(subs[index-1].value)
					if precedingNumeric {
						subs[index].found = true
						subs[index+1].found = true
					}
				}
			}
			continue
		}
		if isCRC32(value) && found.empty(FileChecksum) {
			found.push(FileChecksum, value)
			subs[index].found = true
			continue
		}
		if isResolution(value) {
			found.push(VideoResolution, value)
			subs[index].found = true
			continue
		}
		if k := manager.find(value); k != nil {
			c := k.category
			if !c.searchable() || c.singular() && !found.empty(c) || found.contains(c, value) {
				continue
			}
			if c == Language && !k.identifiable && !t.inside {
				continue
			}
			if upper(value) == "E" && index+2 < len(subs) {
				combined := value + "-" + subs[index+1].value + "-" + subs[index+2].value
				if upper(combined) == "E-AC-3" {
					subs[index].found = true
					subs[index+1].found = true
					subs[index+2].found = true
					found.push(AudioTerm, combined)
					continue
				}
			}
			if upper(value) == "DTS" && index+1 < len(subs) {
				combined := value + "-" + subs[index+1].value
				if upper(combined) == "DTS-ES" {
					subs[index].found = true
					subs[index+1].found = true
					found.push(AudioTerm, combined)
					continue
				}
			}
			if c == AnimeSeasonPrefix {
				found.push(c, value)
				subs[index].found = true
				if index > 0 {
					if ordinal := ordinalsToNumber(subs[index-1].value); ordinal != "" {
						found.push(AnimeSeason, ordinal)
						subs[index-1].found = true
						continue
					}
				}
				if index+1 < len(subs) && isDigit(subs[index+1].value) {
					found.push(AnimeSeason, subs[index+1].value)
					subs[index+1].found = true
				}
				continue
			}
			if c == EpisodePrefix {
				if index+1 < len(subs) && isDigit(subs[index+1].value) {
					next := subs[index+1].value
					left := parseI32Default(next, 0)
					found.push(EpisodeNumber, next)
					subs[index+1].found = true
					if index+2 < len(subs) && isDigit(subs[index+2].value) && left < parseI32Default(subs[index+2].value, 0) {
						found.push(EpisodeNumber, subs[index+2].value)
						subs[index+2].found = true
					}
					if k.valid {
						found.push(c, value)
					}
					subs[index].found = true
					continue
				}
				if k.valid {
					found.push(c, value)
					subs[index].found = true
				}
				continue
			}
			if c == ReleaseVersion {
				found.push(ReleaseVersion, strings.ReplaceAll(lower(value), "v", ""))
				subs[index].found = true
				continue
			}
			if c == VolumePrefix {
				if index+1 < len(subs) {
					next := subs[index+1].value
					if isDigit(next) {
						if !k.identifiable {
							found.push(c, value)
						}
						found.push(VolumeNumber, next)
						subs[index+1].found = true
						subs[index].found = true
						continue
					}
					leading := 0
					for leading < len(next) && next[leading] >= '0' && next[leading] <= '9' {
						leading++
					}
					if leading > 0 {
						rest := next[leading:]
						if len(rest) > 0 && (rest[0] == 'v' || rest[0] == 'V') && isDigit(rest[1:]) {
							if !k.identifiable {
								found.push(c, value)
							}
							found.push(VolumeNumber, next[:leading])
							found.push(ReleaseVersion, rest[1:])
							subs[index+1].found = true
							subs[index].found = true
							continue
						}
					}
				}
				if !k.identifiable {
					found.push(AnimeType, value)
				} else {
					found.push(c, value)
					subs[index].found = true
				}
				continue
			}
			if c == AnimeType {
				if t.inside {
					unknown := false
					for i, st := range subs {
						if i != index && !st.found && !isResolution(st.value) && !isDigit(st.value) && !isCRC32(st.value) && manager.find(st.value) == nil {
							unknown = true
							break
						}
					}
					if unknown {
						continue
					}
				}
				found.push(c, value)
				if !k.searchable {
					subs[index].found = true
				}
				continue
			}
			if c == Language {
				if t.inside && t.paren {
					unknown := false
					for i, st := range subs {
						if i != index && !st.found && manager.find(st.value) == nil {
							unknown = true
							break
						}
					}
					if unknown {
						continue
					}
				}
				languageFound := false
				for _, item := range found.all(Language) {
					if upper(item.Value) == upper(value) {
						languageFound = true
						break
					}
				}
				if languageFound {
					continue
				}
				found.push(c, value)
				subs[index].found = true
				continue
			}
			if c == ReleaseGroup && t.inside {
				other := false
				for i, st := range subs {
					if i != index && !st.found {
						other = true
						break
					}
				}
				if other {
					continue
				}
			}
			if c == Subtitles && !t.inside && found.empty(Language) {
				subs[index].found = true
				continue
			}
			if c != Unknown {
				if c == ReleaseInformation && !k.identifiable {
					prior := false
					for _, st := range subs[:index] {
						prior = prior || st.found
					}
					if prior {
						subs[index].found = true
						continue
					}
				}
				if c == ReleaseInformation && t.inside && !t.paren && index > 0 && !subs[0].found {
					continue
				}
				found.push(c, value)
				subs[index].found = true
				continue
			}
		}
		if index+1 < len(subs) && parseMultipleKeyword(found, manager, subs[index].value, subs[index+1].value) {
			subs[index].found = true
			subs[index+1].found = true
		}
	}
}
