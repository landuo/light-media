package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"light-media/internal/plugins"
	lark "light-media/internal/plugins/larkmessager"
)

func (o *pluginLarkOwner) invokeAPI(ctx context.Context, run PluginRun) (json.RawMessage, error) {
	tx, e := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	r, e := o.a.validatePluginHTTPRunTx(ctx, tx, run)
	if e == nil {
		e = tx.Commit()
	} else {
		tx.Rollback()
	}
	if e != nil {
		return nil, e
	}
	var value any
	switch r.Key {
	case "test":
		value, e = o.plugin.Test(ctx)
	case "status":
		value, e = o.plugin.Status(ctx)
	case "fetch_chats":
		value, e = o.plugin.FetchChats(ctx)
	case "webhook":
		c := o.plugin.ConfigTrusted()
		var decoded lark.SourceWebhook
		decoded, e = lark.DecodeSourceWebhook(r.Body, r.Headers, plugins.LastQuery(r.Query, "token"), c.EncryptKey, c.VerificationToken)
		if e != nil {
			return nil, e
		}
		if decoded.Accepted {
			x, e := o.execution(ctx)
			if e != nil {
				return nil, e
			}
			if e = larkGrantWebhook(x, decoded); e != nil {
				return nil, e
			}
			if e = o.plugin.DispatchTrusted(ctx, decoded.Body); e != nil {
				return nil, e
			}
		}
		return plugins.JSONHTTPResponse(decoded.Status, decoded.Reply)
	default:
		return nil, lark.ErrDenied
	}
	if e != nil {
		return nil, e
	}
	return plugins.JSONHTTPResponse(200, value)
}

// IDs originate only in the already authenticated immutable HTTP run. They are
// used to construct native grants before source dispatch, never read from SDK
// callback JSON. A SourceWebhook.Accepted value alone cannot call this helper:
// the enclosing execution capability must be the actual admitted HTTP owner.
func larkGrantWebhook(x *pluginLarkExecution, decoded lark.SourceWebhook) error {
	if x == nil || x.adapter != nil || x.scope.Action != lark.APICapability || !decoded.Accepted {
		return lark.ErrDenied
	}
	var payload lark.Object
	var e error
	switch decoded.EventType {
	case "im.message.receive_v1":
		payload, e = lark.ReceivePayload(decoded.Body)
	case "card.action.trigger":
		payload, _, e = lark.CardActionPayload(decoded.Body)
	default:
		return nil
	}
	if e != nil {
		return e
	}
	larkGrantMessagePayload(&x.binding, payload)
	return nil
}
func larkSourceText(v any) string {
	if v == nil {
		return ""
	}
	s, e := lark.PythonString(v)
	if e != nil {
		return ""
	}
	return s
}
func larkGrantMessagePayload(b *lark.Binding, payload lark.Object) {
	add := func(kind, id string) {
		if id != "" {
			b.Targets[kind] = append(b.Targets[kind], id)
		}
	}
	if id := larkSourceText(payload["message_id"]); id != "" {
		b.Messages = append(b.Messages, id)
	}
	add("chat_id", larkSourceText(payload["chat_id"]))
	if sender, ok := payload["sender"].(map[string]any); ok {
		open, user := larkSourceText(sender["open_id"]), larkSourceText(sender["user_id"])
		add("open_id", open)
		add("user_id", user)
		if open != "" {
			b.ProfileUsers = append(b.ProfileUsers, open)
		} else if user != "" {
			b.ProfileUsers = append(b.ProfileUsers, user)
		}
	}
}

// Original Page changes two KV flags. It therefore uses a real persistent
// action and the same SDK/deferred owner path, rather than the readonly view.
func (a *App) larkPage(ctx context.Context, in PluginInstallation) (json.RawMessage, error) {
	run, e := a.pluginStore.EnqueueRun(ctx, in.InstanceID, "page", nil)
	if e != nil {
		return nil, e
	}
	a.wakePlugins()
	timer := time.NewTicker(25 * time.Millisecond)
	defer timer.Stop()
	for {
		current, e := a.pluginStore.Run(ctx, run.ID)
		if e != nil {
			return nil, e
		}
		switch current.State {
		case "succeeded":
			return current.Result, nil
		case "failed", "cancelled", "reconcile_pending":
			return nil, bad("PLUGIN_VIEW_FAILED", "加载插件页面失败或等待核对，请查看执行记录")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func larkWorkerInput(run PluginRun) (string, json.RawMessage, error) {
	method, _, e := larkDeferredSpec(run)
	if e != nil {
		return "", nil, e
	}
	var inv plugins.Invocation
	if cleanupJournalStrict(run.Input, &inv) != nil {
		return "", nil, larkEvidenceError()
	}
	switch method {
	case lark.APICapability:
		var r plugins.HTTPRequest
		if cleanupJournalStrict(inv.Input, &r) != nil {
			return "", nil, larkEvidenceError()
		}
		return "http_api", mustCleanSeedJSON(plugins.HTTPRequest{RequestID: r.RequestID}), nil
	case lark.PageCapability:
		return "page", json.RawMessage(`{}`), nil
	default:
		return "invoke", mustCleanSeedJSON(plugins.Invocation{Action: inv.Action, Input: inv.Input}), nil
	}
}
