package app

// Central jobs bind an actual loaded object, just like the original bound
// Python method. A new object with the same textual instance ID is not that
// callback. This is memory-only object ownership, not an installation state.

type originalServiceKey struct{ instance, service string }

func originalObjectStillLoaded(object *originalPluginInstance) bool {
	if object == nil {
		return false
	}
	current, ok := object.app.pluginRunningInstances.Load(object.installation.InstanceID)
	return ok && current == object
}
