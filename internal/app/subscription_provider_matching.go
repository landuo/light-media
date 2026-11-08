package app

import (
	"context"
	"encoding/json"

	"light-media/internal/catalog"
	"light-media/internal/decision"
	"light-media/internal/releasemeta"
)

func (a *App) recognizeProviderSubscriptionMedia(ctx context.Context, identity catalog.Media, meta *releasemeta.MetaObject) (sourceMediaInformation, error) {
	return a.recognizeProviderSubscriptionMediaGroup(ctx, identity, meta, nil)
}

func (a *App) recognizeProviderSubscriptionMediaGroup(ctx context.Context, identity catalog.Media, meta *releasemeta.MetaObject, episodeGroup any) (sourceMediaInformation, error) {
	var information sourceMediaInformation
	if !providerNativeMedia(identity) {
		return information, invalidSubscriptionProvider()
	}
	var fields map[string]any
	if meta != nil {
		var err error
		fields, err = meta.ToDict()
		if err != nil {
			return information, err
		}
	}
	kind := "电影"
	if identity.MediaType == "tv" {
		kind = "电视剧"
	}
	kwargs, err := originalCatalogRecognitionCallKwargs(fields, map[string]any{
		"source": identity.ProviderSource, "mediaid": identity.ProviderID, "mtype": kind,
		"episode_group": episodeGroup, "cache": false,
	})
	if err != nil {
		return information, err
	}
	ctx = context.WithValue(ctx, originalHostMediaFreshContextKey{}, true)
	ctx = context.WithValue(ctx, originalHostMediaNativeSourceKey{}, identity.ProviderSource)
	model, err := a.originalCatalogRecognizeMethod(ctx, "recognize_media", kwargs, func() (json.RawMessage, error) {
		return a.originalHostMediaRecognizeSource(ctx, identity.ProviderSource, kwargs)
	})
	if err != nil {
		return information, err
	}
	if !originalModuleResultTruthy(model) {
		return information, originalRecognitionMissing()
	}
	information.MediaInfo, information.ProviderInfo = model, model
	information.Media, err = subscriptionProviderMedia(information, identity.ProviderSource, identity.ProviderID, identity.MediaType)
	return information, err
}

// Keep the search and acquisition checks on the same recognized provider model.
func providerSubscriptionSearchTarget(in SearchRequest, information sourceMediaInformation) (decision.Target, error) {
	owner := in.originalSubscription
	if owner == nil || owner.Season != in.Season {
		return decision.Target{}, invalidSubscriptionProvider()
	}
	return providerDownloadTarget(in, owner.Media, information.MediaInfo, true)
}
