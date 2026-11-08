package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"light-media/internal/plugins"
)

func originalSubscribeGroupEvent(ctx context.Context, scope originalBaseCallScope) bool {
	lease, _ := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
	return scope.object != nil && scope.object.installation.PluginID == "SubscribeGroup" &&
		(scope.phase == "subscribe_notice" || scope.phase == "download_notice") &&
		lease != nil && lease.sourceKind == originalSourceEvent &&
		scope.object.originalPublicServiceScopeAllowed(ctx)
}

func (a *App) originalSubscribeGroupUpdates(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request plugins.SubscriptionHostGroupUpdatesV1
	if err := originalResourceDecodeBounded(raw, &request, plugins.MaxPayloadBytes); err != nil {
		return nil, err
	}
	if request.Updates == nil {
		return nil, bad("INVALID_PLUGIN_DATA", "订阅规则补填须为有序列表")
	}
	for _, update := range request.Updates {
		if update.SubscribeGroupHistory == nil {
			return nil, bad("INVALID_PLUGIN_DATA", "订阅规则补填缺少历史")
		}
		if err := validateOriginalSubscribeGroupUpdate(ctx, scope, update); err != nil {
			return nil, err
		}
		if _, err := a.originalHostUpdateSubscription(ctx, scope, update); err != nil {
			return nil, err
		}
	}
	return json.Marshal(plugins.HostMutationV1{SchemaVersion: 1, Changed: len(request.Updates) != 0})
}

func validateOriginalSubscribeGroupUpdate(ctx context.Context, scope originalBaseCallScope, update plugins.SubscriptionHostUpdateV1) error {
	entry := update.SubscribeGroupHistory
	if !originalSubscribeGroupEvent(ctx, scope) {
		return bad("PLUGIN_CAPABILITY_DENIED", "订阅规则历史须绑定当前事件")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(entry.Content), &fields) != nil || fields == nil {
		return bad("INVALID_PLUGIN_DATA", "订阅规则更新记录须为对象")
	}
	patch, err := json.Marshal(update.Patch)
	var columns map[string]json.RawMessage
	if err != nil || json.Unmarshal(patch, &columns) != nil || len(columns) != len(fields) || len(update.Patch.NullFields) != 0 {
		return bad("INVALID_PLUGIN_DATA", "订阅规则补丁与历史字段不符")
	}
	for key := range columns {
		switch key {
		case "sites", "resolution", "quality", "effect", "include":
		case "exclude", "save_path", "filter_groups":
			if scope.phase == "subscribe_notice" {
				break
			}
			return bad("INVALID_PLUGIN_DATA", "下载补填不能覆盖该订阅字段")
		default:
			return bad("INVALID_PLUGIN_DATA", "订阅规则补丁含未支持字段")
		}
		if _, recorded := fields[key]; !recorded {
			return bad("INVALID_PLUGIN_DATA", "订阅规则补丁与历史字段不符")
		}
	}
	if _, err := time.Parse(time.DateTime, entry.Time); err != nil {
		return bad("INVALID_PLUGIN_DATA", "订阅规则更新时间无效")
	}
	if scope.phase == "subscribe_notice" && strings.HasPrefix(entry.Type, "二级分类自定义配置 ") && entry.HandledKey == "" {
		return nil
	}
	if scope.phase == "download_notice" && entry.Type == "种子下载自定义配置" &&
		strings.HasPrefix(entry.HandledKey, "电视剧:") && len(fields) > 0 {
		return nil
	}
	return bad("INVALID_PLUGIN_DATA", "订阅规则更新记录与当前事件不符")
}

func (p *PluginStore) appendOriginalSubscribeGroupHistoryTx(ctx context.Context, tx *sql.Tx, instance string, entry *plugins.SubscribeGroupHistoryV1) error {
	row := struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Content string `json:"content"`
		Time    string `json:"time"`
	}{entry.Name, entry.Type, entry.Content, entry.Time}
	if err := p.appendOriginalSubscribeGroupEntryTx(ctx, tx, instance, "history", mustCleanSeedJSON(row)); err != nil {
		return err
	}
	if entry.HandledKey != "" {
		return p.appendOriginalSubscribeGroupEntryTx(ctx, tx, instance, "history_handle", mustCleanSeedJSON(entry.HandledKey))
	}
	return nil
}

func (p *PluginStore) appendOriginalSubscribeGroupEntryTx(ctx context.Context, tx *sql.Tx, instance, key string, entry json.RawMessage) error {
	var revision int64
	var sealed []byte
	err := tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, instance, key).Scan(&revision, &sealed)
	rows := []json.RawMessage{}
	if err == nil {
		raw, err := p.unseal("data", instance, key, sealed)
		if err != nil {
			return err
		}
		if originalBaseJSONTruthy(raw) && json.Unmarshal(raw, &rows) != nil {
			return bad("INVALID_PLUGIN_DATA", "订阅规则历史不是可追加的列表")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	raw := mustCleanSeedJSON(append(rows, entry))
	if len(raw) > pluginDataValueLimit {
		return bad("INVALID_PLUGIN_DATA", "订阅规则历史超过数据存储上限")
	}
	sealed, err = p.seal("data", instance, key, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_values(instance_id,kind,item_key,revision,sealed,updated_at) VALUES(?,'data',?,?,?,?) ON CONFLICT(instance_id,kind,item_key) DO UPDATE SET revision=excluded.revision,sealed=excluded.sealed,updated_at=excluded.updated_at`, instance, key, revision+1, sealed, now())
	return err
}

// Internally applied filters do not revoke previously selected materials from
// the exact current revision. A stale user revision still cannot be promoted.
func advanceOriginalSubscribeGroupIntentsTx(ctx context.Context, tx *sql.Tx, sub Subscription, previousRevision int) error {
	_, err := tx.ExecContext(ctx, `UPDATE records SET payload=json_set(payload,'$.subscription_revision',?),updated_at=? WHERE kind='intent' AND json_extract(payload,'$.subscription_id')=? AND json_extract(payload,'$.subscription_revision')=? AND COALESCE(json_extract(payload,'$.subscription_acquisition'),'')='' AND json_extract(payload,'$.state') IN ('planned','submitting','reconcile_pending')`, sub.Revision, now(), sub.ID, previousRevision)
	return err
}
