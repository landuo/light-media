package subscribegroup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/removelink"
)

const downloadGroupTV = "电视剧"

type groupDownloadContext struct {
	Meta *struct {
		Resolution    string `json:"resource_pix"`
		Quality       string `json:"resource_type"`
		Effect        string `json:"resource_effect"`
		Team          string `json:"resource_team"`
		Customization string `json:"customization"`
	} `json:"meta_info"`
	Torrent *struct {
		Site json.RawMessage `json:"site"`
	} `json:"torrent_info"`
}

type groupDownloadSubscription struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Resolution string            `json:"resolution"`
	Quality    string            `json:"quality"`
	Effect     string            `json:"effect"`
	Include    string            `json:"include"`
	Sites      []json.RawMessage `json:"sites"`
}

func downloadNotice(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller, enabled bool, updateDetails []string, now time.Time) error {
	logger := sdk.Logger{Host: caller}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		_ = logger.Log(ctx, sdk.LogError, "下载事件数据为空")
		return nil
	}
	if !enabled || len(updateDetails) == 0 {
		return nil
	}
	var event sdk.MutableEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	var data struct {
		Hash    string          `json:"hash"`
		Context json.RawMessage `json:"context"`
	}
	if len(event.Data) != 0 {
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return err
		}
	}
	var contextValue any
	if len(data.Context) != 0 {
		var err error
		contextValue, err = removelink.Decode(data.Context)
		if err != nil {
			return err
		}
	}
	if data.Hash == "" || !removelink.Truth(contextValue) {
		// The source Context can contain private torrent and site credentials.
		_ = logger.Log(ctx, sdk.LogError, "下载事件数据不完整")
		return nil
	}
	history, err := (sdk.History{Host: caller}).Download(ctx, sdk.DownloadHistoryRequestV1{Hash: data.Hash, SourceProjection: true})
	if err != nil {
		return err
	}
	download := history.Item
	if download == nil {
		_ = logger.Log(ctx, sdk.LogWarning, "种子对应下载记录不存在")
		return nil
	}
	handledKey := download.Type + ":" + strconv.FormatInt(download.TMDBID, 10)
	historyKey := "history_handle"
	handledRaw, err := (sdk.Base{Host: caller}).GetData(ctx, &historyKey)
	if err != nil {
		return err
	}
	alreadyHandled, err := sourceDownloadHandled(handledRaw, handledKey)
	if err != nil {
		return err
	}
	if alreadyHandled {
		_ = logger.Log(ctx, sdk.LogWarning, fmt.Sprintf("下载历史:%s 已处理过，不再重复处理", download.Title))
		return nil
	}
	if download.Type != downloadGroupTV {
		_ = logger.Log(ctx, sdk.LogWarning, fmt.Sprintf("下载历史:%s 不是电视剧，不进行官组填充", download.Title))
		return nil
	}
	season, err := groupDownloadSeason(download.Seasons)
	if err != nil {
		return err
	}
	if download.TMDBID == 0 {
		_ = logger.Log(ctx, sdk.LogWarning, fmt.Sprintf("下载历史:%s tmdbid:None 对应订阅记录不存在", download.Title))
		return nil
	}
	subscriptions := sdk.Subscriptions{Host: caller}
	result, err := subscriptions.Read(ctx, sdk.SubscriptionHostRequestV1{TMDBID: &download.TMDBID, Season: season, WithRevision: true})
	if err != nil {
		return err
	}
	if len(result.Items) == 0 {
		_ = logger.Log(ctx, sdk.LogWarning, fmt.Sprintf("下载历史:%s tmdbid:%d 对应订阅记录不存在", download.Title, download.TMDBID))
		return nil
	}
	seasonText := "None"
	if season != nil {
		seasonText = strconv.Itoa(*season)
	}
	_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("获取到tmdbid %d season %s 订阅记录:%d 个", download.TMDBID, seasonText, len(result.Items)))

	var downloadContext *groupDownloadContext
	var sourceSite json.RawMessage
	var siteID string
	siteResolved := false
	updates := make([]sdk.SubscriptionHostUpdateV1, 0, len(result.Items))
	otherTypes, unchanged := 0, 0
	for _, item := range result.Items {
		var sub groupDownloadSubscription
		if err := json.Unmarshal(item, &sub); err != nil {
			return err
		}
		if sub.Type != downloadGroupTV {
			otherTypes++
			if len(result.Items) == 1 {
				_ = logger.Log(ctx, sdk.LogWarning, fmt.Sprintf("订阅记录:%s 不是电视剧，不进行官组填充", sub.Name))
			}
			continue
		}
		if downloadContext == nil {
			downloadContext = &groupDownloadContext{}
			if err = json.Unmarshal(data.Context, downloadContext); err != nil {
				return err
			}
			if downloadContext.Torrent != nil {
				sourceSite = downloadContext.Torrent.Site
			}
		}
		patch, fields := groupDownloadPatch(updateDetails, sub, downloadContext)
		if slices.Contains(updateDetails, "站点") && len(sub.Sites) == 0 {
			if !siteResolved {
				siteID, err = groupDownloadRSSSite(ctx, caller, sourceSite)
				if err != nil {
					return err
				}
				siteResolved = true
			}
			if siteID != "" {
				patch.Sites = &[]string{siteID}
				fields = append(fields, field{"sites", []json.RawMessage{sourceSite}})
			}
		}
		if len(fields) == 0 {
			unchanged++
			if len(result.Items) == 1 {
				_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("订阅记录:%s 无需填充", sub.Name))
			}
			continue
		}
		revision, found := result.Revisions[sub.ID]
		if !found || revision < 0 {
			return errors.New("source download subscription revision unavailable")
		}
		history := groupHistory(sub.Name, "种子下载自定义配置", fields, now, handledKey)
		updates = append(updates, sdk.SubscriptionHostUpdateV1{ID: sub.ID, Revision: revision, Patch: patch, SubscribeGroupHistory: history})
	}
	if len(result.Items) > 1 && otherTypes > 0 {
		_ = logger.Log(ctx, sdk.LogWarning, fmt.Sprintf("%d 条订阅记录不是电视剧，不进行官组填充", otherTypes))
	}
	if len(result.Items) > 1 && unchanged > 0 {
		_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("%d 条订阅记录无需填充", unchanged))
	}
	if len(updates) == 0 {
		return nil
	}
	if err = subscriptions.UpdateSubscribeGroup(ctx, updates); err != nil {
		return err
	}
	if len(updates) == 1 {
		history := updates[0].SubscribeGroupHistory
		_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("订阅记录:%s 填充成功", history.Name))
	} else {
		_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("%d 条订阅记录填充成功", len(updates)))
	}
	return nil
}

