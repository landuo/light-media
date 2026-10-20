package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
)

type subscribeGroupHandledValue struct {
	Revision   int64
	Entries    []json.RawMessage
	Strings    []string
	Container  any
	Appendable bool
}

func (v subscribeGroupHandledValue) contains(key string) bool {
	switch container := v.Container.(type) {
	case string:
		return strings.Contains(container, key)
	case map[string]any:
		_, found := container[key]
		return found
	default:
		return slices.Contains(v.Strings, key)
	}
}

// get_data(...) or [] is followed by Python membership before any type/media
// or append check. Old string/dictionary containers can therefore skip cleanly;
// reject their lack of append only if this event really changes a subscription.
func (p *PluginStore) subscribeGroupHandledTx(ctx context.Context, tx *sql.Tx, instance string) (subscribeGroupHandledValue, error) {
	valueState := subscribeGroupHandledValue{Entries: []json.RawMessage{}, Strings: []string{}, Appendable: true}
	var sealed []byte
	err := tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='history_handle'`, instance).Scan(&valueState.Revision, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return valueState, nil
	}
	if err != nil {
		return valueState, err
	}
	raw, err := p.unseal("data", instance, "history_handle", sealed)
	if err != nil {
		return valueState, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return valueState, bad("INVALID_PLUGIN_DATA", "订阅规则已处理历史不是有效JSON")
	}
	falsey := value == nil
	switch v := value.(type) {
	case bool:
		falsey = !v
	case string:
		falsey = v == ""
	case json.Number:
		n, parseErr := strconv.ParseFloat(string(v), 64)
		falsey = parseErr == nil && n == 0
	case map[string]any:
		falsey = len(v) == 0
	case []any:
		falsey = len(v) == 0
	}
	if falsey {
		return valueState, nil
	}
	switch value.(type) {
	case map[string]any, string:
		valueState.Container, valueState.Appendable = value, false
		return valueState, nil
	case []any:
	default:
		return valueState, bad("INVALID_PLUGIN_DATA", "订阅规则已处理历史不能执行成员判断")
	}
	if json.Unmarshal(raw, &valueState.Entries) != nil {
		return valueState, bad("INVALID_PLUGIN_DATA", "订阅规则已处理历史不是可追加的列表")
	}
	for _, entry := range valueState.Entries {
		// JSON null unmarshals into a string without error, but is not a Python
		// string and must not acquire membership in the handled set.
		if len(entry) == 0 || bytes.TrimSpace(entry)[0] != '"' {
			continue
		}
		var text string
		if json.Unmarshal(entry, &text) == nil {
			valueState.Strings = append(valueState.Strings, text)
		}
	}
	return valueState, nil
}

// applySubscribeGroupDownload accepts an already committed downloader fact,
// never a worker-supplied title, metadata object or subscription patch. All
// changed subscriptions, plugin KV and the recovery receipt commit together.
func (a *App) applySubscribeGroupDownload(ctx context.Context, run PluginRun, batchID string) (json.RawMessage, error) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	actual, config, err := a.subscribeGroupInvocation(ctx, run, "event")
	if err != nil {
		return nil, err
	}
	if config.Clear || config.ClearHandle {
		return nil, bad("PLUGIN_RULES_NOT_INITIALIZED", "订阅规则清理操作尚未完成初始化")
	}
	var invocation plugins.Invocation
	var input struct {
		BatchID string `json:"batch_id"`
	}
	if json.Unmarshal(actual.Input, &invocation) != nil || !pluginStrictJSON(invocation.Input, &input) || input.BatchID == "" || input.BatchID != batchID {
		return nil, bad("PLUGIN_BATCH_SCOPE_DENIED", "订阅规则只能处理当前投递批次")
	}
	if receipt, err := a.pluginStore.subscribeGroupReceipt(ctx, actual); err == nil {
		return receipt, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	page, err := a.pluginStore.pluginBatchEvents(ctx, actual, batchID, 0, 2)
	if err != nil {
		return nil, err
	}
	var handler string
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT handler_key FROM plugin_event_batches WHERE id=? AND run_id=?`, batchID, actual.ID).Scan(&handler); err != nil {
		return nil, err
	}
	if handler != "downloaded" || page.Total != 1 || len(page.Events) != 1 || !page.Complete || page.Events[0].Type != "download.accepted" {
		return nil, bad("PLUGIN_BATCH_SCOPE_DENIED", "订阅规则下载处理器只接受单个下载接受事实")
	}
	var snapshot downloadAddedSnapshot
	if config.Enabled && len(config.UpdateDetails) > 0 {
		snapshot, err = a.downloadAddedSnapshot(ctx, page.Events[0])
		if err != nil {
			return nil, err
		}
	}
	// This branch is independent of category rules and their in-memory cache.
	// Preserve the source's early exits before accessing its handled/history KV.
	m := subscribegroup.New()
	m.Config = config
	applicable := config.Enabled && len(config.UpdateDetails) > 0 && snapshot.Hash != "" && snapshot.HasContext && snapshot.HasHistory
	// Submission holds downloadMu from its initial intent read through Add and
	// acceptance. Finish that selected operation before advancing subscription
	// revisions, rather than invalidating its live context or overwriting it
	// later from an old in-memory intent. Keep the existing settings lock order.
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	a.subMu.Lock()
	defer a.subMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = a.pluginStore.guardSubscribeGroupRunTx(ctx, tx, actual); err != nil {
		return nil, err
	}
	handled := subscribeGroupHandledValue{}
	if applicable {
		handled, err = a.pluginStore.subscribeGroupHandledTx(ctx, tx, actual.InstanceID)
		if err != nil {
			return nil, err
		}
		m.Handled = handled.Strings
	}
	previousHandled := len(m.Handled)
	current := map[string]*Subscription{}
	projections := []subscribegroup.Subscription{}
	mediaType := snapshot.MediaType
	if mediaType == "tv" {
		mediaType = "电视剧"
	} else if mediaType == "movie" {
		mediaType = "电影"
	}
	alreadyHandled := handled.contains(mediaType + ":" + strconv.FormatInt(snapshot.TMDBID, 10))
	if applicable && snapshot.MediaType == "tv" && !alreadyHandled {
		// Source list_by_tmdbid returns rows in their stored identity order.
		// records is a rowid table and core UPSERT preserves that insertion order.
		rows, queryErr := tx.QueryContext(ctx, `SELECT id,payload FROM records WHERE kind='subscription' ORDER BY rowid`)
		if queryErr != nil {
			return nil, queryErr
		}
		for rows.Next() {
			var key string
			var raw []byte
			if err = rows.Scan(&key, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			var sub Subscription
			if json.Unmarshal(raw, &sub) != nil || sub.ID != key {
				rows.Close()
				return nil, bad("REVIEW_PLUGIN_SUBSCRIPTION", "订阅记录身份无法读取")
			}
			if sub.Media.ID != snapshot.TMDBID || sub.Media.MediaType != "tv" || sub.State == "cancelled" {
				continue
			}
			current[sub.ID] = &sub
			projections = append(projections, subscribeGroupProjection(sub))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	location, zoneErr := time.LoadLocation(identifiersTimezone())
	if zoneErr != nil {
		location = shanghai()
	}
	if !alreadyHandled {
		if err = m.DownloadAdded(subscribegroup.Download{
			Hash: snapshot.Hash, Type: mediaType, TMDBID: strconv.FormatInt(snapshot.TMDBID, 10),
			Seasons: snapshot.Seasons, SiteID: snapshot.SiteID, Metadata: snapshot.Metadata,
			HasContext: snapshot.HasContext, HasHistory: snapshot.HasHistory,
		}, projections, a.Settings().RSSSites, time.Now().In(location).Format("2006-01-02 15:04:05")); err != nil {
			return nil, bad("INVALID_PLUGIN_DOWNLOAD_SEASON", "下载历史的季编号无法读取")
		}
	}
	if len(m.Updates) > 0 && !handled.Appendable {
		return nil, bad("INVALID_PLUGIN_DATA", "订阅规则已处理历史不是可追加的列表")
	}
	for _, update := range m.Updates {
		sub, ok := current[update.ID]
		if !ok {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "订阅规则不能修改下载影视之外的订阅")
		}
		before, _ := json.Marshal(sub)
		if err = applySubscribeGroupFields(sub, update.Fields); err != nil {
			return nil, err
		}
		after, _ := json.Marshal(sub)
		if !bytes.Equal(before, after) {
			if err = a.advancePlannedQualityRevisionTx(ctx, tx, *sub); err != nil {
				return nil, err
			}
			if err = advanceSubscribeGroupRevisionTx(ctx, tx, sub); err != nil {
				return nil, err
			}
		}
		if err = txPut(ctx, tx, "subscription", sub.ID, sub); err != nil {
			return nil, err
		}
	}
	if len(m.History) > 0 {
		history, revision, err := a.pluginStore.subscribeGroupHistoryTx(ctx, tx, actual.InstanceID)
		if err != nil {
			return nil, err
		}
		for _, entry := range m.History {
			raw, _ := json.Marshal(entry)
			history = append(history, raw)
		}
		raw, err := pluginJSONLimit(history, false, pluginDataValueLimit)
		if err != nil {
			return nil, err
		}
		if _, err = a.pluginStore.saveValueTx(ctx, tx, actual.InstanceID, "data", "history", revision, raw); err != nil {
			return nil, err
		}
		for _, text := range m.Handled[previousHandled:] {
			raw, _ := json.Marshal(text)
			handled.Entries = append(handled.Entries, raw)
		}
		raw, err = pluginJSONLimit(handled.Entries, false, pluginDataValueLimit)
		if err != nil {
			return nil, err
		}
		if _, err = a.pluginStore.saveValueTx(ctx, tx, actual.InstanceID, "data", "history_handle", handled.Revision, raw); err != nil {
			return nil, err
		}
	}
	result, _ := json.Marshal(map[string]any{"updated": len(m.Updates), "history_added": len(m.History), "handled_added": len(m.Handled) - previousHandled})
	if err = a.pluginStore.recordSubscribeGroupReceiptTx(ctx, tx, actual, result); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
