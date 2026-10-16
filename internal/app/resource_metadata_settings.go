package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"
)

// These are source parser inputs. They live outside the general connection
// settings so saving another settings tab cannot overwrite an edited rule.
type resourceMetadataConfiguration struct {
	SourceMode    string   `json:"source_mode"`
	ReleaseGroups []string `json:"release_groups"`
	Customization []string `json:"customization"`
}

func (a *App) resourceMetadataSettings(ctx context.Context) (int64, resourceMetadataConfiguration, error) {
	config := resourceMetadataConfiguration{SourceMode: "rust", ReleaseGroups: []string{}, Customization: []string{}}
	var revision int64
	var sealed []byte
	err := a.Store.DB.QueryRowContext(ctx, `SELECT revision,sealed FROM resource_metadata_settings WHERE id='main'`).Scan(&revision, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, config, nil
	}
	if err != nil {
		return 0, config, err
	}
	raw, err := a.pluginStore.unseal("core_metadata", "system", "main", sealed)
	if err != nil {
		return 0, config, err
	}
	if json.Unmarshal(raw, &config) != nil {
		return 0, config, bad("INVALID_RESOURCE_METADATA", "资源识别配置无法读取")
	}
	return revision, config, nil
}
func validateResourceMetadataSettings(config resourceMetadataConfiguration) error {
	if config.SourceMode != "rust" && config.SourceMode != "legacy" {
		return bad("INVALID_RESOURCE_METADATA", "请选择有效的识别行为")
	}
	total := 0
	for _, lines := range [][]string{config.ReleaseGroups, config.Customization} {
		if len(lines) > 8192 {
			return bad("INVALID_RESOURCE_METADATA", "资源识别配置超过允许范围")
		}
		for _, line := range lines {
			total += len(line)
			if len(line) > 64<<10 || total > 1<<20 || !utf8.ValidString(line) {
				return bad("INVALID_RESOURCE_METADATA", "资源识别配置超过允许范围")
			}
		}
	}
	return nil
}
func (a *App) saveResourceMetadataSettings(ctx context.Context, revision int64, config resourceMetadataConfiguration) (int64, error) {
	if revision < 0 {
		return 0, bad("RESOURCE_METADATA_REVISION_CONFLICT", "资源识别配置版本无效")
	}
	if err := validateResourceMetadataSettings(config); err != nil {
		return 0, err
	}
	if config.ReleaseGroups == nil {
		config.ReleaseGroups = []string{}
	}
	if config.Customization == nil {
		config.Customization = []string{}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return 0, err
	}
	if len(raw) > pluginValueLimit {
		return 0, bad("INVALID_RESOURCE_METADATA", "资源识别配置超过允许范围")
	}
	sealed, err := a.pluginStore.seal("core_metadata", "system", "main", raw)
	if err != nil {
		return 0, err
	}
	var result sql.Result
	if revision == 0 {
		result, err = a.Store.DB.ExecContext(ctx, `INSERT INTO resource_metadata_settings(id,revision,sealed,updated_at) VALUES('main',1,?,?) ON CONFLICT(id) DO NOTHING`, sealed, now())
	} else {
		result, err = a.Store.DB.ExecContext(ctx, `UPDATE resource_metadata_settings SET revision=revision+1,sealed=?,updated_at=? WHERE id='main' AND revision=?`, sealed, now(), revision)
	}
	if err != nil {
		return 0, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return 0, bad("RESOURCE_METADATA_REVISION_CONFLICT", "资源识别配置已变化，请重新载入")
	}
	return revision + 1, nil
}
func (a *App) resourceMetadataSettingsAPI(w http.ResponseWriter, r *http.Request) (any, error) {
	if r.Method == "GET" {
		revision, config, err := a.resourceMetadataSettings(r.Context())
		return map[string]any{"revision": revision, "config": config}, err
	}
	if r.Method != "PUT" {
		return nil, bad("METHOD_NOT_ALLOWED", "不支持此操作")
	}
	var in struct {
		Revision int64                         `json:"revision"`
		Config   resourceMetadataConfiguration `json:"config"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		return nil, err
	}
	revision, err := a.saveResourceMetadataSettings(r.Context(), in.Revision, in.Config)
	return map[string]any{"revision": revision}, err
}