func sourceDownloadHandled(raw json.RawMessage, key string) (bool, error) {
	value, err := removelink.Decode(raw)
	if err != nil || !removelink.Truth(value) {
		return false, err
	}
	switch values := value.(type) {
	case []any:
		for _, item := range values {
			if text, ok := item.(string); ok && text == key {
				return true, nil
			}
		}
		return false, nil
	case string:
		return strings.Contains(values, key), nil
	case map[string]any:
		_, exists := values[key]
		return exists, nil
	default:
		return false, errors.New("source handled history does not support membership")
	}
}

func groupDownloadSeason(seasons *string) (*int, error) {
	if seasons == nil || *seasons == "" || strings.Contains(*seasons, "-") {
		return nil, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(strings.ReplaceAll(*seasons, "S", "")))
	if err != nil {
		return nil, errors.New("invalid source download history season")
	}
	return &value, nil
}

func groupDownloadPatch(details []string, sub groupDownloadSubscription, download *groupDownloadContext) (sdk.SubscriptionHostPatchV1, []field) {
	var patch sdk.SubscriptionHostPatchV1
	var fields []field
	if meta := download.Meta; meta != nil {
		if slices.Contains(details, "分辨率") && sub.Resolution == "" && meta.Resolution != "" {
			value := ParseResolution(meta.Resolution)
			patch.Resolution = &value
			fields = append(fields, field{"resolution", value})
		}
		if slices.Contains(details, "资源质量") && sub.Quality == "" && meta.Quality != "" {
			value := ParseQuality(meta.Quality)
			patch.Quality = &value
			fields = append(fields, field{"quality", value})
		}
		if slices.Contains(details, "特效") && sub.Effect == "" && meta.Effect != "" {
			value := ParseEffect(meta.Effect)
			patch.Effect = &value
			fields = append(fields, field{"effect", value})
		}
		if slices.Contains(details, "制作组") && sub.Include == "" {
			value := meta.Team
			if meta.Customization != "" {
				if value == "" {
					value = meta.Customization
				} else {
					value = meta.Customization + ".+" + value
				}
			}
			if value != "" {
				patch.Include = &value
				fields = append(fields, field{"include", value})
			}
		}
	}
	return patch, fields
}

func groupDownloadRSSSite(ctx context.Context, caller sdk.HostCaller, rawSite json.RawMessage) (string, error) {
	setting, err := (sdk.SystemSettings{Host: caller}).Get(ctx, "RssSites")
	if err != nil {
		return "", err
	}
	var rssSites []string
	if err = json.Unmarshal(setting.Value, &rssSites); err != nil {
		return "", err
	}
	rawSite = bytes.TrimSpace(rawSite)
	if len(rawSite) == 0 || bytes.Equal(rawSite, []byte("null")) || bytes.Equal(rawSite, []byte(`""`)) {
		return "", nil
	}
	value := string(rawSite)
	if rawSite[0] == '"' {
		if err = json.Unmarshal(rawSite, &value); err != nil {
			return "", err
		}
	}
	sourceID, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return "", errors.New("invalid source torrent site ID")
	}
	if sourceID == 0 || len(rssSites) == 0 {
		return "", nil
	}
	sites, err := (sdk.Sites{Host: caller}).Read(ctx, sdk.SiteHostRequestV1{})
	if err != nil {
		return "", err
	}
	for _, site := range sites.Items {
		if site.SourceID == sourceID && slices.Contains(rssSites, site.ID) {
			return site.ID, nil
		}
	}
	return "", nil
}
