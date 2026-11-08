package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"light-media/internal/notice"
)

const (
	originalChatRestartKind = "lark_restart_receipt"
	originalChatRestartKey  = "pending"
)

type originalChatRestartReceipt struct {
	Version           int
	Instance, Account string
	User              string
	Requested         bool
}

func (a *App) originalChatRestart(ctx context.Context, reply originalSlashReply) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	user, err := originalInputID(reply.info.UserID, false)
	_, verified := originalLarkSlashIngress(ctx, reply.info)
	if err != nil || !verified || user == nil || *user != reply.user ||
		strings.EqualFold(strings.TrimSpace(*user), "system") || !a.originalChatSubscriptionIngress(ctx) {
		return bad("PLUGIN_CAPABILITY_DENIED", "重启系统需要已验证的 Lark 消息")
	}
	grant := ctx.Value(originalLarkIncomingKey{}).(originalLarkWebhookGrant)
	receipt := originalChatRestartReceipt{Version: 1, Instance: grant.object.installation.InstanceID, Account: originalChatRestartAccount(grant.object), User: *user}
	if receipt.Account == "" {
		return bad("PLUGIN_NOTIFICATION_PROVIDER_UNAVAILABLE", "Lark 重启通知实例未就绪")
	}
	group, err := a.originalChatRestartNoticeGroup(ctx, receipt)
	if err != nil {
		return err
	}
	if group.object != grant.object {
		return bad("PLUGIN_CAPABILITY_DENIED", "Lark 实例已重新加载，请重新发送重启命令")
	}
	prepared, err := a.pluginStore.seal(originalChatRestartKind, "system", originalChatRestartKey, mustCleanSeedJSON(receipt))
	if err != nil {
		return err
	}
	result, err := a.Store.DB.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO NOTHING`,
		originalChatRestartKind, originalChatRestartKey, prepared, now())
	if err != nil {
		return err
	}
	if inserted, err := result.RowsAffected(); err != nil || inserted == 0 {
		return err
	}
	requested := false
	defer func() {
		if !requested {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			if _, err := a.removeOriginalChatRestartReceipt(cleanup, prepared); err != nil {
				a.log("WARN", "runtime", "lark_restart_receipt_cleanup_failed", "无法清理未提交的 Lark 重启记录", "", "")
			}
		}
	}()

	if err := a.postOriginalChatRestartNotice(ctx, receipt, group, "系统正在重启，请耐心等候！"); err != nil {
		return err
	}
	receipt.Requested = true
	pending, err := a.pluginStore.seal(originalChatRestartKind, "system", originalChatRestartKey, mustCleanSeedJSON(receipt))
	if err != nil {
		return err
	}
	result, err = a.Store.DB.ExecContext(ctx, `UPDATE records SET payload=?,updated_at=? WHERE kind=? AND id=? AND payload=?`,
		pending, now(), originalChatRestartKind, originalChatRestartKey, prepared)
	if err != nil {
		return err
	}
	if updated, err := result.RowsAffected(); err != nil || updated != 1 {
		if err != nil {
			return err
		}
		return bad("RESTART_REQUEST_CHANGED", "Lark 重启请求已发生变化")
	}
	requested = true
	// The server owns its graceful shutdown and plugin code backup. The
	// command uses the same restart signal as the existing App supervisor.
	select {
	case a.RestartRequested <- struct{}{}:
	default:
	}
	return nil
}

// Called synchronously during Start before the listener accepts new commands.
// Reading the old receipt here keeps a new command in this App from completing
// itself. Only a pending receipt starts a cancellable background delivery.
func (a *App) completeOriginalChatRestart(ctx context.Context) {
	var sealed []byte
	err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, originalChatRestartKind, originalChatRestartKey).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) || ctx.Err() != nil {
		return
	}
	var receipt originalChatRestartReceipt
	if err == nil {
		var raw []byte
		raw, err = a.pluginStore.unseal(originalChatRestartKind, "system", originalChatRestartKey, sealed)
		if err == nil {
			err = cleanupJournalStrict(raw, &receipt)
		}
	}
	if err != nil || receipt.Version != 1 || receipt.Instance == "" || receipt.Account == "" || receipt.User == "" {
		a.log("WARN", "runtime", "lark_restart_receipt_invalid", "无法核对 Lark 重启记录", "", "")
		return
	}
	if !receipt.Requested {
		if _, err := a.removeOriginalChatRestartReceipt(ctx, sealed); err != nil {
			a.log("WARN", "runtime", "lark_restart_receipt_cleanup_failed", "无法清理未提交的 Lark 重启记录", "", "")
		}
		return
	}
	if !a.admitOriginalBackground() {
		return
	}
	go func() {
		defer a.wg.Done()
		title := "系统已重启完成！\n" + a.originalChatVersionText(ctx)
		group, routeErr := a.originalChatRestartNoticeGroup(ctx, receipt)
		if ctx.Err() != nil {
			return
		}
		// Claim before the outbound write so a restart after an ambiguous
		// provider response cannot replay this completion notice.
		claimed, err := a.removeOriginalChatRestartReceipt(ctx, sealed)
		if err != nil {
			a.log("WARN", "runtime", "lark_restart_receipt_cleanup_failed", "无法收敛 Lark 重启记录", "", "")
			return
		}
		if !claimed {
			return
		}
		if routeErr != nil || a.postOriginalChatRestartNotice(ctx, receipt, group, title) != nil {
			a.log("WARN", "runtime", "lark_restart_notice_failed", "无法向原 Lark 实例发送重启通知", "", "")
		}
	}()
}

func (a *App) removeOriginalChatRestartReceipt(ctx context.Context, sealed []byte) (bool, error) {
	result, err := a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind=? AND id=? AND payload=?`,
		originalChatRestartKind, originalChatRestartKey, sealed)
	if err != nil {
		return false, err
	}
	removed, err := result.RowsAffected()
	return removed == 1, err
}

