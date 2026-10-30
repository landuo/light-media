package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"light-media/internal/plugins"
)

func originalSharedSubscriptionsCapability(method string) bool {
	switch method {
	case plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsList, plugins.SharedSubscriptionsListByTMDBID, plugins.SharedSubscriptionsUpdate:
		return true
	}
	return false
}
func (a *App) sharedSubscriptionScopeTx(ctx context.Context, tx *sql.Tx, scope originalBaseCallScope) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	p := scope.object
	if p == nil || p.app != a || p.stopping.Load() && scope.phase != "stop" {
		return bad("PLUGIN_CALL_CLOSED", "插件调用已结束")
	}
	// The central validator has already authenticated this exact detached
	// cleanup closure. A replacement installation may now have a new snapshot;
	// only that host-issued stop capability keeps the old worker alive here.
	// Ordinary stale callbacks still take the generation check below.
	if scope.phase == "stop" {
		return nil
	}
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
func (a *App) sharedSubscriptionRow(ctx context.Context, tx *sql.Tx, s Subscription) (plugins.SharedSubscriptionV1, error) {
	raw, e := a.subscriptionSourceProjectionReferences(ctx, tx, s, true)
	return plugins.SharedSubscriptionV1{ID: s.ID, Revision: s.Revision, Row: raw}, e
}
func (a *App) originalSharedSubscriptionsResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedSubscriptionsCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "订阅操作未授权")
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	a.subMu.Lock()
	defer a.subMu.Unlock()
	if e := scope.validate(ctx); e != nil {
		return nil, e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = a.sharedSubscriptionScopeTx(ctx, tx, scope); e != nil {
		return nil, e
	}
	var result any
	var boundaryErr error
	if method == plugins.SharedSubscriptionsGet || method == plugins.SharedSubscriptionsUpdate {
		var id string
		var patch json.RawMessage
		var expected *int
		if method == plugins.SharedSubscriptionsGet {
			var in plugins.SharedSubscriptionGetV1
			if e = sharedSubscriptionDecodeRequest(raw, &in); e != nil {
				return nil, e
			}
			id = in.ID
		} else {
			var in plugins.SharedSubscriptionUpdateV1
			if e = sharedSubscriptionDecodeRequest(raw, &in); e != nil {
				return nil, e
			}
			id, patch, expected = in.ID, in.Patch, in.ExpectedRevision
		}
		var saved []byte
		e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, id).Scan(&saved)
		if !errors.Is(e, sql.ErrNoRows) {
			if e != nil {
				return nil, e
			}
			var sub Subscription
			if e = json.Unmarshal(saved, &sub); e != nil {
				return nil, e
			}
			if sub.ID != id {
				return nil, bad("SUBSCRIPTION_IDENTITY_CHANGED", "订阅记录身份不一致")
			}
			if method == plugins.SharedSubscriptionsUpdate {
				if expected != nil && *expected != sub.Revision {
					return nil, bad("SUBSCRIPTION_CONFLICT", "订阅版本已变化")
				}
				fields, err := sharedSubscriptionPatch(patch)
				if err != nil {
					return nil, err
				}
				if len(fields) > 0 {
					projected, err := a.sharedSubscriptionWorkingRow(ctx, tx, sub)
					if err != nil {
						return nil, err
					}
					var current map[string]json.RawMessage
					if e = json.Unmarshal(projected.Row, &current); e != nil {
						return nil, e
					}
					if e = createSharedSubscriptionProjection(ctx, tx); e != nil {
						return nil, e
					}
					if e = insertSharedSubscriptionProjection(ctx, tx, 1, id, current, sharedSubscriptionNonfinite(sub)); e != nil {
						return nil, e
					}
					if e = updateSharedSubscriptionProjection(ctx, tx, id, fields); e != nil {
						return nil, e
					}
					_, rows, nonfinite, err := readSharedSubscriptionProjection(ctx, tx, " WHERE native_id=?", id)
					if err != nil {
						return nil, err
					}
					if len(rows) != 1 {
						return nil, errors.New("subscription projection missing")
					}
					if e = a.applySharedSubscriptionColumns(ctx, tx, &sub, rows[0], fields, nonfinite[id]); e != nil {
						return nil, e
					}
					sub.Revision++
					if e = a.sharedSubscriptionScopeTx(ctx, tx, scope); e != nil {
						return nil, e
					}
					// This is the only authorized source-column writer. Native writers use
					// the prior-record invalidation hook instead, and cannot resurrect facts.
					encoded, err := json.Marshal(sub)
					if err != nil {
						return nil, err
					}
					if _, e = tx.ExecContext(ctx, `UPDATE records SET payload=?,updated_at=? WHERE kind='subscription' AND id=?`, encoded, now(), id); e != nil {
						return nil, e
					}
					if _, e = tx.ExecContext(ctx, `DELETE FROM shared_subscription_projection`); e != nil {
						return nil, e
					}
				}
			}
			result, e = a.sharedSubscriptionRow(ctx, tx, sub)
			if e != nil {
				if method == plugins.SharedSubscriptionsUpdate && len(sharedSubscriptionNonfinite(sub)) > 0 {
					boundaryErr = e
				} else {
					return nil, e
				}
			}
		}
	} else {
		where := ""
		args := []any{}
		if method == plugins.SharedSubscriptionsList {
			var in plugins.SharedSubscriptionListV1
			if e = sharedSubscriptionDecodeRequest(raw, &in); e != nil {
				return nil, e
			}
			if len(in.State) > 0 && originalBaseJSONTruthy(in.State) {
				var state string
				if json.Unmarshal(in.State, &state) != nil {
					return nil, bad("SUBSCRIPTION_STATE_TYPE", "原订阅状态筛选不支持该值")
				}
				states := strings.Split(state, ",")
				marks := []string{}
				for _, v := range states {
					marks = append(marks, "?")
					args = append(args, v)
				}
				where = ` WHERE state IN (` + strings.Join(marks, ",") + `)`
			}
		}
		if method == plugins.SharedSubscriptionsListByTMDBID {
			var in plugins.SharedSubscriptionTMDBV1
			if e = sharedSubscriptionDecodeRequest(raw, &in); e != nil {
				return nil, e
			}
			if len(in.TMDBID) == 0 {
				return nil, bad("INVALID_PLUGIN_VALUE", "缺少tmdbid")
			}
			v, err := sharedSubscriptionBind(in.TMDBID, "INTEGER")
			if err != nil {
				return nil, err
			}
			if v == nil {
				where = ` WHERE tmdbid IS NULL`
			} else {
				where = ` WHERE tmdbid=?`
				args = append(args, v)
			}
			if len(in.Season) > 0 && string(in.Season) != "null" {
				v, err := sharedSubscriptionBind(in.Season, "INTEGER")
				if err != nil {
					return nil, err
				}
				where += ` AND season=?`
				args = append(args, v)
			}
		}
		nativeRows, err := tx.QueryContext(ctx, `SELECT payload FROM records WHERE kind='subscription' ORDER BY rowid`)
		if err != nil {
			return nil, err
		}
		all := []Subscription{}
		for nativeRows.Next() {
			var b []byte
			var sub Subscription
			if e = nativeRows.Scan(&b); e != nil {
				nativeRows.Close()
				return nil, e
			}
			if e = json.Unmarshal(b, &sub); e != nil {
				nativeRows.Close()
				return nil, e
			}
			all = append(all, sub)
		}
		e = nativeRows.Err()
		nativeRows.Close()
		if e != nil {
			return nil, e
		}
		if e = createSharedSubscriptionProjection(ctx, tx); e != nil {
			return nil, e
		}
		byID := map[string]Subscription{}
		for i, sub := range all {
			projected, err := a.sharedSubscriptionWorkingRow(ctx, tx, sub)
			if err != nil {
				return nil, err
			}
			var row map[string]json.RawMessage
			if e = json.Unmarshal(projected.Row, &row); e != nil {
				return nil, e
			}
			if e = insertSharedSubscriptionProjection(ctx, tx, int64(i+1), sub.ID, row, sharedSubscriptionNonfinite(sub)); e != nil {
				return nil, e
			}
			byID[sub.ID] = sub
		}
		ids, rows, nonfinite, err := readSharedSubscriptionProjection(ctx, tx, where, args...)
		if err != nil {
			return nil, err
		}
		out := []plugins.SharedSubscriptionV1{}
		for i, id := range ids {
			if len(nonfinite[id]) > 0 {
				return nil, bad("SUBSCRIPTION_NONFINITE_JSON", "原订阅非有限数无法通过普通JSON行表示")
			}
			out = append(out, plugins.SharedSubscriptionV1{ID: id, Revision: byID[id].Revision, Row: mustCleanSeedJSON(rows[i])})
		}
		result = out
		if _, e = tx.ExecContext(ctx, `DELETE FROM shared_subscription_projection`); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	if e = scope.validate(ctx); e != nil {
		return nil, e
	}
	if boundaryErr != nil {
		return nil, boundaryErr
	}
	return json.Marshal(result)
}
func sharedSubscriptionPatch(raw json.RawMessage) (map[string]json.RawMessage, error) {
	value, e := sharedSubscriptionDecode(raw)
	if e != nil {
		return nil, bad("INVALID_SUBSCRIPTION_PATCH", "订阅patch无法解码")
	}
	out := map[string]json.RawMessage{}
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			out[k] = mustCleanSeedJSON(x)
		}
	case []any:
		for _, pair := range v {
			entry, ok := pair.([]any)
			if !ok || len(entry) != 2 {
				return nil, bad("INVALID_SUBSCRIPTION_PATCH", "原订阅patch序列须为键值对")
			}
			key, ok := entry[0].(string)
			if !ok {
				return nil, bad("INVALID_SUBSCRIPTION_PATCH", "原订阅属性名须为字符串")
			}
			out[key] = mustCleanSeedJSON(entry[1])
		}
	case string:
		if v != "" {
			return nil, bad("INVALID_SUBSCRIPTION_PATCH", "原订阅patch不是映射")
		}
	default:
		return nil, bad("INVALID_SUBSCRIPTION_PATCH", "原订阅patch不是映射")
	}
	for k := range out {
		if k == "id" {
			return nil, bad("SUBSCRIPTION_ID_IMMUTABLE", "宿主订阅身份不能通过普通列patch改变")
		}
		if _, ok := sharedSubscriptionColumnTypes[k]; !ok {
			delete(out, k)
		}
	}
	return out, nil
}

