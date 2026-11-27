package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"light-media/builtinplugins/plugins/sitemessageautoread"
	"light-media/internal/plugins"
	"light-media/internal/sites"
)

func (a *App) originalHostMarkSiteMessagesRead(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	if scope.object == nil || !scope.object.builtin.Load() || scope.installation.PluginID != sitemessageautoread.ID ||
		(scope.phase != "run" && scope.phase != "run_once") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "仅短讯自动已读插件的执行任务可标记短讯")
	}
	var request plugins.SiteMessagesMarkReadRequestV1
	if err := originalResourceDecodeBounded(raw, &request, pluginValueLimit); err != nil {
		return nil, err
	}
	checkRules := func(ctx context.Context) error {
		if err := scope.validate(ctx); err != nil {
			return err
		}
		in, err := a.pluginStore.Installation(ctx, scope.installation.InstanceID)
		if err != nil {
			return err
		}
		stored, err := a.pluginStore.Config(ctx, in.InstanceID)
		if err != nil {
			return err
		}
		config, err := sitemessageautoread.ParseConfig(stored.Value)
		if err != nil || !in.Installed || !in.DesiredEnabled || (scope.phase == "run" && !config.Scheduled()) ||
			request.SiteID == "" || !slices.Contains(config.Sites, request.SiteID) ||
			len(request.Titles) == 0 || !slices.Equal(request.Titles, config.Titles()) {
			return bad("PLUGIN_SITE_MESSAGE_RULES_CHANGED", "短讯已读规则已变化，请重新运行")
		}
		return nil
	}
	if err := checkRules(ctx); err != nil {
		return nil, err
	}
	site, err := a.siteForRequest(ctx, request.SiteID)
	if err != nil {
		return nil, err
	}
	titles := make(map[string]struct{}, len(request.Titles))
	for _, title := range request.Titles {
		titles[title] = struct{}{}
	}
	var result sites.MessageReadResult
	err = a.withSite(ctx, site, scope.phase == "run", func(client *sites.Client) error {
		client.AddRequestGuard(checkRules)
		var readErr error
		result, readErr = client.MarkMatchingMessagesRead(ctx, func(title string) bool {
			_, matches := titles[strings.TrimSpace(title)]
			return matches
		})
		return readErr
	})
	if guardErr := scope.validate(ctx); guardErr != nil {
		return nil, guardErr
	}
	out := plugins.SiteMessagesMarkReadResultV1{
		SchemaVersion: 1,
		Scanned:       result.Scanned,
		Matched:       result.Matched,
		Marked:        result.Marked,
		Failed:        result.Failed,
	}
	var apiErr *APIError
	switch {
	case errors.Is(err, sites.ErrMessageReadUnsupported):
		out.SkippedReason = "unsupported_site"
	case errors.As(err, &apiErr):
		out.ErrorReason = apiErr.Message
	case err != nil:
		switch err.Error() {
		case "site_authentication_failed", "site_redirect_blocked":
			out.ErrorReason = "站点会话无法访问收件箱，请核对站点凭据"
		case "site_login_challenge", "site_forbidden_or_challenge", "site_forbidden":
			out.ErrorReason = "站点拒绝短讯请求，请检查登录状态"
		case "site_rate_limited":
			out.ErrorReason = "站点短讯请求被限流，请稍后重试"
		case "site_message_mailbox_unrecognized", "site_message_pagination_incomplete":
			out.ErrorReason = "无法完整识别收件箱，已跳过本轮处理"
		case "site_message_mark_read_unconfirmed":
			out.ErrorReason = "站点返回的详情未能确认已读"
		default:
			out.ErrorReason = "收件箱读取或已读确认失败"
		}
	}
	return json.Marshal(out)
}

// The native account refresh can open every unread message. Give an active
// selective schedule exclusive control of automatic reads for its own sites.
func (a *App) siteCollectionMessagePolicy(ctx context.Context, siteID string) (siteMessagePolicy, error) {
	policy, err := a.sourceSiteMessagePolicy(ctx, a.Settings())
	if err != nil || !policy.enabled || !a.pluginAcquisitionClosed() {
		return policy, err
	}
	rows, err := a.Store.reader().QueryContext(ctx, `SELECT i.instance_id,v.sealed
		FROM plugin_installations AS i JOIN plugin_values AS v ON v.instance_id=i.instance_id
		WHERE i.plugin_id=? AND i.installed=1 AND i.desired_enabled=1 AND v.kind='config' AND v.item_key='main'`, sitemessageautoread.ID)
	if err != nil {
		return policy, err
	}
	defer rows.Close()
	for rows.Next() {
		var instance string
		var sealed []byte
		if err = rows.Scan(&instance, &sealed); err != nil {
			return policy, err
		}
		raw, err := a.pluginStore.unseal("config", instance, "main", sealed)
		if err != nil {
			return policy, err
		}
		config, err := sitemessageautoread.ParseConfig(raw)
		if err != nil {
			return policy, err
		}
		if config.Scheduled() && slices.Contains(config.Sites, siteID) {
			policy.enabled = false
			return policy, nil
		}
	}
	return policy, rows.Err()
}
