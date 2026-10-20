package app

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"light-media/internal/acquisitionbatch"
	"light-media/internal/notice"
	"light-media/internal/releasemeta"
	"light-media/internal/torrentfilter"
)

func sourceDefaultText(value any) (*string, error) {
	if value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, bad("INVALID_SOURCE_SUBSCRIBE_DEFAULT", "原订阅文本默认值类型无法使用")
	}
	return &text, nil
}
func sourceDefaultStrings(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		if values, ok := value.([]string); ok {
			return values, nil
		}
		return nil, bad("INVALID_SOURCE_SUBSCRIBE_DEFAULT", "原订阅列表默认值类型无法使用")
	}
	out := []string{}
	for _, value := range values {
		switch value := value.(type) {
		case string:
			out = append(out, value)
		case json.Number:
			out = append(out, string(value))
		default:
			return nil, bad("INVALID_SOURCE_SUBSCRIBE_DEFAULT", "原订阅列表默认值类型无法使用")
		}
	}
	return out, nil
}

func (a *App) larkSourceSubscriptionRequest(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, facts pluginLarkMediaTargets, user, state string, note []int, best *bool) (SubscriptionRequest, error) {
	var request SubscriptionRequest
	if s.Selected == nil {
		return request, larkEvidenceError()
	}
	media, e := a.larkRecognizeSubscriptionMedia(ctx, core, facts.Media)
	if e != nil {
		return request, e
	}
	defaults, e := a.larkSubscriptionDefaults(ctx, core, media.Media, best)
	if e != nil {
		return request, e
	}
	if sourceSubscriptionTruth(defaults["best_version"]) {
		return request, bad("LARK_CORE_DEPENDENCY_PENDING", "原洗版订阅需要完整质量进度与媒体替换消费者，尚未全部接入")
	}
	username, e := a.larkSubscriptionUsername(ctx, core, user)
	if e != nil {
		return request, e
	}
	prepared, e := larkCoreRead(ctx, a, core, "subscription.request", struct {
		Meta        json.RawMessage
		Facts       pluginLarkMediaTargets
		User, State string
		Username    json.RawMessage
		Note        []int
		Defaults    map[string]any
		Recognized  sourceMediaInformation
	}{s.Meta, facts, user, state, username, note, defaults, media}, func(ctx context.Context) (sourceSubscriptionPreparation, error) {
		request, e := a.larkBuildSourceSubscriptionRequest(ctx, s, facts, user, state, note, defaults, username)
		if e == nil {
			request.Media = media.Media
			request.source.MediaInfo, request.source.ProviderInfo = media.MediaInfo, media.ProviderInfo
			var year, season any
			if facts.Media.Year != 0 {
				year = strconv.Itoa(facts.Media.Year)
			}
			if facts.Media.MediaType == "tv" {
				season = request.Season
			}
			kind := "电影"
			if facts.Media.MediaType == "tv" {
				kind = "电视剧"
			}
			request.source.Registration, e = sourceRegistrationFields(facts.Media.Title, year, kind, season, media.MediaInfo)
		}
		return sourceSubscriptionPreparation{Request: request, Origin: request.source}, e
	})
	if e != nil {
		return request, e
	}
	prepared.Request.source = prepared.Origin
	return prepared.Request, nil
}

type sourceSubscriptionPreparation struct {
	Request SubscriptionRequest
	Origin  *sourceSubscriptionOrigin
}

