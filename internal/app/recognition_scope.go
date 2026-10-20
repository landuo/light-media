package app

import (
	"context"
	"slices"
	"strings"

	"light-media/internal/fileextensions"
	"light-media/internal/media/words"
	"light-media/internal/releasemeta"
)

type resourceRecognitionKey struct{}

// Recognition is scoped to one selection, never published in the global cache
// or serialized as part of SearchRequest. The encrypted source Context carries
// the exact same options across confirmation, retries and process restarts.
type resourceRecognition struct {
	options releasemeta.Options
	matcher *words.Matcher
}

func withResourceRecognition(ctx context.Context, options releasemeta.Options) (context.Context, error) {
	options.CustomWords = slices.Clone(options.CustomWords)
	options.ReleaseGroups = slices.Clone(options.ReleaseGroups)
	options.Customization = slices.Clone(options.Customization)
	options.MediaExts = slices.Clone(options.MediaExts)
	matcher, err := words.New(options.CustomWords)
	if err != nil {
		return ctx, bad("INVALID_RECOGNITION_WORDS", "识别词超过允许范围")
	}
	return context.WithValue(ctx, resourceRecognitionKey{}, resourceRecognition{options: options, matcher: matcher}), nil
}

func (a *App) globalRecognitionOptions(ctx context.Context) (releasemeta.Options, error) {
	_, rules, err := a.recognitionWords(ctx)
	if err != nil {
		return releasemeta.Options{}, err
	}
	_, config, err := a.resourceMetadataSettings(ctx)
	if err != nil {
		return releasemeta.Options{}, err
	}
	extensions, err := a.sourceFileExtensions(ctx)
	if err != nil {
		return releasemeta.Options{}, err
	}
	parserExtensions, err := fileextensions.ParserMediaExtensions(extensions)
	if err != nil {
		return releasemeta.Options{}, err
	}
	return releasemeta.Options{SourceMode: config.SourceMode, CustomWords: rules, ReleaseGroups: config.ReleaseGroups, Customization: config.Customization, MediaExts: parserExtensions}, nil
}

func (a *App) ensureResourceRecognition(ctx context.Context) (context.Context, error) {
	if _, ok := ctx.Value(resourceRecognitionKey{}).(resourceRecognition); ok {
		return ctx, nil
	}
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return ctx, err
	}
	return withResourceRecognition(ctx, options)
}

func (a *App) subscriptionRecognitionContext(ctx context.Context, key string, revision int) (context.Context, error) {
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return ctx, err
	}
	a.subMu.Lock()
	defer a.subMu.Unlock()
	if err := a.guardSubscription(ctx, key, revision); err != nil {
		return ctx, err
	}
	text, err := a.subscriptionWords(ctx, key)
	if err != nil {
		return ctx, err
	}
	if text != "" {
		// Source: nonempty raw text replaces globals after split("\n"). Spaces,
		// blank lines and order remain meaningful; do not trim, filter or merge.
		options.CustomWords = strings.Split(text, "\n")
	}
	return withResourceRecognition(ctx, options)
}

func (a *App) intentRecognitionContext(ctx context.Context, in Intent) (context.Context, error) {
	source, err := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
	if err != nil {
		return ctx, err
	}
	if source != nil {
		return withResourceRecognition(ctx, source.Options)
	}
	// Pre-producer legacy intents never froze a selection. Preserve their
	// existing global behavior; do not borrow a live subscription by TMDB ID.
	return a.ensureResourceRecognition(ctx)
}
