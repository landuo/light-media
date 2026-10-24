package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"

	"light-media/internal/commandrouter"
)

// originalCommandEvent is internal CommandExcute input. HTTP clients supply
// only message text; channel, source and user come from the authenticated core
// ingress. No HTTP route accepts an event name, action, method, or event data.
type originalCommandEvent struct {
	Text    string
	Channel string
	Source  string
	User    any
}

// MessageChain sends CommandExcute (the original enum value is intentionally
// spelled "command.excute"). Registered plugin observers receive it too; the
// core Command consumer then resolves the command and sends its declared event.
func (a *App) publishOriginalCommandEvent(ctx context.Context, input originalCommandEvent) (bool, error) {
	raw, err := json.Marshal(map[string]any{
		"cmd": input.Text, "channel": input.Channel, "source": input.Source,
		"user": input.User, "processing_status": nil,
	})
	if err != nil {
		return false, err
	}
	a.broadcastOriginalPluginEvent(context.WithoutCancel(ctx), "command.excute", raw)
	return a.dispatchOriginalCommand(ctx, input)
}

// dispatchOriginalCommand mirrors Command.command_event -> __run_command ->
// send_plugin_event. A command resolves its declaration, deep-copies data, adds
// message context, and broadcasts the declared event. The SDK "event" handler
// remains responsible for interpreting data.action, including ignoring it.
func (a *App) dispatchOriginalCommand(ctx context.Context, input originalCommandEvent) (bool, error) {
	name, args := commandrouter.Split(input.Text)
	a.pluginOriginalCommandsMu.Lock()
	row, found := a.pluginOriginalCommands[name]
	if !found {
		a.pluginOriginalCommandsMu.Unlock()
		return false, nil
	}
	instance, _ := row["pid"].(string)
	loaded, present := a.pluginRunningInstances.Load(instance)
	if !present || !originalObjectStillLoaded(loaded.(*originalPluginInstance)) {
		a.pluginOriginalCommandsMu.Unlock()
		return false, nil
	}
	event, valid := row["event"].(string)
	if !valid || event == "" {
		a.pluginOriginalCommandsMu.Unlock()
		return false, bad("PLUGIN_COMMAND_INVALID", "插件未声明命令事件")
	}
	raw, err := json.Marshal(row["data"])
	a.pluginOriginalCommandsMu.Unlock()
	if err != nil {
		return false, err
	}
	data := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return false, bad("PLUGIN_COMMAND_INVALID", "插件命令参数不是对象")
	}
	if data == nil {
		data = map[string]any{}
	}
	data["channel"], data["source"], data["user"] = input.Channel, input.Source, input.User
	if args != "" {
		data["arg_str"] = args
	}
	raw, err = json.Marshal(data)
	if err != nil {
		return false, err
	}
	a.broadcastOriginalPluginEvent(context.WithoutCancel(ctx), event, raw)
	return true, nil
}

// standardWebCommandHTTP restores only the text-command branch of the fixed
// MoviePilot POST /api/v1/message/web endpoint. /api/message/web is the native
// host alias. The common API wrapper has already checked session and origin;
// the account name is read here, never trusted from request JSON or a header.
// This is not a substitute for a channel parser or the removed Lark pipeline.
func (a *App) standardWebCommandHTTP(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "message/web" && path != "v1/message/web" {
		return false
	}
	if r.Method != http.MethodPost {
		a.writeError(w, r, &APIError{"METHOD_NOT_ALLOWED", "仅支持 POST 文本命令", http.StatusMethodNotAllowed})
		return true
	}
	var user string
	if err := a.Store.DB.QueryRowContext(r.Context(), `SELECT username FROM admin WHERE id=1`).Scan(&user); err != nil || user == "" {
		a.writeError(w, r, &APIError{"UNAUTHORIZED", "请先登录", http.StatusUnauthorized})
		return true
	}
	text := ""
	if values := r.URL.Query()["text"]; len(values) != 0 {
		text = values[len(values)-1]
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") && r.ContentLength != 0 {
		var payload map[string]json.RawMessage
		if err := decodeBody(w, r, &payload); err != nil {
			a.writeError(w, r, err)
			return true
		}
		if raw, present := payload["text"]; present && string(raw) != "null" {
			if err := json.Unmarshal(raw, &text); err != nil {
				a.writeError(w, r, bad("INVALID_MESSAGE_TEXT", "消息 text 必须是字符串"))
				return true
			}
		} else if present {
			text = ""
		}
		// The original MessageChain routes slash commands before media/AI
		// handling. Image metadata never enters CommandExcute or command data;
		// this text-command bridge neither parses nor retrieves media.
	}
	// Other original MessageChain branches are deliberately not reported as
	// implemented. Unknown slash commands remain a source-compatible no-op.
	text, commandText := originalWebCommandText(text)
	if !commandText {
		a.writeError(w, r, &APIError{"MESSAGE_ROUTE_UNAVAILABLE", "本入口目前仅支持已注册的插件文本命令", http.StatusNotImplemented})
		return true
	}
	name, _ := commandrouter.Split(text)
	if _, preset := commandrouter.Lookup(name); preset {
		if _, _, registered := a.lookupOriginalPluginCommand(name); !registered {
			a.writeError(w, r, &APIError{"MESSAGE_ROUTE_UNAVAILABLE", "本入口尚未接入内建命令", http.StatusNotImplemented})
			return true
		}
	}
	accepted, err := a.publishOriginalCommandEvent(r.Context(), originalCommandEvent{Text: text, Channel: "Web", Source: user, User: user})
	if err != nil {
		a.writeError(w, r, err)
	} else {
		writeJSON(w, r, map[string]bool{"success": true, "accepted": accepted})
	}
	return true
}

// Keep the original explicit /noai escape and /ai prefix test, without
// implementing or silently swallowing the unrelated AI/message branches.
func originalWebCommandText(text string) (string, bool) {
	space := func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f }
	trimmed := strings.TrimFunc(text, space)
	if len(trimmed) >= 5 && strings.EqualFold(trimmed[:5], "/noai") {
		rest := trimmed[5:]
		if rest == "" {
			text = ""
		} else {
			for _, r := range rest {
				if space(r) || r == ':' || r == '：' {
					text = strings.TrimFunc(strings.TrimPrefix(rest, string(r)), space)
				}
				break
			}
		}
	}
	return text, strings.HasPrefix(text, "/") && !strings.HasPrefix(strings.ToLower(text), "/ai")
}
