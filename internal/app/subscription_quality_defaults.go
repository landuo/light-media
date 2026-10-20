package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"light-media/internal/subscriptiondefaults"
	"light-media/internal/torrentfilter"
	"math/big"
	"strconv"
)

func qualityJSONValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if e := decoder.Decode(&value); e != nil {
		return nil, e
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, bad("INVALID_SUBSCRIPTION_DEFAULTS", "订阅默认设置不是单个JSON值")
	}
	return value, nil
}
func qualityJSONTruth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		n, ok := new(big.Rat).SetString(string(x))
		return !ok || n.Sign() != 0
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}
func qualityDefaultStrings(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	if values, ok := value.([]string); ok {
		return append([]string{}, values...), nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, bad("INVALID_SUBSCRIPTION_DEFAULTS", "订阅默认规则组须为字符串列表")
	}
	out := []string{}
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, bad("INVALID_SUBSCRIPTION_DEFAULTS", "订阅默认规则组须为字符串列表")
		}
		out = append(out, text)
	}
	return out, nil
}
func (a *App) applySubscriptionQualityDefaults(ctx context.Context, in *SubscriptionRequest, kind string) error {
	defaults := in.qualityDefaults
	if defaults == nil {
		environment := subscriptiondefaults.Object{}
		var sealed []byte
		e := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='source_subscription_environment' AND id='main'`).Scan(&sealed)
		if e == nil {
			raw, e := a.pluginStore.unseal("source_subscription_environment", "system", "main", sealed)
			if e != nil {
				return e
			}
			value, e := qualityJSONValue(raw)
			if e != nil {
				return e
			}
			var ok bool
			environment, ok = value.(map[string]any)
			if !ok {
				return bad("INVALID_SUBSCRIPTION_DEFAULTS", "原订阅环境默认设置不是对象")
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		settings := a.Settings()
		provider := subscriptiondefaults.Provider{Environment: environment, ReadDatabase: func(key string) (any, error) {
			if key == "DefaultMovieSubscribeConfig" {
				return qualityJSONValue(settings.DefaultMovieSubscribeConfig)
			}
			return qualityJSONValue(settings.DefaultTVSubscribeConfig)
		}}
		kwargs := subscriptiondefaults.Object{"sites": in.SiteIDs, "downloader": in.DownloaderID, "save_path": in.DownloadPath, "filter_groups": in.Policy.FilterGroups}
		params := in.Policy.FilterParams
		if params == nil {
			params = &torrentfilter.Parameters{}
		}
		for _, field := range []struct {
			key    string
			value  *string
			legacy string
		}{{"quality", params.Quality, in.Policy.Source}, {"resolution", params.Resolution, in.Policy.Resolution}, {"effect", params.Effect, in.Policy.HDR}, {"include", params.Include, in.Policy.Include}, {"exclude", params.Exclude, in.Policy.Exclude}} {
			kwargs[field.key] = field.legacy
			if field.value != nil {
				kwargs[field.key] = *field.value
			}
		}
		if in.BestVersion != nil {
			kwargs["best_version"] = *in.BestVersion
		}
		if in.BestVersionFull != nil {
			kwargs["best_version_full"] = *in.BestVersionFull
		}
		if in.SearchIMDbID != nil {
			kwargs["search_imdbid"] = *in.SearchIMDbID
		}
		typeName := "电影"
		if kind == "tv" {
			typeName = "电视剧"
		}
		defaults, e = provider.Apply(typeName, kwargs)
		if e != nil {
			return e
		}
	}
	best, full, imdb := qualityJSONTruth(defaults["best_version"]), qualityJSONTruth(defaults["best_version_full"]), qualityJSONTruth(defaults["search_imdbid"])
	in.BestVersion, in.BestVersionFull, in.SearchIMDbID = &best, &full, &imdb
	params := in.Policy.FilterParams
	if params == nil {
		params = &torrentfilter.Parameters{}
	} else {
		copy := *params
		params = &copy
	}
	for _, field := range []struct {
		key string
		dst **string
	}{{"quality", &params.Quality}, {"resolution", &params.Resolution}, {"effect", &params.Effect}, {"include", &params.Include}, {"exclude", &params.Exclude}} {
		if value := defaults[field.key]; value != nil {
			text, ok := value.(string)
			if !ok {
				return bad("INVALID_SUBSCRIPTION_DEFAULTS", "订阅默认筛选字段须为字符串")
			}
			*field.dst = &text
		}
	}
	if params.Quality != nil || params.Resolution != nil || params.Effect != nil || params.Include != nil || params.Exclude != nil {
		in.Policy.FilterParams = params
	}
	if len(in.Policy.FilterGroups) == 0 {
		groups, e := qualityDefaultStrings(defaults["filter_groups"])
		if e != nil {
			return e
		}
		in.Policy.FilterGroups = groups
	}
	if in.DownloadPath == "" && qualityJSONTruth(defaults["save_path"]) {
		text, ok := defaults["save_path"].(string)
		if !ok {
			return bad("INVALID_SUBSCRIPTION_DEFAULTS", "订阅默认保存路径须为字符串")
		}
		in.DownloadPath = text
	}
	if in.DownloaderID == "" && qualityJSONTruth(defaults["downloader"]) {
		name, ok := defaults["downloader"].(string)
		if !ok {
			return bad("INVALID_SUBSCRIPTION_DEFAULTS", "订阅默认下载器须为名称")
		}
		for _, d := range a.Settings().Downloaders {
			if d.Name == name {
				in.DownloaderID = d.ID
				break
			}
		}
		if in.DownloaderID == "" {
			return bad("DEFAULT_DOWNLOADER_MISSING", "原订阅默认下载器尚未配置")
		}
	}
	if len(in.SiteIDs) == 0 && qualityJSONTruth(defaults["sites"]) {
		values, ok := defaults["sites"].([]any)
		if !ok {
			return bad("INVALID_SUBSCRIPTION_DEFAULTS", "原订阅默认站点须为来源编号列表")
		}
		for _, value := range values {
			id, e := strconv.ParseInt(fmt.Sprint(value), 10, 64)
			if e != nil || id < 1 {
				return bad("INVALID_SUBSCRIPTION_DEFAULTS", "原订阅默认站点编号无效")
			}
			identity, e := a.Store.readNativeSiteIdentity(ctx, a.Store.DB, "source_id", id)
			if e != nil {
				return e
			}
			if identity.State != "active" {
				return bad("DEFAULT_SITE_MISSING", "原订阅默认站点已删除")
			}
			in.SiteIDs = append(in.SiteIDs, identity.SiteID)
		}
	}
	in.qualityDefaults = defaults
	return nil
}
