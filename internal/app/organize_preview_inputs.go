package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"light-media/internal/catalog"
	"light-media/internal/media"
	"light-media/internal/releasemeta"
)

// A display-only preview plans its files sequentially. Keep only the small
// local evidence and the most recent native search page for that selection.
// A subsequent click or worker obtains fresh inputs and runs every plugin hook.
type manualPreviewInputsKey struct{}
type manualPreviewInputs struct {
	nfoDirectories          map[string]map[string]bool
	parser                  *releasemeta.MetaInfoParser
	siblingMeta             func(string) (*releasemeta.MetaObject, error)
	nfos                    map[string]manualPreviewNFO
	seriesNFO               importSeriesNFOResult
	pageKey                 string
	pageAt                  time.Time
	page                    catalog.Page
	detail                  sourceMediaInformation
	imageInput, imageOutput json.RawMessage
	imageDomain             string
	projectionInput         json.RawMessage
	projection              sourceMediaInformation
	recognized              catalog.ExploreMedia
	namingInput             json.RawMessage
	namingMedia             map[string]any
}

// Plugins can produce a different MediaInfo for each episode. Compare their
// actual output before reusing the pure projection within a single preview.
func (a *App) transferRecognitionProjection(ctx context.Context, identity catalog.Media, raw json.RawMessage) (sourceMediaInformation, error) {
	inputs, _ := ctx.Value(manualPreviewInputsKey{}).(*manualPreviewInputs)
	if inputs != nil && inputs.projection.Media.ID == identity.ID && inputs.projection.Media.MediaType == identity.MediaType && bytes.Equal(raw, inputs.projectionInput) {
		return inputs.projection, nil
	}
	out, err := a.originalRecognitionProjection(identity, raw)
	if inputs != nil && err == nil && len(raw)+len(out.MediaInfo)+len(out.ProviderInfo) <= 512<<10 {
		inputs.projectionInput, inputs.projection = raw, out
	}
	return out, err
}

// The recognize/obtain_images hooks still run per file. Only reuse decoding
// when their complete result matches, including fields supplied by plugins.
func transferRecognizedMedia(ctx context.Context, raw json.RawMessage) (catalog.ExploreMedia, error) {
	inputs, _ := ctx.Value(manualPreviewInputsKey{}).(*manualPreviewInputs)
	if inputs != nil && len(raw) != 0 && bytes.Equal(raw, inputs.recognized.OriginalMedia) {
		return inputs.recognized, nil
	}
	identity, err := originalCatalogMediaProjection(raw)
	if inputs != nil && err == nil && len(raw) <= 256<<10 {
		inputs.recognized = identity
	}
	return identity, err
}

func transferNamingMedia(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	inputs, _ := ctx.Value(manualPreviewInputsKey{}).(*manualPreviewInputs)
	if inputs != nil && inputs.namingMedia != nil && bytes.Equal(raw, inputs.namingInput) {
		return inputs.namingMedia, nil
	}
	value, err := decodeSourceJSON(raw)
	if err != nil {
		return nil, err
	}
	fields := sourceMap(value)
	// TemplateHelper uses the image getters; MediaInfo.to_dict retains paths.
	if fields != nil {
		for _, name := range []string{"poster", "backdrop"} {
			if _, present := fields[name]; !present {
				fields[name] = strings.ReplaceAll(sourceText(fields[name+"_path"]), "original", "w500")
			}
		}
		if inputs != nil && len(raw) <= 256<<10 {
			inputs.namingInput, inputs.namingMedia = raw, fields
		}
	}
	return fields, nil
}

func (a *App) nativeTransferTMDBInformation(ctx context.Context, identity catalog.Media) (sourceMediaInformation, error) {
	inputs, _ := ctx.Value(manualPreviewInputsKey{}).(*manualPreviewInputs)
	if inputs != nil && inputs.detail.Media.ID == identity.ID && inputs.detail.Media.MediaType == identity.MediaType && identity.ID > 0 {
		return inputs.detail, nil
	}
	info, err := a.cachedTMDBSourceInformation(ctx, identity)
	if err == nil && inputs != nil && len(info.ProviderInfo)+len(info.MediaInfo) <= 512<<10 {
		inputs.detail = info
	}
	return info, err
}

// The native season cache owns its episode slice until another season is
// selected. Plugins and episode groups provide independent slices and must
// always use their own result, even for the same series and season number.
func nativePreviewEpisodeSet(ctx context.Context, episodes []catalog.Episode) *manualPreviewSeason {
	cache, _ := ctx.Value(manualPreviewSeasonKey{}).(*manualPreviewSeason)
	if cache == nil || len(episodes) == 0 || len(cache.season.Episodes) != len(episodes) || &episodes[0] != &cache.season.Episodes[0] {
		return nil
	}
	return cache
}

