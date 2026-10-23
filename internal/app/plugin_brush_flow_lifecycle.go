package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func (a *App) brushFlowLifecycleRequest(ctx context.Context, path, method string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !(parts[1] == "config" && method == "PUT" || (parts[1] == "reload" || parts[1] == "reset" || parts[1] == "uninstall") && method == "POST") {
		return false
	}
	i, err := a.pluginStore.Installation(ctx, parts[0])
	return err == nil && i.PluginID == "BrushFlowLanduo" && i.Version == "5.2.15"
}
func (a *App) pluginBrushFlowLifecycleHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	parts := strings.Split(path, "/")
	ctx := r.Context()
	i, err := a.pluginStore.Installation(ctx, parts[0])
	if err != nil {
		return nil, err
	}
	o, err := a.brushFlowOwner(ctx, i)
	if err != nil {
		return nil, err
	}
	if err = o.lock(ctx); err != nil {
		return nil, err
	}
	defer o.unlock()
	s, _, err := a.pluginStore.brushFlowRead(ctx, a.Store.DB, o.instance, o.generation)
	if err != nil {
		return nil, err
	}
	if err = brushFlowNoUnknown(s); err != nil {
		return nil, err
	}
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	switch parts[1] {
	case "config":
		var body struct {
			Revision int64           `json:"revision"`
			Config   json.RawMessage `json:"config"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			err = bad("INVALID_PLUGIN_JSON", "插件请求含无效或尚未支持的字段")
		}
		if err != nil {
			return nil, err
		}
		value, e := brushFlowConfig(body.Config)
		if e != nil {
			return nil, e
		}
		v, run, e := a.pluginStore.SaveConfigAndQueue(ctx, i.InstanceID, body.Revision, value)
		if e == nil {
			a.wakePlugins()
		}
		return map[string]any{"revision": v.Revision, "run": run}, e
	case "reload":
		run, e := a.pluginStore.EnqueueRun(ctx, i.InstanceID, "reload", nil)
		if e == nil {
			a.wakePlugins()
		}
		return map[string]any{"run": run}, e
	case "reset":
		e := a.pluginStore.Reset(ctx, i.InstanceID)
		if e == nil {
			(&pluginBrushFlowPrepared{o: o}).resolve(true)
		}
		return map[string]bool{"reset": e == nil}, e
	case "uninstall":
		e := a.pluginStore.Uninstall(ctx, i.InstanceID)
		if e == nil {
			(&pluginBrushFlowPrepared{o: o}).resolve(true)
		}
		return map[string]bool{"uninstalled": e == nil, "data_retained": pluginUninstallDataRetained(i)}, e
	}
	return nil, fmt.Errorf("invalid lifecycle operation")
}

// Bulk PluginUnInstall shares the same gate ordering as direct HTTP lifecycle.
// No pending transition can outlive a crash: before SQL this is only an in-memory
// reservation, and actual backup actions are already durable and quiescent.
type pluginBrushFlowPrepared struct {
	o            *pluginBrushFlowOwner
	fromRevision int64
}

func (a *App) prepareBrushFlowTransition(ctx context.Context, i PluginInstallation) (*pluginBrushFlowPrepared, error) {
	o, e := a.brushFlowOwner(ctx, i)
	if e != nil {
		return nil, e
	}
	if e = o.lock(ctx); e != nil {
		return nil, e
	}
	fail := func(err error) (*pluginBrushFlowPrepared, error) { o.unlock(); return nil, err }
	s, _, e := a.pluginStore.brushFlowRead(ctx, a.Store.DB, i.InstanceID, i.Generation)
	if e != nil {
		return fail(e)
	}
	if e = brushFlowNoUnknown(s); e != nil {
		return fail(e)
	}
	current, e := a.pluginStore.Installation(ctx, i.InstanceID)
	if e != nil || !current.Installed || current.Generation != i.Generation {
		return fail(bad("PLUGIN_SNAPSHOT_STALE", "BrushFlow站点刷流安装已变化"))
	}
	config, e := a.pluginStore.Config(ctx, i.InstanceID)
	if e != nil {
		return fail(e)
	}
	return &pluginBrushFlowPrepared{o: o, fromRevision: config.Revision}, nil
}
func (p *pluginBrushFlowPrepared) publishTx(ctx context.Context, tx *sql.Tx) error {
	s, _, e := p.o.a.pluginStore.brushFlowRead(ctx, tx, p.o.instance, p.o.generation)
	if e != nil {
		return e
	}
	return brushFlowNoUnknown(s)
}

func (p *pluginBrushFlowPrepared) resolve(committed bool) {
	if committed {
		r := &p.o.a.pluginBrushFlow
		r.mu.Lock()
		delete(r.owners, p.o.instance+":"+strconv.FormatInt(p.o.generation, 10))
		r.mu.Unlock()
	}
}
