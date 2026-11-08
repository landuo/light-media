package app

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/notice"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

// The source web/API async_add and ordinary chat add share a durable save,
// notice and event. Their recognition methods and statistics timing differ.
type subscriptionAddIdentity struct{ ID, JobID, InputSHA string }
type subscriptionAddInput struct {
	Version      int
	Request      SubscriptionRequest
	Origin       sourceSubscriptionOrigin
	Business     json.RawMessage
	Registration []byte
	Username     json.RawMessage
}
type subscriptionAddReceipt struct {
	Version      int
	Identity     subscriptionAddIdentity
	Input        subscriptionAddInput
	Stage        string
	Existing     bool            `json:",omitempty"`
	Saved        *Subscription   `json:",omitempty"`
	NoticeResult json.RawMessage `json:",omitempty"`
	StatisticsID string          `json:",omitempty"`
	EventID      string          `json:",omitempty"`
}
type subscriptionAddExecution struct {
	a        *App
	Identity subscriptionAddIdentity
	Job      Job
}
type subscriptionAddExecutionKey struct{}

func subscriptionAddEvidenceError() error {
	return bad("SUBSCRIPTION_ADD_UNCONFIRMED", "订阅新增的原始请求、保存结果或通知记录无法核对")
}

func (a *App) buildUISubscriptionAdd(ctx context.Context, in SubscriptionRequest, username string) (subscriptionAddInput, error) {
	return a.buildSourceSubscriptionAdd(ctx, in, username, nil)
}

