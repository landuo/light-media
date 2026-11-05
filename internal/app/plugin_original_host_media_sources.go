package app

import (
	"context"
	"encoding/json"
	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
	"light-media/internal/rsscontext"
)

// Source APIs use truth tests before some mappings/sequences, but a nonempty
// value of the wrong shape raises during .get, iteration or assignment.
func originalHostMediaSourceMapping(value any, allowFalsey bool) (map[string]any, error) {
	if allowFalsey && !sourceSubscriptionTruth(value) {
		return nil, nil
	}
	if fields, ok := value.(map[string]any); ok {
		return fields, nil
	}
	return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "媒体来源返回了非对象字段")
}

func originalHostMediaSourceSequence(value any) ([]any, error) {
	if !sourceSubscriptionTruth(value) {
		return nil, nil
	}
	if rows, ok := value.([]any); ok {
		return rows, nil
	}
	return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "媒体来源返回了非列表字段")
}

func (a *App) originalHostMediaProviderJSON(ctx context.Context, source, region string, key any, skipEmpty bool, load func() (json.RawMessage, error)) (any, error) {
	cacheKey, err := json.Marshal(key)
	if err != nil {
		return nil, err
	}
	return a.originalHostMediaProviderCached(ctx, source, region, string(cacheKey), skipEmpty, func() (any, error) {
		raw, err := load()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			// Source wrappers return None for absent HTTP responses. Bangumi
			// also catches malformed JSON, whereas Douban retries that error.
			if provider, ok := err.(*catalog.ExploreError); ok && provider.Code == "invalid_response" && source == "douban" {
				return nil, err
			}
			return nil, nil
		}
		value, err := decodeSourceJSON(raw)
		if err != nil && source == "bangumi" {
			return nil, nil
		}
		return value, err
	})
}

// Native provider calls stay behind the host's existing outbound transport.
// The source provider payload is normalized before it crosses the SDK boundary.
func (a *App) originalHostMediaRecognizeSource(ctx context.Context, source string, kwargs map[string]any) (json.RawMessage, error) {
	proxy, err := a.proxyConfig()
	if err != nil {
		return nil, err
	}
	transport, err := proxy.Transport(nil)
	if err != nil {
		return nil, err
	}
	explorer := catalog.NewExplorer(a.catalog(), transport)
	defer explorer.CloseIdleConnections()
	if source == "douban" {
		return a.originalHostMediaRecognizeDouban(ctx, explorer, kwargs)
	}
	if source == "bangumi" {
		return a.originalHostMediaRecognizeBangumi(ctx, explorer, kwargs)
	}
	if source == "anilist" {
		return a.originalHostMediaRecognizeAniList(ctx, explorer, kwargs)
	}
	return nil, nil
}

func (a *App) originalHostMediaSourceParser(ctx context.Context) (*releasemeta.MetaInfoParser, error) {
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return nil, err
	}
	return releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
}

func originalHostMediaSourceSerialize(fields map[string]any) (json.RawMessage, error) {
	model, err := rsscontext.NewMediaInfo(nil)
	if err != nil {
		return nil, err
	}
	if err = model.FromDict(fields); err != nil {
		return nil, err
	}
	result, err := model.ToDict()
	if err != nil {
		return nil, err
	}
	result["recognize_cache_hit"] = false
	return json.Marshal(result)
}
