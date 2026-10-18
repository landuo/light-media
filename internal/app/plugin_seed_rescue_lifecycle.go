package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (a *App) seedRescueLifecycleRequest(ctx context.Context, path, method string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !(parts[1] == "config" && method == "PUT" || (parts[1] == "reload" || parts[1] == "reset" || parts[1] == "uninstall") && method == "POST") {
		return false
	}
	i, err := a.pluginStore.Installation(ctx, parts[0])
	return err == nil && i.PluginID == "SeedRescue" && i.Version == "1.6.3"
}
func (a *App) pluginSeedRescueLifecycleHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	parts := strings.Split(path, "/")
	ctx := r.Context()
	i, err := a.pluginStore.Installation(ctx, parts[0])
	if err != nil {
		return nil, err
	}
	o, err := a.seedRescueOwner(ctx, i)
	if err != nil {
		return nil, err
	}
	if err = o.lock(ctx); err != nil {
		return nil, err
	}
	defer o.unlock()
	s, _, err := a.pluginStore.seedRescueRead(ctx, a.Store.DB, o.instance, o.generation)
	if err != nil {
		return nil, err
	}
	if err = seedRescueNoUnknown(s); err != nil {
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
		if err = decodePluginBody(w, r, &body); err != nil {
			return nil, err
		}
		value, e := seedRescueConfig(body.Config)
		if e != nil {
			return nil, e
		}
		if e = a.restoreSeedRescueSecrets(ctx, i.InstanceID, value); e != nil {
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
			(&pluginSeedRescuePrepared{o: o}).resolve(true)
		}
		return map[string]bool{"reset": e == nil}, e
	case "uninstall":
		e := a.pluginStore.Uninstall(ctx, i.InstanceID)
		if e == nil {
			(&pluginSeedRescuePrepared{o: o}).resolve(true)
		}
		return map[string]bool{"uninstalled": e == nil, "data_retained": true}, e
	}
	return nil, fmt.Errorf("invalid lifecycle operation")
}

// Bulk PluginUnInstall shares the same gate ordering as direct HTTP lifecycle.
// No pending transition can outlive a crash: before SQL this is only an in-memory
// reservation, and actual backup actions are already durable and quiescent.
type pluginSeedRescuePrepared struct {
	o            *pluginSeedRescueOwner
	fromRevision int64
}

func (a *App) prepareSeedRescueTransition(ctx context.Context, i PluginInstallation) (*pluginSeedRescuePrepared, error) {
	o, e := a.seedRescueOwner(ctx, i)
	if e != nil {
		return nil, e
	}
	if e = o.lock(ctx); e != nil {
		return nil, e
	}
	fail := func(err error) (*pluginSeedRescuePrepared, error) { o.unlock(); return nil, err }
	s, _, e := a.pluginStore.seedRescueRead(ctx, a.Store.DB, i.InstanceID, i.Generation)
	if e != nil {
		return fail(e)
	}
	if e = seedRescueNoUnknown(s); e != nil {
		return fail(e)
	}
	current, e := a.pluginStore.Installation(ctx, i.InstanceID)
	if e != nil || !current.Installed || current.Generation != i.Generation {
		return fail(bad("PLUGIN_SNAPSHOT_STALE", "SeedRescue稀缺保种安装已变化"))
	}
	config, e := a.pluginStore.Config(ctx, i.InstanceID)
	if e != nil {
		return fail(e)
	}
	return &pluginSeedRescuePrepared{o: o, fromRevision: config.Revision}, nil
}
func (p *pluginSeedRescuePrepared) publishTx(ctx context.Context, tx *sql.Tx) error {
	s, _, e := p.o.a.pluginStore.seedRescueRead(ctx, tx, p.o.instance, p.o.generation)
	if e != nil {
		return e
	}
	return seedRescueNoUnknown(s)
}

func (p *pluginSeedRescuePrepared) resolve(committed bool) {
	if committed {
		r := &p.o.a.pluginSeedRescue
		r.mu.Lock()
		delete(r.owners, p.o.instance+":"+strconv.FormatInt(p.o.generation, 10))
		r.mu.Unlock()
	}
}
