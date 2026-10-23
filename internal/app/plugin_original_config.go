package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

func originalPluginConfigurationRoute(path, method string) bool {
	parts := strings.Split(path, "/")
	return pluginStorageID.MatchString(parts[0]) && ((method == http.MethodPut && (len(parts) == 1 || len(parts) == 2 && parts[1] == "config")) || (method == http.MethodPost && len(parts) == 2 && (parts[1] == "reload" || parts[1] == "reset")))
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
	var revision int64
	var old []byte
	err = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, in.InstanceID).Scan(&revision, &old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PluginStoredValue{}, err
	}
	if err == nil {
		if _, err = p.unseal("config", in.InstanceID, "main", old); err != nil {
			return PluginStoredValue{}, err
		}
	}
	sealed, err := p.sealPluginSnapshot("config", in.InstanceID, "main", in.PluginID, in.Version, raw)
	if err != nil {
		return PluginStoredValue{}, err
	}
	value := PluginStoredValue{Revision: revision + 1, Value: append(json.RawMessage(nil), raw...), UpdatedAt: now()}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_values(instance_id,kind,item_key,revision,sealed,updated_at) VALUES(?,'config','main',?,?,?) ON CONFLICT(instance_id,kind,item_key) DO UPDATE SET revision=excluded.revision,sealed=excluded.sealed,updated_at=excluded.updated_at`, in.InstanceID, value.Revision, sealed, value.UpdatedAt)
	if err != nil {
		return PluginStoredValue{}, err
	}
	return value, tx.Commit()
}

// init_plugin is not reload: it neither stops nor replaces the loaded object.
func (a *App) initOriginalLoadedPlugin(ctx context.Context, instance string) error {
	value, ok := a.pluginRunningInstances.Load(instance)
	if !ok {
		return nil
	}
	object := value.(*originalPluginInstance)
	if err := object.init(ctx); err != nil {
		return err
	}
	return a.updateOriginalPluginEventState(ctx, object)
}

func (a *App) originalPluginConfigurationHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	parts := strings.Split(path, "/")
	instance := parts[0]
	var config map[string]json.RawMessage
	if r.Method == http.MethodPut {
		if len(parts) == 2 {
			// Existing Go transport envelope; the revision field has no original
			// product meaning and is deliberately not used as a save precondition.
			var envelope struct {
				Revision int64                      `json:"revision"`
				Config   map[string]json.RawMessage `json:"config"`
			}
			if err := decodePluginBody(w, r, &envelope); err != nil {
				return nil, err
			}
			config = envelope.Config
		} else {
			if err := decodePluginBody(w, r, &config); err != nil {
				return nil, err
			}
		}
		if config == nil {
			return nil, bad("INVALID_PLUGIN_JSON", "插件配置必须是对象")
		}
	}
	ctx := context.WithoutCancel(r.Context())
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	var err error
	if r.Method == http.MethodPut {
		raw, err := json.Marshal(config)
		if err != nil {
			return nil, err
		}
		// Original save_plugin_config returns false when its class is absent;
		// the endpoint deliberately ignores that boolean and still init/registers.
		if class, exists := a.pluginOriginalClasses.Load(instance); exists {
			if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, class.(originalPluginClass).installation, raw); err != nil {
				return nil, err
			}
		}
		if err = a.initOriginalLoadedPlugin(ctx, instance); err != nil {
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