func (a *App) larkBuildSourceSubscriptionRequest(ctx context.Context, s *pluginLarkMediaSession, facts pluginLarkMediaTargets, user, state string, note []int, defaults map[string]any, username json.RawMessage) (SubscriptionRequest, error) {
	var request SubscriptionRequest
	options, e := a.globalRecognitionOptions(ctx)
	if e != nil {
		return request, e
	}
	parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if e != nil {
		return request, e
	}
	meta, e := parser.ParseTitle(facts.Media.Title, nil, nil)
	if e != nil {
		return request, e
	}
	if facts.Media.Year != 0 {
		meta.Set("year", strconv.Itoa(facts.Media.Year))
	}
	kind := "电影"
	if facts.Media.MediaType == "tv" {
		kind = "电视剧"
	}
	meta.Set("type", kind)
	input, e := releasemeta.DecodeMetaObjectState(s.Meta)
	if e != nil {
		return request, e
	}
	season := 0
	if facts.Media.MediaType == "tv" {
		season = 1
		if n := input.Number("begin_season"); n != nil {
			if !n.IsInt64() || n.Int64() < 0 || n.Int64() > int64(^uint(0)>>1) {
				return request, bad("INVALID_SEASON", "原订阅季号超出范围")
			}
			season = int(n.Int64())
			meta.Set("begin_season", n)
		}
	}
	raw, e := meta.EncodeState()
	if e != nil {
		return request, e
	}
	request = SubscriptionRequest{Media: facts.Media, MediaType: facts.Media.MediaType, TMDBID: facts.Media.ID, Season: season, StartEpisode: 1, TargetScope: "season", State: "active", Schedule: defaultSchedule()}
	if facts.Media.MediaType == "tv" {
		request.TotalEpisodes = len(facts.Known[season])
		if request.TotalEpisodes == 0 {
			value, e := a.season(ctx, facts.Media.ID, season)
			if e != nil {
				return request, e
			}
			request.TotalEpisodes = len(value.Episodes)
		}
		if request.TotalEpisodes == 0 {
			return request, bad("SOURCE_SEASON_MISSING", "未获取到订阅季的总集数")
		}
	}
	params := torrentfilter.Parameters{}
	for _, field := range []struct {
		key   string
		value **string
	}{{"quality", &params.Quality}, {"resolution", &params.Resolution}, {"effect", &params.Effect}, {"include", &params.Include}, {"exclude", &params.Exclude}} {
		*field.value, e = sourceDefaultText(defaults[field.key])
		if e != nil {
			return request, e
		}
	}
	request.Policy.FilterParams = &params
	request.Policy.FilterGroups, e = sourceDefaultStrings(defaults["filter_groups"])
	if e != nil {
		return request, e
	}
	request.SiteIDs, e = sourceDefaultStrings(defaults["sites"])
	if e != nil {
		return request, e
	}
	downloader, e := sourceDefaultText(defaults["downloader"])
	if e != nil {
		return request, e
	}
	if downloader != nil && *downloader != "" {
		matches := 0
		for _, d := range a.Settings().Downloaders {
			if d.Name == *downloader || d.ID == *downloader {
				request.DownloaderID = d.ID
				matches++
			}
		}
		if matches != 1 {
			return request, bad("SOURCE_DOWNLOADER_MISSING", "原订阅默认下载器未能唯一匹配已配置实例")
		}
	}
	path, e := sourceDefaultText(defaults["save_path"])
	if e != nil {
		return request, e
	}
	if path != nil {
		request.DownloadPath = strings.TrimPrefix(*path, "local:")
	}
	request.source = &sourceSubscriptionOrigin{Version: 1, Channel: "飞书", Source: "Lark", UserID: user, Username: username, State: state, Note: note, Meta: raw, Defaults: defaults}
	return request, nil
}

func (a *App) larkSaveSourceSubscription(ctx context.Context, core *pluginLarkCoreExecution, request SubscriptionRequest) (Subscription, error) {
	return larkCoreWrite(ctx, a, core, "subscription.save", larkSourceSubscriptionInput(request), func(ctx context.Context) (Subscription, error) { return a.saveSubscription(ctx, "", request) })
}

