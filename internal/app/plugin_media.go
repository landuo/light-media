package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"light-media/internal/fnos"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func pluginStrictJSON(raw json.RawMessage, value any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value) == nil && decoder.Decode(new(any)) == io.EOF
}

func mediaRefreshFingerprint(config FNConfig) string {
	host, _ := fnos.NormalizeHost(config.Host)
	raw, _ := json.Marshal([]string{host, config.Username})
	return tokenHash(string(raw))
}

func mediaRefreshAssetDigest(asset Asset) []byte {
	raw, _ := json.Marshal(struct {
		ID, Media, Path, Root, CreatedAt, Part string
		Size                                   int64
		Season                                 int
		Episodes                               []int
	}{asset.ID, mediaKey(asset.Media), asset.Path, asset.RootID, asset.CreatedAt, asset.Part, asset.Size, asset.Season, asset.Episodes})
	digest, _ := json.Marshal(tokenHash(string(raw)))
	return digest
}

func (a *App) mediaRefreshConfig(ctx context.Context, instanceID string) (builtin.MediaServerRefreshConfiguration, error) {
	var config builtin.MediaServerRefreshConfiguration
	if value, bound := originalBuiltinHostConfiguration(ctx, a, instanceID, "MediaServerRefresh"); bound {
		var ok bool
		config, ok = value.(builtin.MediaServerRefreshConfiguration)
		if !ok {
			return config, bad("INVALID_PLUGIN_CONFIG", "媒体库刷新实例成员无法读取")
		}
	} else {
		stored, err := a.pluginStore.Config(ctx, instanceID)
		if err != nil || !pluginStrictJSON(stored.Value, &config) {
			return config, bad("INVALID_PLUGIN_CONFIG", "媒体库刷新配置无法读取")
		}
	}
	if _, err := config.Duration(); err != nil {
		return config, bad("INVALID_PLUGIN_CONFIG", "媒体库刷新延迟或媒体服务器引用无效")
	}
	for _, server := range config.MediaServers {
		if server != "fnos" {
			return config, bad("PLUGIN_MEDIA_SERVER_UNMAPPED", "媒体服务器引用尚未映射到本项目的连接")
		}
	}
	return config, nil
}

