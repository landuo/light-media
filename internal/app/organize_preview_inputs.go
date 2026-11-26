package app

import (
	"context"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/media"
	"light-media/internal/releasemeta"
)

// A display-only preview plans its files sequentially. Keep only the small
// local evidence and the most recent native search page for that selection.
// A subsequent click or worker obtains fresh inputs and runs every plugin hook.
type manualPreviewInputsKey struct{}
type manualPreviewInputs struct {
	parser  *releasemeta.MetaInfoParser
	nfos    map[string]manualPreviewNFO
	pageKey string
	pageAt  time.Time
	page    catalog.Page
	detail  sourceMediaInformation
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
	identity, numbering, reason := a.importNFOEvidence(anchor)
	if len(inputs.nfos) >= 64 {
		clear(inputs.nfos)
	}
	inputs.nfos[anchor] = manualPreviewNFO{identity, numbering, reason}
	return identity, numbering, reason
}
