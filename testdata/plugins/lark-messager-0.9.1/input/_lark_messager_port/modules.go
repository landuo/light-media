package larkmessager

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
)

// MediaView is a trusted snapshot of precisely the properties consumed by the
// installed list builders, not a substitute for MoviePilot's full MediaInfo.
type MediaView struct {
	TitleYear, Title, MessageImage, PosterPath, Poster string
	GetMessageImage                                    *string
}
type TorrentView struct {
	Title, SiteName string
	Media           *MediaView
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
		title := text(first(m.Title, m.SiteName, "未知种子"))
		items = append(items, ImageItem{Title: str(i+1) + ". " + title, URL: ExtractPoster(m.Media)})
	}
	t.OriginalMessageID = ""
	proxy := Notification{Title: n.Title, Link: n.Link, Buttons: n.Buttons, UserID: n.UserID, Targets: n.Targets}
	_, e := c.SendNotification(ctx, proxy, t, items)
	return e
}
func (p *Plugin) Edit(ctx context.Context, channel, source, id, chat, title, text string, buttons [][]Button, metadata Object) (*bool, error) {
	if channel != "飞书" || source != "Lark" {
		return nil, nil
	}
	_, c, _ := p.snapshot()
	ok := false
	if c == nil {
		return &ok, nil
	}
	ok, e := c.EditMessage(ctx, id, title, text, buttons, metadata, chat)
	return &ok, e
}
func (p *Plugin) Finalize(ctx context.Context, channel string, metadata Object) (_out bool, _error error) {
	defer sourceErrors(&_error)
	if channel != "飞书" || metadata == nil {
		return false, nil
	}
	_, c, _ := p.snapshot()
	if c == nil {
		return false, nil
	}
	s := obj(metadata["feishu_streaming"])
	if s == nil {
		return false, nil
	}
	id := trim(s["card_id"])
	if id == "" {
		return false, nil
	}
	seq := big.NewInt(0)
	if truth(s["sequence"]) {
		var e error
		seq, e = pythonInt(s["sequence"])
		if e != nil {
			return false, e
		}
	}
	seq.Add(seq, big.NewInt(1))
	return c.CloseStreaming(ctx, id, json.Number(seq.String()))
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
func (p *Plugin) MessageAction(ctx context.Context, event Object) (_error error) {
	defer sourceErrors(&_error)
	if event["plugin_id"] != "LarkMessager" || text(event["text"]) != "test_ok" {
		return nil
	}
	_, c, _ := p.snapshot()
	if c == nil {
		return nil
	}
	_, e := c.ReplyMessage(ctx, text(event["original_message_id"]), "text", Object{"text": "✅ 测试确认成功！LarkMessager 插件卡片交互正常工作。"})
	return e
}
func BuildLegacyCard(title, content string, buttons []Object, color, image, link string) (_out Object, _error error) {
	defer sourceErrors(&_error)
	rows := [][]Button{}
	for _, b := range buttons {
		rows = append(rows, []Button{{Text: b["text"], CallbackData: first(b["value"], b["action_id"], "")}})
	}
	return buildCard(CardInput{Title: title, Text: content, Link: link, Buttons: rows, ImageKey: image, HeaderTemplate: color}), nil
}
func BuildLegacyButton(text, action, value string, kinds ...string) Object {
	kind := "default"
	if len(kinds) > 0 {
		kind = kinds[0]
	}
	return Object{"tag": "button", "text": Object{"tag": "plain_text", "content": text}, "action_id": action, "value": Object{"value": value}, "type": kind}
}
func BuildInteractiveCard(title string, elements, actions []any) Object {
	elements = append([]any{}, elements...)
	if len(actions) > 0 {
		elements = append(elements, Object{"tag": "action", "actions": clone(actions)})
	}
	return Object{"schema": "2.0", "config": Object{"wide_screen_mode": true, "update_multi": true, "summary": Object{"content": title}}, "body": Object{"direction": "vertical", "padding": "12px 12px 12px 12px", "elements": elements}}
}
func (c *Client) SendCard(ctx context.Context, id, typ string, card Object) (_out any, _error error) {
	defer sourceErrors(&_error)
	r, e := c.SendMessage(ctx, id, typ, "interactive", card)
	if e != nil || r == nil || !r.Success {
		return nil, firstError(e, ErrRejected)
	}
	return r.MessageID, nil
}
func (c *Client) SendImageMessage(ctx context.Context, id, typ, key string) (_out any, _error error) {
	defer sourceErrors(&_error)
	r, e := c.SendMessage(ctx, id, typ, "image", Object{"image_key": key})
	if e != nil || r == nil || !r.Success {
		return nil, firstError(e, ErrRejected)
	}
	return r.MessageID, nil
}
func (c *Client) SendFileMessage(ctx context.Context, id, typ, key string) (_out any, _error error) {
	defer sourceErrors(&_error)
	r, e := c.SendMessage(ctx, id, typ, "file", Object{"file_key": key})
	if e != nil || r == nil || !r.Success {
		return nil, firstError(e, ErrRejected)
	}
	return r.MessageID, nil
}
func firstError(e, fallback error) (_error error) {
	defer sourceErrors(&_error)
	if e != nil {
		return e
	}
	return fallback
}
func (c *Client) ReplyLegacy(ctx context.Context, id, kind string, content any) (_out any, _error error) {
	defer sourceErrors(&_error)
	var d any
	if kind == "text" {
		if s, ok := content.(string); ok {
			d = Object{"text": s}
		} else {
			d = content
		}
	} else if o := obj(content); o != nil {
		d = o
	} else if s, ok := content.(string); ok {
		var e error
		if d, e = DecodeSource([]byte(s)); e != nil {
			d = Object{"text": s}
		}
	} else {
		d = Object{"text": str(content)}
	}
	r, e := c.ReplyMessage(ctx, id, kind, d)
	if e != nil || r == nil || !r.Success {
		return nil, firstError(e, ErrRejected)
	}
	return r.MessageID, nil
}

// RawDownload variants retain the old raise_for_status and JSON-error paths.
func (c *Client) DownloadImageRaw(ctx context.Context, key string) ([]byte, error) {
	if c.host == nil {
		return nil, ErrDependency
	}
	r, e := c.host.API(ctx, Request{Op: "image_get", IDs: []string{key}})
	if e != nil {
		return nil, e
	}
	if r.Status >= 400 {
		return nil, ErrRejected
	}
	if strings.HasPrefix(r.Header["Content-Type"], "application/json") {
		var d Object
		if decode(r.Body, &d) != nil {
			return nil, ErrUnsupported
		}
		if !zero(d["code"]) {
			return nil, ErrRejected
		}
	}
	return r.Body, nil
}
func (c *Client) DownloadImageDataURL(ctx context.Context, key string) (_out string, _error error) {
	defer sourceErrors(&_error)
	b, e := c.DownloadImageRaw(ctx, key)
	if e != nil {
		return "", e
	}
	return ImageDataURL(b), nil
}
func (c *Client) DownloadFileRaw(ctx context.Context, key string) ([]byte, error) {
	if c.host == nil {
		return nil, ErrDependency
	}
	r, e := c.host.API(ctx, Request{Op: "file_get", IDs: []string{key}, Query: Object{"file_type": "stream"}})
	if e != nil {
		return nil, e
	}
	if r.Status >= 400 {
		return nil, ErrRejected
	}
	return r.Body, nil
}
func sourceID(v any) (_out string, _error error) {
	defer sourceErrors(&_error)
	switch v.(type) {
	case nil, string, json.Number, int:
		return text(v), nil
	}
	return "", ErrUnsupported
}
