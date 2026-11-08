package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/catalog"
)

func (s *originalChatMediaSession) bounds() (start, end, pages int) {
	total := len(s.media)
	switch s.phase {
	case "torrent":
		total = len(s.candidates)
	case "download-dir":
		total = len(s.dirs)
	}
	pages = max(1, (total+originalChatMediaPageSize-1)/originalChatMediaPageSize)
	s.page = min(max(0, s.page), pages-1)
	start = s.page * originalChatMediaPageSize
	return start, min(start+originalChatMediaPageSize, total), pages
}

func (s *originalChatMediaSession) buttons(count, pages int) [][]originalMessageButton {
	var rows [][]originalMessageButton
	prefix := "media:" + s.id + ":"
	if s.phase == "torrent" {
		rows = append(rows, []originalMessageButton{{"🤖 自动选择下载", prefix + "download:0"}})
	}
	for first := 1; first <= count; first += 3 {
		row := make([]originalMessageButton, 0, 3)
		for n := first; n <= min(first+2, count); n++ {
			value := strconv.Itoa(n)
			row = append(row, originalMessageButton{value, prefix + s.selectionAction() + ":" + value})
		}
		rows = append(rows, row)
	}
	var nav []originalMessageButton
	if s.page > 0 {
		nav = append(nav, originalMessageButton{"⬅️ 上一页", prefix + "page-prev"})
	}
	if s.page+1 < pages {
		nav = append(nav, originalMessageButton{"下一页 ➡️", prefix + "page-next"})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	return rows
}

func originalChatMediaTitle(m catalog.Media) string {
	if m.Year == 0 {
		return m.Title
	}
	return fmt.Sprintf("%s (%d)", m.Title, m.Year)
}

func originalChatMediaCard(m catalog.Media) map[string]string {
	return map[string]string{"title_year": originalChatMediaTitle(m), "title": m.Title, "poster_path": m.PosterPath}
}

func (a *App) renderOriginalChatMedia(ctx context.Context, reply originalSlashReply, s *originalChatMediaSession, callback bool) error {
	start, end, pages := s.bounds()
	buttons := s.buttons(end-start, pages)
	message := map[string]any{"channel": reply.info.Channel, "source": reply.info.Source, "userid": reply.info.UserID, "buttons": buttons, "save_history": false}
	if callback {
		message["original_message_id"] = reply.info.MessageID
		message["original_chat_id"] = reply.info.ChatID
	}
	method := "post_medias_message"
	kwargs := map[string]any{"message": message}
	switch s.phase {
	case "torrent":
		method = "post_torrents_message"
		message["title"] = fmt.Sprintf("【%s】共找到%d条相关资源，请选择下载", s.title, len(s.candidates))
		message["link"] = originalChatMediaLinkTo(a.Settings().AppDomain, "#/resource")
		items := make([]map[string]any, 0, end-start)
		for _, item := range s.candidates[start:end] {
			items = append(items, map[string]any{"torrent_info": map[string]string{"title": item.Resource.Title}, "media_info": originalChatMediaCard(s.selectedDisplay)})
		}
		kwargs["torrents"] = items
	case "download-dir":
		message["title"] = "【" + s.title + "】请选择下载目录"
		lines := make([]string, 0, end-start)
		for i, dir := range s.dirs[start:end] {
			label := dir.Name
			if dir.Path != "" && dir.Name != dir.Path {
				label += " (" + dir.Path + ")"
			}
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, label))
		}
		message["text"] = strings.Join(lines, "\n")
		_, err := a.postOriginalPluginMessage(ctx, message)
		return err
	default:
		message["title"] = fmt.Sprintf("【%s】共找到%d条相关信息，请选择操作", s.title, len(s.media))
		items := make([]map[string]string, 0, end-start)
		for _, item := range s.media[start:end] {
			items = append(items, originalChatMediaCard(item.Media))
		}
		kwargs["medias"] = items
	}
	raw, err := json.Marshal(kwargs)
	if err != nil {
		return err
	}
	_, err = a.invokeOriginalPluginModules(ctx, method, nil, nil, raw)
	return err
}

func (a *App) originalChatMediaDirectories(s *originalChatMediaSession) []originalChatMediaDirectory {
	settings := a.Settings()
	category := ""
	if s.request.originalSearchInfo != nil {
		media, err := decodeSourceJSON(s.request.originalSearchInfo.MediaInfo)
		if err == nil {
			category = sourceText(sourceMap(media)["category"])
		}
	}
	slices.SortStableFunc(settings.Directories, func(left, right DirectoryRule) int {
		switch {
		case left.Priority < right.Priority:
			return -1
		case left.Priority > right.Priority:
			return 1
		default:
			return 0
		}
	})
	var dirs []originalChatMediaDirectory
	for _, rule := range settings.Directories {
		// An unsorted rule remains a download location in the source.
		if rule.MediaType != "" && rule.MediaType != "mixed" && rule.MediaType != s.selected.MediaType || rule.MediaCategory != "" && rule.MediaCategory != category {
			continue
		}
		path := rule.DownloadPath
		if path == "" {
			path = rule.SourcePath
		}
		if path == "" {
			continue
		}
		name := rule.Name
		if name == "" {
			name = path
		}
		dirs = append(dirs, originalChatMediaDirectory{ID: rule.ID, Name: name, Path: path, Downloader: rule.DownloaderID, RootID: rule.RootID})
	}
	return dirs
}

func (a *App) runOriginalChatMediaDownload(ctx context.Context, reply originalSlashReply, s *originalChatMediaSession, index int, dir originalChatMediaDirectory) error {
	ctx, err := a.withOriginalChatDownloadOrigin(originalChatWithMediaProvider(ctx, s.request.chatProvider), reply)
	if err != nil {
		return err
	}
	if index >= 0 {
		candidate := s.candidates[index]
		if err := a.originalChatSubmitDownloadMedia(ctx, s.request, candidate, dir, nil, true); err != nil {
			switch pluginErrorCodeInternal(err) {
			case "CHAT_DOWNLOAD_FAILED", originalChatTorrentFailedCode:
				return nil // The attempt sent its source failure notice.
			case "CHAT_DOWNLOAD_UNCONFIRMED", "CHAT_DOWNLOAD_CANCELLED":
				return reply.post(ctx, a, a.clean(err.Error()), false)
			default:
				return reply.post(ctx, a, "添加下载任务失败："+a.clean(err.Error()), true)
			}
		}
		return nil
	}
	accepted, err := a.originalChatAutoDownloadMedia(ctx, s.request, s.candidates, dir)
	switch pluginErrorCodeInternal(err) {
	case "CHAT_DOWNLOAD_INCOMPLETE":
		return a.originalChatSubscribeMediaAfterAuto(ctx, reply, s.meta, s.selected, accepted)
	case "CHAT_DOWNLOAD_UNCONFIRMED", "CHAT_DOWNLOAD_CANCELLED":
		return reply.post(ctx, a, a.clean(err.Error()), false)
	}
	if err != nil {
		return reply.post(ctx, a, "添加下载任务失败："+a.clean(err.Error()), true)
	}
	return nil
}

func originalChatMediaLinkTo(domain, path string) any {
	if domain == "" {
		return nil
	}
	return strings.TrimRight(domain, "/") + "/" + path
}
