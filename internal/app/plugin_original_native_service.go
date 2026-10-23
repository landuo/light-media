package app

import "bytes"

// The shared scheduler authenticates object, descriptor, occurrence and run.
// This last native-specific check binds that callback to the already-held real
// owner member. It does not decode config or acquire any additional authority.
func originalNativeServiceMatches(object *originalPluginInstance, definition pluginScheduleDefinition, plugin, handler string, actual any) bool {
	if object == nil || object.installation.PluginID != plugin || definition.Handler != handler || actual == nil {
		return false
	}
	if raw := bytes.TrimSpace(definition.Input); len(raw) != 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte("{}")) {
		return false
	}
	object.configMu.RLock()
	defer object.configMu.RUnlock()
	return object.nativeState == actual && object.originalAbsentEngine == nil
}
