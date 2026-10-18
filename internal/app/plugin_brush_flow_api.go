package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
	"strconv"
	"strings"
	"time"
)

func brushFlowReadRun(r PluginRun) bool {
	q, e := brushFlowRequest(r)
	return e == nil && q.Method == "GET" && (q.Key == "status" || q.Key == "detail")
}
func brushFlowRequest(r PluginRun) (plugins.HTTPRequest, error) {
	var inv plugins.Invocation
	var q plugins.HTTPRequest
	if r.Action != "api" || cleanupJournalStrict(r.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &q) != nil || inv.Action != r.Action || q.RequestID == "" || len(q.RequestID) > 128 {
		return q, bad("BRUSH_FLOW_SCOPE_DENIED", "站点刷流HTTP来源无效")
	}
	return q, nil
}
func brushFlowDeferredSpec(run PluginRun) (string, json.RawMessage, error) {
	var inv plugins.Invocation
	if cleanupJournalStrict(run.Input, &inv) != nil || inv.Action != run.Action {
		return "", nil, brushFlowEvidenceError()
	}
	switch run.Action {
	case "init", "reload":
		return bf.ConfigureCapability, json.RawMessage(`{}`), nil
	case "run":
		if len(inv.Input) == 0 || bytes.Equal(bytes.TrimSpace(inv.Input), []byte("null")) || bytes.Equal(bytes.TrimSpace(inv.Input), []byte("{}")) {
			return bf.TaskCapability, json.RawMessage(`{}`), nil
		}
	case "task", "service":
		var token struct {
			ID string `json:"event_id"`
		}
		if cleanupJournalStrict(inv.Input, &token) == nil && pluginStorageID.MatchString(token.ID) {
			return bf.TaskCapability, inv.Input, nil
		}
	case "api":
		q, e := brushFlowRequest(run)
		if e != nil {
			return "", nil, e
		}
		if brushFlowReadRun(run) {
			return "", nil, nil
		}
		allowed := q.Method == "POST" && (q.Key == "settings" || q.Key == "create" || q.Key == "state" || q.Key == "run" || q.Key == "check" || q.Key == "clear") || q.Method == "PUT" && q.Key == "update" || q.Method == "DELETE" && q.Key == "delete"
		if allowed {
			return bf.APICapability, mustCleanSeedJSON(map[string]string{"request_id": q.RequestID}), nil
		}
	}
	return "", nil, bad("BRUSH_FLOW_SCOPE_DENIED", "站点刷流执行来源未绑定")
}
func brushFlowWorkerInput(run PluginRun) (json.RawMessage, error) {
	var inv plugins.Invocation
	if cleanupJournalStrict(run.Input, &inv) != nil {
		return nil, brushFlowEvidenceError()
	}
	inv.Config = json.RawMessage(`{}`)
	if run.Action == "api" {
		q, e := brushFlowRequest(run)
		if e != nil {
			return nil, e
		}
		inv.Input = mustCleanSeedJSON(map[string]any{"request_id": q.RequestID, "read": brushFlowReadRun(run)})
	}
	return json.Marshal(inv)
}
func brushFlowHTTPResponse(v bf.Response) json.RawMessage {
	return mustCleanSeedJSON(plugins.HTTPResponse{Status: 200, Body: mustCleanSeedJSON(v)})
}
func brushFlowValidation(field string) json.RawMessage {
	return mustCleanSeedJSON(plugins.HTTPResponse{Status: 422, Body: mustCleanSeedJSON(map[string]any{"detail": []any{map[string]any{"loc": []any{"body", field}, "msg": "Invalid value", "type": "value_error"}}})})
}
func brushFlowBody(raw []byte) (bf.Row, error) {
	var r bf.Row
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&r) != nil || r == nil || d.Decode(new(any)) != io.EOF {
		return nil, bad("INVALID_PLUGIN_PARAMETER", "请求正文必须为对象")
	}
	return r, nil
}
func brushFlowDetailQuery(q map[string][]string) (string, int, int, error) {
	state, page, size := "active", 1, 50
	var e error
	if v, ok := q["state"]; ok && len(v) > 0 {
		state = plugins.LastQuery(q, "state")
	}
	if state != "active" && state != "deleted" && state != "all" {
		return "", 0, 0, bad("INVALID_PLUGIN_PARAMETER", "无效的种子状态")
	}
	if v, ok := q["page"]; ok && len(v) > 0 {
		page, e = strconv.Atoi(plugins.LastQuery(q, "page"))
		if e != nil {
			return "", 0, 0, e
		}
	}
	if v, ok := q["page_size"]; ok && len(v) > 0 {
		size, e = strconv.Atoi(plugins.LastQuery(q, "page_size"))
		if e != nil {
			return "", 0, 0, e
		}
	}
	if page < 1 || size < 10 || size > 200 {
		return "", 0, 0, bad("INVALID_PLUGIN_PARAMETER", "分页参数超出范围")
	}
	return state, page, size, nil
}
func (a *App) brushFlowReadAPI(ctx context.Context, in PluginInstallation, run PluginRun, raw json.RawMessage) (json.RawMessage, error) {
	var token struct {
		RequestID string `json:"request_id"`
	}
	if cleanupJournalStrict(raw, &token) != nil {
		return nil, brushFlowEvidenceError()
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
	if !brushFlowReadRun(run) || req.RequestID != token.RequestID {
		return nil, brushFlowEvidenceError()
	}
	if h := a.pluginBrushFlow.beforeRead; h != nil {
		h(ctx)
	}
	s, _, e := a.pluginStore.brushFlowRead(ctx, tx, in.InstanceID, in.Generation)
	if e != nil {
		return nil, e
	}
	value := bf.DefaultConfig()
	if len(s.Config) > 0 && string(s.Config) != "null" {
		value, e = brushFlowConfig(s.Config)
		if e != nil {
			return nil, e
		}
	}
	cfg, e := bf.LoadApplied(value)
	if e != nil {
		return nil, e
	}
	data := map[string]json.RawMessage{}
	for k := range s.Values {
		var sealed []byte
		if e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, in.InstanceID, k).Scan(&sealed); e != nil {
			return nil, e
		}
		data[k], e = a.pluginStore.unseal("data", in.InstanceID, k, sealed)
		if e != nil {
			return nil, e
		}
	}
	sites, e := a.brushFlowSitesTx(ctx, tx)
	if e != nil {
		return nil, e
	}
	users, e := a.brushFlowUsersTx(ctx, tx)
	if e != nil {
		return nil, e
	}
	ds := brushFlowDownloaderOptions(a.Settings())
	runtime := map[string]bf.Row{}
	for _, source := range s.Sources {
		var state string
		if e = tx.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, source.Run).Scan(&state); e != nil {
			return nil, e
		}
		if state == "pending" || state == "running" {
			runtime[source.TaskID] = bf.Row{"state": "queued", "operation": source.Operation, "last_error": nil}
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	host := brushFlowReadHost{a: a, run: run, data: data, sites: sites, users: users, downloaders: ds}
	core := bf.NewReadProjection(&host, cfg)
	for id, r := range runtime {
		core.SetRuntime(id, r)
	}
	var out any
	if req.Key == "status" {
		out, e = core.Status(ctx)
	} else {
		id := req.Params["task_id"]
		if id == "" || len(id) > 128 || strings.ContainsAny(id, "/\\\x00\r\n") {
			return nil, brushFlowEvidenceError()
		}
		found := false
		for _, t := range cfg.Tasks {
			if t.ID() == id {
				found = true
			}
		}
		if !found {
			return brushFlowHTTPResponse(bf.Response{Message: "刷流任务不存在"}), nil
		}
		state, page, size, e := brushFlowDetailQuery(req.Query)
		if e != nil {
			return brushFlowValidation("query"), nil
		}
		out, e = core.Detail(ctx, id, state, page, size)
		if e != nil {
			return nil, e
		}
	}
	if e != nil {
		return nil, e
	}
	return brushFlowHTTPResponse(bf.Response{Success: true, Data: out}), nil
}

type brushFlowReadHost struct {
	bf.Host
	a           *App
	run         PluginRun
	data        map[string]json.RawMessage
	sites       []bf.Site
	users       []bf.UserData
	downloaders []bf.Downloader
}

func (h *brushFlowReadHost) Get(ctx context.Context, key string) (any, error) {
	if e := h.a.validatePluginHTTPRun(ctx, h.run); e != nil {
		return nil, e
	}
	raw := h.data[key]
	if len(raw) == 0 {
		return nil, nil
	}
	return brushFlowDecodeValue(raw)
}
func (h *brushFlowReadHost) Now() time.Time                                   { var o pluginBrushFlowOwner; o.a = h.a; return o.Now() }
func (h *brushFlowReadHost) Sites(context.Context) ([]bf.Site, error)         { return h.sites, nil }
func (h *brushFlowReadHost) SiteUsers(context.Context) ([]bf.UserData, error) { return h.users, nil }
func (h *brushFlowReadHost) Downloaders(context.Context) ([]bf.Downloader, error) {
	return h.downloaders, nil
}
