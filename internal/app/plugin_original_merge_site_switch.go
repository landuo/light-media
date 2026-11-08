package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"light-media/internal/plugins"
)

const originalMergeSiteSwitchCapability = "sites.switch"

type originalMergeSiteSwitchRequest struct {
	Action string          `json:"action"`
	Field  string          `json:"field,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
	Domain string          `json:"domain,omitempty"`
}

type originalMergeSiteSwitchPeer struct {
	instance, field string
	custom          bool
}

var originalMergeSiteSwitchPeers = map[string]originalMergeSiteSwitchPeer{
	"signin_sites":     {"AutoSignIn", "sign_sites", true},
	"login_sites":      {"AutoSignIn", "login_sites", true},
	"statistic_sites":  {"SiteStatistic", "statistic_sites", true},
	"iyuu_seed_sites":  {"IYUUAutoSeed", "sites", true},
	"brush_flow_sites": {"BrushFlow", "brushsites", false},
	"cross_seed_sites": {"CrossSeed", "sites", true},
}

func originalMergeSiteSwitchEventMethod(object *originalPluginInstance, method string) bool {
	return object.installation.PluginID == "MergeSiteSwitch" &&
		(method == "listen_site_updated_event" || method == "listen_site_deleted_event")
}

func originalMergeSiteSwitchEvent(ctx context.Context, scope originalBaseCallScope) bool {
	lease, _ := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
	return lease != nil && lease.sourceKind == originalSourceEvent && scope.object.originalPublicServiceScopeAllowed(ctx) &&
		originalMergeSiteSwitchEventMethod(scope.object, scope.phase)
}

func (a *App) waitOriginalMergeSiteSwitchEvent(ctx context.Context) (context.Context, func(), error) {
	if !a.pluginOriginalOperationMu.TryLock() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-ticker.C:
			}
			if a.pluginOriginalOperationMu.TryLock() {
				break
			}
		}
	}
	if err := ctx.Err(); err != nil {
		a.pluginOriginalOperationMu.Unlock()
		return nil, nil, err
	}
	operation, end := a.bindOriginalOperationOwner(ctx)
	return operation, func() { end(); a.pluginOriginalOperationMu.Unlock() }, nil
}

func (a *App) originalMergeSiteSwitchResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	deny := bad("PLUGIN_CAPABILITY_DENIED", "聚合站点开关操作不在当前调用范围内")
	if scope.object == nil || scope.object.installation.PluginID != "MergeSiteSwitch" {
		return nil, deny
	}
	var request originalMergeSiteSwitchRequest
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	event := originalMergeSiteSwitchEvent(ctx, scope)
	init := scope.phase == "init" && scope.initialization != nil && originalLiveInitialization(ctx, scope.object) == scope.initialization
	save := init && scope.initialization.configurationSave
	form := scope.phase == "get_form"
	if request.Action != "set" && request.Value != nil || request.Action != "updated_site" && request.Domain != "" {
		return nil, bad("INVALID_PLUGIN_VALUE", "聚合站点开关参数与操作不匹配")
	}
	switch request.Action {
	case "installed":
		if request.Field != "" || !(form || init || event || scope.phase == "get_state") {
			return nil, deny
		}
		ids, err := a.originalInstalledPluginIDs(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(ids)
	case "custom":
		if request.Field != "" || !(form || init) {
			return nil, deny
		}
		return a.originalMergeSiteSwitchCustom(ctx, scope)
	case "updated_site":
		if request.Field != "" || !event || scope.phase != "listen_site_updated_event" {
			return nil, deny
		}
		return a.originalMergeSiteSwitchUpdatedSite(ctx, scope, request.Domain)
	case "get", "set":
		write := request.Action == "set"
		if write && !save && !event || !write && !(form || init || event) {
			return nil, deny
		}
		switch request.Field {
		case "enable_sites":
			if !write || !save {
				return nil, deny
			}
			ids, err := originalMergeSiteSwitchIDs(request.Value, false)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(`null`), a.originalMergeSiteSwitchEnable(ctx, scope, ids)
		case "search_sites", "rss_sites":
			return a.originalMergeSiteSwitchSystemSelection(ctx, scope, request.Field, request.Value, write)
		default:
			peer, ok := originalMergeSiteSwitchPeers[request.Field]
			if !ok {
				return nil, deny
			}
			if write {
				if _, err := originalMergeSiteSwitchIDs(request.Value, peer.custom); err != nil {
					return nil, err
				}
				return json.RawMessage(`null`), a.originalMergeSiteSwitchSetPeer(ctx, scope, peer, request.Value)
			}
			return a.originalMergeSiteSwitchGetPeer(ctx, scope, peer)
		}
	default:
		return nil, bad("INVALID_PLUGIN_VALUE", "未知聚合站点开关操作")
	}
}

// A numeric source site ID is not a stable host ID or a custom string ID.
// Keep order, repeats, and the source distinction between null and [].
func originalMergeSiteSwitchIDs(raw json.RawMessage, custom bool) ([]int64, error) {
	invalid := bad("INVALID_SITES", "聚合站点选择须为原站点编号数组或 null")
	var items []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return nil, invalid
	}
	if items == nil {
		return nil, nil
	}
	ids := make([]int64, 0, len(items))
	for _, value := range items {
		var id int64
		if json.Unmarshal(value, &id) == nil && id > 0 {
			ids = append(ids, id)
			continue
		}
		if id, ok := sourceSiteString(value); custom && ok && id != "" && len(id) <= 4096 && !strings.ContainsAny(id, "\r\n\x00") {
			continue
		}
		return nil, invalid
	}
	return ids, nil
}

func (a *App) originalMergeSiteSwitchUpdatedSite(ctx context.Context, scope originalBaseCallScope, domain string) (json.RawMessage, error) {
	if domain == "" || len(domain) > 4096 || strings.ContainsAny(domain, "\r\n\x00") {
		return json.RawMessage(`null`), nil
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	rows, err := a.originalSourceSiteRows(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		stored := sourceText(row.fields["domain"])
		if row.provenance == "" && row.site.Domain == "" {
			// A local row has no imported domain column; use the same source
			// projection as the real SiteUpdated publisher, including its port.
			stored = standardSiteEventDomain(row.site.BaseURL)
		}
		if stored == domain {
			return json.Marshal(struct {
				SourceID int64 `json:"source_id"`
				Enabled  bool  `json:"enabled"`
			}{row.sourceID, sourceNativeActive(row.fields["is_active"])})
		}
	}
	return json.RawMessage(`null`), nil
}

func (a *App) originalMergeSiteSwitchEnable(ctx context.Context, scope originalBaseCallScope, ids []int64) error {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return err
	}
	rows, err := a.originalSourceSiteRows(ctx)
	if err != nil {
		return err
	}
	sortNativeSourceRows(rows)
	enabled := make(map[int64]bool, len(ids))
	for _, id := range ids {
		enabled[id] = true
	}
	for _, row := range rows {
		want := enabled[row.sourceID]
		if want == row.site.Enabled {
			continue
		}
		if err := scope.validate(ctx); err != nil {
			return err
		}
		config := a.Settings()
		for i := range config.Sites {
			if config.Sites[i].ID == row.site.ID {
				config.Sites[i].Enabled = want
				break
			}
		}
		// Source uses SiteOper.update for each changed row, in priority order.
		// The existing settings writer reconciles its numeric provenance. It
		// does not produce the site management endpoint's SiteUpdated event.
		if err := a.saveSettingsWithEffect(ctx, config, nil); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) originalMergeSiteSwitchSystemSelection(ctx context.Context, scope originalBaseCallScope, field string, value json.RawMessage, write bool) (json.RawMessage, error) {
	key := "IndexerSites"
	if field == "rss_sites" {
		key = "RssSites"
	}
	if write {
		ids, err := originalMergeSiteSwitchIDs(value, false)
		if err != nil {
			return nil, err
		}
		var stable []string
		if ids != nil {
			stable = make([]string, 0, len(ids))
		}
		for _, id := range ids {
			identity, err := a.Store.nativeSiteByPublicSourceID(ctx, id)
			if errors.Is(err, sql.ErrNoRows) || err == nil && identity.State != "active" {
				return nil, bad("INVALID_SITES", "系统站点选择含不存在的编号")
			}
			if err != nil {
				return nil, err
			}
			stable = append(stable, identity.SiteID)
		}
		_, err = a.originalHostSettingsResource(ctx, scope, plugins.SettingsWriteCapability, mustCleanSeedJSON(plugins.SettingsHostWriteV1{Key: key, Value: mustCleanSeedJSON(stable)}))
		return json.RawMessage(`null`), err
	}
	raw, err := a.originalHostSettingsResource(ctx, scope, plugins.SettingsReadCapability, mustCleanSeedJSON(plugins.SettingsHostRequestV1{Key: key}))
	if err != nil {
		return nil, err
	}
	var result plugins.SettingsHostResultV1
	var stable []string
	if json.Unmarshal(raw, &result) != nil || json.Unmarshal(result.Value, &stable) != nil {
		return nil, bad("INVALID_SITES", "系统站点选择无法读取")
	}
	var ids []int64
	if stable != nil {
		ids = make([]int64, 0, len(stable))
	}
	for _, id := range stable {
		identity, err := a.Store.nativeSiteByAppID(ctx, id)
		if err != nil {
			return nil, err
		}
		mapped, err := a.Store.nativeSourcePublicSiteID(ctx, a.Store.DB, identity)
		if err != nil {
			return nil, err
		}
		ids = append(ids, mapped)
	}
	return json.Marshal(ids)
}

// The host reads a peer's namespace only to project one known field or patch
// that field in place. The caller never receives the peer's other settings.
func (a *App) originalMergeSiteSwitchPeerConfig(ctx context.Context, scope originalBaseCallScope, instance string, patch func(map[string]json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	ids, err := a.originalInstalledPluginIDs(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(ids, instance) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "聚合站点开关关联插件未安装")
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	a.pluginOriginalBase.mu.Lock()
	defer a.pluginOriginalBase.mu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	raw, err := a.originalBaseConfigLocked(ctx, instance)
	if err != nil {
		return nil, err
	}
	config := map[string]json.RawMessage{}
	if originalBaseJSONTruthy(raw) && json.Unmarshal(raw, &config) != nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "聚合站点开关关联插件配置无法读取")
	}
	return patch(config)
}

func (a *App) originalMergeSiteSwitchGetPeer(ctx context.Context, scope originalBaseCallScope, peer originalMergeSiteSwitchPeer) (json.RawMessage, error) {
	return a.originalMergeSiteSwitchPeerConfig(ctx, scope, peer.instance, func(config map[string]json.RawMessage) (json.RawMessage, error) {
		value := config[peer.field]
		if len(value) == 0 {
			return json.RawMessage(`null`), nil
		}
		if _, err := originalMergeSiteSwitchIDs(value, peer.custom); err == nil {
			return value, nil
		} else if originalBaseJSONTruthy(value) {
			return nil, err // Never expose an unexpected object or credential field.
		}
		return json.RawMessage(`null`), nil
	})
}

func (a *App) originalMergeSiteSwitchSetPeer(ctx context.Context, scope originalBaseCallScope, peer originalMergeSiteSwitchPeer, value json.RawMessage) error {
	operation, release, err := a.enterOriginalManagerOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	config, err := a.originalMergeSiteSwitchPeerConfig(operation, scope, peer.instance, func(config map[string]json.RawMessage) (json.RawMessage, error) {
		config[peer.field] = value
		updated, err := json.Marshal(config)
		if err != nil {
			return nil, err
		}
		_, err = a.originalBaseUpdateConfigLocked(operation, peer.instance, updated)
		return updated, err
	})
	if err != nil {
		return err
	}
	if err := scope.validate(operation); err != nil {
		return err
	}
	// Keep the original per-field prefix: persist -> warm init -> jobs. Never
	// hold a storage/settings lock while calling another executable.
	if err := a.originalHostManagerControl(operation, plugins.PluginManagerRequest{InstanceID: peer.instance, Action: "init", Config: config}, nil); err != nil {
		return err
	}
	a.updateOriginalPluginJobs(operation, peer.instance)
	return operation.Err()
}

func (a *App) originalMergeSiteSwitchCustom(ctx context.Context, scope originalBaseCallScope) (json.RawMessage, error) {
	ids, err := a.originalInstalledPluginIDs(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(ids, "CustomSites") {
		return json.RawMessage(`[]`), nil
	}
	return a.originalMergeSiteSwitchPeerConfig(ctx, scope, "CustomSites", func(config map[string]json.RawMessage) (json.RawMessage, error) {
		if !originalBaseJSONTruthy(config["enabled"]) || !originalBaseJSONTruthy(config["sites"]) {
			return json.RawMessage(`[]`), nil
		}
		var rows []map[string]json.RawMessage
		if json.Unmarshal(config["sites"], &rows) != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "自定义站点选项无法读取")
		}
		type option struct {
			ID   json.RawMessage `json:"id"`
			Name string          `json:"name"`
		}
		options := make([]option, 0, len(rows))
		for _, row := range rows {
			if len(row) == 0 {
				continue
			}
			id := row["id"]
			if id == nil {
				id = json.RawMessage(`null`)
			}
			var number int64
			_, stringID := sourceSiteString(id)
			if !stringID && json.Unmarshal(id, &number) != nil {
				return nil, bad("INVALID_PLUGIN_CONFIG", "自定义站点编号无法读取")
			}
			name, _ := sourceSiteString(row["name"])
			options = append(options, option{ID: id, Name: name})
		}
		return json.Marshal(options)
	})
}