func sharedSubscriptionDecodeRequest(raw json.RawMessage, out any) error {
	if len(raw) == 0 || len(raw) > plugins.MaxPayloadBytes || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return bad("INVALID_PLUGIN_VALUE", "订阅参数须为SDK消息范围内对象")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return bad("INVALID_PLUGIN_VALUE", "订阅参数无效")
	}
	if e := d.Decode(new(any)); !errors.Is(e, io.EOF) {
		return bad("INVALID_PLUGIN_VALUE", "订阅参数含尾随内容")
	}
	return nil
}
func sharedSubscriptionNonfinite(s Subscription) map[string]string {
	out := map[string]string{}
	for k, v := range s.SharedSourceFields {
		if v.Nonfinite != "" && sharedSubscriptionJSONEqual(v.Native, sharedSubscriptionNative(s, k)) {
			out[k] = v.Nonfinite
		}
	}
	return out
}
func (a *App) sharedSubscriptionWorkingRow(ctx context.Context, tx *sql.Tx, s Subscription) (plugins.SharedSubscriptionV1, error) {
	s.SharedSourceFields = cloneSharedSubscriptionFields(s.SharedSourceFields)
	for k, v := range s.SharedSourceFields {
		v.Nonfinite = ""
		s.SharedSourceFields[k] = v
	}
	return a.sharedSubscriptionRow(ctx, tx, s)
}
