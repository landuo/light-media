package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
)

type brushFlowConfigurationHost struct {
	*brushFlowReadHost
	saved json.RawMessage
}

func (h *brushFlowConfigurationHost) SaveConfig(ctx context.Context, row bf.Row) error {
	raw, e := json.Marshal(row)
	if e == nil {
		h.saved = raw
	}
	return e
}

// The actual schedules are refreshed by the original init handler after the
// shared config transaction, never during preparation.
func (h *brushFlowConfigurationHost) RefreshSchedules(context.Context, []bf.Service) error {
	return nil
}
func (a *App) brushFlowPrepareSettings(ctx context.Context, view pluginFormView, req plugins.HTTPRequest) (json.RawMessage, json.RawMessage, error) {
	row, e := brushFlowBody(req.Body)
	if e != nil {
		return nil, brushFlowValidation("body"), nil
	}
	if _, e = bf.ValidateSettings(row); e != nil {
		return nil, brushFlowValidation("settings"), nil
	}
	var saved json.RawMessage
	response, e := a.brushFlowInstanceProjection(ctx, view, func(cfg bf.Config, h *brushFlowReadHost, runtime map[string]bf.Row) (json.RawMessage, error) {
		host := &brushFlowConfigurationHost{brushFlowReadHost: h}
		core := bf.NewReadProjection(host, cfg)
		for id, v := range runtime {
			core.SetRuntime(id, v)
		}
		result, e := core.UpdateSettings(ctx, row)
		if e != nil {
			return nil, e
		}
		saved = host.saved
		return brushFlowHTTPResponse(result), nil
	})
	return saved, response, e
}

func (a *App) brushFlowReadSettingsResult(ctx context.Context, v pluginFormView, req plugins.HTTPRequest) (json.RawMessage, error) {
	req.Key = "status"
	req.Method = "GET"
	return a.brushFlowReadInstanceConfiguration(ctx, v, req)
}
