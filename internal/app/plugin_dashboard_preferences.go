package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

// The platform owns only plugin dashboard layout/display preferences. Source
// library/media widgets and unrelated global configuration are not introduced.
func (a *App) pluginDashboardSettingsHTTP(w http.ResponseWriter, r *http.Request, key string) (any, error) {
	if key != "desktop" && key != "tablet" && key != "mobile" && key != "order" && key != "enabled" {
		return nil, &APIError{"NOT_FOUND", "仪表板设置不存在", 404}
	}
	const kind = "plugin_dashboard_settings"
	ctx := r.Context()
	if r.Method == http.MethodGet {
		var sealed []byte
		err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, kind, key).Scan(&sealed)
		if errors.Is(err, sql.ErrNoRows) {
			return map[string]any{"value": nil}, nil
		}
		if err != nil {
			return nil, err
		}
		raw, err := a.pluginStore.unseal(kind, "admin", key, sealed)
		if err != nil {
			return nil, err
		}
		return map[string]any{"value": json.RawMessage(raw)}, nil
	}
	if r.Method != http.MethodPost {
		return nil, &APIError{"METHOD_NOT_ALLOWED", "此设置仅支持读取和保存", 405}
	}
	var raw json.RawMessage
	if err := decodePluginBody(w, r, &raw); err != nil {
		return nil, err
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || (key == "order" && raw[0] != '[') || (key != "order" && raw[0] != '{') {
		return nil, bad("INVALID_DASHBOARD_SETTINGS", "仪表板布局或显示设置格式无效")
	}
	sealed, err := a.pluginStore.seal(kind, "admin", key, raw)
	if err != nil {
		return nil, err
	}
	_, err = a.Store.DB.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, kind, key, sealed, now())
	return map[string]bool{"saved": err == nil}, err
}
