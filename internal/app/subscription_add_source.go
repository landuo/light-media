package app

import (
	"context"
	"encoding/json"
	"strconv"

	"light-media/internal/catalog"
	"light-media/internal/notice"
	"light-media/internal/releasemeta"
)

// This producer models the source web/API async_add call. It is deliberately
// distinct from Lark's synchronous add plus sub_reg_async thread start.
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
	if (kind != "movie" && kind != "tv") || id <= 0 {
		return out, bad("INVALID_MEDIA", "请选择电影或电视剧")
	}
	recognized, e := a.sourceRecognizeTMDBMedia(ctx, catalog.Media{MediaType: kind, ID: id})
	if e != nil {
		return out, e
	}
	originalMedia := in.Media
	in.MediaType, in.TMDBID, in.Media = kind, id, recognized.Media
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
		if in.Season == 0 {
			in.Season = 1
		}
		season = in.Season
		meta.Set("begin_season", in.Season)
	}
	encoded, e := meta.EncodeState()
	if e != nil {
		return out, e
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
	if _, e = larkBusinessNoticeContext(business); e != nil {
		return out, e
	}
	locals := map[string]any{"name": name, "year": year, "type": sourceType, "season": season, "media_source": "themoviedb", "media_id": strconv.FormatInt(id, 10)}
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
