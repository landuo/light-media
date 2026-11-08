package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"light-media/internal/notice"
	"light-media/internal/noticetemplate"
	"light-media/internal/releasemeta"
	"light-media/internal/rsscontext"
)

// The committed Intent supplies accepted file selection. The sealed download
// context supplies the source objects and message attribution for this Add.
func (a *App) postOriginalChatDownloadAdded(ctx context.Context, source *downloadContextSnapshot, in Intent) error {
	if source == nil || source.ChatOrigin == nil {
		return nil
	}
	origin := source.ChatOrigin
	if in.State != "accepted" || source.DownloadKind != "intent" || source.DownloadID != in.ID || !sameHash(source.Hash, in.Hash) || source.Original == nil ||
		origin.Channel != "飞书" || origin.Source != "Lark" || strings.TrimSpace(origin.UserID) == "" || strings.EqualFold(strings.TrimSpace(origin.UserID), "system") {
		return bad("INVALID_DOWNLOAD_CONTEXT", "下载通知的原始消息与接收记录不匹配")
	}
	meta, err := originalChatDownloadNoticeFields(source.Original.Meta)
	if err != nil {
		return err
	}
	media, err := originalChatDownloadNoticeFields(source.Original.Media)
	if err != nil {
		return err
	}
	torrent, err := originalChatDownloadNoticeFields(source.Original.Torrent)
	if err != nil {
		return err
	}
	model, err := rsscontext.NewTorrentInfo(nil)
	if err != nil {
		return err
	}
	if torrent["labels"] == nil {
		// Native listings with no tags serialize a nil Go slice. TorrentInfo's
		// source default is an empty list, which the template can join.
		torrent["labels"] = []any{}
	}
	model.FromDict(torrent)
	stamp := a.pluginStore.noticeNow().Local()
	torrent, err = model.ToDictAt(stamp)
	if err != nil {
		return err
	}
	// The template's raw torrent object must not place private tracker
	// credentials or a private download URL in rendered message history.
	torrent["site_cookie"], torrent["enclosure"] = nil, nil
	media["poster"] = strings.ReplaceAll(sourceText(media["poster_path"]), "original", "w500")
	media["backdrop"] = strings.ReplaceAll(sourceText(media["backdrop_path"]), "original", "w500")
	input := noticetemplate.ContextInput{Meta: meta, Media: media, Torrent: torrent}
	kwargs := notice.Object{"download_episodes": originalChatDownloadEpisodeLabel(in.SelectedEpisodes), "username": origin.Username, "current_time": stamp.Format("2006-01-02 15:04:05")}
	_, err = a.postOriginalPluginMessageWithContext(ctx, notice.Object{
		"channel": origin.Channel, "source": origin.Source, "userid": origin.UserID, "username": origin.Username,
		"mtype": "资源下载", "ctype": "downloadAdded", "image": originalChatDownloadMessageImage(media),
		"link": originalChatMediaLinkTo(a.Settings().AppDomain, "#/downloading"),
	}, nil, input, kwargs)
	return err
}

func originalChatDownloadNoticeFields(raw json.RawMessage) (noticetemplate.Object, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return nil, bad("INVALID_DOWNLOAD_CONTEXT", "下载通知缺少可核对的原始业务数据")
	}
	value, err = noticeV2NormalizeNumberValues(value)
	if err != nil {
		return nil, err
	}
	return sourceMap(value), nil
}

func originalChatDownloadMessageImage(media noticetemplate.Object) string {
	path := sourceText(media["backdrop_path"])
	if path == "" {
		path = sourceText(media["poster_path"])
	}
	return strings.ReplaceAll(path, "original", "w500")
}