func (a *App) larkCompleteSourceSubscription(ctx context.Context, core *pluginLarkCoreExecution, request SubscriptionRequest, subscription Subscription) error {
	if request.source == nil {
		return larkEvidenceError()
	}
	username, e := decodeSourceJSON(request.source.Username)
	if e != nil {
		return e
	}
	payload, e := larkCoreRead(ctx, a, core, "subscription.notice", struct {
		Origin *sourceSubscriptionOrigin
		Media  any
	}{request.source, request.Media}, func(context.Context) (pluginLarkBusinessNotice, error) {
		config := a.Settings()
		return pluginLarkBusinessNotice{Registration: request.source.Registration, Envelope: notice.Object{"channel": "飞书", "source": "Lark", "mtype": "订阅", "ctype": "subscribeAdded", "title": nil, "text": nil, "image": larkSourceMediaImage(request.Media, config.TMDBImageDomain), "link": sourceSubscriptionLink(config.AppDomain, request.Media.MediaType), "userid": request.source.UserID, "username": username}, Meta: request.source.Meta, Media: request.Media, MediaInfo: request.source.MediaInfo, ProviderInfo: request.source.ProviderInfo, Kwargs: notice.Object{"username": username}}, nil
	})
	if e != nil {
		return e
	}
	if e = a.larkPostBusinessNotice(ctx, core, payload); e != nil {
		return e
	}
	_, e = larkCoreWrite(ctx, a, core, "subscription.added", struct {
		Subscription string
		Username     json.RawMessage
		Notice       pluginLarkBusinessNotice
	}{subscription.ID, request.source.Username, payload}, func(ctx context.Context) (eventID string, resultErr error) {
		commitAttempted := false
		defer func() { resultErr = larkP0Failure(ctx, commitAttempted, resultErr) }()
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			return "", e
		}
		defer tx.Rollback()
		if _, _, e = a.larkCoreScopeTx(ctx, tx, core); e != nil {
			return "", e
		}
		if e = a.larkRequireBusinessNoticeTx(ctx, tx, core, payload); e != nil {
			return "", e
		}
		event := DomainEvent{EventID: domainEventID("subscription.added", core.inbox.ID+":"+strconv.Itoa(core.ordinal)+":"+subscription.ID), Type: "subscription.added", MediaID: mediaKey(subscription.Media), SubscriptionID: subscription.ID, Purpose: "subscription", OccurredAt: now()}
		if _, e = validateDomainEvent(event); e != nil {
			return "", e
		}
		snapshot := subscriptionAddedSnapshot{SubscriptionID: subscription.ID, Revision: subscription.Revision, Media: request.Media, Username: append(json.RawMessage(nil), request.source.Username...)}
		sealed, e := a.pluginStore.seal("subscription_added", "system", event.EventID, mustCleanSeedJSON(snapshot))
		if e != nil {
			return "", e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('subscription_added',?,?,?)`, event.EventID, sealed, now()); e != nil {
			return "", e
		}
		if e = recordDomainEventTx(ctx, tx, event); e != nil {
			return "", e
		}
		if e = a.enqueueLarkSubscriptionStatsTx(ctx, tx, core, subscription, payload, event.EventID); e != nil {
			return "", e
		}
		if e = a.larkP0CommitTx(ctx, tx, "subscription.added", event.EventID); e != nil {
			return "", e
		}
		commitAttempted = true
		return event.EventID, tx.Commit()
	})
	return e
}

func (a *App) larkSubscribeSelected(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, facts pluginLarkMediaTargets, user, chat string) error {
	best := s.Action == "ReSubscribe"
	request, e := a.larkSourceSubscriptionRequest(ctx, core, s, facts, user, "N", nil, &best)
	if e != nil {
		return e
	}
	saved, e := a.larkSaveSourceSubscription(ctx, core, request)
	if e != nil {
		return e
	}
	return a.larkCompleteSourceSubscription(ctx, core, request, saved)
}

func (a *App) larkSubscribeBatchRemainder(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, facts pluginLarkMediaTargets, result acquisitionbatch.Result) error {
	var note []int
	if len(result.Downloaded) > 0 && facts.Media.MediaType == "tv" {
		note = []int{}
		for _, download := range result.Downloaded {
			if len(download.Episodes) > 0 && download.Episodes[0] != 0 {
				note = append(note, download.Episodes[0])
			}
		}
	}
	request, e := a.larkSourceSubscriptionRequest(ctx, core, s, facts, s.User, "R", note, nil)
	if e != nil {
		return e
	}
	saved, e := a.larkSaveSourceSubscription(ctx, core, request)
	if e != nil {
		return e
	}
	return a.larkCompleteSourceSubscription(ctx, core, request, saved)
}
