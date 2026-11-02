package app

// Existing ordinary-core facts and source projections, relocated without changing algorithms.
// Native message adapter scopes are not accepted as ordinary core mutation authority.
import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/catalog"
	"light-media/internal/jinja/exec"
	"light-media/internal/notice"
	"light-media/internal/noticetemplate"
	"light-media/internal/priorityrules"
	"light-media/internal/releasemeta"
	"light-media/internal/subscriptiondefaults"
	"light-media/internal/zhconv"
	"math/big"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var pluginFileEventIDPattern = regexp.MustCompile(`^(pfe|sfe|hfe)_[a-f0-9]{64}$`)

func validateDownloadFileDomainEvent(in DomainEvent) (DomainEvent, error) {
	invalid := func() (DomainEvent, error) {
		return in, bad("INVALID_DOMAIN_EVENT", "域事件契约无效，业务结果未提交")
	}
	if in.Type != "downloadfile.deleted" || in.SchemaVersion != 1 || in.Sequence != 0 || !domainEventIDPattern.MatchString(in.EventID) || !pluginFileEventIDPattern.MatchString(in.FileEventID) {
		return invalid()
	}
	if in.WorkflowID != "" || in.JobID != "" || in.MediaID != "" || in.IntentID != "" || in.NativeDownloadID != "" || in.AssetID != "" || in.SiteID != "" || in.SubscriptionID != "" || in.CausationID != "" || in.ParentJobID != "" {
		return invalid()
	}
	if in.Purpose == "" {
		in.Purpose = "file_deletion"
	}
	if in.Origin == "" {
		in.Origin = "core"
	}
	if in.SubjectID == "" {
		in.SubjectID = in.FileEventID
	}
	if in.SubjectKind == "" {
		in.SubjectKind = "file_event"
	}
	if in.Purpose != "file_deletion" || in.Origin != "core" || in.SubjectID != in.FileEventID || in.SubjectKind != "file_event" || in.EventID != domainEventID(in.Type, in.FileEventID) {
		return invalid()
	}
	if in.OccurredAt == "" {
		in.OccurredAt = now()
	}
	at, err := time.Parse(time.RFC3339Nano, in.OccurredAt)
	if err != nil {
		return invalid()
	}
	in.OccurredAt = at.UTC().Format(time.RFC3339Nano)
	return in, nil
}

func validateDownloadFileEventBackingTx(ctx context.Context, tx *sql.Tx, event DomainEvent) error {
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT file_event_id FROM plugin_download_file_events WHERE file_event_id=?`, event.FileEventID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return bad("FILE_EVENT_UNCOMMITTED", "文件删除事件缺少已提交的私有事实")
		}
		return err
	}
	return nil
}

func (a *App) larkSortCandidateMetas(ctx context.Context, candidates []Candidate, metas []json.RawMessage, media catalog.Media) ([]Candidate, []json.RawMessage, error) {
	if len(candidates) != len(metas) {
		return nil, nil, larkEvidenceError()
	}
	sorter, e := a.preparePrioritySorter(ctx, SearchRequest{priorityMedia: &media}, a.Settings())
	if e != nil {
		return nil, nil, e
	}
	type item struct {
		candidate Candidate
		meta      json.RawMessage
		key       string
	}
	items := make([]item, len(candidates))
	for i, candidate := range candidates {
		meta, e := releasemeta.DecodeMetaObjectState(metas[i])
		if e != nil {
			return nil, nil, e
		}
		seasons, episodes, e := larkMetaNumbers(meta)
		if e != nil {
			return nil, nil, e
		}
		r := candidate.Resource
		items[i] = item{candidate, metas[i], priorityrules.SortKey(priorityrules.SortInput{Title: media.Title, SiteOrder: sorter.orders[r.SiteID], Upload: sorter.uploads[sorter.names[r.SiteID]], Priority: r.PriorityOrder, Seeders: r.Seeders, Seasons: len(seasons), Episodes: len(episodes)}, sorter.priorities)}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].key > items[j].key })
	out, states := make([]Candidate, len(items)), make([]json.RawMessage, len(items))
	for i, item := range items {
		out[i], states[i] = item.candidate, item.meta
	}
	return out, states, nil
}

type sourceBusinessNotice struct {
	AbsentContext           bool            `json:",omitempty"`
	Registration            json.RawMessage `json:",omitempty"`
	Envelope                notice.Object
	Meta                    json.RawMessage
	Media                   catalog.Media
	MediaInfo, ProviderInfo json.RawMessage
	Kwargs                  notice.Object
}

func larkEvidenceError() error {
	return bad("LARK_EVIDENCE_MISSING", "Lark执行证据缺失或无法校验，未重新初始化或重发消息")
}

type sourceSubscriptionOrigin struct {
	SourceAddID                    string          `json:",omitempty"`
	Registration                   json.RawMessage `json:",omitempty"`
	Version                        int
	Channel, Source, UserID, State string
	Username                       json.RawMessage
	Note                           []int
	Meta                           json.RawMessage
	Defaults                       subscriptiondefaults.Object
	MediaInfo                      json.RawMessage `json:",omitempty"`
	ProviderInfo                   json.RawMessage `json:",omitempty"`
}

func (a *App) sourceSubscriptionOrigin(ctx context.Context, sub Subscription) (*sourceSubscriptionOrigin, error) {
	if sub.SourceContextSHA == "" {
		return nil, nil
	}
	var sealed []byte
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='source_subscription_origin' AND id=?`, sub.ID).Scan(&sealed); e != nil {
		return nil, e
	}
	raw, e := a.pluginStore.unseal("source_subscription_origin", "system", sub.ID, sealed)
	if e != nil {
		return nil, e
	}
	var origin sourceSubscriptionOrigin
	if cleanupJournalStrict(raw, &origin) != nil || origin.Version != 1 || tokenHash(string(mustCleanSeedJSON(origin))) != sub.SourceContextSHA {
		return nil, larkEvidenceError()
	}
	return &origin, nil
}

