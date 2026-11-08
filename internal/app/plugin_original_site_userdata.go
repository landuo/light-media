package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"light-media/internal/sites"
)

// SiteChain.refresh_userdata asks the module dispatcher before the native
// indexer. The source DTO stays distinct from the site's connection diagnostic.
// A list still reaches the core module, whose scalar DTO cannot replace it.
func (a *App) originalSiteUserData(ctx context.Context, site SiteConfig, native func() (sites.UserStats, *sites.SourceUserData, error)) (sites.UserStats, *sites.SourceUserData, error) {
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil || !originalModuleGroupsHaveMethod(groups, "refresh_userdata") {
		if err != nil {
			return sites.UserStats{}, nil, err
		}
		return native()
	}
	fields, err := a.originalResourceSiteFields(ctx, site)
	if err != nil {
		return sites.UserStats{}, nil, err
	}
	kwargs, _ := json.Marshal(map[string]any{"site": fields})
	result, err := a.invokeOriginalPluginModuleGroups(ctx, "refresh_userdata", nil, json.RawMessage(`[]`), kwargs, nil, groups)
	if err != nil {
		return sites.UserStats{}, nil, err
	}
	if originalModuleResultEmpty(result) {
		return native()
	}
	var list []json.RawMessage
	if json.Unmarshal(result, &list) == nil && list != nil {
		_, _, _ = native()
		if len(list) == 0 {
			return sites.UserStats{}, nil, nil
		}
		return sites.UserStats{}, nil, originalSiteUserDataInvalid()
	}
	if !originalModuleResultTruthy(result) {
		return sites.UserStats{}, nil, nil
	}
	var fieldsRaw map[string]json.RawMessage
	if json.Unmarshal(result, &fieldsRaw) != nil || fieldsRaw == nil {
		return sites.UserStats{}, nil, originalSiteUserDataInvalid()
	}
	dto := &sites.SourceUserData{SeedingInfo: [][2]int64{}, Messages: [][]any{}}
	for _, key := range []string{"seeding_info", "message_unread_contents"} {
		if _, exists := fieldsRaw[key]; !exists {
			fieldsRaw[key] = json.RawMessage(`[]`)
		}
	}
	for _, key := range []string{"domain", "updated_day"} {
		if _, exists := fieldsRaw[key]; !exists {
			fieldsRaw[key] = json.RawMessage(`null`)
		}
	}
	result, _ = json.Marshal(fieldsRaw)
	if json.Unmarshal(result, dto) != nil {
		return sites.UserStats{}, nil, originalSiteUserDataInvalid()
	}
	return originalSiteUserDataStats(dto), dto, nil
}

func originalSiteUserDataInvalid() error {
	return bad("PLUGIN_MODULE_RESULT_INVALID", "站点用户数据插件返回格式无效")
}

func (a *App) reportOriginalSiteModuleFailure(ctx context.Context, err error) {
	kind, stack, known := sites.SourceUserModuleFailureDiagnostic(err)
	if !known {
		return
	}
	title := siteSystemModuleName + "发生了错误"
	message := siteNativeModuleErrorText(kind)
	a.systemMessages.put("system", &title, message, nil, time.Now())
	raw, marshalErr := json.Marshal(SystemErrorEvent{Type: "module", ModuleID: "IndexerModule", ModuleName: siteSystemModuleName, ModuleMethod: "refresh_userdata", Error: message, Traceback: stack})
	if marshalErr == nil {
		a.broadcastOriginalPluginEvent(context.WithoutCancel(ctx), "system.error", raw)
	}
}

func originalSiteUserDataStats(d *sites.SourceUserData) sites.UserStats {
	s := sites.UserStats{Uploaded: &d.Upload, Downloaded: &d.Download, Ratio: &d.Ratio, Bonus: &d.Bonus, Seeding: &d.Seeding, Leeching: &d.Leeching, SeedingBytes: &d.SeedingSize, LeechingBytes: &d.LeechingSize, SeedingInfo: d.SeedingInfo, MessageUnread: &d.MessageUnread, Error: d.Error, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano), Sources: map[string]string{}, Warnings: []string{}}
	for key, field := range map[string]**int64{"upload": &s.Uploaded, "download": &s.Downloaded, "seeding": &s.Seeding, "leeching": &s.Leeching, "seeding_size": &s.SeedingBytes, "leeching_size": &s.LeechingBytes, "message_unread": &s.MessageUnread} {
		if d.SourceFieldNull(key) || !d.SourceIntegerRepresentable(key) {
			*field = nil
		}
	}
	if d.SourceFieldNull("ratio") {
		s.Ratio = nil
	}
	if d.SourceFieldNull("bonus") {
		s.Bonus = nil
	}
	if d.UserID != nil {
		s.UserID = fmt.Sprint(d.UserID)
	}
	if d.Username != nil {
		s.Username = *d.Username
	}
	if d.UserLevel != nil {
		s.Level = *d.UserLevel
	}
	if d.JoinAt != nil {
		s.JoinedAt = *d.JoinAt
	}
	return s
}