// Called under pluginMu after the ordinary identity/generation/revision check.
func (a *App) pluginMediaHost(ctx context.Context, installation PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method == "media.servers" {
		if string(raw) != "{}" {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "媒体服务器列表不接受查询目标")
		}
		options := []map[string]string{}
		// Source get_configs lists enabled configurations, not connected instances.
		if a.Settings().FNOS.Enabled {
			options = append(options, map[string]string{"title": "飞牛影视", "value": "fnos"})
		}
		return json.Marshal(options)
	}
	if run == nil || installation.PluginID != "MediaServerRefresh" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "媒体刷新只允许当前插件执行调用")
	}
	config, err := a.mediaRefreshConfig(ctx, run.InstanceID)
	if err != nil {
		return nil, err
	}
	if method == "events.configure" {
		var request struct {
			Handler string   `json:"handler"`
			Types   []string `json:"event_types"`
			DelayMS int64    `json:"delay_ms"`
			Active  bool     `json:"active"`
		}
		delay, _ := config.Duration()
		if (run.Action != "init" && run.Action != "reload") || !pluginStrictJSON(raw, &request) || request.Handler != "refresh" || !slices.Equal(request.Types, []string{"import.completed"}) || request.DelayMS != delay.Milliseconds() || request.Active != (config.Enabled && len(config.MediaServers) != 0) {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "媒体刷新只能注册当前配置声明的整理完成事件")
		}
		if err := a.pluginStore.configureEventSubscription(ctx, *run, request.Handler, request.Types, delay, request.Active); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"registered":true}`), nil
	}
	var input plugins.Invocation
	var expected, requested struct {
		BatchID string `json:"batch_id"`
	}
	if method != "media.refresh" || run.Action != "event" || !config.Enabled || !slices.Contains(config.MediaServers, "fnos") || json.Unmarshal(run.Input, &input) != nil || !pluginStrictJSON(input.Input, &expected) || !pluginStrictJSON(raw, &requested) || requested.BatchID == "" || requested.BatchID != expected.BatchID {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "媒体刷新只能执行当前投递批次")
	}
	configuration := a.Settings().FNOS
	if !configuration.Enabled || configuration.Host == "" {
		a.pluginExecutionLog(installation, *run, "WARN", "media_service_inactive", "媒体服务器未启用，未请求刷新", "")
		return json.RawMessage(`{"skipped":true,"reason":"media_service_inactive"}`), nil
	}
	service, err := fnos.NewService(fnos.Config{Host: configuration.Host, Username: configuration.Username, Password: configuration.Password})
	if err != nil {
		return nil, bad("PLUGIN_MEDIA_CONNECTION_INVALID", "媒体服务器连接配置无效")
	}
	fingerprint := mediaRefreshFingerprint(configuration)
	var state, storedFingerprint string
	var prior []byte
	err = a.Store.DB.QueryRowContext(ctx, `SELECT state,service_fingerprint,result FROM plugin_media_refresh_operations WHERE run_id=?`, run.ID).Scan(&state, &storedFingerprint, &prior)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if state != "" && fingerprint != storedFingerprint {
		return nil, bad("REVIEW_PLUGIN_MEDIA_TARGET", "媒体服务器已变化，不能复用原刷新执行")
	}
	if state == "confirmed" {
		return a.pluginStore.unseal("media_refresh_result", run.InstanceID, run.ID, prior)
	}
	if state == "submitting" {
		return nil, bad("REVIEW_PLUGIN_MEDIA_REFRESH", "先前刷新提交结果尚未确认，未重复发送")
	}
	if _, err := a.Store.DB.ExecContext(ctx, `INSERT INTO plugin_media_refresh_operations VALUES(?,?,?,'preparing',NULL,?) ON CONFLICT(run_id) DO UPDATE SET state='preparing',updated_at=excluded.updated_at`, run.ID, run.InstanceID, fingerprint, now()); err != nil {
		return nil, err
	}
	if _, err := a.Store.DB.ExecContext(ctx, `DELETE FROM plugin_media_refresh_items WHERE run_id=?`, run.ID); err != nil {
		return nil, err
	}
	var after, totalRead int64
	complete := false
	phase := "media_connect"
	var releaseWrite func()
	defer func() {
		if releaseWrite != nil {
			releaseWrite()
		}
	}()
	result, err := service.RefreshLibraryBatches(ctx, func(ctx context.Context, libraries []fnos.ServiceLibrary) ([]string, error) {
		phase = "batch_prepare"
		if complete {
			return nil, nil
		}
		page, err := a.pluginStore.pluginBatchEvents(ctx, *run, requested.BatchID, after, 200)
		if err != nil {
			return nil, err
		}
		type item struct {
			id     string
			digest []byte
		}
		items := make([]item, 0, len(page.Events))
		paths := make([]string, 0, len(page.Events))
		for _, event := range page.Events {
			var asset Asset
			if event.Type != "import.completed" || a.Store.Get(ctx, "asset", event.AssetID, &asset) != nil || mediaKey(asset.Media) != event.MediaID {
				return nil, bad("REVIEW_PLUGIN_MEDIA_ASSET", "整理事件引用的影视资产已变化，未发送刷新")
			}
			root, err := a.importRoot(asset.RootID)
			if err != nil || !within(root.Path, asset.Path) {
				return nil, bad("REVIEW_PLUGIN_MEDIA_ROOT", "整理资产不在当前授权媒体目录内")
			}
			actual := asset.Path
			if len(configuration.Mappings) != 0 {
				if mapped, err := mapPath(actual, configuration.Mappings); err == nil {
					actual = mapped
				}
			}
			if _, translated, err := matchFNOSDirectory(asset.Path, actual, libraries); err == nil {
				actual = translated
			}
			sealed, err := a.pluginStore.seal("media_refresh_item", run.InstanceID, run.ID+":"+asset.ID, mediaRefreshAssetDigest(asset))
			if err != nil {
				return nil, err
			}
			items = append(items, item{id: asset.ID, digest: sealed})
			paths = append(paths, actual)
		}
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		for _, item := range items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_media_refresh_items VALUES(?,?,?) ON CONFLICT(run_id,asset_id) DO UPDATE SET digest=excluded.digest`, run.ID, item.id, item.digest); err != nil {
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		after, complete = page.NextCursor, page.Complete
		totalRead += int64(len(page.Events))
		if complete && totalRead != page.Total {
			return nil, bad("PLUGIN_BATCH_CORRUPT", "刷新批次事件数量不一致")
		}
		return paths, nil
	}, func() error {
		phase = "media_submit"
		if !complete || !a.Settings().FNOS.Enabled || mediaRefreshFingerprint(a.Settings().FNOS) != fingerprint {
			return bad("REVIEW_PLUGIN_MEDIA_TARGET", "刷新前媒体服务器已变化，未发送请求")
		}
		release, err := a.beginWrite()
		if err != nil {
			return err
		}
		// The pluginMu held by this call serializes lifecycle changes. The
		// durable submitting boundary precedes every possible remote scan.
		releaseWrite = release
		changed, err := a.Store.DB.ExecContext(ctx, `UPDATE plugin_media_refresh_operations SET state='submitting',updated_at=? WHERE run_id=? AND state='preparing'`, now(), run.ID)
		if err != nil {
			return err
		}
		if count, _ := changed.RowsAffected(); count != 1 {
			return bad("PLUGIN_REFRESH_STATE_CONFLICT", "未能保存刷新提交状态，未发送请求")
		}
		return nil
	})
	if err != nil {
		var api *APIError
		if errors.As(err, &api) {
			return nil, err
		}
		var serviceError *fnos.ServiceError
		if errors.As(err, &serviceError) {
			stage := map[string]string{"media_connect": "连接", "batch_prepare": "批次准备", "media_submit": "扫描提交"}[phase]
			a.pluginExecutionLog(installation, *run, "ERROR", "media_refresh_rejected", "媒体库刷新在"+stage+"阶段未得到业务确认", "FNOS_"+strings.ToUpper(serviceError.Code))
		} else {
			a.pluginExecutionLog(installation, *run, "ERROR", phase, "媒体库刷新批次准备或提交失败", "PLUGIN_REFRESH_HOST_FAILED")
		}
		return nil, bad("PLUGIN_MEDIA_REFRESH_FAILED", "媒体服务器未确认刷新请求，详情见插件执行记录")
	}
	rawResult, _ := json.Marshal(map[string]any{"scan_requested": true, "result": result})
	sealed, err := a.pluginStore.seal("media_refresh_result", run.InstanceID, run.ID, rawResult)
	if err != nil {
		return nil, err
	}
	changed, err := a.Store.DB.ExecContext(ctx, `UPDATE plugin_media_refresh_operations SET state='confirmed',result=?,updated_at=? WHERE run_id=? AND state='submitting'`, sealed, now(), run.ID)
	if err != nil {
		return nil, err
	}
	if count, _ := changed.RowsAffected(); count != 1 {
		return nil, bad("PLUGIN_REFRESH_STATE_CONFLICT", "刷新结果未能持久确认，需要核对后再试")
	}
	return rawResult, nil
}
