package app

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"light-media/internal/plugins"
)

// EventManager resolves class/method names through running_plugins when the
// callback executes. A reload may replace that class's object after submission.
// The host class identity includes authenticated clone lineage; neither worker
// payload nor a newly unrelated class can redirect the callback's authority.
type originalEventClass struct {
	instance, plugin, source string
}

func originalEventClassOf(object *originalPluginInstance) (originalEventClass, bool) {
	in := object.installation
	identity := originalEventClass{instance: in.InstanceID, plugin: in.PluginID}
	var envelope struct {
		Marker *pluginInstanceMarker `json:"host_instance"`
	}
	if json.Unmarshal(in.Manifest, &envelope) != nil {
		return identity, false
	}
	if envelope.Marker != nil {
		marker := envelope.Marker
		if marker.Purpose != "instance-clone/v1" || marker.InstanceID != in.InstanceID || marker.SourceInstanceID == in.InstanceID || !pluginStorageID.MatchString(marker.SourceInstanceID) {
			return identity, false
		}
		identity.source = marker.SourceInstanceID
	}
	return identity, true
}

func (p *originalPluginInstance) currentOriginalEventObject() *originalPluginInstance {
	bound, valid := originalEventClassOf(p)
	if !valid {
		return nil
	}
	loaded, ok := p.app.pluginRunningInstances.Load(bound.instance)
	if !ok {
		return nil
	}
	current := loaded.(*originalPluginInstance)
	identity, valid := originalEventClassOf(current)
	if !valid || identity != bound {
		return nil
	}
	return current
}

func (p *originalPluginInstance) originalEventEnabled() bool {
	current := p.currentOriginalEventObject()
	if current == nil || current.stopping.Load() {
		return false
	}
	current.configMu.RLock()
	defer current.configMu.RUnlock()
	return current.eventsEnabled
}

// Recheck current enablement after acquiring a legacy worker's serial method
// slot. If reload replaced the object while waiting, resolve it again and use
// the replacement's own session and fresh host call scope. Never reuse a prior
// invocation capability or invoke a stopped object's retained process.
func (p *originalPluginInstance) callOriginalEventMethod(ctx context.Context, method string, input json.RawMessage) (plugins.RunResult, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return plugins.RunResult{}, false, err
		}
		current := p.currentOriginalEventObject()
		if current == nil || !slices.Contains(current.manifest.Methods, method) {
			return plugins.RunResult{}, false, nil
		}
		unlock := current.lockOriginalMethod()
		if p.currentOriginalEventObject() != current {
			unlock()
			continue
		}
		if !current.originalEventMethodEnabled(method) {
			unlock()
			return plugins.RunResult{}, false, nil
		}
		result, err := current.callSession(ctx, plugins.Runner{Timeout: 30 * time.Second}, method, input, nil)
		unlock()
		return result, true, err
	}
}
