package larkmessager

import (
	"context"
	"strings"
)

// MediaView is a trusted snapshot of precisely the properties consumed by the
// installed list builders, not a substitute for MoviePilot's full MediaInfo.
type MediaView struct {
	TitleYear       string  `json:"title_year"`
	Title           string  `json:"title"`
	MessageImage    string  `json:"message_image"`
	PosterPath      string  `json:"poster_path"`
	Poster          string  `json:"poster"`
	GetMessageImage *string `json:"get_message_image"`
}
type TorrentView struct {
	Torrent struct {
		Title    string `json:"title"`
		SiteName string `json:"site_name"`
	} `json:"torrent_info"`
	Media *MediaView `json:"media_info"`
}

func ExtractPoster(v *MediaView) string {
	if v == nil {
		return ""
	}
	if v.GetMessageImage != nil && *v.GetMessageImage != "" {
		return *v.GetMessageImage
	}
	return text(first(v.MessageImage, v.PosterPath, v.Poster))
}
func (p *Plugin) PostMedias(ctx context.Context, n Notification, medias []MediaView) (_error error) {
	defer sourceErrors(&_error)
	t, c, ok := p.messageContext(n)
	if !ok || t.UserID == "" && t.ChatID == "" {
		return nil
	}
	items := []ImageItem{}
	for i, m := range medias {
		if i >= 10 {
			break
		}
		title := text(first(m.TitleYear, m.Title, "未知媒体"))
		items = append(items, ImageItem{Title: str(i+1) + ". " + title, URL: ExtractPoster(&m)})
	}
	t.OriginalMessageID = ""
	proxy := Notification{Title: n.Title, Link: n.Link, Buttons: n.Buttons, UserID: n.UserID, Targets: n.Targets}
	_, e := c.SendNotification(ctx, proxy, t, items)
	return e
}
func (p *Plugin) PostTorrents(ctx context.Context, n Notification, torrents []TorrentView) (_error error) {
	defer sourceErrors(&_error)
	t, c, ok := p.messageContext(n)
	if !ok || t.UserID == "" && t.ChatID == "" {
		return nil
	}
	items := []ImageItem{}
	for i, m := range torrents {
		if i >= 10 {
			break
		}
		title := text(first(m.Torrent.Title, m.Torrent.SiteName, "未知种子"))
		items = append(items, ImageItem{Title: str(i+1) + ". " + title, URL: ExtractPoster(m.Media)})
	}
	t.OriginalMessageID = ""
	proxy := Notification{Title: n.Title, Link: n.Link, Buttons: n.Buttons, UserID: n.UserID, Targets: n.Targets}
	_, e := c.SendNotification(ctx, proxy, t, items)
	return e
}
func (p *Plugin) Edit(ctx context.Context, channel, source, id, chat, title, text string, buttons [][]Button) (*bool, error) {
	if channel != "飞书" || source != "Lark" {
		return nil, nil
	}
	_, c, _ := p.snapshot()
	ok := false
	if c == nil {
		return &ok, nil
	}
	ok, e := c.EditMessage(ctx, id, title, text, buttons)
	return &ok, e
}
func (p *Plugin) AddReaction(ctx context.Context, id, emoji, source string) (*string, error) {
	_, c, _ := p.snapshot()
	if c == nil || source != "Lark" {
		return nil, nil
	}
	rid, e := c.AddReaction(ctx, id, emoji)
	if rid == "" {
		return nil, e
	}
	return &rid, e
}
func (p *Plugin) DeleteReaction(ctx context.Context, id, reaction, source string) (_out bool, _error error) {
	defer sourceErrors(&_error)
	_, c, _ := p.snapshot()
	if c == nil || source != "Lark" {
		return false, nil
	}
	return c.DeleteReaction(ctx, id, reaction)
}
func (p *Plugin) ProcessingStarted(ctx context.Context, channel, source string, user any, id, chat, text string) (_out Object, _error error) {
	defer sourceErrors(&_error)
	if channel != "飞书" || source != "Lark" || id == "" || text == "" || strings.HasPrefix(text, "CALLBACK:") {
		return nil, nil
	}
	rid, e := p.AddReaction(ctx, id, "GLANCE", source)
	if e != nil || rid == nil {
		return nil, e
	}
	return Object{"channel": channel, "source": source, "userid": user, "message_id": id, "chat_id": nullString(chat), "metadata": Object{"kind": "reaction", "reaction_id": *rid, "emoji_type": "GLANCE"}}, nil
}
func (p *Plugin) ProcessingFinished(ctx context.Context, channel, source, id string, status Object) (*bool, error) {
	if channel != "飞书" || source != "Lark" {
		return nil, nil
	}
	meta, e := requireObject(status["metadata"])
	if e != nil {
		return nil, e
	}
	target, reaction := text(first(status["message_id"], id)), text(meta["reaction_id"])
	ok := false
	if target == "" || reaction == "" {
		return &ok, nil
	}
	ok, e = p.DeleteReaction(ctx, target, reaction, source)
	return &ok, e
}
func (p *Plugin) MessageAction(ctx context.Context, instance string, event Object) (_error error) {
	defer sourceErrors(&_error)
	if event["plugin_id"] != instance || text(event["text"]) != "test_ok" {
		return nil
	}
	_, c, _ := p.snapshot()
	if c == nil {
		return nil
	}
	_, e := c.ReplyMessage(ctx, text(event["original_message_id"]), "text", Object{"text": "✅ 测试确认成功！LarkMessager 插件卡片交互正常工作。"})
	return e
}
func (c *Client) SendCard(ctx context.Context, id, typ string, card Object) (_out any, _error error) {
	defer sourceErrors(&_error)
	r, e := c.SendMessage(ctx, id, typ, "interactive", card)
	if e != nil {
		return nil, e
	}
	if r == nil || !r.Success {
		return nil, ErrRejected
	}
	return r.MessageID, nil
}
