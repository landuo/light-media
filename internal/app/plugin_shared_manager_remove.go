package app

import (
	"context"
	"encoding/json"
	"slices"
	"time"
	"unicode/utf8"

	"light-media/internal/plugins"
)

type originalManagerRemoval struct {
	id        string
	class     *originalPluginClass
	object    *originalPluginInstance
	operation *originalRetainedLease
}

func (a *App) snapshotOriginalManagerRemoval(ctx context.Context, id string) (targets []originalManagerRemoval, err error) {
	a.pluginOriginalRunningMu.Lock()
	defer a.pluginOriginalRunningMu.Unlock()
	ids := []string{id}
	if id == "" {
		ids = slices.Clone(a.pluginOriginalRunningOrder)
		for _, classID := range a.pluginOriginalClassOrder {
			if !slices.Contains(ids, classID) {
				ids = append(ids, classID)
			}
		}
	}
	for _, id := range ids {
		target := originalManagerRemoval{id: id}
		if value, ok := a.pluginOriginalClasses.Load(id); ok {
			class := value.(originalPluginClass)
			target.class = &class
		}
		if value, ok := a.pluginRunningInstances.Load(id); ok {
			target.object = value.(*originalPluginInstance)
			target.operation, err = target.object.originalRetainedState().retain(originalRetainedOperation, target.object.originalRetainedParent(ctx))
			if err != nil {
				return targets, err
			}
		}
		targets = append(targets, target)
	}
	return targets, nil
}

// Manager.remove invokes ordinary hooks on the still-live exact client. It
// does not fence transport, cancel submissions, invoke business stop or join
// its own caller. A close failure skips stop_service, like the source one try.
func (p *originalPluginInstance) closeOriginalManagerObject(ctx context.Context, operation *originalRetainedLease) error {
	ctx, err := p.originalRetainedParentContext(ctx, operation)
	if err != nil {
		return err
	}
	unlock := p.lockOriginalMethod()
	defer unlock()
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	if client == nil {
		return nil
	}
	for _, method := range []string{"close", "stop_service"} {
		if !slices.Contains(p.manifest.Methods, method) {
			continue
		}
		phaseCtx := context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client})
		lease, callCtx, err := p.reserveOriginalCall(phaseCtx)
		if err != nil {
			return err
		}
		if !client.SupportsConcurrent() {
			callCtx = context.WithoutCancel(callCtx)
		}
		callback, revoke := p.originalBaseSessionHost(callCtx, "stop", nil)
		options := plugins.CallOptions{Timeout: 10 * time.Second}
		if client.SupportsConcurrent() {
			options = plugins.CallOptions{CallerDeadlineOnly: true}
		}
		if _, err = p.callRetainedSession(callCtx, client, method, json.RawMessage(`{}`), callback, options, lease, revoke); err != nil {
			return err
		}
	}
	return nil
}

// Private until bound job/API owners and their actual integration gates are
// complete. The coordinator opens the resource dispatch only with that slice.
func (a *App) originalSharedManagerRemove(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	if scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件移除未绑定当前调用")
	}
	if !utf8.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件标识必须是有效Unicode字符串")
	}
	var input struct {
		PluginID *string `json:"plugin_id"`
	}
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if input.PluginID == nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件标识缺失")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	targets, err := a.snapshotOriginalManagerRemoval(ctx, *input.PluginID)
	defer func() {
		for _, target := range targets {
			if target.object != nil {
				target.object.releaseOriginalRetainedOwner(target.operation)
			}
		}
	}()
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		if target.object != nil {
			if err := originalManagerNestedTarget(scope, target.object); err != nil {
				return nil, err
			}
		}
	}
	for _, target := range targets {
		if target.object == nil {
			continue
		}
		if err := scope.validate(ctx); err != nil {
			return nil, err
		}
		target.object.configMu.Lock()
		target.object.eventsEnabled = false
		target.object.configMu.Unlock()
		if err := target.object.closeOriginalManagerObject(ctx, target.operation); err != nil {
			a.log("WARN", "plugins", "manager_close_failed", "停止插件时发生错误", "", "")
		}
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	a.pluginOriginalRunningMu.Lock()
	for _, target := range targets {
		// Do not let a captured old object remove a concurrently replaced one.
		// The exact installation-generation binding remains the host boundary.
		if value, ok := a.pluginRunningInstances.Load(target.id); ok {
			if target.object == nil || value != target.object {
				continue
			}
			a.pluginRunningInstances.Delete(target.id)
			target.object.managerRemoved.Store(true)
			a.pluginOriginalRunningOrder = slices.DeleteFunc(a.pluginOriginalRunningOrder, func(id string) bool { return id == target.id })
			// An operation pin still exists, so no physical action occurs here.
			_ = target.object.originalRetainedState().detachManager()
		}
		if value, ok := a.pluginOriginalClasses.Load(target.id); ok && target.class != nil {
			current := value.(originalPluginClass)
			if current.installation.PluginID == target.class.installation.PluginID && current.installation.Version == target.class.installation.Version && current.installation.Generation == target.class.installation.Generation {
				a.forgetOriginalPluginClassLocked(target.id)
			}
		}
	}
	a.pluginOriginalRunningMu.Unlock()
	return json.RawMessage(`null`), nil
}
