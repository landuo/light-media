package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
)

// Decode BrushFlow's existing private history into its original read model.
// Instance identity, mode and configuration are supplied by the common session.
// Genesis is already authenticated by install; absence/corruption is never
// treated as a new empty owner and this reader never initializes business state.
func (a *App) brushFlowReadInstanceConfiguration(ctx context.Context, view pluginFormView, req plugins.HTTPRequest) (json.RawMessage, error) {
	in := view.installation
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = a.pluginStore.validatePluginInstanceReadTx(ctx, tx, in); err != nil {
		return nil, err
	}
	if h := a.pluginBrushFlow.beforeRead; h != nil {
		h(ctx)
	}
	state, _, err := a.pluginStore.brushFlowReadCurrent(ctx, tx, in.InstanceID, in.Generation)
	if err != nil {
		return nil, err
	}
	value, err := brushFlowConfig(view.durable.Value)
	if err != nil {
		return nil, err
	}
	cfg, err := bf.LoadApplied(value)
	if err != nil {
		return nil, err
	}
	data := map[string]json.RawMessage{}
	for key := range state.allValues() {
		raw, _, e := a.pluginStore.brushFlowValueProof(ctx, tx, state, key)
		if e != nil {
			return nil, e
		}
		data[key] = raw
	}
	sites, err := a.brushFlowSitesTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	users, err := a.brushFlowUsersTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	runtime := map[string]bf.Row{}
	for _, source := range state.pendingSources(ctx, tx) {
		var status string
		if err = tx.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, source.Run).Scan(&status); err != nil {
			return nil, err
		}
		if status == "pending" || status == "running" {
			runtime[source.TaskID] = bf.Row{"state": "queued", "operation": source.Operation, "last_error": nil}
		}
	}
	if err = state.check(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	session := pluginFormSessionFromContext(ctx)
	host := brushFlowReadHost{a: a, data: data, sites: sites, users: users, downloaders: brushFlowDownloaderOptions(a.Settings()), guard: session.validateAnchor}
	core := bf.NewReadProjection(&host, cfg)
	for id, value := range runtime {
		core.SetRuntime(id, value)
	}
	return brushFlowReadProjectionResponse(ctx, cfg, core, req)
}
