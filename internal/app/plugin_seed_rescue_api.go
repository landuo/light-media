package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"light-media/internal/plugins"
	sr "light-media/internal/plugins/seedrescue"
)

func seedRescueReadRun(r PluginRun) bool {
	request, e := seedRescueRequest(r)
	return e == nil && request.Method == "GET" && (request.Key == "status" || request.Key == "job")
}
func seedRescueRequest(r PluginRun) (plugins.HTTPRequest, error) {
	var inv plugins.Invocation
	var req plugins.HTTPRequest
	if r.Action != "api" || cleanupJournalStrict(r.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &req) != nil || inv.Action != r.Action || req.RequestID == "" || len(req.RequestID) > 128 {
		return req, bad("SEED_RESCUE_SCOPE_DENIED", "稀缺保种HTTP来源无效")
	}
	return req, nil
}
func seedRescueDeferredSpec(run PluginRun) (string, json.RawMessage, error) {
	var inv plugins.Invocation
	if cleanupJournalStrict(run.Input, &inv) != nil || inv.Action != run.Action {
		return "", nil, bad("INVALID_PLUGIN_RUN", "稀缺保种执行快照无效")
	}
	switch run.Action {
	case "init", "reload":
		return sr.ConfigureCapability, json.RawMessage(`{}`), nil
	case "run":
		if len(inv.Input) == 0 || bytes.Equal(bytes.TrimSpace(inv.Input), []byte("null")) || bytes.Equal(bytes.TrimSpace(inv.Input), []byte("{}")) {
			return sr.ScanCapability, json.RawMessage(`{}`), nil
		}
	case "scan", "once", "service":
		var token struct {
			ID string `json:"event_id"`
		}
		if cleanupJournalStrict(inv.Input, &token) == nil && pluginStorageID.MatchString(token.ID) {
			return sr.ScanCapability, inv.Input, nil
		}
	case "api":
		req, e := seedRescueRequest(run)
		if e != nil {
			return "", nil, e
		}
		if seedRescueReadRun(run) {
			return "", nil, nil
		}
		if req.Method == "POST" && (req.Key == "scan" || req.Key == "download" || req.Key == "retry" || req.Key == "ignore") {
			return sr.APICapability, mustCleanSeedJSON(map[string]string{"request_id": req.RequestID}), nil
		}
	}
	return "", nil, bad("SEED_RESCUE_SCOPE_DENIED", "稀缺保种执行来源未绑定")
}
func seedRescueWorkerInput(run PluginRun) (json.RawMessage, error) {
	var inv plugins.Invocation
	if cleanupJournalStrict(run.Input, &inv) != nil {
		return nil, bad("INVALID_PLUGIN_RUN", "稀缺保种执行输入无效")
	}
	inv.Config = json.RawMessage(`{}`)
	if run.Action == "api" {
		req, e := seedRescueRequest(run)
		if e != nil {
			return nil, e
		}
		inv.Input = mustCleanSeedJSON(map[string]any{"request_id": req.RequestID, "read": seedRescueReadRun(run)})
	}
	return json.Marshal(inv)
}
func seedRescueHTTPResponse(r sr.Response) json.RawMessage {
	body := mustCleanSeedJSON(r)
	return mustCleanSeedJSON(plugins.HTTPResponse{Status: 200, Body: body})
}
func seedRescueStatusFilter(q map[string][]string) (sr.StatusFilter, error) {
	f := sr.StatusFilter{View: "current", Page: 1, PageSize: 20}
	last := func(k string) string { return plugins.LastQuery(q, k) }
	if s, ok := q["view"]; ok && len(s) > 0 {
		f.View = last("view")
		if f.View == "" {
			return f, bad("INVALID_PLUGIN_PARAMETER", "无效的 RSS 列表类型")
		}
	}
	f.State = last("state")
	f.Promotion = last("promotion")
	f.SourcePromotion = last("source_promotion")
	for key, target := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
		if v := last(key); v != "" {
			n, e := strconv.Atoi(v)
			if e != nil {
				return f, bad("INVALID_PLUGIN_PARAMETER", "分页参数必须为整数")
			}
			*target = n
		}
	}
	for key, target := range map[string]**int{"source_site_id": &f.SourceSiteID, "min_seeders": &f.MinSeeders, "max_seeders": &f.MaxSeeders} {
		if v := last(key); v != "" {
			n, e := strconv.Atoi(v)
			if e != nil {
				return f, bad("INVALID_PLUGIN_PARAMETER", "筛选参数必须为整数")
			}
			*target = &n
		}
	}
	return f, nil
}

// Read API views use one verified SQLite snapshot and no mutation-owner wait.
func (a *App) seedRescueReadAPI(ctx context.Context, in PluginInstallation, run PluginRun, raw json.RawMessage) (json.RawMessage, error) {
	var token struct {
		RequestID string `json:"request_id"`
	}
	if cleanupJournalStrict(raw, &token) != nil {
		return nil, bad("SEED_RESCUE_SCOPE_DENIED", "稀缺保种只读权限无效")
	}
	tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	req, e := a.validatePluginHTTPRunTx(ctx, tx, run)
	if e != nil {
		return nil, e
	}
	if !seedRescueReadRun(run) || req.RequestID != token.RequestID {
		return nil, bad("SEED_RESCUE_SCOPE_DENIED", "稀缺保种只读路由不匹配")
	}
	return a.seedRescueReadProjectionTx(ctx, tx, in, run, req, nil, nil)
}

