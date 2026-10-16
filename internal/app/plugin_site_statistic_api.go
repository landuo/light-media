package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func (a *App) pluginSiteStatisticAPIHost(ctx context.Context, installation PluginInstallation, run *PluginRun, raw json.RawMessage) (json.RawMessage, error) {
	var wanted struct {
		Domain string `json:"domain"`
	}
	if run == nil || installation.PluginID != "SiteStatistic" || !pluginStrictJSON(raw, &wanted) {
		return nil, bad("PLUGIN_API_SCOPE_DENIED", "站点刷新API执行范围无效")
	}
	a.pluginMu.Lock()
	if err := a.validatePluginHostRun(ctx, installation, run, "site.statistics.refresh_domain"); err != nil {
		a.pluginMu.Unlock()
		return nil, err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		a.pluginMu.Unlock()
		return nil, err
	}
	request, err := a.validatePluginHTTPRunTx(ctx, tx, *run)
	tx.Rollback()
	if err != nil {
		a.pluginMu.Unlock()
		return nil, err
	}
	if request.Key != "refresh_by_domain" || request.Method != "GET" || request.LastQuery("domain") != wanted.Domain || !request.CredentialMatches["apikey"] {
		a.pluginMu.Unlock()
		return nil, bad("PLUGIN_API_SCOPE_DENIED", "站点刷新目标不属于当前API请求")
	}
	if result, err := a.pluginStore.siteStatisticReceipt(ctx, *run); err == nil {
		a.pluginMu.Unlock()
		return result, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		a.pluginMu.Unlock()
		return nil, err
	}
	var site SiteConfig
	for _, candidate := range a.Settings().Sites {
		if siteStatisticDomain(candidate) == wanted.Domain {
			if site.ID != "" {
				a.pluginMu.Unlock()
				return nil, bad("PLUGIN_SITE_REFERENCE_AMBIGUOUS", "多个配置使用相同站点域名，无法确定刷新目标")
			}
			site = candidate
		}
	}
	if site.ID == "" {
		response, err := plugins.JSONHTTPResponse(200, builtin.SiteStatisticResponse(false, "站点 "+wanted.Domain+" 不存在", map[string]any{}))
		if err == nil {
			tx, e := a.Store.DB.BeginTx(ctx, nil)
			err = e
			if err == nil {
				defer tx.Rollback()
				_, err = a.pluginStore.saveSiteStatisticReceiptTx(ctx, tx, *run, json.RawMessage(response))
				if err == nil {
					err = tx.Commit()
				}
			}
		}
		a.pluginMu.Unlock()
		return response, err
	}
	a.pluginMu.Unlock()
	var result json.RawMessage
	_, err = a.collectManagedSite(allowDisabledSiteRequests(ctx), site.ID, run.ID+":domain", &siteCollectionHooks{
		Guard: func(tx *sql.Tx) error { _, err := a.validatePluginHTTPRunTx(ctx, tx, *run); return err },
		Commit: func(tx *sql.Tx, snapshot siteSnapshot) error {
			var body any
			if snapshot.Connection.Status != "ok" || snapshot.Stats == nil {
				body = builtin.SiteStatisticResponse(false, "站点 "+wanted.Domain+" 刷新数据失败，未获取到数据", map[string]any{})
			} else {
				body = builtin.SiteStatisticResponse(true, "站点 "+wanted.Domain+" 刷新成功", siteStatisticAPIData(site, snapshot))
			}
			response, err := plugins.JSONHTTPResponse(200, body)
			if err != nil {
				return err
			}
			result, err = a.pluginStore.saveSiteStatisticReceiptTx(ctx, tx, *run, json.RawMessage(response))
			return err
		},
	})
	return result, err
}

// Preserve the original nullable response schema. An unobserved value remains
// null; missing scraping fields are not fabricated as zero or an empty list.
func siteStatisticAPIData(site SiteConfig, snapshot siteSnapshot) map[string]any {
	stats := snapshot.Stats
	nullable := func(value string) any {
		if value == "" {
			return nil
		}
		return value
	}
	var updatedTime any
	if at, err := time.Parse(time.RFC3339Nano, snapshot.Connection.CheckedAt); err == nil {
		updatedTime = at.In(shanghai()).Format("15:04:05")
	}
	return map[string]any{"domain": siteStatisticDomain(site), "username": nullable(stats.Username), "userid": nullable(stats.UserID), "user_level": nullable(stats.Level), "join_at": nullable(stats.JoinedAt), "bonus": stats.Bonus, "upload": stats.Uploaded, "download": stats.Downloaded, "ratio": stats.Ratio, "seeding": stats.Seeding, "leeching": stats.Leeching, "seeding_size": stats.SeedingBytes, "leeching_size": nil, "seeding_info": nil, "message_unread": nil, "message_unread_contents": nil, "err_msg": nil, "updated_day": snapshot.Day, "updated_time": updatedTime}
}
