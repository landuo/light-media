package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
)

// This is the explicit new MediaServers namespace, not a projection of FNOS or
// an arbitrary SystemConfig DAO. The origin record survives a source row delete.
// An absent origin is unknown legacy provenance, not a fabricated source None.
const originalMediaServerConfigKind = "source_media_servers_v1"
const originalMediaServerConfigKey = "MediaServers"
const originalMediaServerConfigOrigin = `{"schema":1,"origin":"explicit-admin"}`

type originalMediaServerConfigSnapshot struct {
	established bool
	present     bool
	value       json.RawMessage
}

type originalMediaServerConfigStore struct {
	producerMu  sync.Mutex // Original async_set's independent async lock.
	cacheMu     sync.Mutex // Short deepcopy/get/publication lock, never around SQL.
	plugin      *PluginStore
	loaded      bool
	established bool
	value       json.RawMessage
}

type originalMediaServerConfigReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func newOriginalMediaServerConfigStore(ctx context.Context, plugin *PluginStore) (*originalMediaServerConfigStore, error) {
	if ctx == nil || plugin == nil || plugin.store == nil || plugin.store.DB == nil {
		return nil, errors.New("media server configuration requires the owned application store")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &originalMediaServerConfigStore{plugin: plugin}
	state, err := s.read(ctx, plugin.store.DB)
	if err != nil {
		return nil, err
	}
	s.loaded, s.established = true, state.established
	if state.established {
		s.value = append(json.RawMessage(nil), state.value...)
	}
	return s, nil
}

// Use the existing SDK data budget symmetrically, not PluginStore.unseal's
// default 1 MiB. This is not an HTTP body budget or an increase to global limits.
// Checking normalized bytes too prevents a successful write that cold-read fails.
func originalMediaServerConfigJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > pluginDataValueLimit {
		return nil, bad("INVALID_MEDIA_SERVER_CONFIG", "媒体服务器配置超出既有宿主数据范围")
	}
	value, _, err := sharedSystemConfigJSON(raw)
	if err != nil || len(value) > pluginDataValueLimit {
		return nil, bad("INVALID_MEDIA_SERVER_CONFIG", "媒体服务器配置不是有效的有界JSON")
	}
	return value, nil
}

// The original endpoint filters only top-level list items using Python truth,
// without mutating its caller or validating MediaServerConf during saving.
// Authentication and HTTP Union/body rendering remain the HTTP adapter's job.
func originalMediaServerSettingValue(raw json.RawMessage) (json.RawMessage, error) {
	value, err := originalMediaServerConfigJSON(raw)
	if err != nil || value[0] != '[' {
		return value, err
	}
	var input []json.RawMessage
	if err = json.Unmarshal(value, &input); err != nil {
		return nil, err
	}
	filtered := make([]json.RawMessage, 0, len(input))
	for _, item := range input {
		if originalBaseJSONTruthy(item) {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		return json.RawMessage(`null`), nil
	}
	out, err := json.Marshal(filtered)
	if err != nil {
		return nil, err
	}
	return originalMediaServerConfigJSON(out)
}

func (s *originalMediaServerConfigStore) read(ctx context.Context, db originalMediaServerConfigReader) (originalMediaServerConfigSnapshot, error) {
	state := originalMediaServerConfigSnapshot{value: json.RawMessage(`null`)}
	rows, err := db.QueryContext(ctx, `SELECT id,payload FROM records WHERE kind=? AND id IN ('origin','value')`, originalMediaServerConfigKind)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var sealed []byte
		if err = rows.Scan(&id, &sealed); err != nil {
			return state, err
		}
		key := originalMediaServerConfigKey
		if id == "origin" {
			key += ".origin"
		}
		raw, err := s.plugin.unsealBound(originalMediaServerConfigKind, "system", key, sealed, pluginDataValueLimit)
		if err != nil {
			return state, err // Never convert an integrity failure to an empty config.
		}
		switch id {
		case "origin":
			if !bytes.Equal(raw, []byte(originalMediaServerConfigOrigin)) {
				return state, bad("MEDIA_SERVER_CONFIG_CORRUPT", "媒体服务器配置来源记录无效")
			}
			state.established = true
		case "value":
			state.value, err = originalMediaServerConfigJSON(raw)
			if err != nil {
				return state, err
			}
			state.present = true
		default:
			return state, bad("MEDIA_SERVER_CONFIG_CORRUPT", "媒体服务器配置记录无效")
		}
	}
	if err = rows.Err(); err != nil {
		return state, err
	}
	if state.present && !state.established {
		return state, bad("MEDIA_SERVER_CONFIG_CORRUPT", "媒体服务器配置缺少已保存来源")
	}
	return state, nil
}

