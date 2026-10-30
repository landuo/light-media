package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

type sharedSubscriptionColumn struct{ Name, Type string }

var sharedSubscriptionColumns = []sharedSubscriptionColumn{
	{"name", "TEXT"}, {"year", "TEXT"}, {"type", "TEXT"}, {"keyword", "TEXT"}, {"tmdbid", "INTEGER"}, {"imdbid", "TEXT"}, {"tvdbid", "INTEGER"}, {"doubanid", "TEXT"}, {"bangumiid", "INTEGER"}, {"anilistid", "INTEGER"}, {"mediaid", "TEXT"}, {"media_source", "TEXT"}, {"media_id", "TEXT"}, {"season", "INTEGER"}, {"poster", "TEXT"}, {"backdrop", "TEXT"}, {"vote", "REAL"}, {"description", "TEXT"}, {"filter", "TEXT"}, {"include", "TEXT"}, {"exclude", "TEXT"}, {"quality", "TEXT"}, {"resolution", "TEXT"}, {"effect", "TEXT"}, {"total_episode", "INTEGER"}, {"start_episode", "INTEGER"}, {"lack_episode", "INTEGER"}, {"note", "JSON"}, {"state", "TEXT"}, {"last_update", "TEXT"}, {"date", "TEXT"}, {"username", "TEXT"}, {"sites", "JSON"}, {"downloader", "TEXT"}, {"best_version", "INTEGER"}, {"best_version_full", "INTEGER"}, {"current_priority", "INTEGER"}, {"episode_priority", "JSON"}, {"save_path", "TEXT"}, {"search_imdbid", "INTEGER"}, {"manual_total_episode", "INTEGER"}, {"custom_words", "TEXT"}, {"media_category", "TEXT"}, {"filter_groups", "JSON"}, {"episode_group", "TEXT"},
}
var sharedSubscriptionColumnTypes = func() map[string]string {
	out := map[string]string{}
	for _, c := range sharedSubscriptionColumns {
		out[c.Name] = c.Type
	}
	return out
}()

