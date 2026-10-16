package legacyanime

import (
	"strings"
	"unicode/utf8"
)

// Parse runs the complete installed Anitopy single-title sequence in isolated
// per-call state. The result preserves insertion-ordered category values and Python list flattening.
func Parse(filename string, options Options) (result Result, err error) {
	if !utf8.ValidString(filename) {
		return Result{}, SourceError{"UnicodeError"}
	}
	s, working := initialize(filename, options)
	defer func() {
		if value := recover(); value != nil {
			if source, ok := value.(SourceError); ok {
				err = source
				result.Dictionary = nil
			} else {
				panic(value)
			}
		}
		result.Stages = s.stages
		result.FinalState = s.snapshot("final")
	}()
	if working == "" {
		return result, nil
	}
	tokenized := false
	s.phase("tokenize", func() { tokenized = s.tokenize(working) })
	if !tokenized {
		return result, nil
	}
	s.phase("search_for_keywords", s.searchKeywords)
	s.phase("search_for_isolated_numbers", s.searchIsolatedNumbers)
	if enabled(options.EpisodeNumber) {
		s.phase("search_for_episode_number", s.searchEpisodeNumber)
	}
	s.phase("search_for_anime_title", s.searchAnimeTitle)
	if enabled(options.ReleaseGroup) && !s.contains(ReleaseGroup) {
		s.phase("search_for_release_group", s.searchReleaseGroup)
	}
	if enabled(options.EpisodeTitle) && s.contains(EpisodeNumber) {
		s.phase("search_for_episode_title", s.searchEpisodeTitle)
	}
	s.phase("validate_elements", s.validateElements)
	if len(s.elements) > 0 {
		result.Dictionary = s.dictionary()
	}
	return result, nil
}
func (s *state) phase(name string, fn func()) {
	if s.options.Trace {
		defer func() { s.stages = append(s.stages, s.snapshot(name)) }()
	}
	fn()
}
func (s *state) searchKeywords() {
	for _, t := range s.list(flagUnknown, nil, nil) {
		word := strings.Trim(t.content, " -")
		if word == "" {
			continue
		}
		if runeLen(word) != 8 && pyDigit(word) {
			continue
		}
		category := Unknown
		k := findKeyword(normalize(word), Unknown)
		if k != nil {
			category = k.category
			if !enabled(s.options.ReleaseGroup) && category == ReleaseGroup {
				continue
			}
			if !category.searchable() || !k.searchable {
				continue
			}
			if category.singular() && s.contains(category) {
				continue
			}
			switch category {
			case AnimeSeasonPrefix:
				s.checkAnimeSeason(t)
				continue
			case EpisodePrefix:
				if k.valid {
					s.checkExtent(EpisodeNumber, t)
				}
				continue
			case ReleaseVersion:
				word = runeSlice(word, 1, runeLen(word))
			case VolumePrefix:
				s.checkExtent(VolumeNumber, t)
				continue
			}
		} else {
			if !s.contains(FileChecksum) && isCRC32(word) {
				category = FileChecksum
			} else if !s.contains(VideoResolution) && isResolution(word) {
				category = VideoResolution
			}
		}
		if category != Unknown {
			s.insert(category, word)
			if k == nil || k.identifiable {
				t.category = tokenIdentifier
			}
		}
	}
}
func (s *state) searchIsolatedNumbers() {
	for _, t := range s.list(flagUnknown, nil, nil) {
		if !pyDigit(t.content) || !s.isolated(t) {
			continue
		}
		number := pyInt(t.content)
		if number.IsInt64() {
			n := number.Int64()
			if n >= 1900 && n <= 2050 && !s.contains(AnimeYear) {
				s.insert(AnimeYear, t.content)
				t.category = tokenIdentifier
				continue
			}
			if (n == 480 || n == 720 || n == 1080) && !s.contains(VideoResolution) {
				s.insert(VideoResolution, t.content)
				t.category = tokenIdentifier
				continue
			}
		}
	}
}
func (s *state) searchEpisodeNumber() {
	var tokens []*token
	for _, t := range s.list(flagUnknown, nil, nil) {
		if numberIndex(t.content) >= 0 {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		return
	}
	s.checkAlt = s.contains(EpisodeNumber)
	if s.searchEpisodePatterns(tokens) {
		return
	}
	if s.contains(EpisodeNumber) {
		return
	}
	var numeric []*token
	for _, t := range tokens {
		if pyDigit(t.content) {
			numeric = append(numeric, t)
		}
	}
	if len(numeric) == 0 {
		return
	}
	if s.searchEquivalent(numeric) {
		return
	}
	if s.searchSeparated(numeric) {
		return
	}
	if s.searchIsolatedEpisodes(numeric) {
		return
	}
	s.searchLastNumber(numeric)
}
func (s *state) searchAnimeTitle() {
	enclosed := false
	begin := s.find(flagNotEnclosed | flagUnknown)
	if begin == nil {
		enclosed = true
		begin = s.get(0)
		skipped := false
		for begin != nil {
			begin = s.next(begin, flagUnknown)
			if begin == nil {
				break
			}
			if mostlyLatin(begin.content) && skipped {
				break
			}
			begin = s.next(begin, flagBracket)
			skipped = true
		}
	}
	if begin == nil {
		return
	}
	flags := flagIdentifier
	if enclosed {
		flags |= flagBracket
	}
	end := s.next(begin, flags)
	if !enclosed {
		last, open := end, false
		for _, t := range s.list(flagBracket, begin, end) {
			last = t
			open = !open
		}
		if open {
			end = last
		}
	}
	if !enclosed {
		t := s.previous(end, flagNotDelimiter)
		for tokenCat(t) == tokenBracket && t.content != ")" {
			t = s.previous(t, flagBracket)
			if t != nil {
				end = t
				t = s.previous(end, flagNotDelimiter)
			}
		}
	}
	end = s.previous(end, flagValid)
	s.buildElement(AnimeTitle, begin, end, false)
}
func (s *state) searchReleaseGroup() {
	var end *token
	for {
		var begin *token
		if end != nil {
			begin = s.next(end, flagEnclosed|flagUnknown)
		} else {
			begin = s.find(flagEnclosed | flagUnknown)
		}
		if begin == nil {
			return
		}
		end = s.next(begin, flagBracket|flagIdentifier)
		if end == nil {
			return
		}
		if end.category != tokenBracket {
			continue
		}
		previous := s.previous(begin, flagNotDelimiter)
		if previous != nil && previous.category != tokenBracket {
			continue
		}
		end = s.previous(end, flagValid)
		s.buildElement(ReleaseGroup, begin, end, true)
		return
	}
}
func (s *state) searchEpisodeTitle() {
	var end *token
	for {
		var begin *token
		if end != nil {
			begin = s.next(end, flagNotEnclosed|flagUnknown)
		} else {
			begin = s.find(flagNotEnclosed | flagUnknown)
		}
		if begin == nil {
			return
		}
		end = s.next(begin, flagBracket|flagIdentifier)
		if s.distance(begin, end) <= 2 && runeLen(begin.content) == 1 && strings.Contains(dashes, begin.content) {
			if end == nil {
				sourcePanic("source_non_terminating")
			}
			continue
		}
		if end != nil && end.category == tokenBracket {
			end = s.previous(end, flagValid)
		}
		s.buildElement(EpisodeTitle, begin, end, false)
		return
	}
}
func (s *state) validateElements() {
	if s.contains(AnimeType) && s.contains(EpisodeTitle) {
		title := s.elements[EpisodeTitle][0]
		values := append([]string{}, s.elements[AnimeType]...)
		for _, animeType := range values {
			if animeType == title {
				s.erase(EpisodeTitle)
			} else if strings.Contains(title, animeType) && findKeyword(normalize(animeType), AnimeType) != nil {
				s.remove(AnimeType, animeType)
				continue
			}
		}
	}
}