func (s *originalMediaServerConfigStore) get(ctx context.Context) (json.RawMessage, error) {
	if ctx == nil {
		return nil, errors.New("media server configuration requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if !s.loaded || !s.established {
		return nil, bad("MEDIA_SERVER_CONFIG_NOT_ESTABLISHED", "尚未保存原媒体服务器配置，未将现有飞牛设置推定为原值")
	}
	return append(json.RawMessage(nil), s.value...), nil
}

func (s *originalMediaServerConfigStore) publish(raw json.RawMessage) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.loaded, s.established = true, true
	s.value = append(json.RawMessage(nil), raw...)
}

// set is the source async setter after an admitted producer supplies its value.
// It is not a new plugin SystemConfig.Set capability. No event is emitted here:
// the owning adapter emits only after true, and must not leak raw credentials.
// nil means original None/no-op; errors leave the old hot object/value intact.
func (s *originalMediaServerConfigStore) set(ctx context.Context, raw json.RawMessage) (*bool, error) {
	if ctx == nil {
		return nil, errors.New("media server configuration requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, err := originalMediaServerConfigJSON(raw)
	if err != nil {
		return nil, err
	}
	s.producerMu.Lock()
	defer s.producerMu.Unlock()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := s.plugin.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	before, err := s.read(ctx, tx)
	if err != nil {
		return nil, err
	}
	if before.present && sharedSystemConfigEqual(before.value, value) {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		// Source closes its read session without a mutation commit, but still
		// replaces the cache with a new deepcopy even when its value is equal.
		if err = tx.Rollback(); err != nil {
			return nil, err
		}
		s.publish(value)
		return nil, nil
	}
	if before.present && !originalBaseJSONTruthy(value) {
		_, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind=? AND id='value'`, originalMediaServerConfigKind)
	} else {
		persisted := value
		// Match original SQLite JSON affinity only for scalar numbers; arrays,
		// dictionaries, strings and null stay exact normalized JSON text.
		if value[0] == '-' || value[0] >= '0' && value[0] <= '9' {
			persisted, err = sharedSystemConfigBoundPersisted(ctx, tx, value)
		}
		if err == nil {
			var sealed []byte
			sealed, err = s.plugin.seal(originalMediaServerConfigKind, "system", originalMediaServerConfigKey, persisted)
			if err == nil {
				if before.present {
					_, err = tx.ExecContext(ctx, `UPDATE records SET payload=?,updated_at=? WHERE kind=? AND id='value'`, sealed, now(), originalMediaServerConfigKind)
				} else {
					_, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'value',?,?)`, originalMediaServerConfigKind, sealed, now())
				}
			}
		}
	}
	if err != nil {
		return nil, err // Ordinary SQL/commit errors are source errors, not None.
	}
	if !before.established {
		origin, err := s.plugin.seal(originalMediaServerConfigKind, "system", originalMediaServerConfigKey+".origin", []byte(originalMediaServerConfigOrigin))
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'origin',?,?)`, originalMediaServerConfigKind, origin, now()); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	// Once committed, cancellation cannot suppress publication of the actual
	// committed fact. The future owning event adapter handles detached delivery.
	s.publish(value)
	yes := true
	return &yes, nil
}