func originalChatRestartAccount(object *originalPluginInstance) string {
	object.configMu.RLock()
	raw := copyOriginalBaseJSON(object.appliedConfig)
	object.configMu.RUnlock()
	var config struct {
		AppID string `json:"app_id"`
	}
	if json.Unmarshal(raw, &config) != nil || strings.TrimSpace(config.AppID) == "" {
		return ""
	}
	return tokenHash(strings.TrimSpace(config.AppID))
}

func (a *App) originalChatRestartNoticeGroup(ctx context.Context, receipt originalChatRestartReceipt) (originalPluginModuleGroup, error) {
	groups, err := a.originalPluginModules(ctx, receipt.Instance)
	if err != nil {
		return originalPluginModuleGroup{}, err
	}
	if len(groups) == 1 && groups[0].object.installation.PluginID == "LarkMessager" &&
		originalChatRestartAccount(groups[0].object) == receipt.Account {
		return groups[0], nil
	}
	return originalPluginModuleGroup{}, bad("PLUGIN_NOTIFICATION_PROVIDER_UNAVAILABLE", "原 Lark 重启通知实例不可用")
}

func (a *App) postOriginalChatRestartNotice(ctx context.Context, receipt originalChatRestartReceipt, group originalPluginModuleGroup, title string) error {
	message, err := notice.NewEnvelope(map[string]any{
		"channel": "飞书", "source": "Lark", "userid": receipt.User, "title": title, "save_history": false,
	})
	if err != nil {
		return err
	}
	router := notice.Router{Dependencies: notice.Dependencies{
		NoticeMessage: func(ctx context.Context, event notice.Object) error {
			raw, err := json.Marshal(event)
			if err == nil {
				a.broadcastOriginalPluginEvent(ctx, "notice.message", raw)
			}
			return err
		},
		QueueCall: func(ctx context.Context, call notice.Call) error {
			if !originalObjectStillLoaded(group.object) || originalChatRestartAccount(group.object) != receipt.Account {
				return bad("PLUGIN_NOTIFICATION_PROVIDER_UNAVAILABLE", "原 Lark 重启通知实例不可用")
			}
			// A directed source notice bypasses allowed-send windows. Keep
			// this delivery in the bound account, including after App reopen.
			delete(call.Kwargs, "immediately")
			kwargs, err := json.Marshal(call.Kwargs)
			if err != nil {
				return err
			}
			_, err = a.invokeOriginalPluginModuleGroups(ctx, "post_message", nil, nil, kwargs, nil, []originalPluginModuleGroup{group})
			return err
		},
	}}
	return router.Post(ctx, message, notice.Business{}, nil)
}
