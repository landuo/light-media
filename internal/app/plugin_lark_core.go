package app

import (
	"context"
	"strings"
	"time"

	lark "light-media/internal/plugins/larkmessager"
)

func (a *App) larkInboxForJob(ctx context.Context, j Job, id string, core *pluginLarkCoreExecution) (pluginLarkInbox, error) {
	var f pluginLarkInbox
	var instance string
	var generation int64
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT instance_id,generation FROM plugin_lark_inbox WHERE inbox_id=?`, id).Scan(&instance, &generation); e != nil {
		return f, e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return f, e
	}
	defer tx.Rollback()
	root, e := a.pluginStore.larkRootRead(ctx, tx, instance, generation)
	if e != nil {
		return f, e
	}
	f, ok := root.Inboxes[id]
	if !ok || f.Job != j.ID {
		return f, lark.ErrDenied
	}
	if e = a.pluginStore.larkInboxBackingTx(ctx, tx, f); e != nil {
		return f, e
	}
	actual, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, j.ID))
	if e != nil {
		return f, e
	}
	if actual.State != "running" || actual.Generation != j.Generation || !larkSameRaw(actual.Payload, j.Payload) {
		return f, lark.ErrDenied
	}
	if f.State == "unknown" {
		return f, larkUnknownError()
	}
	if f.State != "done" && f.State != "failed" {
		f.State = "started"
		if e = a.pluginStore.larkSaveInboxTx(ctx, tx, root, f); e != nil {
			return f, e
		}
	}
	if f.State == "done" || f.State == "failed" {
		return f, tx.Commit()
	}
	core.inbox = f
	release, e := a.pluginStore.larkKeepCore(ctx, core)
	if e != nil {
		return f, e
	}
	if e = tx.Commit(); e != nil {
		release()
		return f, e
	}
	core.release = release
	return f, nil
}
func (a *App) finishLarkInbox(ctx context.Context, core *pluginLarkCoreExecution, cause error) error {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	root, f, e := a.larkCoreScopeTx(ctx, tx, core)
	if e != nil {
		return e
	}
	f.State = "done"
	if cause != nil {
		f.State = "failed"
		f.ErrorCode = pluginErrorCodeInternal(cause)
		if f.ErrorCode == "LARK_UNCONFIRMED" || f.ErrorCode == "LARK_EVIDENCE_MISSING" {
			f.State = "unknown"
		}
	}
	if e = a.pluginStore.larkSaveInboxTx(ctx, tx, root, f); e != nil {
		return e
	}
	return tx.Commit()
}
func (a *App) runLarkMessage(ctx context.Context, j Job, id string) (resultErr error) {
	core := &pluginLarkCoreExecution{a: a, job: j}
	f, e := a.larkInboxForJob(ctx, j, id, core)
	if e != nil {
		return e
	}
	if core.release != nil {
		defer core.release()
	}
	if f.State == "done" {
		return nil
	}
	if f.State == "failed" {
		return bad(f.ErrorCode, "Lark原消息流程已失败，保留原始处理记录")
	}
	// Forward returns before the source HTTP run finishes, matching the original
	// detached MessageChain boundary. Wait without retaining SQL/owner resources.
	timer := time.NewTicker(25 * time.Millisecond)
	defer timer.Stop()
	for {
		origin, e := a.pluginStore.Run(ctx, f.OriginRun)
		if e != nil {
			return e
		}
		if origin.State == "succeeded" {
			break
		}
		if origin.State == "failed" || origin.State == "cancelled" || origin.State == "reconcile_pending" {
			return larkUnknownError()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	ctx = context.WithValue(ctx, pluginLarkCoreKey{}, core)
	defer func() {
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if e := a.finishLarkInbox(finish, core, resultErr); e != nil {
			resultErr = e
		}
	}()
	raw, e := a.larkCoreModule(ctx, core, pluginLarkModuleInput{Name: "message_parser", Source: "Lark", Metadata: f.Payload})
	if e != nil {
		return e
	}
	info, e := lark.DecodeSourceObject(raw)
	if e != nil {
		return e
	}
	if info == nil {
		return nil
	}
	ctx = context.WithValue(ctx, pluginLarkCallerKey{}, pluginLarkCaller{Username: mustCleanSeedJSON(info["username"])})
	user := larkSourceText(info["userid"])
	text := strings.TrimSpace(larkSourceText(info["text"]))
	if user == "" {
		return nil
	}
	images, _ := info["images"].([]any)
	audio, _ := info["audio_refs"].([]any)
	files, _ := info["files"].([]any)
	if text == "" && len(images)+len(audio)+len(files) == 0 {
		return nil
	}
	messageID, chatID := larkSourceText(info["message_id"]), larkSourceText(info["chat_id"])
	var status lark.Object
	if !strings.HasPrefix(text, "CALLBACK:") {
		raw, e = a.larkCoreModule(ctx, core, pluginLarkModuleInput{Name: "mark_message_processing_started", Channel: "飞书", Source: "Lark", User: user, MessageID: messageID, ChatID: chatID, Text: text})
		if e != nil {
			return e
		}
		status, e = lark.DecodeSourceObject(raw)
		if e != nil {
			return e
		}
	}
	defer func() {
		if code := pluginErrorCodeInternal(resultErr); code == "LARK_UNCONFIRMED" || code == "LARK_EVIDENCE_MISSING" {
			return
		}
		_, e := a.larkCoreModule(ctx, core, pluginLarkModuleInput{Name: "mark_message_processing_finished", Channel: "飞书", Source: "Lark", MessageID: messageID, Status: status})
		if e != nil && resultErr == nil {
			resultErr = e
		}
	}()
	return a.larkRouteCore(ctx, core, info, text, user, messageID, chatID)
}
func (a *App) larkCoreReply(ctx context.Context, core *pluginLarkCoreExecution, user, chat, title, text string) error {
	_, e := a.larkCoreModule(ctx, core, pluginLarkModuleInput{Name: "post_message", Notification: lark.Notification{Title: title, Text: text, UserID: user, Targets: lark.Object{"feishu_chat_id": chat}}})
	return e
}

// The original AI-disabled responses are reachable without an AI provider.
// Other original commands/media routes are added through the real P0 ports;
// an absent consumer is an explicit failure, never a synthetic success reply.
func (a *App) larkRouteCore(ctx context.Context, core *pluginLarkCoreExecution, info lark.Object, text, user, messageID, chatID string) error {
	lower := strings.ToLower(text)
	if strings.HasPrefix(text, "/") && !strings.HasPrefix(lower, "/ai") {
		return a.larkDispatchCommand(ctx, core, text, user)
	}
	if lower == "/ai" || strings.HasPrefix(lower, "/ai ") {
		return a.larkCoreReply(ctx, core, user, chatID, "MoviePilot智能助手未启用，请在系统设置中启用", "")
	}
	if handled, e := a.larkMediaInteraction(ctx, core, text, user, messageID, chatID); handled || e != nil {
		return e
	}
	return bad("LARK_CORE_DEPENDENCY_PENDING", "原消息命令与用户消息事件消费者尚未全部接入")
}
