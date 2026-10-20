package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"light-media/internal/catalog"
	lark "light-media/internal/plugins/larkmessager"
	"light-media/internal/releasemeta"
	"light-media/internal/releasemeta/legacyanime"
)

type pluginLarkMediaSearchResult struct {
	Name   string
	Meta   json.RawMessage
	Medias []catalog.Media
}

func (a *App) larkSearchMedia(ctx context.Context, core *pluginLarkCoreExecution, content string) (pluginLarkMediaSearchResult, error) {
	return larkCoreRead(ctx, a, core, "media_search", content, func(ctx context.Context) (pluginLarkMediaSearchResult, error) {
		out := pluginLarkMediaSearchResult{}
		k, e := releasemeta.SearchInputKeyword(content)
		if e != nil {
			return out, e
		}
		options, e := a.globalRecognitionOptions(ctx)
		if e != nil {
			return out, e
		}
		parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
		if e != nil {
			return out, e
		}
		meta, e := parser.ParseTitle(k.Content, nil, nil)
		if e != nil {
			return out, e
		}
		if meta.Name() == "" {
			meta.Set("cn_name", k.Content)
		}
		if k.Type != "" {
			meta.Set("type", k.Type)
		}
		if k.Season != nil && k.Season.Sign() != 0 {
			meta.Set("begin_season", k.Season)
		}
		if k.Episode != nil && k.Episode.Sign() != 0 {
			meta.Set("begin_episode", k.Episode)
		}
		if k.Year != "" {
			meta.Set("year", k.Year)
		}
		out.Name = meta.Name()
		out.Meta, e = meta.EncodeState()
		if e != nil || out.Name == "" {
			return out, e
		}
		out.Medias, e = a.catalog().SearchSourceMedia(ctx, meta.Name(), meta.Text("type"), meta.Text("year"))
		if e != nil {
			return out, e
		}
		if season := meta.Number("begin_season"); season != nil {
			label, e := larkSourceSeasonName(season)
			if e != nil {
				return out, e
			}
			for i := range out.Medias {
				if out.Medias[i].MediaType == "tv" {
					out.Medias[i].Title += " 第" + label + "季"
				}
			}
		}
		return out, nil
	})
}
func larkMediaTitleYear(m catalog.Media) string {
	if m.Year == 0 {
		return m.Title
	}
	return fmt.Sprintf("%s (%d)", m.Title, m.Year)
}
func larkMediaView(m catalog.Media) lark.MediaView {
	return lark.MediaView{Title: m.Title, TitleYear: larkMediaTitleYear(m), MessageImage: m.BackdropPath, PosterPath: m.PosterPath}
}
func larkMediaCount(s *pluginLarkMediaSession) int {
	switch s.Phase {
	case "torrent":
		return len(s.Candidates)
	case "download-dir":
		return len(s.Directories)
	}
	return len(s.Medias)
}
func (a *App) larkRenderMedia(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, user, message, chat string) error {
	total := larkMediaCount(s)
	start, end, page, pages := larkMediaPage(total, s.Page)
	s.Page = page
	n := lark.Notification{UserID: user, OriginalMessageID: message, Targets: lark.Object{"feishu_chat_id": chat}, Buttons: larkMediaButtons(s.Request, s.Phase, end-start, total, page, pages)}
	in := pluginLarkModuleInput{Notification: n}
	switch s.Phase {
	case "torrent":
		in.Name = "post_torrents_message"
		in.Notification.Title = fmt.Sprintf("【%s】共找到%d条相关资源，请选择下载", s.Title, total)
		for _, candidate := range s.Candidates[start:end] {
			v := lark.TorrentView{Title: candidate.Resource.Title, SiteName: s.SiteNames[candidate.Resource.SiteID]}
			if s.Selected != nil {
				m := larkMediaView(*s.Selected)
				v.Media = &m
			}
			in.Torrents = append(in.Torrents, v)
		}
	case "download-dir":
		in.Name = "post_message"
		in.Notification.Title = "【" + s.Title + "】请选择下载目录"
		labels := []string{}
		for i, d := range s.Directories[start:end] {
			label := larkDownloadDirectoryLabel(d)
			labels = append(labels, strconv.Itoa(i+1)+". "+label)
		}
		in.Notification.Text = strings.Join(labels, "\n")
	default:
		in.Name = "post_medias_message"
		in.Notification.Title = fmt.Sprintf("【%s】共找到%d条相关信息，请选择操作", s.Title, total)
		for _, m := range s.Medias[start:end] {
			in.Medias = append(in.Medias, larkMediaView(m))
		}
	}
	_, e := a.larkCoreModule(ctx, core, in)
	return e
}
func (a *App) larkStartMedia(ctx context.Context, core *pluginLarkCoreExecution, action, content, user, chat string) error {
	found, e := a.larkSearchMedia(ctx, core, content)
	if e != nil {
		return e
	}
	if found.Name == "" {
		return a.larkCoreReply(ctx, core, user, chat, "无法识别输入内容！", "")
	}
	if len(found.Medias) == 0 {
		return a.larkCoreReply(ctx, core, user, chat, found.Name+" 没有找到对应的媒体信息！", "")
	}
	request, e := larkCoreRead(ctx, a, core, "media_request_id", nil, func(context.Context) (string, error) {
		b := make([]byte, 16)
		_, e := rand.Read(b)
		return hex.EncodeToString(b)[:12], e
	})
	if e != nil {
		return e
	}
	s, e := a.larkSaveMediaSession(ctx, core, pluginLarkMediaSession{Request: request, User: user, Action: action, Keyword: content, Title: found.Name, Phase: "media", Meta: found.Meta, Medias: found.Medias})
	if e != nil {
		return e
	}
	return a.larkRenderMedia(ctx, core, s, user, "", chat)
}

