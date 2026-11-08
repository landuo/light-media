package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

func (a *App) originalChatSubscriptionIngress(ctx context.Context) bool {
	grant, ok := ctx.Value(originalLarkIncomingKey{}).(originalLarkWebhookGrant)
	return ok && grant.verified && grant.object != nil && grant.object.app == a &&
		grant.object.installation.PluginID == "LarkMessager" && originalObjectStillLoaded(grant.object)
}

// Selecting a movie or season calls the source synchronous subscription add.
// Its success notice belongs to the durable add workflow, so a returned title
// is only for the precheck where the ordinary subscription is unnecessary.
func (a *App) originalChatSubscribeMedia(ctx context.Context, reply originalSlashReply, meta *releasemeta.MetaObject, media catalog.Media, selectedModel json.RawMessage, bestVersion bool) (string, error) {
	request, origin, err := a.originalChatSubscriptionRequest(ctx, reply, meta, media)
	if err != nil {
		return "", err
	}
	request.BestVersion = &bestVersion
	if !bestVersion {
		var exists bool
		if provider := originalChatMediaProviderFrom(ctx); provider.sourceOnly() {
			info := sourceMediaInformation{Media: media, MediaInfo: selectedModel}
			info.Media, err = subscriptionProviderMedia(info, provider.Source, provider.ID, media.MediaType)
			if err == nil {
				exists, err = a.originalChatMediaLibraryExistsWithInfo(ctx, meta, info)
			}
		} else {
			exists, err = a.originalChatMediaLibraryExists(ctx, meta, media)
		}
		if err != nil {
			return "", err
		}
		if exists {
			title := media.Title
			if media.Year != 0 {
				title = fmt.Sprintf("%s (%d)", title, media.Year)
			}
			if meta.Number("begin_season") != nil {
				title += meta.Season()
			}
			return fmt.Sprintf("【%s 媒体库中已存在，如需洗版请发送：洗版 XXX】", title), nil
		}
	}
	return "", a.saveOriginalChatSubscription(ctx, request, reply.username, origin)
}

func (a *App) originalChatSubscribeMediaAfterAuto(ctx context.Context, reply originalSlashReply, meta *releasemeta.MetaObject, media catalog.Media, accepted []Candidate) error {
	request, origin, err := a.originalChatSubscriptionRequest(ctx, reply, meta, media)
	if err != nil {
		return err
	}
	origin.State = "R"
	if media.MediaType == "tv" && len(accepted) > 0 {
		origin.Note = []int{}
		var parser *releasemeta.MetaInfoParser
		for _, candidate := range accepted {
			var item *releasemeta.MetaObject
			if len(candidate.originalSelectionMeta) != 0 {
				item, err = releasemeta.DecodeMetaObjectState(candidate.originalSelectionMeta)
			} else {
				if parser == nil {
					options, err := a.globalRecognitionOptions(ctx)
					if err != nil {
						return err
					}
					parser, err = releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
					if err != nil {
						return err
					}
				}
				subtitle := candidate.Resource.SourceSubtitle
				if subtitle == nil && candidate.Resource.Subtitle != "" {
					subtitle = &candidate.Resource.Subtitle
				}
				item, err = parser.ParseTitle(candidate.Resource.Title, subtitle, nil)
			}
			if err != nil {
				return err
			}
			if ep := item.Number("begin_episode"); ep != nil && ep.IsInt64() && ep.Sign() != 0 && int64(int(ep.Int64())) == ep.Int64() {
				origin.Note = append(origin.Note, int(ep.Int64()))
			}
		}
	}
	return a.saveOriginalChatSubscription(ctx, request, reply.username, origin)
}

