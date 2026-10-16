// Package legacyanime translates the actual installed Anitopy 2.1.1 source.
// MPL-2.0 applies;
// the original license is retained in LICENSE.txt. Runtime code is Go only.
package legacyanime

type Category string

const (
	AnimeSeason         Category = "anime_season"
	AnimeSeasonPrefix   Category = "anime_season_prefix"
	AnimeTitle          Category = "anime_title"
	AnimeType           Category = "anime_type"
	AnimeYear           Category = "anime_year"
	AudioTerm           Category = "audio_term"
	DeviceCompatibility Category = "device_compatibility"
	EpisodeNumber       Category = "episode_number"
	EpisodeNumberAlt    Category = "episode_number_alt"
	EpisodePrefix       Category = "episode_prefix"
	EpisodeTitle        Category = "episode_title"
	FileChecksum        Category = "file_checksum"
	FileExtension       Category = "file_extension"
	FileName            Category = "file_name"
	Language            Category = "language"
	Other               Category = "other"
	ReleaseGroup        Category = "release_group"
	ReleaseInformation  Category = "release_information"
	ReleaseVersion      Category = "release_version"
	Source              Category = "source"
	Subtitles           Category = "subtitles"
	VideoResolution     Category = "video_resolution"
	VideoTerm           Category = "video_term"
	VolumeNumber        Category = "volume_number"
	VolumePrefix        Category = "volume_prefix"
	Unknown             Category = "unknown"
)

func (c Category) singular() bool {
	switch c {
	case AnimeSeason, AnimeType, AudioTerm, DeviceCompatibility, EpisodeNumber, Language, Other, ReleaseInformation, Source, VideoTerm:
		return false
	}
	return true
}
func (c Category) searchable() bool {
	switch c {
	case AnimeSeasonPrefix, AnimeType, AudioTerm, DeviceCompatibility, EpisodePrefix, FileChecksum, Language, Other, ReleaseGroup, ReleaseInformation, ReleaseVersion, Source, Subtitles, VideoResolution, VideoTerm, VolumePrefix:
		return true
	}
	return false
}

type tokenCategory int

const (
	tokenUnknown tokenCategory = iota + 1
	tokenBracket
	tokenDelimiter
	tokenIdentifier
	tokenInvalid
)

func (c tokenCategory) String() string {
	switch c {
	case tokenUnknown:
		return "UNKNOWN"
	case tokenBracket:
		return "BRACKET"
	case tokenDelimiter:
		return "DELIMITER"
	case tokenIdentifier:
		return "IDENTIFIER"
	case tokenInvalid:
		return "INVALID"
	}
	return ""
}

type tokenFlags uint

const (
	flagBracket tokenFlags = 1 << iota
	flagNotBracket
	flagDelimiter
	flagNotDelimiter
	flagIdentifier
	flagNotIdentifier
	flagUnknown
	flagNotUnknown
	flagValid
	flagNotValid
	flagEnclosed
	flagNotEnclosed
)
const maskCategories = flagBracket | flagNotBracket | flagDelimiter | flagNotDelimiter | flagIdentifier | flagNotIdentifier | flagUnknown | flagNotUnknown | flagValid | flagNotValid

type token struct {
	category tokenCategory
	content  string
	enclosed bool
}

func (t *token) check(flags tokenFlags) bool {
	if flags&(flagEnclosed|flagNotEnclosed) != 0 {
		if flags&flagEnclosed != 0 {
			if !t.enclosed {
				return false
			}
		} else if t.enclosed {
			return false
		}
	}
	if flags&maskCategories == 0 {
		return true
	}
	for _, test := range []struct {
		yes, no  tokenFlags
		category tokenCategory
	}{{flagBracket, flagNotBracket, tokenBracket}, {flagDelimiter, flagNotDelimiter, tokenDelimiter}, {flagIdentifier, flagNotIdentifier, tokenIdentifier}, {flagUnknown, flagNotUnknown, tokenUnknown}, {flagNotValid, flagValid, tokenInvalid}} {
		if flags&test.yes != 0 {
			if t.category == test.category {
				return true
			}
		} else if flags&test.no != 0 && t.category != test.category {
			return true
		}
	}
	return false
}

// SourceError preserves the original Python error class without leaking input.
type SourceError struct{ Class string }

func (e SourceError) Error() string { return e.Class }
func sourcePanic(class string)      { panic(SourceError{class}) }

type state struct {
	elements map[Category][]string
	order    []Category
	tokens   []*token
	checkAlt bool
	options  Options
	stages   []Stage
}

