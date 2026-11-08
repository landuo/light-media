package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"light-media/internal/commandrouter"
	"light-media/internal/notice"
	"light-media/internal/plugins"
)

func (a *App) originalHostMessageInboundResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	if scope.object == nil || scope.object.app != a || scope.phase == "stop" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "接收消息需要当前插件调用")
	}
	lark := scope.object.installation.PluginID == "LarkMessager"
	grant, _ := ctx.Value(originalLarkWebhookKey{}).(originalLarkWebhookGrant)
	if lark && (scope.phase != "http" || grant.object != scope.object) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "Lark 入站消息须绑定实际回调路由")
	}
	var input plugins.ReceiveMessageRequestV1
	if err := originalResourceDecodeBounded(raw, &input, plugins.MaxPayloadBytes); err != nil {
		return nil, err
	}
	if input.Form == nil {
		input.Form = plugins.MessageValues{}
	}
	if input.Args == nil {
		input.Args = plugins.MessageValues{}
	}
	if source, present := input.Args.Get("source"); present {
		if input.Source != nil && *input.Source != source {
			return nil, bad("INVALID_PLUGIN_VALUE", "消息 source 与参数不一致")
		}
		input.Source = &source
	} else if input.Source != nil {
		input.Args["source"] = []string{*input.Source}
	}
	if lark && (input.Source == nil || *input.Source != "Lark") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "Lark 回调只能提交其自身消息来源")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	// Reuse the existing exact-object background lease: shutdown revokes it and
	// joins its goroutine. The completed producer RPC is not reused as a grant.
	lease, child, err := scope.object.reserveOriginalSubmission()
	if err != nil {
		return nil, err
	}
	if lark {
		child = context.WithValue(child, originalLarkIncomingKey{}, grant)
	}
	go func() {
		defer a.wg.Done()
		defer scope.object.releaseOriginalSubmission(lease)
		if err := a.processOriginalIncomingMessage(child, input); err != nil {
			// Body, parser values and transport errors can contain private channel
			// material. Ordinary failure is observable without logging those values.
			a.log("ERROR", "plugins", "incoming_message_failed", "普通入站消息处理失败", "", "")
		}
	}()
	return json.Marshal(plugins.ReceiveMessageResultV1{SchemaVersion: 1, Accepted: true})
}

type originalIncomingMessage struct {
	UserID           any             `json:"userid"`
	Username         any             `json:"username"`
	Channel          *string         `json:"channel"`
	Source           *string         `json:"source"`
	Text             *string         `json:"text"`
	MessageID        any             `json:"message_id"`
	ChatID           any             `json:"chat_id"`
	ReplyToMessageID any             `json:"reply_to_message_id"`
	Images           json.RawMessage `json:"images"`
	AudioRefs        json.RawMessage `json:"audio_refs"`
	Files            json.RawMessage `json:"files"`
}

func originalIncomingStrip(value string) string {
	return strings.TrimFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
}