// Private data decoding is shared by normal API reads and the common trusted
// configuration view. The caller authenticates its scope in this same TX.
func (a *App) seedRescueReadProjectionTx(ctx context.Context, tx *sql.Tx, in PluginInstallation, run PluginRun, req plugins.HTTPRequest, configOverride json.RawMessage, guard func(context.Context) error) (json.RawMessage, error) {
	if hook := a.pluginSeedRescue.beforeRead; hook != nil {
		hook(ctx)
	}
	state, _, e := a.pluginStore.seedRescueRead(ctx, tx, in.InstanceID, in.Generation)
	if e != nil {
		return nil, e
	}
	// A common read session has already authenticated configuration presence or
	// absence in this same transaction. Legacy execution reads retain their
	// original persisted-config requirement.
	if guard == nil {
		var current PluginStoredValue
		var sealed []byte
		if e = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, in.InstanceID).Scan(&current.Revision, &sealed); e != nil {
			return nil, e
		}
		current.Value, e = a.pluginStore.unseal("config", in.InstanceID, "main", sealed)
		if e != nil {
			return nil, e
		}
	}
	effective := state.Config
	if guard != nil || len(configOverride) != 0 {
		effective = configOverride
	}
	if len(effective) == 0 || string(effective) == "null" {
		effective = mustCleanSeedJSON(sr.ParseConfig(sr.Object{}).Current())
	}
	config, e := seedRescueConfig(effective)
	if e != nil {
		return nil, e
	}
	snapshot := map[string]json.RawMessage{}
	for key := range state.Values {
		var cell []byte
		if e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, in.InstanceID, key).Scan(&cell); e != nil {
			return nil, e
		}
		snapshot[key], e = a.pluginStore.unseal("data", in.InstanceID, key, cell)
		if e != nil {
			return nil, e
		}
	}
	options, e := a.seedRescueOptionsTx(ctx, tx, in.InstanceID)
	if e != nil {
		return nil, e
	}
	pending, e := seedRescueHasPendingSource(ctx, tx, state, "")
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	status := sr.Object{"state": "idle"}
	if b := snapshot[sr.ScanStatusKey]; len(b) > 0 {
		_ = json.Unmarshal(b, &status)
	}
	busy := pending
	a.pluginSeedRescue.mu.Lock()
	if owner := a.pluginSeedRescue.owners[in.InstanceID+":"+strconv.FormatInt(in.Generation, 10)]; owner != nil {
		busy = busy || len(owner.gate) > 0
	}
	a.pluginSeedRescue.mu.Unlock()
	host := seedRescueReadHost{a: a, run: run, data: snapshot, options: options, guard: guard}
	m := sr.NewReadProjection(ctx, &host, config, status, busy)
	var result sr.Response
	if req.Key == "status" {
		if values, ok := req.Query["view"]; ok && len(values) > 0 && plugins.LastQuery(req.Query, "view") == "" {
			msg := "无效的 RSS 列表类型"
			return seedRescueHTTPResponse(sr.Response{Message: &msg}), nil
		}
		filter, e := seedRescueStatusFilter(req.Query)
		if e != nil {
			return seedRescueValidationResponse("query", "integer"), nil
		}
		result, e = m.GetStatus(ctx, filter)
		if e != nil {
			return nil, e
		}
	} else {
		hash := req.Params["target_hash"]
		if len(hash) > 128 || strings.ContainsAny(hash, "/\\\x00\r\n") {
			return nil, bad("INVALID_PLUGIN_PARAMETER", "种子Hash参数无效")
		}
		result, e = m.GetJob(ctx, hash)
		if e != nil {
			return nil, e
		}
	}
	return seedRescueHTTPResponse(result), nil
}

type seedRescueReadHost struct {
	sr.Host
	a       *App
	run     PluginRun
	data    map[string]json.RawMessage
	options sr.Object
	guard   func(context.Context) error
}

func (h *seedRescueReadHost) Guard(ctx context.Context) error {
	if h.guard != nil {
		return h.guard(ctx)
	}
	return h.a.validatePluginHTTPRun(ctx, h.run)
}
func (h *seedRescueReadHost) LoadData(ctx context.Context, key string) (json.RawMessage, error) {
	if e := h.Guard(ctx); e != nil {
		return nil, e
	}
	return append(json.RawMessage(nil), h.data[key]...), nil
}
func (h *seedRescueReadHost) Options(ctx context.Context) (sr.Object, error) {
	if e := h.Guard(ctx); e != nil {
		return nil, e
	}
	return h.options, nil
}
func seedRescueStrictBody(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return bad("INVALID_PLUGIN_PARAMETER", "稀缺保种请求参数无效")
	}
	return nil
}
