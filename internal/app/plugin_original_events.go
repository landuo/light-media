package app

import (
	"context"
)

// PluginReload is a broadcast, not a chain event. Send only queues it; each
// registered handler is submitted independently, as in EventManager. All these
// events have the same original default priority, hence FIFO queue order.
func (a *App) emitOriginalPluginReload(instance string) {
	a.pluginOriginalEventsMu.Lock()
	a.pluginOriginalReloadQueue = append(a.pluginOriginalReloadQueue, instance)
	if a.pluginOriginalEventDispatching {
		a.pluginOriginalEventsMu.Unlock()
		return
	}
	a.pluginOriginalEventDispatching = true
	a.wg.Add(1)
	a.pluginOriginalEventsMu.Unlock()
	go func() {
		defer a.wg.Done()
		for {
			a.pluginOriginalEventsMu.Lock()
			if len(a.pluginOriginalReloadQueue) == 0 {
				a.pluginOriginalEventDispatching = false
				a.pluginOriginalEventsMu.Unlock()
				return
			}
			id := a.pluginOriginalReloadQueue[0]
			a.pluginOriginalReloadQueue = a.pluginOriginalReloadQueue[1:]
			a.pluginOriginalEventsMu.Unlock()
			// Scheduler.on_plugin_reload is always registered, independent of the
			// plugin's get_state. It obtains the current running object when invoked.
			a.wg.Add(1)
			go func(instance string) { defer a.wg.Done(); a.updateOriginalPluginJobs(context.Background(), instance) }(id)
			// CustomHosts' actual original callback calls its own initializer. This
			// is an event effect, not a substitute for scheduler/API registration.
			a.wg.Add(1)
			go func(instance string) {
				defer a.wg.Done()
				loaded, ok := a.pluginRunningInstances.Load(instance)
				if !ok {
					return
				}
				object := loaded.(*originalPluginInstance)
				if object.installation.PluginID != "CustomHosts" {
					return
				}
				object.configMu.RLock()
				enabled := object.eventsEnabled
				object.configMu.RUnlock()
				if !enabled {
					return
				}
				if err := object.init(context.Background()); err != nil {
					a.log("ERROR", "plugins", "reload_handler_failed", "插件重载事件处理失败", "", "")
				}
			}(id)
		}
	}()
}
