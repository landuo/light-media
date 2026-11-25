package larkmessager

import (
	"context"
	_ "embed"
	"errors"
	"strings"
)

//go:embed test-card.json
var testCardJSON []byte

func BuildTestCard(timestamp string) (_out Object, _error error) {
	defer sourceErrors(&_error)
	var card Object
	if e := decode(testCardJSON, &card); e != nil {
		return nil, e
	}
	var replace func(any) any
	replace = func(v any) any {
		switch v := v.(type) {
		case string:
			if v == "{{LARK_TEST_TIME}}" {
				return timestamp
			}
			return v
		case []any:
			for i, x := range v {
				v[i] = replace(x)
			}
		case Object:
			for k, x := range v {
				v[k] = replace(x)
			}
		}
		return v
	}
	return obj(replace(card)), nil
}

type ResolvedTarget struct {
	ID   string
	Type string
}

func (p *Plugin) ResolveTargets(ctx context.Context, user string) ([]ResolvedTarget, string) {
	s, c, _ := p.snapshot()
	targets := []ResolvedTarget{}
	seen := map[string]bool{}
	warnings := []string{}
	add := func(id, typ string) {
		if id != "" && !seen[id] {
			seen[id] = true
			targets = append(targets, ResolvedTarget{id, typ})
		}
	}
	if s.ChatID != "" {
		add(s.ChatID, "chat_id")
	}
	if s.UserID != "" {
		uid := strip(s.UserID)
		if strings.HasPrefix(uid, "ou_") {
			add(uid, "open_id")
		} else if c != nil {
			parsed := false
			var result map[string]string
			var err error
			if strings.Contains(uid, "@") {
				result, err = c.BatchGetID(ctx, []string{uid}, nil, nil)
				parsed = true
			} else if sourceDigits(uid) {
				result, err = c.BatchGetID(ctx, nil, []string{uid}, nil)
				parsed = true
			} else {
				warnings = append(warnings, "用户解析失败：Lark 不支持通过工号/用户名（"+uid+"）查询用户。请填写邮箱地址或手机号，或在 Lark 开放平台查看用户 Open ID 并填写 ou_xxx")
			}
			if err != nil {
				var apiError *APIError
				if errors.As(err, &apiError) && apiError.ContactPermissionMissing {
					warnings = append(warnings, "用户解析失败：缺少权限 contact:user.id:readonly。请在 Lark 开放平台 > 权限管理 开通此权限，并重新发布应用版本")
				} else {
					warnings = append(warnings, "用户解析失败：Lark API request failed")
				}
			} else if parsed {
				if id := result[uid]; id != "" {
					add(id, "open_id")
				} else {
					warnings = append(warnings, "用户解析失败：找不到用户（"+uid+"），请检查邮箱/手机号是否正确")
				}
			}
		} else {
			warnings = append(warnings, "用户解析失败：插件未启用或 Client 未初始化")
		}
	}
	if user != "" {
		typ := "open_id"
		if strings.HasPrefix(user, "oc_") {
			typ = "chat_id"
		}
		add(user, typ)
	}
	return targets, strings.Join(warnings, "；")
}
func pythonStringList(v []string) string {
	parts := []string{}
	for _, s := range v {
		quote := "'"
		if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
			quote = "\""
		}
		s = strings.ReplaceAll(s, "\\", "\\\\")
		s = strings.ReplaceAll(s, quote, "\\"+quote)
		s = strings.ReplaceAll(s, "\n", "\\n")
		s = strings.ReplaceAll(s, "\r", "\\r")
		s = strings.ReplaceAll(s, "\t", "\\t")
		parts = append(parts, quote+s+quote)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
func (p *Plugin) Test(ctx context.Context) (_out Object, _error error) {
	defer sourceErrors(&_error)
	_, c, _ := p.snapshot()
	store := func(ok bool, msg string) (Object, error) {
		return p.storeResult(ctx, "last_test_result", Object{"ok": ok, "msg": msg})
	}
	if c == nil {
		return store(false, "插件未启用，或 App ID / App Secret 未配置")
	}
	targets, warning := p.ResolveTargets(ctx, "")
	if len(targets) == 0 {
		if warning == "" {
			warning = "未配置默认通知用户或群聊，请至少填一项"
		}
		return store(false, warning)
	}
	card, e := BuildTestCard(p.host.Now().Format("2006-01-02 15:04:05"))
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for _, target := range targets {
		id, e := c.SendCard(ctx, target.ID, target.Type, card)
		if e != nil {
			return store(false, "发送失败：Lark API request failed")
		}
		ids = append(ids, str(id))
	}
	message := "测试消息已发送 " + str(len(ids)) + " 条，请到 Lark 查收（message_ids=" + pythonStringList(ids) + "）"
	if warning != "" {
		message += "；注意：" + warning
	}
	return store(true, message)
}