func sharedSubscriptionDecode(raw json.RawMessage) (any, error) {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	e := d.Decode(&v)
	return v, e
}
func sharedSubscriptionBind(raw json.RawMessage, kind string) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	v, e := sharedSubscriptionDecode(raw)
	if e != nil {
		return nil, e
	}
	if kind == "JSON" {
		normalized, _, err := sharedSubscriptionNormalizeJSON(raw)
		return string(normalized), err
	}
	if v == nil {
		return nil, nil
	}
	if kind == "REAL" {
		var f float64
		switch x := v.(type) {
		case bool:
			if x {
				f = 1
			}
		case json.Number:
			f, e = x.Float64()
		case string:
			f, e = sharedSubscriptionPythonFloat(x)
		default:
			e = errors.New("not a float")
		}
		if e != nil && !math.IsInf(f, 0) {
			return nil, bad("SUBSCRIPTION_BIND_ERROR", "原订阅FLOAT列无法绑定该值")
		}
		return f, nil
	}
	switch x := v.(type) {
	case bool:
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	case string:
		return x, nil
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			f, e := x.Float64()
			if e != nil && !math.IsInf(f, 0) {
				return nil, bad("SUBSCRIPTION_BIND_ERROR", "原订阅浮点数无法绑定")
			}
			return f, nil
		}
		n, e := x.Int64()
		if e != nil {
			return nil, bad("SUBSCRIPTION_BIND_ERROR", "原订阅整数超出SQLite绑定范围")
		}
		return n, nil
	}
	return nil, bad("SUBSCRIPTION_BIND_ERROR", "原订阅标量列不能绑定容器")
}
func sharedSubscriptionSQLValue(v any, kind string) (json.RawMessage, error) {
	if v == nil {
		return json.RawMessage("null"), nil
	}
	if kind == "JSON" {
		if s, ok := v.(string); ok {
			if !json.Valid([]byte(s)) {
				return nil, errors.New("invalid stored source JSON")
			}
			return json.RawMessage(s), nil
		}
	}
	if f, ok := v.(float64); ok && (math.IsInf(f, 0) || math.IsNaN(f)) {
		return nil, bad("SUBSCRIPTION_NONFINITE_JSON", "原订阅非有限数无法通过JSON协议表示")
	}
	return json.Marshal(v)
}
func createSharedSubscriptionProjection(ctx context.Context, tx *sql.Tx) error {
	parts := []string{"position INTEGER PRIMARY KEY", "native_id TEXT NOT NULL UNIQUE"}
	for _, c := range sharedSubscriptionColumns {
		definition := `"` + c.Name + `" ` + c.Type
		if c.Name == "name" || c.Name == "state" {
			definition += " NOT NULL"
		}
		parts = append(parts, definition)
	}
	if _, e := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS shared_subscription_projection (`+strings.Join(parts, ",")+`)`); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM shared_subscription_projection`); e != nil {
		return e
	}
	// Source predicates use these indexes with no invented ORDER BY. Selecting
	// complete rows, rather than only a covering index key, preserves that plan.
	for _, c := range []string{"name", "state", "tmdbid", "doubanid", "bangumiid", "anilistid", "mediaid", "media_source", "media_id", "username"} {
		if _, e := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS temp.shared_subscription_`+c+` ON shared_subscription_projection("`+c+`")`); e != nil {
			return e
		}
	}
	return nil
}
func insertSharedSubscriptionProjection(ctx context.Context, tx *sql.Tx, position int64, id string, row map[string]json.RawMessage, nonfinite map[string]string) error {
	marks := []string{"?", "?"}
	args := []any{position, id}
	for _, c := range sharedSubscriptionColumns {
		v, e := sharedSubscriptionBind(row[c.Name], c.Type)
		// A persisted SQLite INTEGER-affinity column can hold a REAL outside
		// int64. JSON may render that REAL without an exponent; rehydration is
		// not a new Python integer bind and must not reject the existing fact.
		if e != nil && c.Type == "INTEGER" {
			var number json.Number
			if json.Unmarshal(row[c.Name], &number) == nil {
				if f, err := number.Float64(); err == nil {
					v, e = f, nil
				}
			}
		}
		if value := nonfinite[c.Name]; value != "" && value != sharedSubscriptionNestedNonfinite {
			sign := 1
			if value == "-Inf" {
				sign = -1
			}
			v, e = math.Inf(sign), nil
		}
		if e != nil {
			return e
		}
		args = append(args, v)
		marks = append(marks, "?")
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO shared_subscription_projection VALUES (`+strings.Join(marks, ",")+`)`, args...)
	return e
}
func updateSharedSubscriptionProjection(ctx context.Context, tx *sql.Tx, id string, patch map[string]json.RawMessage) error {
	setters := []string{}
	args := []any{}
	for _, c := range sharedSubscriptionColumns {
		raw, ok := patch[c.Name]
		if !ok {
			continue
		}
		v, e := sharedSubscriptionBind(raw, c.Type)
		if e != nil {
			return e
		}
		setters = append(setters, `"`+c.Name+`"=?`)
		args = append(args, v)
	}
	if len(setters) == 0 {
		return nil
	}
	args = append(args, id)
	_, e := tx.ExecContext(ctx, `UPDATE shared_subscription_projection SET `+strings.Join(setters, ",")+` WHERE native_id=?`, args...)
	return e
}
func readSharedSubscriptionProjection(ctx context.Context, tx *sql.Tx, where string, args ...any) ([]string, []map[string]json.RawMessage, map[string]map[string]string, error) {
	rows, e := tx.QueryContext(ctx, `SELECT * FROM shared_subscription_projection`+where, args...)
	if e != nil {
		return nil, nil, nil, e
	}
	defer rows.Close()
	ids := []string{}
	out := []map[string]json.RawMessage{}
	nonfinite := map[string]map[string]string{}
	for rows.Next() {
		values := make([]any, len(sharedSubscriptionColumns)+2)
		ptrs := make([]any, len(values))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if e = rows.Scan(ptrs...); e != nil {
			return nil, nil, nil, e
		}
		id, ok := values[1].(string)
		if !ok {
			return nil, nil, nil, fmt.Errorf("invalid subscription identity")
		}
		row := map[string]json.RawMessage{"id": mustCleanSeedJSON(id)}
		for i, c := range sharedSubscriptionColumns {
			if f, ok := values[i+2].(float64); ok && math.IsInf(f, 0) {
				if nonfinite[id] == nil {
					nonfinite[id] = map[string]string{}
				}
				value := "+Inf"
				if math.IsInf(f, -1) {
					value = "-Inf"
				}
				nonfinite[id][c.Name] = value
				row[c.Name] = json.RawMessage("null")
				continue
			}
			row[c.Name], e = sharedSubscriptionSQLValue(values[i+2], c.Type)
			if e != nil {
				return nil, nil, nil, e
			}
			if c.Type == "JSON" {
				var nested bool
				row[c.Name], nested, e = sharedSubscriptionNormalizeJSON(row[c.Name])
				if e != nil {
					return nil, nil, nil, e
				}
				if nested {
					if nonfinite[id] == nil {
						nonfinite[id] = map[string]string{}
					}
					nonfinite[id][c.Name] = sharedSubscriptionNestedNonfinite
				}
			}
		}
		ids = append(ids, id)
		out = append(out, row)
	}
	return ids, out, nonfinite, rows.Err()
}