func originalChatDownloadEpisodeLabel(episodes []int) any {
	if len(episodes) == 0 {
		return nil
	}
	ordered := slices.Clone(episodes)
	slices.Sort(ordered)
	var ranges []string
	for first := 0; first < len(ordered); {
		last := first
		for last+1 < len(ordered) && ordered[last+1] == ordered[last]+1 {
			last++
		}
		label := fmt.Sprintf("E%02d", ordered[first])
		if first != last {
			label += fmt.Sprintf("-E%02d", ordered[last])
		}
		ranges = append(ranges, label)
		first = last + 1
	}
	return strings.Join(ranges, "、")
}

// Source take-material and Add failures use different ordinary notices. The
// selected source context already contains the required fields.
func (a *App) postOriginalChatDownloadFailure(ctx context.Context, origin originalChatDownloadOrigin, request SearchRequest, candidate Candidate, failure error) {
	if ctx.Err() != nil {
		return
	}
	var original originalDownloadContext
	if len(candidate.originalSelectionContext) != 0 {
		_ = json.Unmarshal(candidate.originalSelectionContext, &original)
	}
	meta, _ := originalChatDownloadNoticeFields(original.Meta)
	if meta == nil {
		meta = a.originalChatDownloadFailureMeta(ctx, candidate)
	}
	media, _ := originalChatDownloadNoticeFields(original.Media)
	if media == nil && request.originalSearchInfo != nil {
		media, _ = originalChatDownloadNoticeFields(request.originalSearchInfo.MediaInfo)
	}
	torrent, _ := originalChatDownloadNoticeFields(original.Torrent)
	if torrent == nil {
		torrent, _ = originalChatDownloadNoticeFields(candidate.Resource.ModuleSourceFields)
	}
	title := sourceText(media["title_year"])
	if title == "" && request.originalSearchInfo != nil {
		title = originalChatMediaTitle(request.originalSearchInfo.Media)
	}
	if title == "" {
		title = candidate.Resource.Title
	}
	name := sourceText(meta["org_string"])
	if name == "" {
		name = candidate.Resource.Title
	}
	site := sourceText(torrent["site_name"])
	if site == "" {
		for _, configured := range a.Settings().Sites {
			if configured.ID == candidate.Resource.SiteID {
				site = configured.Name
				break
			}
		}
	}
	message := notice.Object{"channel": origin.Channel, "source": origin.Source, "userid": origin.UserID, "mtype": "手动处理"}
	reason := a.clean(failure.Error())
	if pluginErrorCodeInternal(failure) == originalChatTorrentFailedCode {
		resourceTitle := sourceText(torrent["title"])
		if resourceTitle == "" {
			resourceTitle = candidate.Resource.Title
		}
		message["title"] = resourceTitle + " 种子下载失败！"
		message["text"] = fmt.Sprintf("错误信息：%s\n站点：%s", reason, site)
	} else {
		message["title"] = "添加下载任务失败：" + title + " " + sourceText(meta["season_episode"])
		message["text"] = fmt.Sprintf("站点：%s\n种子名称：%s\n错误信息：%s", site, name, reason)
		message["image"] = originalChatDownloadMessageImage(media)
	}
	if _, err := a.postOriginalPluginMessage(ctx, message); err != nil {
		a.logOriginalPluginContext(ctx, "ERROR", "chat_download_failure_notification_failed", "飞书下载失败通知发送失败")
	}
}

func (a *App) originalChatDownloadFailureMeta(ctx context.Context, candidate Candidate) noticetemplate.Object {
	state := candidate.originalSelectionMeta
	if len(state) == 0 {
		options, err := a.globalRecognitionOptions(ctx)
		if err != nil {
			return nil
		}
		parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
		if err != nil {
			return nil
		}
		subtitle := candidate.Resource.SourceSubtitle
		if subtitle == nil && candidate.Resource.Subtitle != "" {
			subtitle = &candidate.Resource.Subtitle
		}
		meta, err := parser.ParseTitle(candidate.Resource.Title, subtitle, nil)
		if err != nil {
			return nil
		}
		state, err = meta.EncodeState()
		if err != nil {
			return nil
		}
	}
	meta, _ := sourceNoticeMeta(state)
	return meta
}
