package mergesiteswitch

import (
	"context"
	"encoding/json"
	"slices"

	sdk "light-media/builtinplugins/internal/sdk"
)

func (p *Plugin) siteEvent(ctx context.Context, host sdk.HostCaller, updated bool, raw json.RawMessage) error {
	logger := sdk.Logger{Host: host}
	operation := "站点删除"
	if updated {
		operation = "站点更新"
	}
	_ = logger.Log(ctx, sdk.LogInfo, "监听到"+operation+"事件")
	var event sdk.MutableEvent
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &event); err != nil {
			return err
		}
	}
	var fields map[string]json.RawMessage
	if len(event.Data) > 0 {
		if err := json.Unmarshal(event.Data, &fields); err != nil {
			return err
		}
	}
	id := fields["site_id"]
	var domain string
	if updated {
		if value := fields["domain"]; truth(value) {
			if err := json.Unmarshal(value, &domain); err != nil {
				return err
			}
		}
	}
	if updated && domain == "" || !updated && (len(id) == 0 || string(id) == "null") {
		_ = logger.Log(ctx, sdk.LogWarn, "事件信息无效，忽略事件")
		return nil
	}
	ids, err := installed(ctx, host)
	if err != nil {
		return err
	}
	if !anyFollow(p.config, ids) {
		_ = logger.Log(ctx, sdk.LogWarn, "未打开任一【跟随启用的站点】开关，忽略事件")
		return nil
	}
	active := false
	if updated {
		var site *struct {
			ID      json.RawMessage `json:"source_id"`
			Enabled bool            `json:"enabled"`
		}
		if err := call(ctx, host, request{Action: "updated_site", Domain: domain}, &site); err != nil {
			return err
		}
		if site == nil {
			_ = logger.Log(ctx, sdk.LogWarn, "目标站点不存在，忽略事件: domain = "+domain)
			return nil
		}
		id, active = site.ID, site.Enabled
	}
	for _, s := range selections {
		if !s.applies(ids) || !truth(p.config[s.follow()]) {
			continue
		}
		raw, err := get(ctx, host, s.field)
		if err != nil {
			return err
		}
		selected, err := siteList(raw)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(selected, func(candidate json.RawMessage) bool { return sameID(candidate, id) })
		switch {
		case active && i < 0:
			selected = append(selected, id)
		case !active && i >= 0:
			selected = slices.Delete(selected, i, i+1)
		default:
			continue
		}
		raw, err = json.Marshal(selected)
		if err != nil {
			return err
		}
		if err = set(ctx, host, s.field, raw); err != nil {
			return err
		}
	}
	_ = logger.Log(ctx, sdk.LogInfo, operation+"事件监听任务执行完成")
	return nil
}
