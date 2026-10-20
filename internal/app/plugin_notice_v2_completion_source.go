package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"light-media/internal/notice"
	"light-media/internal/releasemeta"
)

// The caller supplies the original current Subscribe.to_dict projection from
// its owned subscription model. This function does not invent missing source
// fields from the UI DTO. Its result is frozen by the completion executor
// before check/history/delete and never re-recognized during notification.
func (a *App) buildSubscriptionCompletionBusiness(ctx context.Context, s Subscription, subscribe json.RawMessage) (subscriptionCompletionSource, error) {
	var out subscriptionCompletionSource
	value, e := decodeSourceJSON(subscribe)
	if e != nil || sourceMap(value) == nil {
		return out, notice.ErrTemplateDependency
	}
	source := sourceMap(value)
	name, ok := source["name"].(string)
	if !ok {
		return out, notice.ErrTemplateDependency
	}
	for _, key := range []string{"year", "season", "type", "tmdbid", "doubanid", "bangumiid", "anilistid", "media_source", "media_id", "best_version", "username"} {
		if _, ok := source[key]; !ok {
			return out, notice.ErrTemplateDependency
		}
	}
	kind := sourceText(source["type"])
	if kind != "电影" && kind != "电视剧" {
		return out, notice.ErrTemplateDependency
	}
	expectedKind := "movie"
	if kind == "电视剧" {
		expectedKind = "tv"
	}
	id, e := strconv.ParseInt(fmt.Sprint(source["tmdbid"]), 10, 64)
	if e != nil || id != s.Media.ID || expectedKind != s.Media.MediaType {
		return out, notice.ErrTemplateDependency
	}
	if provider := sourceText(source["media_source"]); provider != "" && provider != "themoviedb" {
		return out, notice.ErrTemplateDependency
	}
	options, e := a.globalRecognitionOptions(ctx)
	if e != nil {
		return out, e
	}
	parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if e != nil {
		return out, e
	}
	meta, e := parser.ParseTitle(name, nil, nil)
	if e != nil {
		return out, e
	}
	// Original build_subscribe_meta overwrites even None, preserving source
	// username and provider IDs independently from recognition's attributes.
	for target, key := range map[string]string{"year": "year", "begin_season": "season", "type": "type", "tmdbid": "tmdbid", "doubanid": "doubanid", "bangumiid": "bangumiid", "anilistid": "anilistid", "media_source": "media_source", "media_id": "media_id"} {
		v, e := noticeV2NormalizeNumberValues(source[key])
		if e != nil {
			return out, e
		}
		meta.Set(target, v)
	}
	encodedMeta, e := meta.EncodeState()
	if e != nil {
		return out, e
	}
	recognized, e := a.sourceRecognizeTMDBMedia(ctx, s.Media)
	if e != nil {
		return out, e
	}
	mi, e := decodeSourceJSON(recognized.MediaInfo)
	if e != nil {
		return out, e
	}
	fields := sourceMap(mi)
	image := fields["backdrop"]
	if !sourceSubscriptionTruth(image) {
		image = fields["poster"]
	}
	msgstr := "订阅"
	if sourceSubscriptionTruth(source["best_version"]) {
		msgstr = "洗版"
	}
	payload := sourceBusinessNotice{Envelope: notice.Object{"mtype": "订阅", "ctype": "subscribeComplete", "image": image, "link": sourceSubscriptionLink(a.Settings().AppDomain, expectedKind), "username": source["username"]}, Meta: encodedMeta, Media: recognized.Media, MediaInfo: recognized.MediaInfo, ProviderInfo: recognized.ProviderInfo, Kwargs: notice.Object{"msgstr": msgstr, "username": source["username"]}}
	if _, e = larkBusinessNoticeContext(payload); e != nil {
		return out, e
	}
	out = subscriptionCompletionSource{Subscription: mustCleanSeedJSON(s), Subscribe: mustCleanSeedJSON(subscribe), Meta: encodedMeta, MediaInfo: recognized.MediaInfo, ProviderInfo: recognized.ProviderInfo, Business: mustCleanSeedJSON(payload)}
	if s.SourceContextSHA != "" {
		origin, e := a.pluginStore.readSubscriptionCompletionSealed(ctx, a.Store.DB, "source_subscription_origin", s.ID)
		if e != nil || tokenHash(string(origin)) != s.SourceContextSHA {
			return subscriptionCompletionSource{}, completionEvidenceError()
		}
		out.Origin = append(json.RawMessage(nil), origin...)
	}
	return out, nil
}
