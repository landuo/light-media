package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"

	"light-media/internal/plugininput"
	"light-media/internal/plugins"
)

type originalPluginInputRuntime struct{ plugininput.Manager }

func (s *originalPluginInputRuntime) clear() { s.Clear() }

func originalInputCapability(method string) bool {
	return method == plugins.InputCreateCapability || method == plugins.InputGetCapability || method == plugins.InputPopCapability || method == plugins.InputRemoveCapability
}

// UseNumber keeps chat/user identifiers intact across the JSON boundary. Like
// the source helper's Union[str,int], IDs are strings or integers, never floats.
func originalInputID(value any, required bool) (*string, error) {
	if value == nil {
		if required {
			return nil, bad("INVALID_PLUGIN_INPUT", "输入会话需要用户标识")
		}
		return nil, nil
	}
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case json.Number:
		text = v.String()
		if strings.ContainsAny(text, ".eE") {
			return nil, bad("INVALID_PLUGIN_INPUT", "输入会话标识必须为字符串或整数")
		}
	default:
		return nil, bad("INVALID_PLUGIN_INPUT", "输入会话标识必须为字符串或整数")
	}
	if !required && text == "" {
		return nil, nil
	}
	return &text, nil
}

func (a *App) originalInputResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if scope.object == nil || scope.object.app != a || scope.phase == "stop" && method != plugins.InputRemoveCapability {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "输入会话需要当前插件实例")
	}
	if len(raw) == 0 || len(raw) > 128<<10 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, bad("INVALID_PLUGIN_INPUT", "输入会话参数无效")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	owner := scope.installation.InstanceID
	if method == plugins.InputRemoveCapability {
		var input struct {
			RequestID string `json:"request_id"`
		}
		if err := originalResourceDecode(raw, &input); err != nil {
			return nil, err
		}
		a.pluginOriginalInput.Remove(input.RequestID, owner)
		return json.RawMessage(`null`), nil
	}
	var input plugins.InputInteractionRequest
	if err := decoder.Decode(&input); err != nil {
		return nil, bad("INVALID_PLUGIN_INPUT", "输入会话参数无效")
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return nil, bad("INVALID_PLUGIN_INPUT", "输入会话参数无效")
	}
	user, err := originalInputID(input.UserID, true)
	if err != nil {
		return nil, err
	}
	chat, err := originalInputID(input.ChatID, false)
	if err != nil {
		return nil, err
	}
	if method == plugins.InputGetCapability || method == plugins.InputPopCapability {
		return json.Marshal(a.pluginOriginalInput.Lookup(plugininput.NewKey(*user, input.Channel, input.Source, chat), owner, method == plugins.InputPopCapability, time.Now()))
	}
	prompt, err := originalInputID(input.PromptMessageID, false)
	if err != nil {
		return nil, err
	}
	timeout := 120
	if input.TimeoutSeconds != nil {
		timeout = *input.TimeoutSeconds
	}
	if timeout > int((time.Duration(1<<63-1))/time.Second) {
		return nil, bad("INVALID_PLUGIN_INPUT", "输入会话超时超出时钟范围")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	// Close seals admission before clearing the store. Keep insertion in that
	// same critical section so an already running callback cannot repopulate it.
	a.pluginOriginalBackground.mu.Lock()
	defer a.pluginOriginalBackground.mu.Unlock()
	if a.pluginOriginalBackground.closed {
		return nil, bad("PLUGIN_NOT_LOADED", "插件管理器已停止")
	}
	request, err := a.pluginOriginalInput.Create(plugins.PendingInputInteraction{UserID: *user, PluginID: owner, Channel: input.Channel, Source: input.Source, Username: input.Username, ChatID: chat, PromptID: input.PromptID, PromptMessageID: prompt, Payload: input.Payload, TimeoutSeconds: timeout}, time.Now())
	if err != nil {
		return nil, err
	}
	return json.Marshal(request)
}

type originalInputMessage struct {
	Text, User                                          string
	Channel, Source, Username, ChatID, ReplyToMessageID *string
	HasMedia                                            bool
}

// This is the MessageChain branch before /noai, slash commands and AI routing.
// feedback is a local Web response, not a claimed delivery through a channel.
func (a *App) consumeOriginalPluginInput(ctx context.Context, input originalInputMessage) (consumed bool, feedback string) {
	trimmed := strings.TrimFunc(input.Text, func(r rune) bool {
		return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f
	})
	if trimmed == "" || input.HasMedia || strings.HasPrefix(input.Text, "CALLBACK:") {
		return false, ""
	}
	cancel := false
	switch strings.ToLower(trimmed) {
	case "取消", "退出", "q", "quit", "exit":
		cancel = true
	}
	request, status := a.pluginOriginalInput.Consume(plugininput.NewKey(input.User, input.Channel, input.Source, input.ChatID), input.ReplyToMessageID, cancel, time.Now())
	if request == nil {
		return false, ""
	}
	data := map[string]any{"plugin_id": request.PluginID, "__mp_target_plugin_id": request.PluginID, "userid": input.User, "channel": input.Channel, "source": input.Source, "username": input.Username, "chat_id": input.ChatID, "reply_to_message_id": input.ReplyToMessageID, "prompt_id": request.PromptID, "input_session_id": request.RequestID, "payload": request.Payload}
	prefix := "plugin_input"
	consumed = true
	if status == "expired" {
		prefix = "plugin_input_expired"
		data["expired"] = true
		consumed = !strings.HasPrefix(trimmed, "/")
		feedback = "插件输入已超时，请重新发起操作。"
	} else if cancel {
		prefix = "plugin_input_cancel"
		data["cancelled"] = true
		feedback = "已取消插件输入"
	} else {
		data["input_text"] = input.Text
	}
	data["text"] = prefix + "|" + request.RequestID
	raw, err := json.Marshal(data)
	if err != nil {
		return false, ""
	}
	a.broadcastOriginalPluginEvent(context.WithoutCancel(ctx), "message.action", raw)
	if feedback != "" {
		value := func(v *string) any {
			if v == nil {
				return nil
			}
			return *v
		}
		if _, err := a.postOriginalPluginMessage(ctx, map[string]any{"channel": value(input.Channel), "source": value(input.Source), "userid": input.User, "username": value(input.Username), "title": feedback, "save_history": false}); err != nil {
			a.log("ERROR", "plugins", "input_feedback_failed", "插件输入反馈发送失败", "", "")
		}
	}
	return consumed, feedback
}