func (s *state) insert(c Category, v string) {
	if _, ok := s.elements[c]; !ok {
		s.order = append(s.order, c)
	}
	s.elements[c] = append(s.elements[c], v)
}
func (s *state) contains(c Category) bool { return len(s.elements[c]) > 0 }
func (s *state) erase(c Category) {
	delete(s.elements, c)
	for i, key := range s.order {
		if key == c {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}
func (s *state) remove(c Category, v string) {
	for i, item := range s.elements[c] {
		if item == v {
			s.elements[c] = append(s.elements[c][:i], s.elements[c][i+1:]...)
			if len(s.elements[c]) == 0 {
				s.erase(c)
			}
			return
		}
	}
	sourcePanic("ValueError")
}
func (s *state) index(t *token) int {
	for i, other := range s.tokens {
		if other == t {
			return i
		}
	}
	sourcePanic("ValueError")
	return 0
}
func (s *state) get(index int) *token {
	if index < 0 {
		index += len(s.tokens)
	}
	if index < 0 || index >= len(s.tokens) {
		sourcePanic("IndexError")
	}
	return s.tokens[index]
}
func (s *state) list(flags tokenFlags, begin, end *token) []*token {
	first, last := 0, len(s.tokens)
	if begin != nil {
		first = s.index(begin)
	}
	if end != nil {
		last = s.index(end)
	}
	var values []*token
	for i := first; i < len(s.tokens) && i <= last; i++ {
		if s.tokens[i].check(flags) {
			values = append(values, s.tokens[i])
		}
	}
	return values
}
func (s *state) find(flags tokenFlags) *token { return s.next(nil, flags) }
func (s *state) next(t *token, flags tokenFlags) *token {
	start := 0
	if t != nil {
		start = s.index(t) + 1
	}
	for _, item := range s.tokens[start:] {
		if item.check(flags) {
			return item
		}
	}
	return nil
}
func (s *state) previous(t *token, flags tokenFlags) *token {
	start := len(s.tokens) - 1
	if t != nil {
		start = s.index(t) - 1
		if start == -1 {
			start = len(s.tokens) - 1
		}
	} // Python tokens[-1::-1] includes the entire list.
	for i := start; i >= 0; i-- {
		if s.tokens[i].check(flags) {
			return s.tokens[i]
		}
	}
	return nil
}
func (s *state) distance(begin, end *token) int {
	first, last := 0, len(s.tokens)
	if begin != nil {
		first = s.index(begin)
	}
	if end != nil {
		last = s.index(end)
	}
	return last - first
}
func (s *state) insertToken(index int, t *token) {
	if index < 0 {
		index += len(s.tokens)
		if index < 0 {
			index = 0
		}
	}
	if index > len(s.tokens) {
		index = len(s.tokens)
	}
	s.tokens = append(s.tokens, nil)
	copy(s.tokens[index+1:], s.tokens[index:])
	s.tokens[index] = t
}

// Options matches Anitopy's public single-title options. Nil booleans use its
// defaults; a non-nil empty delimiter string is not replaced with a default.
type Options struct {
	AllowedDelimiters *string  `json:"allowed_delimiters,omitempty"`
	IgnoredStrings    []string `json:"ignored_strings,omitempty"`
	EpisodeNumber     *bool    `json:"parse_episode_number,omitempty"`
	EpisodeTitle      *bool    `json:"parse_episode_title,omitempty"`
	FileExtension     *bool    `json:"parse_file_extension,omitempty"`
	ReleaseGroup      *bool    `json:"parse_release_group,omitempty"`
	Trace             bool     `json:"trace,omitempty"`
}

func enabled(value *bool) bool { return value == nil || *value }
func (s *state) delimiters() string {
	if s.options.AllowedDelimiters != nil {
		return *s.options.AllowedDelimiters
	}
	return " _.&+,|"
}

type TokenState struct {
	Category string `json:"category"`
	Content  string `json:"content"`
	Enclosed bool   `json:"enclosed"`
}
type Stage struct {
	Name     string                `json:"name"`
	Elements map[Category][]string `json:"elements"`
	CheckAlt bool                  `json:"check_alt_number"`
	Tokens   []TokenState          `json:"tokens"`
}
type Result struct {
	Dictionary map[string]any `json:"result"`
	Stages     []Stage        `json:"stages,omitempty"`
	FinalState Stage          `json:"final_state"`
}

func (s *state) snapshot(name string) Stage {
	out := Stage{Name: name, Elements: map[Category][]string{}, CheckAlt: s.checkAlt, Tokens: make([]TokenState, 0, len(s.tokens))}
	for c, values := range s.elements {
		out.Elements[c] = append([]string{}, values...)
	}
	for _, t := range s.tokens {
		out.Tokens = append(out.Tokens, TokenState{t.category.String(), t.content, t.enclosed})
	}
	return out
}
func (s *state) dictionary() map[string]any {
	out := map[string]any{}
	for c, values := range s.elements {
		if len(values) == 1 {
			out[string(c)] = values[0]
		} else {
			out[string(c)] = append([]string{}, values...)
		}
	}
	return out
}
