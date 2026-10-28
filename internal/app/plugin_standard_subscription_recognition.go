package app

import (
	"context"
	"encoding/json"

	"light-media/internal/notice"
)

// The fixed search path recognizes once, then passes the same Meta/MediaInfo
// to total refresh, completion check and notice. Go keeps that local snapshot
// for one preparation; it is never a persisted recognition cache.
type standardSubscriptionRecognition struct {
	Meta        json.RawMessage
	Information sourceMediaInformation
	Existence   *originalSubscriptionExistence
}

func (a *App) resolveStandardSubscriptionRecognition(ctx context.Context, s Subscription, subscribe json.RawMessage) (*standardSubscriptionRecognition, error) {
	if len(subscribe) == 0 {
		var err error
		subscribe, err = a.subscriptionSourceProjection(ctx, s)
		if err != nil {
			return nil, err
		}
	}
	value, err := decodeSourceJSON(subscribe)
	if err != nil || sourceMap(value) == nil {
		return nil, notice.ErrTemplateDependency
	}
	source := sourceMap(value)
	meta, err := a.originalSubscriptionRecognitionMeta(ctx, source)
	if err != nil {
		return nil, err
	}
	encoded, err := meta.EncodeState()
	if err != nil {
		return nil, err
	}
	information, err := a.originalRecognizeSubscriptionTMDBMedia(ctx, s.Media, meta, source)
	if err != nil {
		return nil, err
	}
	return &standardSubscriptionRecognition{Meta: encoded, Information: information}, nil
}