func (a *App) buildSourceSubscriptionAdd(ctx context.Context, in SubscriptionRequest, username string, chat *sourceSubscriptionOrigin) (subscriptionAddInput, error) {
	var out subscriptionAddInput
	if in.source != nil {
		return out, subscriptionAddEvidenceError()
	}
	kind, id := in.MediaType, in.TMDBID
	if kind == "" {
		kind = in.Media.MediaType
	}
	if id == 0 {
		id = in.Media.ID
	}
	provider, providerID, err := subscriptionProviderIdentity(in)
	if err != nil {
		return out, err
	}
	if provider == "themoviedb" {
		explicit, err := strconv.ParseInt(providerID, 10, 64)
		if err != nil || id != 0 && id != explicit {
			return out, bad("MEDIA_SOURCE_CHANGED", "订阅媒体来源与 TMDB 编号不一致")
		}
		id, provider, providerID = explicit, "", ""
	}
	if (kind != "movie" && kind != "tv") || id < 0 || id == 0 && provider == "" && (chat == nil || !chat.MediaProvider.sourceOnly()) {
		return out, bad("INVALID_MEDIA", "请选择电影或电视剧")
	}
	originalMedia := in.Media
	var e error
	// The existing native UI sends these source locals in Media; explicit name
	// and year fields are the source-compatible API spellings. A genuinely
	// absent local remains None, even when recognition finds a title/year.
	var name, year any
	if len(in.SourceName) > 0 {
		name, e = decodeSourceJSON(in.SourceName)
		if e != nil {
			return out, e
		}
	} else if originalMedia.Title != "" {
		name = originalMedia.Title
	}
	if len(in.SourceYear) > 0 {
		year, e = decodeSourceJSON(in.SourceYear)
		if e != nil {
			return out, e
		}
	} else if originalMedia.Year != 0 {
		year = strconv.Itoa(originalMedia.Year)
	}
	if name != nil {
		if _, ok := name.(string); !ok {
			return out, bad("INVALID_SUBSCRIPTION_NAME", "影视名称须为文本或空值")
		}
	}
	if year != nil {
		if _, ok := year.(string); !ok {
			return out, bad("INVALID_SUBSCRIPTION_YEAR", "影视年份须为文本或空值")
		}
	}
	options, e := a.globalRecognitionOptions(ctx)
	if e != nil {
		return out, e
	}
	parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if e != nil {
		return out, e
	}
	text, _ := name.(string)
	meta, e := parser.ParseTitle(text, nil, nil)
	if e != nil {
		return out, e
	}
	if name == nil {
		meta.Set("org_string", nil)
	}
	if sourceSubscriptionTruth(year) {
		meta.Set("year", year)
	}
	sourceType := "电影"
	if kind == "tv" {
		sourceType = "电视剧"
	}
	meta.Set("type", sourceType)
	var season any
	if kind == "tv" {
		specified := in.Season != 0
		if in.Season == 0 {
			in.Season = 1
		}
		season = in.Season
		if specified || chat == nil {
			meta.Set("begin_season", in.Season)
		}
	}
	encoded, e := meta.EncodeState()
	if e != nil {
		return out, e
	}
	metaFields, e := meta.ToDict()
	if e != nil {
		return out, e
	}
	identity := catalog.Media{MediaType: kind, ID: id}
	var recognized sourceMediaInformation
	if id == 0 || provider != "" {
		recognized, e = a.recognizeSubscriptionProviderAdd(ctx, kind, provider, providerID, metaFields, chat != nil)
	} else if chat == nil {
		recognized, e = a.sourceAsyncRecognizeTMDBMedia(ctx, identity, metaFields)
	} else {
		recognized, e = a.originalRecognizeTMDBModule(ctx, identity, "recognize_media", metaFields, strconv.FormatInt(id, 10), nil, false, func() (sourceMediaInformation, error) {
			return a.sourceRecognizeTMDBCore(ctx, identity)
		})
	}
	if e != nil {
		return out, e
	}
	id = recognized.Media.ID
	in.MediaType, in.TMDBID, in.Media = kind, id, recognized.Media
	if id == 0 {
		in.MediaSource, in.MediaID = in.Media.ProviderSource, in.Media.ProviderID
	}
	if e = a.applySubscriptionQualityDefaults(ctx, &in, kind); e != nil {
		return out, e
	}
	if in.qualityDefaults == nil {
		return out, subscriptionAddEvidenceError()
	}
	fieldsValue, e := decodeSourceJSON(recognized.MediaInfo)
	if e != nil {
		return out, e
	}
	fields := sourceMap(fieldsValue)
	if fields == nil {
		return out, notice.ErrTemplateDependency
	}
	if kind == "tv" && in.TotalEpisodes == 0 {
		current := len(sourceRows(sourceMap(fields["seasons"])[strconv.Itoa(in.Season)]))
		total, err := a.applyStandardSubscriptionEpisodes(ctx, current, in.Season, recognized.MediaInfo, nil, "create")
		if err != nil {
			return out, err
		}
		if total == 0 {
			return out, bad("INVALID_TOTAL_EPISODES", "未获取到当前季总集数")
		}
		in.TotalEpisodes = total
		if in.ManualTotalEpisode == nil {
			automatic := false
			in.ManualTotalEpisode = &automatic
		}
	}
	var alias *originalChatMediaProvider
	if chat != nil && chat.MediaProvider != nil && !chat.MediaProvider.sourceOnly() {
		if !chat.MediaProvider.valid(id) {
			return out, bad("MEDIA_SOURCE_CHANGED", "所选媒体与已核对的来源身份不一致")
		}
		alias = chat.MediaProvider
	}
	if chat != nil || id == 0 {
		enriched, e := a.originalTransferObtainImages(ctx, recognized.MediaInfo)
		if e != nil {
			return out, e
		}
		recognized.MediaInfo, e = originalPublicCatalogMedia(enriched)
		if e != nil {
			return out, e
		}
		recognized.MediaInfo, e = originalChatAttachProvider(recognized.MediaInfo, alias, id)
		if e != nil {
			return out, e
		}
		value, err := decodeSourceJSON(recognized.MediaInfo)
		if err != nil {
			return out, err
		}
		fields = sourceMap(value)
		if fields == nil {
			return out, notice.ErrTemplateDependency
		}
		fields["poster"] = strings.ReplaceAll(sourceText(fields["poster_path"]), "original", "w500")
		fields["backdrop"] = strings.ReplaceAll(sourceText(fields["backdrop_path"]), "original", "w500")
		recognized.MediaInfo = mustCleanSeedJSON(fields)
	}
	image := fields["backdrop"]
	if !sourceSubscriptionTruth(image) {
		image = fields["poster"]
	}
	user := mustCleanSeedJSON(username)
	state := "N"
	if in.State == "paused" {
		state = "S"
	}
	origin := sourceSubscriptionOrigin{Version: 1, State: state, Username: user, Meta: encoded, Defaults: in.qualityDefaults, MediaInfo: recognized.MediaInfo, ProviderInfo: recognized.ProviderInfo}
	business := sourceBusinessNotice{Envelope: notice.Object{"channel": nil, "source": nil, "mtype": "订阅", "ctype": "subscribeAdded", "image": image, "link": sourceSubscriptionLink(a.Settings().AppDomain, kind), "userid": nil, "username": username}, Meta: encoded, Media: recognized.Media, MediaInfo: recognized.MediaInfo, ProviderInfo: recognized.ProviderInfo, Kwargs: notice.Object{"username": username}}
	if chat != nil {
		origin.Channel, origin.Source, origin.UserID = chat.Channel, chat.Source, chat.UserID
		origin.MediaProvider = alias
		origin.State, origin.Note = chat.State, sourceSubscriptionNoteClone(chat.Note)
		business.Envelope["channel"], business.Envelope["source"], business.Envelope["userid"] = chat.Channel, chat.Source, chat.UserID
	}
	if _, e = larkBusinessNoticeContext(business); e != nil {
		return out, e
	}
	registrationSource, registrationID := "themoviedb", strconv.FormatInt(id, 10)
	if id == 0 {
		registrationSource, registrationID = in.Media.ProviderSource, in.Media.ProviderID
	}
	locals := map[string]any{"name": name, "year": year, "type": sourceType, "season": season, "media_source": registrationSource, "media_id": registrationID}
	for target, source := range map[string]string{"tmdbid": "tmdb_id", "imdbid": "imdb_id", "tvdbid": "tvdb_id", "doubanid": "douban_id", "bangumiid": "bangumi_id", "anilistid": "anilist_id", "poster": "poster", "backdrop": "backdrop", "vote": "vote_average", "description": "overview"} {
		v, ok := fields[source]
		if !ok {
			return out, notice.ErrTemplateDependency
		}
		locals[target] = v
	}
	raw, e := cleanupJournalJSON(locals)
	if e != nil {
		return out, e
	}
	return subscriptionAddInput{Version: 1, Request: in, Origin: origin, Business: mustCleanSeedJSON(business), Registration: raw, Username: user}, nil
}

