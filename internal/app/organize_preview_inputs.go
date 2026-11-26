package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	nfos                    map[string]manualPreviewNFO
	pageKey                 string
	pageAt                  time.Time
	page                    catalog.Page
	detail                  sourceMediaInformation
	imageInput, imageOutput json.RawMessage
	imageDomain             string
	projectionInput         json.RawMessage
	projection              sourceMediaInformation
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

type manualPreviewNFO struct {
	media     *importInferredMedia
	numbering *media.Numbering
	reason    string
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
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
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
	identity, numbering, reason := a.readImportNFOEvidence(anchor, inputs.readNFO)
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
