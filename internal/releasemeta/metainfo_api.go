package releasemeta

import (
	"errors"
	"slices"
	"unicode/utf8"

	"light-media/internal/releasemeta/pinyin"
)

// MetaInfoEnvironment is one frozen source configuration frame. A nil platform map uses
// the fixed installed StreamingPlatforms table; an explicit empty map stays empty.
// Options.CustomWords is GLOBAL. Parse's local pointer preserves None versus [].
type MetaInfoEnvironment struct {
	Options            Options
	StreamingPlatforms map[string]string
	// Reusable parsers can retain more compiled rules from one configuration.
	ReuseConfiguredRules bool
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
	options.patterns = &configuredPatterns{}
	if env.ReuseConfiguredRules {
		options.patterns.limit = min(512, max(64, len(options.CustomWords)))
	}
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

// SameOptions compares the frozen source inputs, including absent versus empty lists.
func (p *MetaInfoParser) SameOptions(o Options) bool {
	if p == nil {
		return false
	}
	frozen := p.environment.Defaults
	equal := func(a, b []string) bool { return (a == nil) == (b == nil) && slices.Equal(a, b) }
	return o.SourceMode == frozen.SourceMode && equal(o.CustomWords, frozen.CustomWords) && equal(o.ReleaseGroups, frozen.ReleaseGroups) && equal(o.Customization, frozen.Customization) && equal(o.MediaExts, frozen.MediaExts)
}