func (a *App) recognizeSubscriptionProviderAdd(ctx context.Context, kind, source, providerID string, meta map[string]any, chat bool) (sourceMediaInformation, error) {
	method := "async_recognize_media"
	if chat {
		method = "recognize_media"
	}
	request := plugins.MediaRecognizeRequest{Type: kind, Meta: meta, Source: source, MediaID: providerID}
	if providerID != "" {
		fresh := false
		request.Cache = &fresh
	}
	raw, err := a.originalHostMediaRecognizeMethod(ctx, method, request)
	if err != nil {
		return sourceMediaInformation{}, err
	}
	if !originalModuleResultTruthy(raw) && providerID != "" {
		// An explicitly chosen provider keeps ownership of its title fallback.
		request.MediaID, request.Cache = "", nil
		raw, err = a.originalHostMediaRecognizeMethod(ctx, method, request)
		if err != nil {
			return sourceMediaInformation{}, err
		}
	}
	if !originalModuleResultTruthy(raw) {
		return sourceMediaInformation{}, bad("MEDIA_NOT_FOUND", "未识别到媒体信息")
	}
	view, err := originalCatalogMediaProjection(raw)
	if err != nil || view.MediaType != kind || source != "" && (view.Source != source || view.SourceID != providerID) {
		return sourceMediaInformation{}, bad("MEDIA_SOURCE_CHANGED", "订阅媒体来源身份无法核对")
	}
	if view.Source == "themoviedb" && validMedia(kind, view.ID) {
		return a.originalRecognitionProjection(view.Media, raw)
	}
	info := sourceMediaInformation{Media: view.Media, MediaInfo: raw, ProviderInfo: raw}
	info.Media, err = subscriptionProviderMedia(info, view.Source, view.SourceID, kind)
	return info, err
}
