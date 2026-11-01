package downloadercommands

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
)

// QBTagRows owns the actual decoded list. Filtering finally clears this
// container, not a private cloned slice with an unchanged caller-visible length.
type QBTagRows struct{ Values []any }
type QBTagPrimitives interface {
	Info(context.Context, any) (*QBTagRows, error)
	RemoveTags(context.Context, any, any) (any, error)
	DeleteTags(context.Context, any) (any, error)
	Sleep(context.Context, time.Duration) error
}
type QBTagScopePort interface {
	WithTagScope(context.Context, func(context.Context, QBTagPrimitives) (any, error)) (any, error)
}
type QBTagCommands struct {
	QBittorrent
	Sleep func(context.Context, time.Duration) error // Only the no-client path.
}

func qbTagSleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// Only directly classified source exceptions enter the source catch. Wrapped
// authority/unknown-effect errors must not become False or a retried query.
func (q QBTagCommands) catchSource(ctx context.Context, prefix string, err error) error {
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	source, ok := err.(*SourceException)
	if !ok {
		return err
	}
	if q.Log != nil {
		q.Log(ctx, "ERROR", prefix+source.Message)
	}
	return nil
}
func (q QBTagCommands) withScope(ctx context.Context, fn func(context.Context, QBTagPrimitives) (any, error)) (any, error) {
	port, ok := q.Client.(QBTagScopePort)
	if !ok {
		return nil, &BoundaryError{"configured qB client lacks the fixed aggregate tag scope"}
	}
	called := false
	result, err := port.WithTagScope(ctx, func(c context.Context, ops QBTagPrimitives) (any, error) {
		if called {
			return nil, &BoundaryError{"aggregate tag callback repeated"}
		}
		called = true
		if c == nil || ops == nil {
			return nil, &BoundaryError{"invalid aggregate tag context or primitives"}
		}
		if err := c.Err(); err != nil {
			return nil, err
		}
		return fn(c, ops)
	})
	if err == nil && !called {
		return nil, &BoundaryError{"aggregate tag callback was not invoked"}
	}
	return result, err
}
func (q QBTagCommands) deleteInside(ctx context.Context, ops QBTagPrimitives, ids, tag any) (bool, error) {
	_, err := ops.RemoveTags(ctx, ids, tag)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return false, q.catchSource(ctx, "删除种子Tag出错：", err)
	}
	_, err = ops.DeleteTags(ctx, tag)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return false, q.catchSource(ctx, "删除种子Tag出错：", err)
	}
	return true, nil
}
func (q QBTagCommands) DeleteTorrentsTag(ctx context.Context, ids, tag any) (bool, error) {
	if err := fourCommandReady(ctx, ids, tag); err != nil {
		return false, err
	}
	if q.Client == nil {
		return false, nil
	}
	result, err := q.withScope(ctx, func(c context.Context, ops QBTagPrimitives) (any, error) { return q.deleteInside(c, ops, ids, tag) })
	if err != nil {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	value, ok := result.(bool)
	if !ok {
		return false, &BoundaryError{"aggregate tag delete lost source boolean"}
	}
	return value, nil
}

func qbTagType(v any) string {
	switch x := v.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case string:
		return "str"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return "float"
		}
		return "int"
	}
	return "unsupported"
}
func qbTagAttribute(value any, attribute string) error {
	return &SourceException{Class: "AttributeError", Message: "'" + qbTagType(value) + "' object has no attribute '" + attribute + "'"}
}
func qbTagRequested(tags any) ([]any, error) {
	if values, ok := tags.([]any); ok {
		return values, nil
	}
	if text, ok := tags.(string); ok {
		parts := strings.Split(text, ",")
		values := make([]any, len(parts))
		for i, v := range parts {
			values[i] = v
		}
		return values, nil
	}
	return nil, qbTagAttribute(tags, "split")
}
func qbTagSubset(requested []any, returned string) (bool, error) {
	available := map[string]bool{}
	for _, part := range strings.Split(returned, ",") {
		available[strings.TrimFunc(part, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })] = true
	}
	matched := true
	for _, item := range requested {
		switch item.(type) {
		case []any, map[string]any:
			return false, &SourceException{Class: "TypeError", Message: "unhashable type: '" + qbTagType(item) + "'"}
		}
		text, ok := item.(string)
		if !ok || !available[text] {
			matched = false
		}
	}
	return matched, nil
}

func (q QBTagCommands) filteredRows(ctx context.Context, ops QBTagPrimitives, tags, status any) ([]any, error) {
	rows, err := ops.Info(ctx, status)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, q.catchSource(ctx, "获取种子列表出错：", err)
	}
	if rows == nil {
		return nil, &BoundaryError{"qB tag info did not return an owned list container"}
	}
	if !truth(tags) {
		return rows.Values, nil
	}
	requested, err := qbTagRequested(tags)
	if err != nil {
		return nil, q.catchSource(ctx, "获取种子列表出错：", err)
	}
	// The source's finally starts AFTER requested tags.split, visits the whole
	// info list, and clears it even if a later row invalidates an earlier match.
	defer func() { rows.Values = []any{} }()
	results := []any{}
	for _, value := range rows.Values {
		row, ok := value.(map[string]any)
		if !ok {
			return nil, q.catchSource(ctx, "获取种子列表出错：", qbTagAttribute(value, "get"))
		}
		text, ok := row["tags"].(string)
		if !ok {
			return nil, q.catchSource(ctx, "获取种子列表出错：", qbTagAttribute(row["tags"], "split"))
		}
		matches, err := qbTagSubset(requested, text)
		if err != nil {
			return nil, q.catchSource(ctx, "获取种子列表出错：", err)
		}
		if matches {
			results = append(results, row)
		}
	}
	return results, nil
}

func (q QBTagCommands) pollInside(ctx context.Context, ops QBTagPrimitives, tags, status any) (any, error) {
	sleep := q.Sleep
	if ops != nil {
		sleep = ops.Sleep
	} else if sleep == nil {
		sleep = qbTagSleep
	}
	for i := 1; i < 10; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := sleep(ctx, 3*time.Second); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if ops == nil {
			continue
		} // Original absent-client get_torrents returns [],True.
		rows, err := q.filteredRows(ctx, ops, tags, status)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			continue
		}
		// This .get is OUTSIDE the original private helper try, hence escapes.
		first, ok := rows[0].(map[string]any)
		if !ok {
			return nil, qbTagAttribute(rows[0], "get")
		}
		id := first["hash"]
		if id == nil {
			continue
		}
		// Never call public DeleteTorrentsTag here: it would open another scope.
		// Source False from classified cleanup failure is ignored, not errors.
		if _, err = q.deleteInside(ctx, ops, id, tags); err != nil {
			return nil, err
		}
		return id, nil
	}
	return nil, nil
}
func (q QBTagCommands) GetTorrentIDByTag(ctx context.Context, tags, status any) (any, error) {
	if err := fourCommandReady(ctx, tags, status); err != nil {
		return nil, err
	}
	if q.Client == nil {
		return q.pollInside(ctx, nil, tags, status)
	}
	result, err := q.withScope(ctx, func(c context.Context, ops QBTagPrimitives) (any, error) { return q.pollInside(c, ops, tags, status) })
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
