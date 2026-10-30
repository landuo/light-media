package app

import (
	"context"
	"database/sql"
	"encoding/json"
)

// The exceptional source duplicate-key state remains one authoritative native
// fact. Different authenticated data identifies this internal envelope; no user
// JSON object can masquerade as it. Ordinary legacy single rows stay unchanged.
const sharedSystemConfigRowsKind = "core_words_rows_v1"

type sharedSystemConfigSourceRow struct {
	ID    int64           `json:"id"`
	Value json.RawMessage `json:"value"`
}
type sharedSystemConfigRowsEnvelope struct {
	Rows []sharedSystemConfigSourceRow `json:"rows"`
}

func (a *App) sharedSystemConfigRows(snapshot sharedSystemConfigSnapshot) ([]sharedSystemConfigSourceRow, error) {
	if !snapshot.present {
		return nil, nil
	}
	sealed := []byte(snapshot.sealed)
	if raw, e := a.pluginStore.unseal("core_words", "system", "CustomIdentifiers", sealed); e == nil {
		normalized, _, e := sharedSystemConfigJSON(raw)
		if e != nil {
			return nil, e
		}
		return []sharedSystemConfigSourceRow{{ID: 1, Value: normalized}}, nil
	}
	// From empty/legacy-one state these two APIs can reach at most two rows:
	// sync only creates if empty; one serialized async create can race that insert.
	// Another async query with two rows fails before it can create a third.
	raw, e := a.pluginStore.unsealBound(sharedSystemConfigRowsKind, "system", "CustomIdentifiers", sealed, 2*pluginDataValueLimit+4096)
	if e != nil {
		return nil, e
	}
	var envelope sharedSystemConfigRowsEnvelope
	if e = json.Unmarshal(raw, &envelope); e != nil {
		return nil, e
	}
	if len(envelope.Rows) < 1 || len(envelope.Rows) > 2 {
		return nil, bad("PLUGIN_DATA_CORRUPT", "全局配置行集合无效")
	}
	previous := int64(0)
	for i := range envelope.Rows {
		row := &envelope.Rows[i]
		if row.ID <= previous || len(row.Value) > pluginDataValueLimit {
			return nil, bad("PLUGIN_DATA_CORRUPT", "全局配置行身份无效")
		}
		previous = row.ID
		row.Value, _, e = sharedSystemConfigJSON(row.Value)
		if e != nil {
			return nil, e
		}
	}
	return envelope.Rows, nil
}
func (a *App) readSharedSystemConfigRows(ctx context.Context, db sharedSystemConfigReader) (sharedSystemConfigSnapshot, []sharedSystemConfigSourceRow, error) {
	observed, e := readSharedSystemConfigRow(ctx, db)
	if e != nil {
		return observed, nil, e
	}
	rows, e := a.sharedSystemConfigRows(observed)
	return observed, rows, e
}
func sharedSystemConfigNextRowID(rows []sharedSystemConfigSourceRow) int64 {
	// This allocation describes the isolated supported-key namespace only. The
	// full source SystemConfig table's other-key row identity remains a tracked
	// integration dependency, not an assertion of per-key SQLite allocation.
	next := int64(1)
	for _, row := range rows {
		if row.ID >= next {
			next = row.ID + 1
		}
	}
	return next
}
func (a *App) sealSharedSystemConfigRows(rows []sharedSystemConfigSourceRow) ([]byte, error) {
	if len(rows) == 1 && rows[0].ID == 1 {
		return a.pluginStore.seal("core_words", "system", "CustomIdentifiers", rows[0].Value)
	}
	raw, e := json.Marshal(sharedSystemConfigRowsEnvelope{Rows: rows})
	if e != nil {
		return nil, e
	}
	return a.pluginStore.seal(sharedSystemConfigRowsKind, "system", "CustomIdentifiers", raw)
}
func (a *App) persistSharedSystemConfigRows(ctx context.Context, tx *sql.Tx, previous sharedSystemConfigSnapshot, rows []sharedSystemConfigSourceRow) (sharedSystemConfigSnapshot, error) {
	if len(rows) == 0 {
		_, e := tx.ExecContext(ctx, `DELETE FROM recognition_words WHERE id='CustomIdentifiers'`)
		return sharedSystemConfigSnapshot{}, e
	}
	sealed, e := a.sealSharedSystemConfigRows(rows)
	if e != nil {
		return previous, e
	}
	if previous.present {
		_, e = tx.ExecContext(ctx, `UPDATE recognition_words SET revision=revision+1,sealed=?,updated_at=? WHERE id='CustomIdentifiers'`, sealed, now())
	} else {
		_, e = tx.ExecContext(ctx, `INSERT INTO recognition_words(id,revision,sealed,updated_at) VALUES('CustomIdentifiers',1,?,?)`, sealed, now())
	}
	if e != nil {
		return previous, e
	}
	return sharedSystemConfigSnapshot{present: true, revision: previous.revision + 1, sealed: string(sealed)}, nil
}
func sharedSystemConfigBoundPersisted(ctx context.Context, tx *sql.Tx, raw json.RawMessage) (json.RawMessage, error) {
	persisted, e := sharedSystemConfigPersistedJSON(ctx, tx, raw)
	if e == nil && len(persisted) > pluginDataValueLimit {
		e = bad("INVALID_PLUGIN_VALUE", "全局配置超过既有宿主存储上限")
	}
	return persisted, e
}