func (a *App) processOriginalIncomingMessage(ctx context.Context, input plugins.ReceiveMessageRequestV1) error {
	body, err := plugins.ModuleResultBytes(input.Body)
	if err != nil {
		return err
	}
	kwargs, err := json.Marshal(map[string]any{"source": input.Source, "body": body, "form": input.Form, "args": input.Args})
	if err != nil {
		return err
	}
	var attempts originalModuleAttempts
	var groups []originalPluginModuleGroup
	if grant, ok := ctx.Value(originalLarkIncomingKey{}).(originalLarkWebhookGrant); ok {
		// Raw Lark text can carry a site's login credentials. Only the
		// exact admitted parser sees it, even when other plugins or clones
		// have registered a general message_parser.
		groups, err = a.originalPluginModules(ctx, grant.object.installation.InstanceID)
		if err != nil {
			return err
		}
		if len(groups) != 1 || groups[0].object != grant.object {
			return nil
		}
	}
	raw, err := a.invokeOriginalPluginModuleGroups(ctx, "message_parser", nil, nil, kwargs, &attempts, groups)
	if err != nil {
		return err
	}
	if !originalViewJSONTruth(raw) {
		if attempts.Failed > 0 {
			return errors.New("incoming message parser failed")
		}
		a.log("INFO", "plugins", "incoming_message_unrecognized", "普通入站消息未识别到有效内容", "", "")
		return nil
	}
	var info originalIncomingMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&info) != nil {
		return errors.New("invalid incoming message parser result")
	}
	if info.Channel != nil {
		switch *info.Channel {
		case "微信", "飞书", "微信ClawBot", "Telegram", "Slack", "Discord", "SynologyChat", "VoceChat", "Web", "WebPush", "QQ":
		default:
			return errors.New("invalid ordinary incoming message channel")
		}
	}
	user, err := originalInputID(info.UserID, false)
	if err != nil {
		return err
	}
	if user == nil || *user == "" {
		return nil
	}
	username := *user
	if info.Username != nil {
		name, err := originalInputID(info.Username, true)
		if err != nil {
			return err
		}
		if *name != "" {
			username = *name
		}
	}
	chat, err := originalInputID(info.ChatID, false)
	if err != nil {
		return err
	}
	reply, err := originalInputID(info.ReplyToMessageID, false)
	if err != nil {
		return err
	}
	text := ""
	if info.Text != nil {
		text = originalIncomingStrip(*info.Text)
	}
	hasMedia := originalIncomingImagesPresent(info.Images) || originalViewJSONTruth(info.AudioRefs) || originalViewJSONTruth(info.Files)
	if text == "" && !hasMedia {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.handleOriginalIncomingMessage(ctx, info, text, *user, username, chat, reply, hasMedia)
}

