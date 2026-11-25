package larkmessager

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
)

//go:embed page-templates.json
var pageTemplatesJSON []byte

var errChatListTransport = errors.New("Lark transport failed")

func (p *Plugin) Page(ctx context.Context) ([]any, error) {
	s, c, _ := p.snapshot()
	var templates map[string][]any
	if e := decode(pageTemplatesJSON, &templates); e != nil {
		return nil, e
	}
	active := "0"
	if s.Enabled && c != nil {
		active = "1"
	}
	page, ok := templates[active+":"+strings.Join(s.Missing, ",")]
	if !ok {
		return nil, ErrUnsupported
	}
	page = clone(page)
	if p.host.KV == nil {
		return nil, ErrDependency
	}
	p.results.Lock()
	defer p.results.Unlock()
	for _, spec := range []struct {
		key, title string
		chats      bool
	}{{"last_test_result", "测试结果", false}, {"last_chats", "群聊 Chat ID", true}} {
		v, e := p.host.KV.Get(ctx, spec.key)
		if e != nil {
			return nil, e
		}
		if !truth(v) {
			continue
		}
		displayed, exists := v["displayed"]
		if !exists || truth(displayed) {
			continue
		}
		ok := truth(v["ok"])
		message := text(v["msg"])
		if timestamp := text(v["time"]); timestamp != "" {
			message += "\n更新时间：" + timestamp
		}
		typ, icon := "error", "mdi-close-circle"
		if ok {
			typ = "success"
			icon = "mdi-check-circle"
		}
		title := Object{"component": "VCardTitle", "text": spec.title}
		if spec.chats {
			title["props"] = Object{"prependIcon": "mdi-forum"}
			if ok {
				icon = "mdi-forum"
			} else {
				typ = "warning"
				icon = "mdi-alert-circle"
			}
		}
		page = append(page, Object{"component": "VCard", "props": Object{"class": "mb-4"}, "content": []any{title, Object{"component": "VCardText", "content": []any{Object{"component": "VAlert", "props": Object{"type": typ, "variant": "tonal", "density": "compact", "icon": icon}, "text": message}}}}})
		v["displayed"] = true
		if e = p.host.KV.Put(ctx, spec.key, v); e != nil {
			return nil, e
		}
	}
	return page, nil
}
func (p *Plugin) Status(ctx context.Context) (_out Object, _error error) {
	defer sourceErrors(&_error)
	if p.host.KV == nil {
		return nil, ErrDependency
	}
	p.results.Lock()
	defer p.results.Unlock()
	if e := p.host.KV.Delete(ctx, "last_test_result"); e != nil {
		return nil, e
	}
	if e := p.host.KV.Delete(ctx, "last_chats"); e != nil {
		return nil, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	appid, chat := "", ""
	if p.state.AppID != "" {
		appid = prefixRunes(p.state.AppID, 8) + "..."
	}
	if p.state.ChatID != "" {
		chat = prefixRunes(p.state.ChatID, 8) + "..."
	}
	return Object{"enabled": p.state.Enabled, "app_id": appid, "has_client": p.client != nil, "has_crypto": p.crypto != nil, "admin_count": len(p.state.Admins), "chat_id": chat}, nil
}
func (p *Plugin) storeResult(ctx context.Context, key string, v Object) (_out Object, _error error) {
	defer sourceErrors(&_error)
	v = clone(v)
	v["time"] = p.host.Now().Format("2006-01-02 15:04:05")
	v["displayed"] = false
	if p.host.KV == nil {
		return nil, ErrDependency
	}
	p.results.Lock()
	defer p.results.Unlock()
	if e := p.host.KV.Put(ctx, key, v); e != nil {
		return nil, e
	}
	return v, nil
}
func (p *Plugin) FetchChats(ctx context.Context) (_out Object, _error error) {
	defer sourceErrors(&_error)
	s, _, _ := p.snapshot()
	result := failedChatList("请先在插件配置中填写 App ID 和 App Secret 并保存。")
	if s.AppID != "" && s.AppSecret != "" {
		api := p.host.API
		if p.host.newChatsAPI != nil {
			api = p.host.newChatsAPI(s.AppID, s.AppSecret)
		}
		var err error
		result, err = requestChatList(ctx, api)
		if err != nil {
			if p.host.LogFailure != nil {
				p.host.LogFailure(ctx, "LarkMessager 获取群聊失败")
			}
			message := "Lark 返回的群聊数据无效"
			if errors.Is(err, errChatListTransport) {
				message = errChatListTransport.Error()
			}
			result = failedChatList("请求失败：" + message)
		}
	}
	return p.storeResult(ctx, "last_chats", result)
}

func failedChatList(message string) Object {
	return Object{"ok": false, "msg": message, "chats": []any{}}
}

func requestChatList(ctx context.Context, api Host) (_out Object, _error error) {
	defer sourceErrors(&_error)
	if api == nil {
		return nil, errChatListTransport
	}
	response, err := api.API(ctx, Request{Op: "chats", Query: Object{"user_id_type": "open_id", "page_size": 100}})
	if err != nil {
		return nil, errChatListTransport
	}
	var data Object
	if decode(response.Body, &data) != nil || data == nil {
		return nil, ErrUnsupported
	}
	if code := data["code"]; !zero(code) {
		message := "Lark API 错误：请求被拒绝"
		if number, ok := code.(json.Number); ok && len(number) <= 32 {
			message += "（code=" + number.String() + "）"
		}
		return failedChatList(message), nil
	}
	var items []any
	if raw := data["data"]; truth(raw) {
		body := obj(raw)
		if body == nil {
			return nil, ErrUnsupported
		}
		if rawItems := body["items"]; truth(rawItems) {
			items = arr(rawItems)
			if items == nil {
				return nil, ErrUnsupported
			}
		}
	}
	if len(items) == 0 {
		return failedChatList("Lark 返回空列表。请确认：本应用已被添加为群聊机器人。"), nil
	}
	chats := make([]any, 0, len(items))
	lines := make([]string, 0, len(items))
	for _, value := range items {
		item := obj(value)
		if item == nil {
			return nil, ErrUnsupported
		}
		chat := Object{"chat_id": "", "name": "(未命名)", "description": ""}
		for _, key := range []string{"chat_id", "name", "description"} {
			if value, ok := item[key]; ok {
				chat[key] = value
			}
		}
		line := "- " + str(chat["name"]) + " (" + str(chat["chat_id"]) + ")"
		if truth(chat["description"]) {
			line += "\n  " + str(chat["description"])
		}
		chats = append(chats, chat)
		lines = append(lines, line)
	}
	return Object{"ok": true, "msg": "共 " + str(len(chats)) + " 个群聊：\n" + strings.Join(lines, "\n"), "chats": chats}, nil
}
