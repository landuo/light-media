package releasemeta

import (
	"errors"
	"light-media/internal/releasemeta/pinyin"
	"unicode/utf8"
)

// MetaInfoEnvironment is one frozen source configuration frame. A nil platform map uses
// the fixed installed StreamingPlatforms table; an explicit empty map stays empty.
// Options.CustomWords is GLOBAL. Parse's local pointer preserves None versus [].
type MetaInfoEnvironment struct {
	Options            Options
	StreamingPlatforms map[string]string
}
type MetaInfoParser struct{ environment metaInfoEnvironment }

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append(make([]string, 0, len(in)), in...)
}
func NewMetaInfoParser(env MetaInfoEnvironment) (*MetaInfoParser, error) {
	options := env.Options
	for _, list := range [][]string{options.CustomWords, options.ReleaseGroups, options.Customization, options.MediaExts} {
		for _, value := range list {
			if !utf8.ValidString(value) {
				return nil, errors.New("metainfo_invalid_utf8")
			}
		}
	}
	if options.SourceMode == "" {
		return nil, errors.New("metainfo_source_mode_required")
	}
	if options.SourceMode != "rust" && options.SourceMode != "legacy" {
		return nil, errors.New("metainfo_source_mode_invalid")
	}
	options.CustomWords = cloneStrings(options.CustomWords)
	options.ReleaseGroups = cloneStrings(options.ReleaseGroups)
	options.Customization = cloneStrings(options.Customization)
	options.MediaExts = cloneStrings(options.MediaExts)
	platforms := env.StreamingPlatforms
	if platforms == nil {
		platforms = installedStreamingPlatforms
	}
	frozen := make(map[string]string, len(platforms))
	for k, v := range platforms {
		frozen[k] = v
	}
	return &MetaInfoParser{environment: metaInfoEnvironment{Defaults: options, StreamingPlatforms: frozen, LegacyPinyin: pinyin.LegacyName, RustPinyin: pinyin.RustName}}, nil
}

// Parse reconstructs the real title entry. localWords=nil means source None;
// a nonnil pointer to [] is present empty, not the absence of local configuration.
// The effective source mode may fall back to legacy for an actual Rust failure.
func (p *MetaInfoParser) ParseTitle(title string, subtitle *string, localWords *[]string) (*MetaObject, error) {
	if p == nil {
		return nil, errors.New("metainfo_parser_nil")
	}
	if !utf8.ValidString(title) || subtitle != nil && !utf8.ValidString(*subtitle) {
		return nil, errors.New("metainfo_invalid_utf8")
	}
	if localWords != nil {
		for _, word := range *localWords {
			if !utf8.ValidString(word) {
				return nil, errors.New("metainfo_invalid_utf8")
			}
		}
	}
	var sub *string
	if subtitle != nil {
		copy := *subtitle
		sub = &copy
	}
	var local *[]string
	if localWords != nil {
		copy := cloneStrings(*localWords)
		local = &copy
	}
	return parseFullTitle(title, sub, local, p.environment)
}