func transferModuleEpisodeRows(ctx context.Context, episodes []catalog.Episode) (json.RawMessage, error) {
	cached := nativePreviewEpisodeSet(ctx, episodes)
	if cached != nil && cached.moduleEpisodes != nil {
		return cached.moduleEpisodes, nil
	}
	rows := make([]map[string]any, 0, len(episodes))
	for _, episode := range episodes {
		raw := episode.OriginalEpisode
		if len(raw) == 0 {
			raw, _ = json.Marshal(episode)
		}
		row, err := originalCatalogEpisodeProjection(raw)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	encoded, err := json.Marshal(rows)
	if cached != nil && err == nil && len(encoded) <= 128<<10 {
		cached.moduleEpisodes = encoded
	}
	return encoded, err
}

// TemplateHelper's raw episode objects include source fields and guests; keep
// the complete objects available to a rename hook on any episode.
func transferNamingEpisodeRows(ctx context.Context, episodes []catalog.Episode) ([]map[string]any, error) {
	cached := nativePreviewEpisodeSet(ctx, episodes)
	if cached != nil && cached.namingEpisodes != nil {
		return cached.namingEpisodes, nil
	}
	var rows []map[string]any
	total := 0
	for _, episode := range episodes {
		raw, err := json.Marshal(episode)
		if err != nil {
			return nil, err
		}
		value, err := decodeSourceJSON(raw)
		if err != nil {
			return nil, err
		}
		total += len(raw)
		rows = append(rows, sourceMap(value))
	}
	if cached != nil && total <= 128<<10 {
		cached.namingEpisodes = rows
	}
	return rows, nil
}

type manualPreviewNFO struct {
	media     *importInferredMedia
	numbering *media.Numbering
	reason    string
}

// Only pure filename parsing is shared between requests. Filesystem evidence
// and storage/recognition plugin calls keep their normal request lifetime.
type manualRecognitionParser struct {
	mu      sync.Mutex
	current *releasemeta.MetaInfoParser
}

func (cache *manualRecognitionParser) get(options releasemeta.Options) (*releasemeta.MetaInfoParser, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.current.SameOptions(options) {
		return cache.current, nil
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options, ReuseConfiguredRules: true})
	if err == nil {
		cache.current = parser
	}
	return parser, err
}

func (a *App) transferMetaParser(ctx context.Context) (*releasemeta.MetaInfoParser, error) {
	inputs, _ := ctx.Value(manualPreviewInputsKey{}).(*manualPreviewInputs)
	if inputs != nil && inputs.parser != nil {
		return inputs.parser, nil
	}
	var options releasemeta.Options
	if scope, ok := ctx.Value(resourceRecognitionKey{}).(resourceRecognition); inputs != nil && ok {
		options = scope.options
	} else {
		var err error
		options, err = a.globalRecognitionOptions(ctx)
		if err != nil {
			return nil, err
		}
	}
	parser, err := a.manualParser.get(options)
	if err == nil && inputs != nil {
		inputs.parser = parser
	}
	return parser, err
}

func (a *App) transferNFOEvidence(ctx context.Context, anchor string) (*importInferredMedia, *media.Numbering, string) {
	inputs, _ := ctx.Value(manualPreviewInputsKey{}).(*manualPreviewInputs)
	if inputs == nil {
		return a.importNFOEvidence(anchor)
	}
	if e, ok := inputs.nfos[anchor]; ok {
		return e.media, e.numbering, e.reason
	}
	identity, numbering, reason := a.readImportNFOEvidence(anchor, inputs.readNFO, &inputs.seriesNFO)
	if len(inputs.nfos) >= 64 {
		clear(inputs.nfos)
	}
	inputs.nfos[anchor] = manualPreviewNFO{identity, numbering, reason}
	return identity, numbering, reason
}

// Group previews probe the same movie.nfo and tvshow.nfo ancestors for every
// episode. Index only a short, completely read directory. Ambiguous names,
// large or unreadable directories and present entries use the guarded reader.
func (inputs *manualPreviewInputs) readNFO(path string) (assetLocalNFO, int64, error) {
	dir := filepath.Dir(path)
	if inputs.nfoDirectories == nil {
		return readAssetNFO(path)
	}
	names, known := inputs.nfoDirectories[dir]
	if !known {
		if len(inputs.nfoDirectories) >= 8 {
			clear(inputs.nfoDirectories)
		}
		if f, err := importDir(dir, false); err == nil {
			entries, err := f.Readdirnames(129)
			if len(entries) <= 128 && err == nil {
				_, err = f.Readdirnames(1) // A short read by itself does not establish EOF.
			}
			f.Close()
			if len(entries) <= 128 && err == io.EOF {
				names = make(map[string]bool)
				for _, name := range entries {
					if !strings.EqualFold(filepath.Ext(name), ".nfo") {
						continue
					}
					if len(name) != utf8.RuneCountInString(name) {
						names = nil
						break
					}
					names[strings.ToLower(name)] = true
				}
			}
		}
		inputs.nfoDirectories[dir] = names
	}
	base := filepath.Base(path)
	if names != nil && (len(names) == 0 || len(base) == utf8.RuneCountInString(base) && !names[strings.ToLower(base)]) {
		return assetLocalNFO{}, 0, os.ErrNotExist
	}
	return readAssetNFO(path)
}
