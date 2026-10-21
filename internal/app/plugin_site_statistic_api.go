package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

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
		RequestGuard: func(requestCtx context.Context) error {
			a.pluginMu.Lock()
			defer a.pluginMu.Unlock()
			return a.validatePluginHTTPRun(requestCtx, *run)
		},
		Guard: func(tx *sql.Tx) error { _, err := a.validatePluginHTTPRunTx(ctx, tx, *run); return err },
		Commit: func(tx *sql.Tx, snapshot siteSnapshot) error {
			var body any
			if snapshot.SourceData == nil {
				body = builtin.SiteStatisticResponse(false, "站点 "+wanted.Domain+" 刷新数据失败，未获取到数据", map[string]any{})
			} else {
				body = builtin.SiteStatisticResponse(true, "站点 "+wanted.Domain+" 刷新成功", siteStatisticAPIData(snapshot))
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

// Source constructor defaults belong only to this DTO. Native account values
// remain nullable; they are observations rather than protocol defaults.
func siteStatisticAPIData(snapshot siteSnapshot) map[string]any {
	if d := snapshot.SourceData; d != nil {
		return map[string]any{"domain": d.Domain, "username": d.Username, "userid": d.UserID, "user_level": d.UserLevel, "join_at": d.JoinAt, "bonus": d.Bonus, "upload": d.Upload, "download": d.Download, "ratio": d.Ratio, "seeding": d.Seeding, "leeching": d.Leeching, "seeding_size": d.SeedingSize, "leeching_size": d.LeechingSize, "seeding_info": d.SeedingInfo, "message_unread": d.MessageUnread, "message_unread_contents": d.Messages, "err_msg": d.Error, "updated_day": d.Day, "updated_time": d.Time}
	}
	return nil
}
