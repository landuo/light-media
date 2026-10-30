package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"light-media/internal/media/words"
)

type sharedSystemConfigRuntime struct {
	uiMu          sync.Mutex // Source async_set has its own lock, independent of synchronous callers.
	loaded        bool       // All cache fields except consumerError are guarded by recognitionMu.
	value         json.RawMessage
	observed      sharedSystemConfigSnapshot
	consumerError atomic.Pointer[sharedSystemConfigError]
}
type sharedSystemConfigError struct{ err error }
type sharedSystemConfigSnapshot struct {
	present  bool
	revision int64
	sealed   string
}
type sharedSystemConfigReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readSharedSystemConfigRow(ctx context.Context, db sharedSystemConfigReader) (sharedSystemConfigSnapshot, error) {
	var x sharedSystemConfigSnapshot
	var sealed []byte
	e := db.QueryRowContext(ctx, `SELECT revision,sealed FROM recognition_words WHERE id='CustomIdentifiers'`).Scan(&x.revision, &sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return x, nil
	}
	if e != nil {
		return x, e
	}
	x.present = true
	x.sealed = string(sealed)
	return x, nil
}
func (a *App) publishSharedSystemConfigLocked(raw json.RawMessage) {
	rules, e := sharedSystemConfigCodepointRules(raw)
	var matcher *words.Matcher
	if e == nil {
		matcher, e = words.NewCodepointRules(rules)
	}
	if e != nil {
		a.sharedSystemConfig.consumerError.Store(&sharedSystemConfigError{e})
		a.recognitionMatcher.Store(nil)
		return
	}
	a.recognitionMatcher.Store(matcher)
	a.sharedSystemConfig.consumerError.Store(nil)
}
func (a *App) readSharedSystemConfigLocked(ctx context.Context, db sharedSystemConfigReader) (sharedSystemConfigSnapshot, json.RawMessage, error) {
	state := &a.sharedSystemConfig
	if !state.loaded {
		observed, rows, e := a.readSharedSystemConfigRows(ctx, db)
		if e != nil {
			return observed, nil, e
		}
		raw := json.RawMessage(`null`)
		if len(rows) > 0 {
			raw = rows[len(rows)-1].Value
		} // Source singleton initialization assigns every row in order.
		state.loaded = true
		state.observed = observed
		state.value = append(json.RawMessage(nil), raw...)
		a.publishSharedSystemConfigLocked(raw)
	}
	// Cache is not a SQL snapshot. A successful concurrent async publication or
	// failed synchronous write may intentionally leave a different hot value.
	return state.observed, append(json.RawMessage(nil), state.value...), nil
}
func (a *App) publishSharedSystemConfigValueLocked(raw json.RawMessage) {
	a.sharedSystemConfig.value = append(json.RawMessage(nil), raw...)
	a.sharedSystemConfig.loaded = true
	a.publishSharedSystemConfigLocked(raw)
}

// SQLite's original JSON column has NUMERIC affinity. Applying it before
// sealing preserves cold-read scalar conversion without a second stored fact.
func sharedSystemConfigPersistedJSON(ctx context.Context, tx *sql.Tx, raw json.RawMessage) (json.RawMessage, error) {
	if _, e := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS shared_system_config_projection(value JSON); DELETE FROM shared_system_config_projection;`); e != nil {
		return nil, e
	}
	if _, e := tx.ExecContext(ctx, `INSERT INTO shared_system_config_projection(value) VALUES(?)`, string(raw)); e != nil {
		return nil, e
	}
	var value any
	if e := tx.QueryRowContext(ctx, `SELECT value FROM shared_system_config_projection`).Scan(&value); e != nil {
		return nil, e
	}
	switch v := value.(type) {
	case string:
		return json.RawMessage(v), nil
	case []byte:
		return json.RawMessage(v), nil
	case int64:
		return json.Marshal(v)
	case float64:
		if math.IsInf(v, -1) {
			return json.RawMessage(`-1e999`), nil
		}
		if math.IsInf(v, 1) {
			return json.RawMessage(`1e999`), nil
		}
		text := strconv.FormatFloat(v, 'g', -1, 64)
		if !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		return json.RawMessage(text), nil
	default:
		return nil, errors.New("unexpected SQLite JSON affinity result")
	}
}

// The caller owns recognitionMu and the SQL transaction. This is the source
// synchronous setter; async UI deliberately does not share that outer lock.
func (a *App) writeSharedSystemConfigLocked(ctx context.Context, tx *sql.Tx, raw json.RawMessage, async bool) (*bool, error) {
	if async {
		return nil, errors.New("async setter requires its independent snapshot path")
	}
	_, old, e := a.readSharedSystemConfigLocked(ctx, tx)
	if e != nil {
		return nil, e
	}
	a.publishSharedSystemConfigValueLocked(raw) // Intentionally before the query/commit, including failures.
	observed, rows, e := a.readSharedSystemConfigRows(ctx, tx)
	if e != nil {
		return nil, e
	}
	if len(rows) > 0 && sharedSystemConfigEqual(old, raw) {
		return nil, nil
	}
	changed := true
	if len(rows) > 0 && !originalBaseJSONTruthy(raw) {
		rows = rows[1:]
	} else if len(rows) > 0 && sharedSystemConfigEqual(rows[0].Value, raw) {
		// SQLAlchemy's loaded JSON attribute is unchanged: commit but no UPDATE.
		changed = false
	} else {
		value, e := sharedSystemConfigBoundPersisted(ctx, tx, raw)
		if e != nil {
			return nil, e
		}
		if len(rows) == 0 {
			rows = []sharedSystemConfigSourceRow{{ID: sharedSystemConfigNextRowID(rows), Value: value}}
		} else {
			rows[0].Value = value
		}
	}
	after := observed
	if changed {
		after, e = a.persistSharedSystemConfigRows(ctx, tx, observed, rows)
		if e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	a.sharedSystemConfig.observed = after
	yes := true
	return &yes, nil
}