func (a *App) handleOriginalIncomingMessage(ctx context.Context, info originalIncomingMessage, text, user, username string, chat, reply *string, hasMedia bool) error {
	lark, verified := originalLarkSlashIngress(ctx, info)
	siteReply := originalSlashReply{info: info, user: user, username: username}
	var siteSession, subscribeSession originalSlashSession
	sensitive := false
	if lark {
		subscribeSession = a.pluginOriginalSubscribes.current(user)
		siteSession, sensitive = a.pluginOriginalSiteInteractions.incoming(user, text, subscribeSession)
	}
	input := originalInputMessage{Text: text, User: user, Username: &username, Channel: info.Channel, Source: info.Source, ChatID: chat, ReplyToMessageID: reply, HasMedia: hasMedia}
	if !sensitive {
		if consumed, _ := a.consumeOriginalPluginInput(ctx, input); consumed {
			return nil
		}
	}
	// Fixed MessageChain's audio transcription calls AgentCapabilityManager and
	// LLMHelper; that branch is excluded. Parser audio/image/file values remain
	// intact and still prevent an attachment from consuming a pending text input.
	commandText, command := originalWebCommandText(text)
	callback := strings.HasPrefix(text, "CALLBACK:")
	publicText := text
	if sensitive {
		publicText = "站点凭据输入"
		if command {
			publicText = "/sites"
		}
	}
	if !callback && !sensitive {
		if err := a.saveOriginalIncomingChannelMessage(ctx, info.Channel, info.Source, username, text); err != nil {
			return err
		}
	}
	status, err := a.startOriginalIncomingProcessing(ctx, info, publicText)
	if err != nil {
		return err
	}
	defer a.finishOriginalIncomingProcessing(ctx, info, status)
	if callback {
		return a.dispatchOriginalIncomingCallback(ctx, info, text, username)
	}
	// The source checks again inside _handle_message_core after starting the
	// channel status. A status module can create a pending input, or the first
	// check can expire a more-specific request and expose a fallback session.
	if !sensitive {
		if consumed, _ := a.consumeOriginalPluginInput(ctx, input); consumed {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if command {
		// Preserve processing_status in the ordinary event, then reuse the exact
		// registered-command consumer. Mark it consumed to avoid duplicate work.
		raw, err := json.Marshal(map[string]any{"cmd": publicText, "channel": info.Channel, "source": info.Source, "user": info.UserID, "processing_status": status})
		if err != nil {
			return err
		}
		a.broadcastOriginalPluginEventCoreState(ctx, "command.excute", raw, true)
		handled, err := a.dispatchOriginalCommand(ctx, originalCommandEvent{Text: publicText, Channel: info.Channel, Source: info.Source, User: info.UserID})
		if err != nil || handled || !lark {
			return err
		}
		if verified {
			name, args := commandrouter.Split(commandText)
			siteReply.username = ""
			switch name {
			case "/sites":
				return a.openOriginalSiteInteraction(ctx, siteReply, args)
			case "/subscribes":
				return a.openOriginalSubscribeInteraction(ctx, siteReply, args)
			case "/downloading":
				return a.originalChatDownloading(ctx, siteReply)
			case "/transfer":
				return a.originalChatTransferCommand(ctx, siteReply)
			case "/version":
				return siteReply.post(ctx, a, a.originalChatVersionText(ctx), false)
			case "/clear_cache":
				return a.originalChatClearCache(ctx, siteReply)
			case "/restart":
				return a.originalChatRestart(ctx, siteReply)
			case "/redo":
				return a.originalChatTransferRedo(ctx, siteReply, args)
			}
		}
		return nil
	}
	if verified {
		if subscribeSession.created.After(siteSession.created) {
			if sensitive && !originalSubscribePendingIDs(subscribeSession.awaiting, text) {
				return siteReply.post(ctx, a, "请使用「搜索 <编号>」或「删除 <编号>」继续订阅操作", false)
			}
			if handled, err := a.originalSubscribeInteractionText(ctx, siteReply, subscribeSession, text); handled || err != nil {
				return err
			}
		} else if handled, err := a.originalSiteInteractionText(ctx, siteReply, siteSession, text); handled || err != nil {
			return err
		}
	}
	if sensitive {
		return nil
	}
	if verified {
		if handled, err := a.originalChatMediaText(ctx, siteReply, text); handled || err != nil {
			return err
		}
	}
	a.emitStandardBusinessEvent(ctx, "user.message", map[string]any{"text": commandText, "userid": info.UserID, "channel": info.Channel, "source": info.Source, "chat_id": info.ChatID, "reply_to_message_id": info.ReplyToMessageID})
	return nil
}

func (a *App) saveOriginalIncomingChannelMessage(ctx context.Context, channel, source *string, user, text string) error {
	n := Notification{ID: id("message_"), Message: text, UserID: &user, Action: json.RawMessage(`0`), Note: json.RawMessage(`{}`), CreatedAt: now(), Read: true}
	if channel != nil {
		n.Channel = *channel
	}
	if source != nil {
		n.Source = *source
	}
	fields := notice.Object{"channel": channel, "source": source, "userid": user, "text": text, "action": 0}
	record, err := a.originalMessageHistoryRecord(n, fields)
	if err != nil {
		return err
	}
	record.Notification = Notification{ID: n.ID, CreatedAt: n.CreatedAt, Read: true}
	return a.Store.Put(ctx, "message", n.ID, record)
}

// Fixed callback-capable ordinary channels; processing status excludes QQ.
func originalIncomingChannelActions(channel *string) bool {
	if channel == nil {
		return false
	}
	switch *channel {
	case "Telegram", "飞书", "Slack", "Discord", "QQ":
		return true
	}
	return false
}

// CommingMessage.MessageImage.normalize_list drops unsupported scalars and
// dictionaries without a truthy ref/url/image_url/file_url. Such values must
// not prevent ordinary text from consuming its pending plugin input.
func originalIncomingImagesPresent(raw json.RawMessage) bool {
	if !originalViewJSONTruth(raw) {
		return false
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		values = []json.RawMessage{raw}
	}
	for _, value := range values {
		var text string
		if json.Unmarshal(value, &text) == nil && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return true
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(value, &fields) == nil {
			for _, key := range []string{"ref", "url", "image_url", "file_url"} {
				if originalViewJSONTruth(fields[key]) {
					return true
				}
			}
		}
	}
	return false
}

func (a *App) dispatchOriginalIncomingCallback(ctx context.Context, info originalIncomingMessage, text, username string) error {
	if !originalIncomingChannelActions(info.Channel) {
		return nil
	}
	data := strings.TrimPrefix(text, "CALLBACK:")
	if number, ok := originalChatTransferRetryNumber(data); ok {
		lark, verified := originalLarkSlashIngress(ctx, info)
		if lark {
			if !verified {
				return nil
			}
			user, err := originalInputID(info.UserID, false)
			if err != nil {
				return err
			}
			if user != nil && *user != "" {
				return a.originalChatTransferRetryCallback(ctx, originalSlashReply{info: info, user: *user, username: username}, number)
			}
			return nil
		}
	}
	kind, _, _ := strings.Cut(data, ":")
	if lark, verified := originalLarkSlashIngress(ctx, info); lark && (kind == "sites" || kind == "subscribes") {
		if !verified {
			return nil
		}
		user, err := originalInputID(info.UserID, false)
		if err != nil {
			return err
		}
		if user != nil && *user != "" {
			reply := originalSlashReply{info: info, user: *user, username: username}
			callback := a.originalSiteInteractionCallback
			if kind == "subscribes" {
				callback = a.originalSubscribeInteractionCallback
			}
			if handled, err := callback(ctx, reply, data); handled || err != nil {
				return err
			}
		}
	}
	if lark, verified := originalLarkSlashIngress(ctx, info); lark {
		if _, action, _ := originalChatMediaCallback(data); action != "" {
			if !verified {
				return nil
			}
			user, err := originalInputID(info.UserID, false)
			if err != nil {
				return err
			}
			if user != nil && *user != "" {
				reply := originalSlashReply{info: info, user: *user, username: username}
				if handled, err := a.originalChatMediaCallback(ctx, reply, data); handled || err != nil {
					return err
				}
			}
		}
	}
	if strings.HasPrefix(data, "[PLUGIN]") {
		plugin, content, ok := strings.Cut(data, "|")
		if !ok {
			return errors.New("invalid plugin message callback")
		}
		a.emitStandardBusinessEvent(ctx, "message.action", map[string]any{"plugin_id": strings.ReplaceAll(plugin, "[PLUGIN]", ""), "text": content, "userid": info.UserID, "channel": info.Channel, "source": info.Source, "original_message_id": info.MessageID, "original_chat_id": info.ChatID})
		return nil
	}
	_, err := a.postOriginalPluginMessage(ctx, map[string]any{"channel": info.Channel, "source": info.Source, "userid": info.UserID, "username": username, "title": "回调数据格式错误，请检查！", "save_history": false})
	return err
}

func (a *App) startOriginalIncomingProcessing(ctx context.Context, info originalIncomingMessage, text string) (map[string]any, error) {
	if !originalIncomingChannelActions(info.Channel) || info.Channel != nil && *info.Channel == "QQ" {
		return nil, nil
	}
	kwargs, err := json.Marshal(map[string]any{"channel": info.Channel, "source": info.Source, "userid": info.UserID, "message_id": info.MessageID, "chat_id": info.ChatID, "text": text})
	if err != nil {
		return nil, err
	}
	raw, err := a.invokeOriginalPluginModules(ctx, "mark_message_processing_started", nil, nil, kwargs)
	if err != nil {
		return nil, err
	}
	var status map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&status) != nil || len(status) == 0 {
		return nil, nil
	}
	for key, fallback := range map[string]any{"userid": info.UserID, "message_id": info.MessageID, "chat_id": info.ChatID} {
		if _, found := status[key]; !found {
			status[key] = fallback
		}
	}
	metadata, ok := status["metadata"].(map[string]any)
	if !ok {
		metadata = map[string]any{}
	}
	return map[string]any{"channel": info.Channel, "source": info.Source, "userid": status["userid"], "message_id": status["message_id"], "chat_id": status["chat_id"], "metadata": metadata}, nil
}

func (a *App) finishOriginalIncomingProcessing(ctx context.Context, info originalIncomingMessage, status map[string]any) {
	if status == nil {
		return
	}
	fallback := func(key string, value any) any {
		if sourceSubscriptionTruth(status[key]) {
			return status[key]
		}
		return value
	}
	kwargs, err := json.Marshal(map[string]any{"channel": info.Channel, "source": fallback("source", info.Source), "userid": fallback("userid", info.UserID), "message_id": fallback("message_id", info.MessageID), "chat_id": fallback("chat_id", info.ChatID), "status": status})
	if err == nil {
		_, _ = a.invokeOriginalPluginModules(ctx, "mark_message_processing_finished", nil, nil, kwargs)
	}
}
