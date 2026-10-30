package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"

	"light-media/internal/plugins"
)

func originalSharedSystemConfigCapability(method string) bool {
	return method == plugins.SharedSystemConfigGet || method == plugins.SharedSystemConfigSet
}
func sharedSystemConfigDecode(raw json.RawMessage, out any) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return bad("INVALID_PLUGIN_VALUE", "全局配置参数必须为对象")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return bad("INVALID_PLUGIN_VALUE", "全局配置参数无效")
	}
	if e := d.Decode(new(any)); !errors.Is(e, io.EOF) {
		return bad("INVALID_PLUGIN_VALUE", "全局配置参数无效")
	}
	return nil
}
func (a *App) sharedSystemConfigScopeTx(ctx context.Context, tx *sql.Tx, scope originalBaseCallScope) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	p := scope.object
	if p == nil || p.app != a || p.stopping.Load() && scope.phase != "stop" {
		return bad("PLUGIN_CALL_CLOSED", "插件调用已结束")
	}
	if scope.phase == "stop" {
		return nil
	} // Exact cleanup closure already authenticated by scope.validate.
	var pluginID, version string
	var generation int64
	if e := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, scope.installation.InstanceID).Scan(&pluginID, &version, &generation); e != nil {
		return e
	}
	if pluginID != scope.installation.PluginID || version != scope.installation.Version || generation != scope.installation.Generation {
		return bad("PLUGIN_SNAPSHOT_STALE", "插件实例已变化")
	}
	return nil
}
func (a *App) originalSharedSystemConfigResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedSystemConfigCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "全局配置操作未授权")
	}
	var key string
	var value json.RawMessage
	if method == plugins.SharedSystemConfigGet {
		var in plugins.SharedSystemConfigGetV1
		if e := sharedSystemConfigDecode(raw, &in); e != nil {
			return nil, e
		}
		key = in.Key
	} else {
		var in plugins.SharedSystemConfigSetV1
		if e := sharedSystemConfigDecode(raw, &in); e != nil {
			return nil, e
		}
		key, value = in.Key, in.Value
	}
	if key != plugins.SystemConfigCustomIdentifiers {
		return nil, bad("SYSTEM_CONFIG_KEY_DENIED", "该全局配置键未开放")
	}
	if method == plugins.SharedSystemConfigSet {
		var e error
		value, _, e = sharedSystemConfigJSON(value)
		if e != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "全局配置值不是有效JSON")
		}
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	a.recognitionMu.Lock()
	defer a.recognitionMu.Unlock()
	if e := scope.validate(ctx); e != nil {
		return nil, e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = a.sharedSystemConfigScopeTx(ctx, tx, scope); e != nil {
		return nil, e
	}
	if method == plugins.SharedSystemConfigGet {
		_, result, e := a.readSharedSystemConfigLocked(ctx, tx)
		if e != nil {
			return nil, e
		}
		_, nonfinite, e := sharedSystemConfigJSON(result)
		if e != nil {
			return nil, e
		}
		// Internal source DAO reads are not UTF-8 HTTP rendering. Preserve
		// escaped lone code points for the ordinary plugin caller.
		if nonfinite {
			return nil, bad("SYSTEM_CONFIG_JSON_NONFINITE", "当前全局配置不能编码为普通JSON")
		}
		return result, nil
	}
	result, e := a.writeSharedSystemConfigLocked(ctx, tx, value, false)
	if e != nil {
		return nil, e
	}
	return json.Marshal(result)
}
