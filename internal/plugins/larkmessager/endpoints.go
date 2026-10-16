package larkmessager

import (
	"context"
	_ "embed"
	"strings"
)

//go:embed page-templates.json
var pageTemplatesJSON []byte

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
	if e := p.host.KV.Put(ctx, key, v); e != nil {
		return nil, e
	}
	return v, nil
}
func (p *Plugin) FetchChats(ctx context.Context) (_out Object, _error error) {
	defer sourceErrors(&_error)
	s, _, _ := p.snapshot()
	result := Object{}
	chats := []any{}
	if s.AppID == "" || s.AppSecret == "" {
		result = Object{"ok": false, "msg": "请先在插件配置中填写 App ID 和 App Secret 并保存。", "chats": chats}
	} else {
		c := NewClient(p.host.API)
		_, data, e := c.call(ctx, Request{Op: "chats", Query: Object{"user_id_type": "open_id", "page_size": 100}})
		if e != nil {
			result = Object{"ok": false, "msg": "请求失败：Lark transport failed", "chats": chats}
		} else if !zero(data["code"]) {
			result = Object{"ok": false, "msg": "Lark API 错误：请求被拒绝", "chats": chats}
		} else {
			for _, v := range arr(obj(data["data"])["items"]) {
				item := obj(v)
				if item == nil {
					return nil, ErrUnsupported
				}
				get := func(k, defaultValue string) any {
					if v, ok := item[k]; ok {
						return v
					}
					return defaultValue
				}
				chats = append(chats, Object{"chat_id": get("chat_id", ""), "name": get("name", "(未命名)"), "description": get("description", "")})
			}
			if len(chats) == 0 {
				result = Object{"ok": false, "msg": "Lark 返回空列表。请确认：本应用已被添加为群聊机器人。", "chats": chats}
			} else {
				lines := []string{}
				for _, v := range chats {
					c := obj(v)
					s := "- " + str(c["name"]) + " (" + str(c["chat_id"]) + ")"
					if truth(c["description"]) {
						s += "\n  " + str(c["description"])
					}
					lines = append(lines, s)
				}
				result = Object{"ok": true, "msg": "共 " + str(len(chats)) + " 个群聊：\n" + strings.Join(lines, "\n"), "chats": chats}
			}
		}
	}
	return p.storeResult(ctx, "last_chats", result)
}
