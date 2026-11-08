package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"light-media/internal/plugins"
)

func originalPluginConfigurationRoute(path, method string) bool {
	parts := strings.Split(path, "/")
	if method == http.MethodGet && len(parts) == 1 && pluginStorageID.MatchString(path) {
		switch path {
		case "markets", "market", "organization", "navigation", "install":
			return false
		}
		return true
	}
	return pluginStorageID.MatchString(parts[0]) && ((method == http.MethodPut && (len(parts) == 1 || len(parts) == 2 && parts[1] == "config")) || (method == http.MethodPost && len(parts) == 2 && (parts[1] == "reload" || parts[1] == "reset")))
}

// IYUU retains three caches of 10,000 info hashes in its configuration.
// Bind their capacity to the stored implementation identity so versions,
// retained installations, and real clones can read the same snapshot.
func originalPluginConfigValueLimit(pluginID string) int {
	switch pluginID {
	case "IYUUAutoSeed":
		return 2 << 20
	case "BrushFlowLanduo":
		return plugins.MaxPayloadBytes
	}
	return pluginValueLimit
}

// saveOriginalPluginConfiguration is the encrypted storage adaptation of
// save_plugin_config. Revision is internal execution metadata, not an original
// client CAS/review precondition. Saving precedes initialization and is not
// rolled back if the original init method subsequently raises.
func (p *PluginStore) saveOriginalPluginConfiguration(ctx context.Context, in PluginInstallation, raw json.RawMessage) (PluginStoredValue, error) {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginStoredValue{}, err
	}
	defer tx.Rollback()
	observed, old, err := p.readOriginalBaseConfigRow(ctx, tx, in.InstanceID)
	if err != nil {
		return PluginStoredValue{}, err
	}
	if observed.present {
		if _, err = p.unsealBound("config", in.InstanceID, "main", old, observed.valueLimit); err != nil {
			return PluginStoredValue{}, err
		}
	}
	if len(raw) > observed.valueLimit || !json.Valid(raw) {
		return PluginStoredValue{}, bad("INVALID_PLUGIN_VALUE", "插件数据不是有效JSON或超过大小上限")
	}
	sealed, err := p.seal("config", in.InstanceID, "main", raw)
	if err != nil {
		return PluginStoredValue{}, err
	}
	value := PluginStoredValue{Revision: observed.revision + 1, Value: append(json.RawMessage(nil), raw...), UpdatedAt: now()}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_values(instance_id,kind,item_key,revision,sealed,updated_at) VALUES(?,'config','main',?,?,?) ON CONFLICT(instance_id,kind,item_key) DO UPDATE SET revision=excluded.revision,sealed=excluded.sealed,updated_at=excluded.updated_at`, in.InstanceID, value.Revision, sealed, value.UpdatedAt)
	if err != nil {
		return PluginStoredValue{}, err
	}
	return value, tx.Commit()
}

// init_plugin is not reload: it neither stops nor replaces the loaded object.
func (a *App) initOriginalLoadedPlugin(ctx context.Context, instance string) error {
	return a.initializeOriginalLoadedPlugin(ctx, instance, false)
}

func (a *App) initializeOriginalLoadedPlugin(ctx context.Context, instance string, configurationSave bool) error {
	value, ok := a.pluginRunningInstances.Load(instance)
	if !ok {
		return nil
	}
	object := value.(*originalPluginInstance)
	initCtx, endInit := object.beginOriginalInitialization(ctx, configurationSave)
	defer endInit()
	if err := object.init(initCtx); err != nil {
		return err
	}
	// Source retains the local object and reads state even after self removal.
	if err := a.updateOriginalPluginEventState(initCtx, object); err != nil {
		return err
	}
	return nil
}

// The source-sized snapshot contract belongs to a trusted imported class and
// its current installation identity, not the request ID or submitted manifest.
// Resolving the class's PluginID also preserves that contract for real clones.
// Ordinary and unregistered targets keep the existing bounded decoder.
func (a *App) decodeOriginalPluginConfigBody(w http.ResponseWriter, r *http.Request, instance string, value any) error {
	if class, ok := a.pluginOriginalClasses.Load(instance); ok && class.(originalPluginClass).installation.PluginID == "BrushFlowLanduo" {
		return decodePluginJSONBody(http.MaxBytesReader(w, r.Body, plugins.MaxLogicalMessageBytes), value)
	}
	return decodePluginBody(w, r, value)
}

func (a *App) originalPluginConfigurationHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	parts := strings.Split(path, "/")
	instance := parts[0]
	if r.Method == http.MethodGet {
		class, exists := a.pluginOriginalClasses.Load(instance)
		if !exists {
			return json.RawMessage(`{}`), nil
		}
		raw, err := a.originalManagerStoredConfiguration(r.Context(), instance)
		if err != nil {
			return nil, err
		}
		switch class.(originalPluginClass).installation.PluginID {
		case "RemoteIdentifiers":
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) == nil && maskRemoteIdentifiersURLs(fields) {
				raw, err = json.Marshal(fields)
			}
		case "SeedRescue":
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) == nil && maskOriginalSeedRescueSecrets(fields) {
				raw, err = json.Marshal(fields)
			}
		}
		return raw, err
	}
	var config map[string]json.RawMessage
	if r.Method == http.MethodPut {
		if len(parts) == 2 {
			// Existing Go transport envelope; the revision field has no original
			// product meaning and is deliberately not used as a save precondition.
			var envelope struct {
				Revision int64                      `json:"revision"`
				Config   map[string]json.RawMessage `json:"config"`
			}
			if err := a.decodeOriginalPluginConfigBody(w, r, instance, &envelope); err != nil {
				return nil, err
			}
			config = envelope.Config
		} else {
			if err := a.decodeOriginalPluginConfigBody(w, r, instance, &config); err != nil {
				return nil, err
			}
		}
		if config == nil {
			return nil, bad("INVALID_PLUGIN_JSON", "插件配置必须是对象")
		}
	}
	ctx := context.WithoutCancel(r.Context())
	a.pluginOriginalOperationMu.Lock()
	defer a.pluginOriginalOperationMu.Unlock()
	ctx, releaseOperationOwner := a.bindOriginalOperationOwner(ctx)
	defer releaseOperationOwner()
	if !a.admitOriginalBackground() {
		return nil, &APIError{"APP_STOPPING", "服务正在停止", http.StatusServiceUnavailable}
	}
	defer a.wg.Done()
	var err error
	if r.Method == http.MethodPut {
		// Original save_plugin_config returns false when its class is absent;
		// the endpoint deliberately ignores that boolean and still init/registers.
		if class, exists := a.pluginOriginalClasses.Load(instance); exists {
			installation := class.(originalPluginClass).installation
			switch installation.PluginID {
			case "RemoteIdentifiers":
				if err := a.restoreRemoteIdentifiersURLs(ctx, instance, config); err != nil {
					return nil, err
				}
			case "SeedRescue":
				if err := a.restoreOriginalSeedRescueSecrets(ctx, instance, config); err != nil {
					return nil, err
				}
			}
			raw, err := json.Marshal(config)
			if err != nil {
				return nil, err
			}
			a.pluginMu.Lock()
			_, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, installation, raw)
			if err == nil {
				a.invalidateOriginalBaseConfig(instance)
			}
			a.pluginMu.Unlock()
			if err != nil {
				return nil, err
			}
		}
		if err = a.initializeOriginalLoadedPlugin(ctx, instance, true); err != nil {
			return nil, err
		}
	} else {
		if parts[1] == "reset" {
			if err = a.resetOriginalPluginData(ctx, instance); err != nil {
				return nil, err
			}
		}
		if _, err = a.reloadOriginalPlugin(ctx, instance); err != nil {
			return nil, err
		}
	}
	a.registerOriginalPlugin(ctx, instance)
	return map[string]any{"success": true}, nil
}