func (a *App) originalChatSubscriptionRequest(ctx context.Context, reply originalSlashReply, meta *releasemeta.MetaObject, media catalog.Media) (SubscriptionRequest, *sourceSubscriptionOrigin, error) {
	var request SubscriptionRequest
	if err := ctx.Err(); err != nil {
		return request, nil, err
	}
	_, verified := originalLarkSlashIngress(ctx, reply.info)
	user, err := originalInputID(reply.info.UserID, false)
	if err != nil || user == nil || *user != reply.user || strings.EqualFold(strings.TrimSpace(*user), "system") || !verified || !a.originalChatSubscriptionIngress(ctx) {
		return request, nil, bad("PLUGIN_CAPABILITY_DENIED", "添加订阅需要已验证的 Lark 选择")
	}
	provider := originalChatMediaProviderFrom(ctx)
	providerOnly := provider.sourceOnly() && media.ID == 0 && (media.MediaType == "movie" || media.MediaType == "tv")
	if meta == nil || !validMedia(media.MediaType, media.ID) && !providerOnly || media.Title == "" {
		return request, nil, bad("INVALID_MEDIA", "未识别到媒体信息")
	}
	request.Media = media
	if media.MediaType == "tv" {
		if season := meta.Number("begin_season"); season != nil {
			if !season.IsInt64() || season.Int64() < 1 || season.Int64() > 99 {
				return request, nil, bad("INVALID_SEASON", "请选择标准季号")
			}
			request.Season = int(season.Int64())
		}
	}
	origin := &sourceSubscriptionOrigin{Channel: *reply.info.Channel, Source: *reply.info.Source, UserID: *user, State: "N", MediaProvider: provider}
	return request, origin, nil
}

func (a *App) saveOriginalChatSubscription(ctx context.Context, request SubscriptionRequest, username string, origin *sourceSubscriptionOrigin) error {
	ctx, finish, err := a.beginSubscriptionStatistics(ctx)
	if err != nil {
		return err
	}
	defer finish()
	input, err := a.buildSourceSubscriptionAdd(ctx, request, username, origin)
	if err != nil {
		return err
	}
	_, err = a.executeNewSubscriptionAdd(ctx, input, "Lark 新增订阅原始业务流程")
	return err
}

// Use the ordinary media-exists module and native inventory. A show is fully
// present only if its relevant provider seasons have no missing episodes.
func (a *App) originalChatMediaLibraryExists(ctx context.Context, meta *releasemeta.MetaObject, media catalog.Media) (bool, error) {
	info, err := a.sourceRecognizeTMDBMedia(ctx, media)
	if err != nil {
		return false, err
	}
	return a.originalChatMediaLibraryExistsWithInfo(ctx, meta, info)
}

func (a *App) originalChatMediaLibraryExistsWithInfo(ctx context.Context, meta *releasemeta.MetaObject, info sourceMediaInformation) (bool, error) {
	sub := Subscription{Media: info.Media}
	if sub.Media.MediaType == "tv" {
		sub.Season = 1
	}
	resolved, err := a.resolveOriginalSubscriptionExistence(ctx, sub, &standardSubscriptionRecognition{Information: info})
	if err != nil {
		return false, err
	}
	observed := resolved.Existence
	if observed == nil {
		raw, err := a.originalNativeSubscriptionExistence(ctx, sub)
		if err != nil {
			return false, err
		}
		observed = &originalSubscriptionExistence{Result: raw}
	}
	if sub.Media.MediaType == "movie" {
		episodes, err := observed.episodes(sub)
		return len(episodes) != 0, err
	}
	fields, err := decodeSourceJSON(info.MediaInfo)
	if err != nil {
		return false, err
	}
	seasons := sourceMap(sourceMap(fields)["seasons"])
	var requested []int
	if meta.Number("begin_season") != nil {
		seasonList, err := meta.SeasonList()
		if err != nil {
			return false, err
		}
		for _, n := range seasonList {
			if n.IsInt64() && n.Int64() >= 0 && n.Int64() <= 99 {
				requested = append(requested, int(n.Int64()))
			}
		}
		if len(requested) == 0 {
			return false, nil
		}
	}
	found := false
	for key, list := range seasons {
		season, err := strconv.Atoi(key)
		if err != nil || len(requested) != 0 && !slices.Contains(requested, season) {
			continue
		}
		episodes := sourceRows(list)
		if len(episodes) == 0 {
			continue
		}
		sub.Season = season
		owned, err := observed.episodes(sub)
		if err != nil {
			return false, err
		}
		for _, value := range episodes {
			episode, err := originalResourceInteger(value)
			if err != nil || episode <= 0 || episode > 9999 || !slices.Contains(owned, int(episode)) {
				return false, nil
			}
		}
		found = true
	}
	return found, nil
}