// Both modern callback IDs and legacy numeric/text replies resolve only the
// authenticated user's current source session; old buttons cannot select a
// different user's or replaced search results.
func (a *App) larkMediaInteraction(ctx context.Context, core *pluginLarkCoreExecution, text, user, message, chat string) (bool, error) {
	s, e := a.larkCurrentMediaSession(ctx, core, user)
	if e != nil {
		return false, e
	}
	value := strings.TrimSpace(text)
	var callback *larkMediaCallback
	if strings.HasPrefix(value, "CALLBACK:") {
		callback, e = larkParseMediaCallback(strings.TrimPrefix(value, "CALLBACK:"))
		if e != nil {
			return true, e
		}
		if callback == nil {
			return false, nil
		}
		if s == nil || callback.Request != nil && *callback.Request != "" && *callback.Request != s.Request {
			return true, a.larkCoreReply(ctx, core, user, chat, "交互已失效，请重新搜索或订阅", "")
		}
	}
	action := ""
	var index *big.Int
	if callback != nil {
		action, index = callback.Action, callback.Index
	} else {
		lower := strings.ToLower(value)
		if s != nil && (lower == "退出" || lower == "关闭" || lower == "q" || lower == "quit" || lower == "exit") {
			s.Removed = true
			_, e = a.larkSaveMediaSession(ctx, core, *s)
			if e != nil {
				return true, e
			}
			return true, a.larkCoreReply(ctx, core, user, chat, "媒体交互已结束", "")
		}
		switch lower {
		case "p", "prev", "上一页":
			action = "page-prev"
		case "n", "next", "下一页":
			action = "page-next"
		}
		if legacyanime.IsDigit(value) {
			index, e = legacyanime.Integer(value)
			if e != nil {
				return true, e
			}
			action = "select"
			if s != nil && s.Phase == "torrent" {
				action = "download"
			}
			if s != nil && s.Phase == "download-dir" {
				action = "download-dir"
			}
		}
	}
	if action != "" {
		invalid := func(title string) (bool, error) { return true, a.larkCoreReply(ctx, core, user, chat, title, "") }
		if s == nil {
			return invalid("输入有误！")
		}
		if action == "page-prev" || action == "page-next" {
			_, _, page, pages := larkMediaPage(larkMediaCount(s), s.Page)
			if action == "page-prev" {
				if page <= 0 {
					return invalid("已经是第一页了！")
				}
				s.Page--
			} else {
				if page >= pages-1 {
					return invalid("已经是最后一页了！")
				}
				s.Page++
			}
			s, e = a.larkSaveMediaSession(ctx, core, *s)
			if e != nil {
				return true, e
			}
			return true, a.larkRenderMedia(ctx, core, s, user, message, chat)
		}
		if action == "select" {
			return true, a.larkSelectMedia(ctx, core, s, index, user, message, chat)
		}
		if action == "download" {
			return true, a.larkSelectTorrent(ctx, core, s, index, user, message, chat)
		}
		if action == "download-dir" {
			return true, a.larkSelectDownloadDirectory(ctx, core, s, index, user, chat)
		}
		return false, nil
	}
	action, content := releasemeta.ResolveMediaAction(value)
	if action == "" {
		return false, nil
	}
	return true, a.larkStartMedia(ctx, core, action, content, user, chat)
}