func larkSourceSubscriptionInput(in SubscriptionRequest) any {
	return struct {
		Request SubscriptionRequest
		Source  *sourceSubscriptionOrigin
	}{in, in.source}
}

func (a *App) saveSourceSubscriptionOriginTx(ctx context.Context, tx *sql.Tx, sub Subscription, origin *sourceSubscriptionOrigin, inputSHA string) error {
	if origin == nil {
		return nil
	}
	if sub.SourceContextSHA != tokenHash(string(mustCleanSeedJSON(origin))) {
		return bad("LARK_CORE_SCOPE_DENIED", "订阅新增缺少原消息来源授权")
	}
	if e := a.guardSourceSubscriptionMutationTx(ctx, tx, origin, inputSHA); e != nil {
		return e
	}
	sealed, e := a.pluginStore.seal("source_subscription_origin", "system", sub.ID, mustCleanSeedJSON(origin))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('source_subscription_origin',?,?,?)`, sub.ID, sealed, now())
	return e
}

func (a *App) guardSourceSubscriptionMutationTx(ctx context.Context, tx *sql.Tx, origin *sourceSubscriptionOrigin, inputSHA string) error {
	if origin != nil && origin.SourceAddID != "" {
		return a.guardUISubscriptionAddMutationTx(ctx, tx, origin, inputSHA)
	}
	// A source-owned UI subscription has its ordinary core authorization above.
	// The retired native Lark host cannot manufacture a core mutation scope.
	return bad("PLUGIN_ADAPTER_UNAVAILABLE", "消息来源尚未适配新的标准模块")
}

func (a *App) sourceRecognizeTMDBMedia(ctx context.Context, input catalog.Media) (sourceMediaInformation, error) {
	return a.originalRecognizeTMDBMedia(ctx, input, func() (sourceMediaInformation, error) { return a.sourceRecognizeTMDBCore(ctx, input) })
}

func (a *App) sourceRecognizeTMDBCore(ctx context.Context, input catalog.Media) (sourceMediaInformation, error) {
	policy, e := a.currentMediaCategoryPolicy()
	if e != nil {
		return sourceMediaInformation{}, e
	}
	raw, media, e := a.catalog().SourceInformation(catalog.OriginalPluginContext(ctx), input.MediaType, input.ID, policy.extraFields(input.MediaType))
	if e != nil {
		return sourceMediaInformation{}, e
	}
	category, e := policy.categoryWithError(media)
	if e != nil {
		return sourceMediaInformation{}, e
	}
	projection, e := sourceTMDBMediaInfo(raw, input.MediaType, a.Settings().TMDBImageDomain, category)
	if e != nil {
		return sourceMediaInformation{}, e
	}
	projected, e := decodeSourceJSON(projection)
	if e != nil {
		return sourceMediaInformation{}, e
	}
	media.Title, media.OriginalTitle = sourceText(sourceMap(projected)["title"]), sourceText(sourceMap(projected)["original_title"])
	return sourceMediaInformation{ProviderInfo: raw, MediaInfo: projection, Media: media}, nil
}

func (a *App) buildSubscriptionCompletionBusiness(ctx context.Context, s Subscription, subscribe json.RawMessage) (subscriptionCompletionSource, error) {
	return a.buildSubscriptionCompletionBusinessResolved(ctx, s, subscribe, nil)
}

func (a *App) buildSubscriptionCompletionBusinessResolved(ctx context.Context, s Subscription, subscribe json.RawMessage, resolved *standardSubscriptionRecognition) (subscriptionCompletionSource, error) {
	var out subscriptionCompletionSource
	value, e := decodeSourceJSON(subscribe)
	if e != nil || sourceMap(value) == nil {
		return out, notice.ErrTemplateDependency
	}
	source := sourceMap(value)
	if _, ok := source["name"].(string); !ok {
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
	if resolved == nil {
		resolved, e = a.resolveStandardSubscriptionRecognition(ctx, s, subscribe)
		if e != nil {
			return out, e
		}
	}
	encodedMeta, recognized := resolved.Meta, resolved.Information
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

func larkBusinessNoticeContext(payload pluginLarkBusinessNotice) (noticetemplate.ContextInput, error) {
	var out noticetemplate.ContextInput
	if payload.AbsentContext {
		// Original command messages have meta/mediainfo/torrentinfo/transferinfo
		// all None. This is a distinct trusted projection, never an empty or
		// fabricated TMDB MediaInfo. No partially supplied object is accepted.
		absent := func(raw json.RawMessage) bool {
			return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
		}
		if !absent(payload.Registration) || !absent(payload.Meta) || !absent(payload.MediaInfo) || !absent(payload.ProviderInfo) || !reflect.DeepEqual(payload.Media, catalog.Media{}) {
			return out, notice.ErrTemplateDependency
		}
		out.Extra = map[string]any{}
		for key, value := range payload.Kwargs {
			v, e := noticeV2NormalizeNumberValues(value)
			if e != nil {
				return out, e
			}
			out.Extra[key] = v
		}
		return out, nil
	}
	if len(payload.ProviderInfo) == 0 || len(payload.MediaInfo) == 0 {
		return out, notice.ErrTemplateDependency
	}
	meta, e := sourceNoticeMeta(payload.Meta)
	if e != nil {
		return out, e
	}
	value, e := noticeV2NormalizeNumberValues(meta)
	if e != nil {
		return out, e
	}
	out.Meta = value.(map[string]any)
	media, e := decodeSourceJSON(payload.MediaInfo)
	if e != nil || sourceMap(media) == nil || sourceMap(media)["source"] != "themoviedb" {
		return out, notice.ErrTemplateDependency
	}
	value, e = noticeV2NormalizeNumberValues(media)
	if e != nil {
		return out, e
	}
	out.Media = value.(map[string]any)
	id, ok := out.Media["tmdb_id"].(*big.Int)
	if !ok || !id.IsInt64() || id.Int64() != payload.Media.ID {
		return out, notice.ErrTemplateDependency
	}
	provider, e := decodeSourceJSON(payload.ProviderInfo)
	if e != nil {
		return out, e
	}
	// The source writes these dictionaries in provider-season order. Preserve
	// integer keys (including season 0) and overwrite an equal key in place.
	for _, field := range []string{"seasons", "season_years"} {
		dict := exec.NewDict()
		values := sourceMap(out.Media[field])
		for _, row := range sourceRows(sourceMap(provider)["seasons"]) {
			s := sourceMap(row)
			key, e := noticeV2NormalizeNumberValues(s["season_number"])
			if e != nil || key == nil {
				continue
			}
			text := exec.AsValue(key).String()
			value, present := values[text]
			if !present {
				continue
			}
			found := false
			for _, p := range dict.Pairs {
				if p.Key.EqualValueTo(exec.AsValue(key)) {
					p.Value, found = exec.AsValue(value), true
					break
				}
			}
			if !found {
				dict.Pairs = append(dict.Pairs, &exec.Pair{Key: exec.AsValue(key), Value: exec.AsValue(value)})
			}
		}
		out.Media[field] = dict
	}
	out.Extra = map[string]any{}
	for key, value := range payload.Kwargs {
		out.Extra[key], e = noticeV2NormalizeNumberValues(value)
		if e != nil {
			return out, e
		}
	}
	return out, nil
}

//go:embed defaults/source-mediainfo.json
var sourceMediaInfoDefaults []byte

type sourceMediaInformation struct {
	ProviderInfo json.RawMessage
	MediaInfo    json.RawMessage
	Media        catalog.Media
}

func sourceRows(value any) []any { out, _ := value.([]any); return out }

func sourceText(value any) string { out, _ := value.(string); return out }

func sourceHasChinese(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

func sourceImagePath(path any, domain string) (any, error) {
	if !sourceSubscriptionTruth(path) {
		return nil, nil
	}
	value, ok := path.(string)
	if !ok {
		return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "原TMDB图片字段类型无法使用")
	}
	if domain == "" {
		domain = "image.tmdb.org"
	}
	return "https://" + domain + "/t/p/original/" + strings.TrimLeft(value, "/"), nil
}

// sourceTMDBProviderInfo 保留原 get_info 的详情增强，供识别消歧前使用。
// 与 MediaInfo 构造分离；旧 sourceTMDBMediaInfo 仍依次执行两阶段。
func sourceTMDBProviderInfo(raw json.RawMessage, kind string) (json.RawMessage, error) {
	value, e := decodeSourceJSON(raw)
	if e != nil || sourceMap(value) == nil || kind != "movie" && kind != "tv" {
		return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "未获取原TMDB媒体详情")
	}
	info := sourceMap(value)
	mediaType, titleKey, originalKey := "电影", "title", "original_title"
	if kind == "tv" {
		mediaType, titleKey, originalKey = "电视剧", "name", "original_name"
	}
	info["media_type"] = mediaType
	genres := []any{}
	for _, row := range sourceRows(info["genres"]) {
		genres = append(genres, sourceMap(row)["id"])
	}
	info["genre_ids"] = genres
	alternativeKey := "titles"
	if kind == "tv" {
		alternativeKey = "results"
	}
	names, seen := []any{}, map[string]bool{}
	addName := func(value any) {
		if s, ok := value.(string); ok && s != "" && !seen[s] {
			seen[s], names = true, append(names, s)
		}
	}
	for _, row := range sourceRows(sourceMap(info["alternative_titles"])[alternativeKey]) {
		addName(sourceMap(row)["title"])
	}
	translations := sourceRows(sourceMap(info["translations"])["translations"])
	for _, row := range translations {
		addName(sourceMap(sourceMap(row)["data"])[titleKey])
	}
	info["names"] = names
	translation := func(region string) any {
		for _, row := range translations {
			item := sourceMap(row)
			if item["iso_3166_1"] == region {
				return sourceMap(item["data"])[titleKey]
			}
		}
		return nil
	}
	original := info[originalKey]
	enTitle := translation("US")
	if info["original_language"] == "en" || !sourceSubscriptionTruth(enTitle) {
		enTitle = original
	}
	info["en_title"], info["hk_title"], info["tw_title"] = enTitle, translation("HK"), translation("TW")
	info["sg_title"] = translation("SG")
	if !sourceSubscriptionTruth(info["sg_title"]) {
		info["sg_title"] = original
	}
	if !sourceHasChinese(sourceText(info[titleKey])) {
		selectedCN := false
		for _, row := range sourceRows(sourceMap(info["alternative_titles"])[alternativeKey]) {
			item := sourceMap(row)
			if item["iso_3166_1"] == "CN" {
				if number, numeric := item["title"].(json.Number); numeric {
					// JSON float truth follows binary64 (including underflow to
					// zero); every truthy numeric title reaches the source regex
					// with a non-string and raises an ordinary TypeError.
					value, _ := number.Float64()
					if value != 0 {
						return nil, &originalMediaRecognitionSourceError{Class: "TypeError"}
					}
				}
			}
			title := sourceText(item["title"])
			if item["iso_3166_1"] == "CN" && sourceHasChinese(title) {
				converted, err := zhconv.Hans(title)
				if err != nil {
					return nil, err
				}
				// The source accepts an already-Hans alias, not its converted
				// replacement. Preserve the first qualifying original title.
				if converted == title {
					info[titleKey], selectedCN = title, true
					break
				}
			}
		}
		if !selectedCN && sourceHasChinese(sourceText(info["sg_title"])) && info["sg_title"] != info[titleKey] {
			info[titleKey] = info["sg_title"]
		}
	}
	ratings := map[string]any{}
	releases := sourceRows(sourceMap(info["release_dates"])["results"])
	if len(releases) > 0 {
		for _, row := range releases {
			item := sourceMap(row)
			dates := sourceRows(item["release_dates"])
			if region := sourceText(item["iso_3166_1"]); region != "" && len(dates) > 0 && sourceSubscriptionTruth(sourceMap(dates[0])["certification"]) {
				ratings[region] = sourceMap(dates[0])["certification"]
			}
		}
	} else {
		for _, row := range sourceRows(sourceMap(info["content_ratings"])["results"]) {
			item := sourceMap(row)
			if region := sourceText(item["iso_3166_1"]); region != "" && sourceSubscriptionTruth(item["rating"]) {
				ratings[region] = item["rating"]
			}
		}
	}
	info["content_rating"] = ratings["CN"]
	if !sourceSubscriptionTruth(info["content_rating"]) {
		info["content_rating"] = ratings["US"]
	}
	if !sourceSubscriptionTruth(info["id"]) {
		return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "原TMDB媒体详情缺少身份")
	}
	encoded, e := json.Marshal(info)
	if e != nil {
		return nil, e
	}
	return encoded, nil
}

func sourceTMDBMediaInfo(raw json.RawMessage, kind, domain, category string) (json.RawMessage, error) {
	encoded, e := sourceTMDBProviderInfo(raw, kind)
	if e != nil {
		return nil, e
	}
	return sourceTMDBRecognitionModel(encoded, kind, domain, category)
}

// sourceTMDBRecognitionModel 在类型消歧之后构造完整模型，保留旧后处理。
func sourceTMDBRecognitionModel(encoded json.RawMessage, kind, domain, category string) (json.RawMessage, error) {
	model, e := sourceTMDBMediaModel(encoded, kind, domain, category)
	if e != nil {
		return nil, e
	}
	value, e := decodeSourceJSON(model)
	if e != nil {
		return nil, e
	}
	fields := sourceMap(value)
	fields["recognize_cache_hit"] = false
	fields["poster"], fields["backdrop"] = "", ""
	for target, source := range map[string]string{"poster": "poster_path", "backdrop": "backdrop_path"} {
		if path, ok := fields[source].(string); ok && path != "" {
			fields[target] = strings.ReplaceAll(path, "original", "w500")
		}
	}
	return mustCleanSeedJSON(fields), nil
}

func sourceJSONType(value any) string {
	switch value := value.(type) {
	case json.Number:
		if strings.ContainsAny(string(value), ".eE") {
			return "float"
		}
		return "int"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func sourceNoticeMeta(raw json.RawMessage) (noticetemplate.Object, error) {
	meta, e := releasemeta.DecodeMetaObjectState(raw)
	if e != nil {
		return nil, e
	}
	projection := noticetemplate.Object{}
	// All nullable MetaBase class attributes exist even when no instance value
	// was assigned. Derived properties are evaluated by the actual native port.
	for _, key := range []string{"org_string", "subtitle", "cn_name", "en_name", "original_name", "year", "begin_season", "end_season", "begin_episode", "end_episode", "part", "resource_type", "resource_effect", "resource_pix", "resource_team", "customization", "web_source", "video_encode", "video_bit", "audio_encode", "apply_words", "tmdbid", "doubanid", "bangumiid", "anilistid", "media_source", "media_id", "episode_group", "fps", "isfile", "title", "type", "total_season", "total_episode"} {
		projection[key] = meta.Get(key)
	}
	for key, value := range meta.Instance {
		projection[key] = value
	}
	derived, e := meta.Derived()
	if e != nil {
		return nil, e
	}
	for key, value := range derived {
		projection[key] = value
	}
	return projection, nil
}

func noticeV2NormalizeNumberValues(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch v := value.(type) {
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			n, ok := new(big.Int).SetString(string(v), 10)
			if !ok {
				return nil, notice.ErrTemplateDependency
			}
			return n, nil
		}
		return strconv.ParseFloat(string(v), 64)
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			value, e := noticeV2NormalizeNumberValues(item)
			if e != nil {
				return nil, e
			}
			out[key] = value
		}
		return out, nil
	case *big.Int:
		return new(big.Int).Set(v), nil
	}
	r := reflect.ValueOf(value)
	if r.Kind() == reflect.Slice {
		out := []any{}
		for i := 0; i < r.Len(); i++ {
			item, e := noticeV2NormalizeNumberValues(r.Index(i).Interface())
			if e != nil {
				return nil, e
			}
			out = append(out, item)
		}
		return out, nil
	}
	return value, nil
}

type pluginLarkBusinessNotice = sourceBusinessNotice

func sourceBusinessProjection(payload sourceBusinessNotice) string {
	if payload.AbsentContext {
		return "absent-source-context/v1"
	}
	return "tmdb-source-attributes/v1"
}

const reviewedStatisticSourceSHA = "763a88be85185a94082a2f131ed1c80d8306abe24b10ebe9ae95447cfe5da17d"

const reviewedStatisticConfigSHA = "2ec13ebdddbe2e5a7595cbd744552c016e45e989516a48f53be27f84e9de253f"

const reviewedStatisticCatalogSHA = "b27625bd4cdfbcc188683874968786eb4e1dc548fa4ffa1171ccfb9db3e331a1"

const reviewedStatisticAccountsSHA = "fac3972705d612955dcd77294a90dc920e35ec362b1021dd52bc3fadb4e3031e"

type reviewedStatisticSite struct {
	OriginalID int64  `json:"original_source_id"`
	URL        string `json:"url"`
	Domain     string `json:"domain"`
}

func reviewedStatisticFingerprint() string {
	return tokenHash("SiteStatistic/1.9.7/reviewed-source/v1/" + reviewedStatisticSourceSHA + "/" + reviewedStatisticConfigSHA + "/" + reviewedStatisticCatalogSHA + "/" + reviewedStatisticAccountsSHA)
}

func validSourceAccountBinding(b sourceAccountBinding) bool {
	if b.Version != 1 || b.ConfigSHA != reviewedStatisticConfigSHA || b.DataSHA != reviewedStatisticAccountsSHA || b.CatalogSHA != reviewedStatisticCatalogSHA || b.SourceFingerprint != reviewedStatisticFingerprint() || !pluginStorageDigest.MatchString(b.SettingsSHA) || len(b.Sites) != 17 || len(b.Rows) != 2133 {
		return false
	}
	catalog, ids, domains := []reviewedStatisticSite{}, map[string]bool{}, map[string]bool{}
	var previous int64
	for _, site := range b.Sites {
		_, origin, _, e := siteImportOrigin(site.URL)
		if e != nil || origin != site.Origin || site.SiteID == "" || site.NativeID < 1 || site.OriginalID <= previous || site.Domain == "" || ids[site.SiteID] || domains[site.Domain] {
			return false
		}
		previous, ids[site.SiteID], domains[site.Domain] = site.OriginalID, true, true
		catalog = append(catalog, reviewedStatisticSite{site.OriginalID, site.URL, site.Domain})
	}
	if tokenHash(string(reviewedIYUUJSON(catalog))) != b.CatalogSHA {
		return false
	}
	previous = 0
	for _, h := range b.Rows {
		if h.ID <= previous || h.Domain == "" || h.Day == "" || h.Time == "" || !pluginStorageDigest.MatchString(h.SHA) {
			return false
		}
		previous = h.ID
	}
	return true
}

func reviewedIYUUJSON(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
