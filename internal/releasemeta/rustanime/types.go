// Package rustanime is the pure Go translation of anitomy-pure 0.1.0.
// Source crate checksum: 33f525032668d2aff5dff115958157db7aecf1dc2fd5f5df93cf1be1452dfd4a.
// Translated source files retain the upstream MPL-2.0 license in LICENSE.md.
package rustanime

// Category 保留源解析元素的类别名称。
type Category string

const (
	AnimeSeason         Category = "AnimeSeason"
	AnimeSeasonPrefix   Category = "AnimeSeasonPrefix"
	AnimeTitle          Category = "AnimeTitle"
	AnimeType           Category = "AnimeType"
	AnimeYear           Category = "AnimeYear"
	AudioTerm           Category = "AudioTerm"
	DeviceCompatibility Category = "DeviceCompatibility"
	EpisodeNumber       Category = "EpisodeNumber"
	EpisodeNumberAlt    Category = "EpisodeNumberAlt"
	EpisodePrefix       Category = "EpisodePrefix"
	EpisodeTitle        Category = "EpisodeTitle"
	FileChecksum        Category = "FileChecksum"
	FileExtension       Category = "FileExtension"
	FileName            Category = "FileName"
	Language            Category = "Language"
	Other               Category = "Other"
	ReleaseGroup        Category = "ReleaseGroup"
	ReleaseInformation  Category = "ReleaseInformation"
	ReleaseVersion      Category = "ReleaseVersion"
	Source              Category = "Source"
	Subtitles           Category = "Subtitles"
	VideoResolution     Category = "VideoResolution"
	VideoTerm           Category = "VideoTerm"
	VolumeNumber        Category = "VolumeNumber"
	VolumePrefix        Category = "VolumePrefix"
	Unknown             Category = "Unknown"
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

// Element 保存单个原始元素；重复类别和值不能在传输时去重。
type Element struct {
	Category Category `json:"category"`
	Value    string   `json:"value"`
}

type elements []Element

func (e *elements) push(c Category, v string) { *e = append(*e, Element{c, v}) }
func (e *elements) contains(c Category, v string) bool {
	for _, item := range *e {
		if item.Category == c && item.Value == v {
			return true
		}
	}
	return false
}
func (e *elements) find(c Category) *Element {
	for _, item := range *e {
		if item.Category == c {
			v := item
			return &v
		}
	}
	return nil
}
func (e *elements) all(c Category) []Element {
	var result []Element
	for _, item := range *e {
		if item.Category == c {
			result = append(result, item)
		}
	}
	return result
}
func (e *elements) count(c Category) int  { return len(e.all(c)) }
func (e *elements) empty(c Category) bool { return e.find(c) == nil }
func (e *elements) removeFirst(c Category) *string {
	for i, item := range *e {
		if item.Category == c {
			*e = append((*e)[:i], (*e)[i+1:]...)
			value := item.Value
			return &value
		}
	}
	return nil
}

type subtoken struct {
	value string
	found bool
}
type token struct {
	raw                         string
	subtokens                   []*subtoken
	inside, weak, paren, corner bool
}

func (t *token) containsUnknown() bool {
	for _, st := range t.subtokens {
		if !st.found {
			return true
		}
	}
	return false
}
func (t *token) fullUnknown() bool {
	for _, st := range t.subtokens {
		if st.found {
			return false
		}
	}
	return true
}
func (t *token) isolatedNumber() bool {
	return len(t.subtokens) == 1 && !t.subtokens[0].found && isDigit(t.subtokens[0].value)
}

// TokenState 保存一个阶段的只读诊断快照，仅由显式 Trace 选项产生。
type TokenState struct {
	Raw       string          `json:"raw"`
	Inside    bool            `json:"inside"`
	Weak      bool            `json:"weak"`
	Paren     bool            `json:"paren"`
	Corner    bool            `json:"corner"`
	Subtokens []SubtokenState `json:"subtokens"`
}

// SubtokenState 保留原 Found/Unknown 分类状态。
type SubtokenState struct {
	Value string `json:"value"`
	Found bool   `json:"found"`
}

// Stage 保存顺序元素与 token 的阶段快照。
type Stage struct {
	Name     string       `json:"name"`
	Elements []Element    `json:"elements"`
	Tokens   []TokenState `json:"tokens"`
}

// Result 保存最终有序元素及可选诊断阶段。
type Result struct {
	Elements []Element `json:"elements"`
	Stages   []Stage   `json:"stages,omitempty"`
}

// Options 对应源 Parser 的 builder 选项；nil 布尔值和分隔符使用原默认值。
type Options struct {
	IgnoredStrings    []string `json:"ignored_strings,omitempty"`
	AllowedDelimiters *string  `json:"allowed_delimiters,omitempty"`
	EpisodeNumber     *bool    `json:"episode_number,omitempty"`
	EpisodeTitle      *bool    `json:"episode_title,omitempty"`
	FileExtension     *bool    `json:"file_extension,omitempty"`
	ReleaseGroup      *bool    `json:"release_group,omitempty"`
	Trace             bool     `json:"trace,omitempty"`
}

type keyword struct {
	category                        Category
	identifiable, searchable, valid bool
}
type keywordManager map[string]keyword

func (m keywordManager) find(value string) *keyword {
	if k, ok := m[upper(value)]; ok {
		return &k
	}
	return nil
}
