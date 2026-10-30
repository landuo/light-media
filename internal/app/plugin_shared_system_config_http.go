package app

import (
	"context"
	"encoding/json"
	"net/http"
)

// One existing-source words card, not a general settings proxy. The authenticated
// router handles its normal session/origin checks before reaching this method.
func (a *App) sharedSystemConfigWordsAPI(w http.ResponseWriter, r *http.Request) (any, error) {
	if r.Method == http.MethodGet {
		a.recognitionMu.Lock()
		defer a.recognitionMu.Unlock()
		_, raw, e := a.readSharedSystemConfigLocked(r.Context(), a.Store.DB)
		if e != nil {
			return nil, e
		}
		raw, e = sharedSystemConfigUIJSON(raw)
		if e != nil {
			return nil, e
		}
		return map[string]any{"value": raw}, nil
	}
	if r.Method != http.MethodPut {
		return nil, bad("METHOD_NOT_ALLOWED", "不支持此操作")
	}
	var in struct {
		Text json.RawMessage `json:"text"`
	}
	if e := decodeBodyWithLimit(w, r, &in, pluginDataValueLimit+(2<<20)); e != nil {
		return nil, e
	}
	text, _, e := sharedSystemConfigString(in.Text)
	if e != nil {
		return nil, bad("INVALID_RECOGNITION_WORDS", "缺少有效识别词文本")
	}
	// The source frontend splits UTF-16 text; the endpoint drops empty lines.
	rules := []json.RawMessage{}
	start := 0
	for i, r := range text {
		if r == '\n' {
			if i > start {
				rules = append(rules, sharedSystemConfigQuote(text[start:i]))
			}
			start = i + 1
		}
	}
	if len(text) > start {
		rules = append(rules, sharedSystemConfigQuote(text[start:]))
	}
	raw := json.RawMessage(`null`)
	if len(rules) > 0 {
		raw, _ = json.Marshal(rules)
	}

	changed, e := a.setSharedSystemConfigFromUI(r.Context(), raw)
	if e != nil {
		return nil, e
	}
	if changed != nil && *changed {
		event, _ := json.Marshal(map[string]any{"key": "CustomIdentifiers", "value": raw, "change_type": "update"})
		a.broadcastOriginalPluginEvent(r.Context(), "config.updated", event)
	}
	return map[string]bool{"success": true}, nil
}
func (a *App) setSharedSystemConfigFromUI(ctx context.Context, raw json.RawMessage) (*bool, error) {
	a.sharedSystemConfig.uiMu.Lock()
	defer a.sharedSystemConfig.uiMu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	// A completed SELECT snapshot is not held in a transaction or recognitionMu;
	// source synchronous setters may run before this async mutation/publication.
	_, snapshot, e := a.readSharedSystemConfigRows(ctx, a.Store.DB)
	if e != nil {
		return nil, e
	}
	if len(snapshot) > 1 {
		return nil, bad("SYSTEM_CONFIG_MULTIPLE_RESULTS", "同一全局配置存在多行，异步查询不能选定唯一行")
	}
	if len(snapshot) == 1 && sharedSystemConfigEqual(snapshot[0].Value, raw) {
		a.recognitionMu.Lock()
		a.publishSharedSystemConfigValueLocked(raw)
		a.recognitionMu.Unlock()
		return nil, nil
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	observed, current, e := a.readSharedSystemConfigRows(ctx, tx)
	if e != nil {
		return nil, e
	}
	changed := true
	if len(snapshot) == 0 {
		value, e := sharedSystemConfigBoundPersisted(ctx, tx, raw)
		if e != nil {
			return nil, e
		}
		current = append(current, sharedSystemConfigSourceRow{ID: sharedSystemConfigNextRowID(current), Value: value})
	} else {
		index := -1
		for i, row := range current {
			if row.ID == snapshot[0].ID {
				index = i
				break
			}
		}
		if !originalBaseJSONTruthy(raw) {
			// Source async_delete re-queries by identity, and is successful if missing.
			if index >= 0 {
				current = append(current[:index], current[index+1:]...)
			} else {
				changed = false
			}
		} else {
			if index < 0 {
				return nil, bad("SYSTEM_CONFIG_STALE_DATA", "异步更新原全局配置行已不存在")
			}
			value, e := sharedSystemConfigBoundPersisted(ctx, tx, raw)
			if e != nil {
				return nil, e
			}
			current[index].Value = value
		}
	}
	after := observed
	if changed {
		after, e = a.persistSharedSystemConfigRows(ctx, tx, observed, current)
		if e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	a.recognitionMu.Lock()
	a.sharedSystemConfig.observed = after
	a.publishSharedSystemConfigValueLocked(raw)
	a.recognitionMu.Unlock()
	yes := true
	return &yes, nil
}
