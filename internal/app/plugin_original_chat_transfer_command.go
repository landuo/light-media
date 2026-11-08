package app

import (
	"context"
	"slices"
	"strings"
)

// The fixed MoviePilot command runs the ordinary transfer scheduler between
// two saved notices. Light Media reuses its completed-download monitor, which
// serializes with the regular scheduler and only organizes owned downloads.
func (a *App) originalChatTransferCommand(ctx context.Context, reply originalSlashReply) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, verified := originalLarkSlashIngress(ctx, reply.info)
	user, err := originalInputID(reply.info.UserID, false)
	if err != nil || !verified || user == nil || *user != reply.user ||
		strings.EqualFold(strings.TrimSpace(*user), "system") || !a.originalChatSubscriptionIngress(ctx) {
		return bad("PLUGIN_CAPABILITY_DENIED", "下载文件整理需要已验证的 Lark 消息")
	}
	if err = reply.post(ctx, a, "开始执行 下载文件整理 ...", true); err != nil {
		return err
	}
	// Source TransferChain.process skips if no local downloader-monitored
	// directory is configured. Native directory rules here are all local.
	if slices.ContainsFunc(a.Settings().Directories, func(rule DirectoryRule) bool {
		return rule.Enabled && rule.MonitorType == "downloader"
	}) {
		if err = a.pollDownloads(ctx); err != nil {
			// The source scheduler logs a failed run before returning to the
			// command, whose final notice describes the scheduler call.
			a.log("WARN", "downloads", "poll_failed", a.clean(err.Error()), "", "")
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return reply.post(ctx, a, "下载文件整理 执行完成", true)
}
